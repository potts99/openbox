// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/tls"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/assets"
)

func TestRootHandlerKeepsAPISeparateFromDashboardFallback(t *testing.T) {
	api := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Test-API", "true")
		response.WriteHeader(http.StatusTeapot)
	})
	handler := rootHandler(api)

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if apiResponse.Code != http.StatusTeapot || apiResponse.Header().Get("X-Test-API") != "true" {
		t.Fatalf("API response status=%d headers=%v", apiResponse.Code, apiResponse.Header())
	}

	dashboardResponse := httptest.NewRecorder()
	handler.ServeHTTP(dashboardResponse, httptest.NewRequest(http.MethodGet, "/instances/example", nil))
	if dashboardResponse.Code != http.StatusOK || !strings.Contains(dashboardResponse.Body.String(), `id="root"`) {
		t.Fatalf("dashboard status=%d body=%q", dashboardResponse.Code, dashboardResponse.Body.String())
	}

	entries, err := fs.ReadDir(assets.Files, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded assets=%v err=%v", entries, err)
	}
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, httptest.NewRequest(http.MethodGet, "/assets/"+entries[0].Name(), nil))
	if assetResponse.Code != http.StatusOK || assetResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset status=%d cache=%q", assetResponse.Code, assetResponse.Header().Get("Cache-Control"))
	}
}

func TestRootHandlerSetsHSTSOnlyForTLS(t *testing.T) {
	handler := rootHandler(http.NotFoundHandler())
	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/", nil))
	if value := plain.Header().Get("Strict-Transport-Security"); value != "" {
		t.Fatalf("HSTS sent over plaintext: %q", value)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.TLS = &tls.ConnectionState{}
	secure := httptest.NewRecorder()
	handler.ServeHTTP(secure, request)
	if value := secure.Header().Get("Strict-Transport-Security"); value != "max-age=31536000" {
		t.Fatalf("HSTS = %q", value)
	}
}
