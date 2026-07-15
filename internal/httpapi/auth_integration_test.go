// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	h, m, bootstrap := newAuthHandler(t)
	body := []byte(`{"secret":"` + bootstrap + `","password":"a sufficiently long password"}`)
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

func TestBootstrapStatusRemainsRequiredAfterChallengeExpires(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	h, m, secret := newAuthHandlerWithClock(t, &now)
	now = now.Add(auth.DefaultBootstrapTTL + time.Second)
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
	if !status.Required || status.ExpiresAt != nil {
		t.Fatalf("expired challenge status=%+v", status)
	}
	if _, _, err := m.Bootstrap(context.Background(), "loopback", secret, "a sufficiently long password"); !errors.Is(err, auth.ErrBootstrapUnavailable) {
		t.Fatalf("expired bootstrap error=%v", err)
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
	var rotated auth.Session
	if err := json.Unmarshal(rw.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	newCookie := rw.Result().Cookies()[0].Value
	if newCookie == cookie || rotated.CSRFToken == session.CSRFToken {
		t.Fatal("refresh did not rotate session and CSRF")
	}
	logout := func(csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/v1/session", nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: newCookie})
		r.Header.Set(auth.CSRFHeader, csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := logout(session.CSRFToken); w.Code != http.StatusForbidden {
		t.Fatalf("old CSRF status=%d", w.Code)
	}
	if w := logout(rotated.CSRFToken); w.Code != http.StatusNoContent {
		t.Fatalf("new CSRF logout=%d body=%s", w.Code, w.Body.String())
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
	secret, err := m.EnsureBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	options.Auth = m
	h, err := New(&fakeService{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return h, m, secret
}
