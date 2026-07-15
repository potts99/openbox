// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesShellAndHashedAssetsWithSafeCaching(t *testing.T) {
	files := fstest.MapFS{
		"index.html":                 &fstest.MapFile{Data: []byte(`<main>shell</main><noscript>JavaScript is required.</noscript>`)},
		"assets/app-0123456789ab.js": &fstest.MapFile{Data: []byte(`console.log("openbox")`)},
	}
	handler := NewHandler(files)

	for _, test := range []struct {
		path, contentType, cache, contains string
	}{
		{path: "/", contentType: "text/html", cache: "no-store", contains: "<noscript>"},
		{path: "/instances/box-1", contentType: "text/html", cache: "no-store", contains: "shell"},
		{path: "/assets/app-0123456789ab.js", contentType: "text/javascript", cache: "public, max-age=31536000, immutable", contains: "openbox"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) || response.Header().Get("Cache-Control") != test.cache || !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("status=%d content-type=%q cache=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Body.String())
			}
			assertSecurityHeaders(t, response)
		})
	}
}

func TestHandlerDoesNotFallbackForMissingAssetsOrUnsafePaths(t *testing.T) {
	handler := NewHandler(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("shell")}})
	for _, path := range []string{"/assets/missing.js", "/../secret", "/%2e%2e/secret", "/folder%5c..%5csecret"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d body=%q", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache=%q", path, response.Header().Get("Cache-Control"))
		}
		assertSecurityHeaders(t, response)
	}
}

func TestHandlerRejectsMutationMethods(t *testing.T) {
	handler := NewHandler(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("shell")}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestEmbeddedFilesContainNoJavaScriptFallback(t *testing.T) {
	body, err := fs.ReadFile(Files, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "<noscript>") {
		t.Fatal("embedded index omits no-JavaScript failure message")
	}
}

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{"Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if response.Header().Get(name) == "" {
			t.Fatalf("missing %s", name)
		}
	}
}
