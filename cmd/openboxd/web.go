// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"strings"

	"github.com/openbox-dev/openbox/internal/assets"
)

func rootHandler(api http.Handler) http.Handler {
	dashboard := assets.NewHandler(assets.Files)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if request.URL.Path == "/v1" || strings.HasPrefix(request.URL.Path, "/v1/") {
			api.ServeHTTP(response, request)
			return
		}
		dashboard.ServeHTTP(response, request)
	})
}
