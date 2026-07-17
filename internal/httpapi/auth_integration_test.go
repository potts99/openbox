// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
)

func TestRemoteBootstrapIgnoresForwardingHeadersAndTLSCookieIsSecure(t *testing.T) {
	h, m, username := newAuthHandler(t)
	body := []byte(`{"username":"` + username + `","password":"a sufficiently long password"}`)
	plain := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", bytes.NewReader(body))
	plain.RemoteAddr = "203.0.113.10:42000"
	plain.Header.Set("X-Forwarded-For", "127.0.0.1")
	plain.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, plain)
	if w.Code != http.StatusForbidden {
		t.Fatalf("spoofed remote status=%d body=%s", w.Code, w.Body.String())
	}
	status, err := m.BootstrapStatus(context.Background())
	if err != nil || !status.Required {
		t.Fatalf("bootstrap consumed by rejected request: %+v %v", status, err)
	}
	tlsRequest := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", bytes.NewReader(body))
	tlsRequest.RemoteAddr = "203.0.113.10:42000"
	tlsRequest.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, tlsRequest)
	if w.Code != http.StatusCreated {
		t.Fatalf("TLS bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != int(auth.DefaultSessionTTL.Seconds()) {
		t.Fatalf("unsafe cookie: %+v", cookies)
	}
	configured, err := m.BootstrapStatus(context.Background())
	if err != nil || configured.Required {
		t.Fatalf("configured owner status=%+v err=%v", configured, err)
	}
}

func TestBootstrapStatusRemainsRequiredUntilFirstAdmin(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	h, m, username := newAuthHandlerWithClock(t, &now)
	now = now.Add(24 * time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/v1/bootstrap", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var status auth.BootstrapStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Required {
		t.Fatalf("bootstrap status=%+v", status)
	}
	if _, _, err := m.Bootstrap(context.Background(), "loopback", username, "a sufficiently long password"); err != nil {
		t.Fatalf("bootstrap error=%v", err)
	}
}

func TestBootstrapRejectsInvalidUsername(t *testing.T) {
	h, _, _ := newAuthHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", strings.NewReader(`{"username":"not valid","password":"a sufficiently long password"}`))
	request.RemoteAddr = "127.0.0.1:4000"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCookieCSRFSessionRefreshAndBearerRevocation(t *testing.T) {
	h, m, bootstrap := newAuthHandler(t)
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	mutation := func(cookieValue, csrf, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/tokens", strings.NewReader(`{"name":"created"}`))
		if cookieValue != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookieValue})
		}
		if csrf != "" {
			r.Header.Set(auth.CSRFHeader, csrf)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := mutation(cookie, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("cookie mutation without CSRF=%d body=%s", w.Code, w.Body.String())
	}
	bearer, err := m.CreateToken(context.Background(), "owner-local", "bootstrap-token", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := mutation("", "", bearer.Secret); w.Code != http.StatusCreated {
		t.Fatalf("bearer mutation status=%d body=%s", w.Code, w.Body.String())
	}
	refresh := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	refresh.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, refresh)
	if rw.Code != http.StatusOK {
		t.Fatalf("refresh=%d body=%s", rw.Code, rw.Body.String())
	}
	var inspected auth.Session
	if err := json.Unmarshal(rw.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.OwnerID != session.OwnerID || len(rw.Result().Cookies()) != 0 {
		t.Fatal("session inspection unexpectedly changed credentials")
	}
	if inspected.CSRFToken == "" {
		t.Fatal("session refresh must return csrf for websocket clients")
	}
	logout := func(csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/v1/session", nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
		r.Header.Set(auth.CSRFHeader, csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := logout(inspected.CSRFToken); w.Code != http.StatusNoContent {
		t.Fatalf("logout=%d body=%s", w.Code, w.Body.String())
	}
	if err := m.RevokeToken(context.Background(), "owner-local", bearer.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	r.Header.Set("Authorization", "Bearer "+bearer.Secret)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked next use=%d body=%s", w.Code, w.Body.String())
	}
}

func TestScopedBearerCannotMutateInstances(t *testing.T) {
	h, manager, bootstrap := newAuthHandler(t)
	if _, _, err := manager.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	token, err := manager.CreateToken(context.Background(), "owner-local", "instance-reader", []string{auth.ScopeInstancesRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		r.Header.Set("Authorization", "Bearer "+token.Secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/v1/instances"); response.Code != http.StatusOK {
		t.Fatalf("read-only list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/v1/instances"); response.Code != http.StatusForbidden {
		t.Fatalf("read-only create status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/v1/tokens"); response.Code != http.StatusForbidden {
		t.Fatalf("read-only token management status=%d body=%s", response.Code, response.Body.String())
	}
	if got := requiredScope([]string{"v1", "instances", "inst-1", "artifacts"}, http.MethodGet); got != auth.ScopeArtifactsRead {
		t.Fatalf("artifact GET scope=%q", got)
	}
	if got := requiredScope([]string{"v1", "instances", "inst-1", "artifacts"}, http.MethodPut); got != auth.ScopeArtifactsWrite {
		t.Fatalf("artifact PUT scope=%q", got)
	}
}

func TestLoginFailuresAreGenericRateLimitedAndRecover(t *testing.T) {
	now := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	h, m, bootstrap := newAuthHandlerWithClock(t, &now)
	login := func(address, password string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"password":"`+password+`"}`))
		r.RemoteAddr = address
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	missing := login("127.0.0.2:4000", "this password is wrong")
	if missing.Code != http.StatusUnauthorized || len(missing.Result().Cookies()) != 0 {
		t.Fatalf("missing credential response=%d cookies=%v body=%s", missing.Code, missing.Result().Cookies(), missing.Body.String())
	}
	if _, _, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	wrong := login("127.0.0.1:4000", "this password is wrong")
	if wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("wrong password response=%d cookies=%v body=%s", wrong.Code, wrong.Result().Cookies(), wrong.Body.String())
	}
	var missingEnvelope, wrongEnvelope struct {
		Error struct {
			Code, Message, Field string
			Retryable            bool
		} `json:"error"`
	}
	if err := json.Unmarshal(missing.Body.Bytes(), &missingEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wrong.Body.Bytes(), &wrongEnvelope); err != nil {
		t.Fatal(err)
	}
	if missingEnvelope.Error != wrongEnvelope.Error {
		t.Fatalf("credential existence leaked: missing=%+v wrong=%+v", missingEnvelope.Error, wrongEnvelope.Error)
	}
	for attempt := 2; attempt <= 5; attempt++ {
		response := login("127.0.0.1:4000", "this password is wrong")
		if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
			t.Fatalf("attempt %d status=%d cookies=%v body=%s", attempt, response.Code, response.Result().Cookies(), response.Body.String())
		}
	}
	limited := login("127.0.0.1:4000", "a sufficiently long password")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "900" || len(limited.Result().Cookies()) != 0 {
		t.Fatalf("limited status=%d retry=%q cookies=%v body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Result().Cookies(), limited.Body.String())
	}
	now = now.Add(15*time.Minute + time.Second)
	if response := login("127.0.0.1:4000", "this password is wrong"); response.Code != http.StatusUnauthorized {
		t.Fatalf("window recovery status=%d body=%s", response.Code, response.Body.String())
	}
	success := login("127.0.0.1:4000", "a sufficiently long password")
	if success.Code != http.StatusCreated || len(success.Result().Cookies()) != 1 {
		t.Fatalf("success status=%d cookies=%v body=%s", success.Code, success.Result().Cookies(), success.Body.String())
	}
	if response := login("127.0.0.1:4000", "this password is wrong"); response.Code != http.StatusUnauthorized {
		t.Fatalf("successful-login reset status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAdminCreatesMemberWhoseLoginKeepsOrganizationOwnerID(t *testing.T) {
	h, manager, bootstrap := newAuthHandler(t)
	admin, cookie, err := manager.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(http.MethodPost, "/v1/users", strings.NewReader(`{"username":"alice","display_name":"Alice","password":"another sufficiently long password"}`))
	create.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	create.Header.Set(auth.CSRFHeader, admin.CSRFToken)
	created := httptest.NewRecorder()
	h.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", created.Code, created.Body.String())
	}
	var user auth.User
	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.Username != "alice" || user.Role != "member" {
		t.Fatalf("created user=%+v", user)
	}

	list := httptest.NewRequest(http.MethodGet, "/v1/users", nil)
	list.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	listed := httptest.NewRecorder()
	h.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"username":"alice"`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	login := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"username":"alice","password":"another sufficiently long password"}`))
	login.RemoteAddr = "127.0.0.1:4000"
	loggedIn := httptest.NewRecorder()
	h.ServeHTTP(loggedIn, login)
	if loggedIn.Code != http.StatusCreated {
		t.Fatalf("member login status=%d body=%s", loggedIn.Code, loggedIn.Body.String())
	}
	var session auth.Session
	if err := json.Unmarshal(loggedIn.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.OwnerID != admin.OwnerID || session.UserID != user.ID || session.Role != "member" {
		t.Fatalf("member session=%+v admin=%+v user=%+v", session, admin, user)
	}

	memberCookie := loggedIn.Result().Cookies()[0].Value
	memberCreate := httptest.NewRequest(http.MethodPost, "/v1/users", strings.NewReader(`{"username":"mallory","password":"yet another long password"}`))
	memberCreate.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: memberCookie})
	memberCreate.Header.Set(auth.CSRFHeader, session.CSRFToken)
	forbidden := httptest.NewRecorder()
	h.ServeHTTP(forbidden, memberCreate)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("member create status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
	memberToken := httptest.NewRequest(http.MethodPost, "/v1/tokens", strings.NewReader(`{"name":"escalation"}`))
	memberToken.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: memberCookie})
	memberToken.Header.Set(auth.CSRFHeader, session.CSRFToken)
	forbidden = httptest.NewRecorder()
	h.ServeHTTP(forbidden, memberToken)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("member token creation status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
}

func newAuthHandler(t *testing.T) (*Handler, *auth.Manager, string) {
	now := time.Now().UTC()
	return newAuthHandlerWithClock(t, &now)
}

func newAuthHandlerWithOptions(t *testing.T, options Options) (*Handler, *auth.Manager, string) {
	t.Helper()
	now := time.Now().UTC()
	return newAuthHandlerWithClockAndOptions(t, &now, options)
}

func newAuthHandlerWithClock(t *testing.T, now *time.Time) (*Handler, *auth.Manager, string) {
	return newAuthHandlerWithClockAndOptions(t, now, Options{})
}

func newAuthHandlerWithClockAndOptions(t *testing.T, now *time.Time, options Options) (*Handler, *auth.Manager, string) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: *now, UpdatedAt: *now}); err != nil {
		t.Fatal(err)
	}
	m, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	m.WithClock(func() time.Time { return *now })
	options.Auth = m
	h, err := New(&fakeService{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return h, m, "admin"
}
