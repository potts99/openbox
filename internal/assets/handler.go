// SPDX-License-Identifier: AGPL-3.0-only

// Package assets serves the embedded OpenBox operator dashboard.
package assets

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Files contains the production dashboard generated from web/.
//
//go:embed static/* static/assets/*
var embedded embed.FS

var Files fs.FS = mustSub(embedded, "static")

func mustSub(files fs.FS, directory string) fs.FS {
	result, err := fs.Sub(files, directory)
	if err != nil {
		panic(err)
	}
	return result
}

// NewHandler serves immutable content-hashed assets and falls back to the SPA
// shell only for application routes.
func NewHandler(files fs.FS) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setSecurityHeaders(response.Header())
		response.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		name, safe := safeAssetPath(request.URL)
		if !safe {
			http.NotFound(response, request)
			return
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
				http.NotFound(response, request)
				return
			}
			name = "index.html"
			body, err = fs.ReadFile(files, name)
		}
		if err != nil {
			http.NotFound(response, request)
			return
		}

		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" && name == "index.html" {
			contentType = "text/html; charset=utf-8"
		}
		if contentType != "" {
			response.Header().Set("Content-Type", contentType)
		}
		if name == "index.html" {
			response.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(name, "assets/") && contentHashed(name) {
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			response.Header().Set("Cache-Control", "no-store")
		}
		response.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = response.Write(body)
		}
	})
}

func safeAssetPath(value *url.URL) (string, bool) {
	decoded, err := url.PathUnescape(value.EscapedPath())
	if err != nil || strings.ContainsRune(decoded, '\x00') || strings.Contains(decoded, "\\") {
		return "", false
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", false
		}
	}
	name := strings.TrimPrefix(path.Clean("/"+decoded), "/")
	if name == "." || name == "" {
		return "index.html", true
	}
	return name, true
}

func contentHashed(name string) bool {
	base := path.Base(name)
	extension := path.Ext(base)
	stem := strings.TrimSuffix(base, extension)
	separator := strings.LastIndexByte(stem, '-')
	if separator < 0 || len(stem)-separator-1 < 8 {
		return false
	}
	for _, character := range stem[separator+1:] {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func setSecurityHeaders(header http.Header) {
	// style-src includes 'unsafe-inline' because xterm.js injects a runtime <style>
	// tag for viewport/canvas layout. Google Fonts stay allowed until we self-host
	// IBM Plex; font files load from fonts.gstatic.com.
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; font-src 'self' https://fonts.gstatic.com; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
}
