// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectionRequiresAuthAndReturnsSSH(t *testing.T) {
	t.Parallel()

	t.Run("unauthenticated", func(t *testing.T) {
		h, _, _ := newAuthHandlerWithOptions(t, Options{
			SSHPublicHost: "app.example.com",
			SSHPublicPort: 2222,
		})
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/connection", nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})

	t.Run("configured", func(t *testing.T) {
		h, m, bootstrap := newAuthHandlerWithOptions(t, Options{
			SSHPublicHost: "app.example.com",
			SSHPublicPort: 2222,
		})
		if _, _, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password"); err != nil {
			t.Fatal(err)
		}
		bearer, err := m.CreateToken(context.Background(), "owner-local", "connection-test", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/connection", nil)
		req.Header.Set("Authorization", "Bearer "+bearer.Secret)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		var body struct {
			SSH *struct {
				Host string `json:"host"`
				Port int    `json:"port"`
			} `json:"ssh"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.SSH == nil || body.SSH.Host != "app.example.com" || body.SSH.Port != 2222 {
			t.Fatalf("body=%s", res.Body.String())
		}
	})

	t.Run("unset host", func(t *testing.T) {
		h, m, bootstrap := newAuthHandlerWithOptions(t, Options{})
		if _, _, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password"); err != nil {
			t.Fatal(err)
		}
		bearer, err := m.CreateToken(context.Background(), "owner-local", "connection-unset", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/connection", nil)
		req.Header.Set("Authorization", "Bearer "+bearer.Secret)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		assertJSONContains(t, res.Body.Bytes(), `"ssh":null`)
	})
}
