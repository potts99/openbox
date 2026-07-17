// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

func TestNetworkProfilesListJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "api_version": "v1"})
		case r.URL.Path == "/v1/network/egress-profiles":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{
					"id": "egress-restricted", "name": "restricted", "mode": "restricted",
					"allowed_destinations": []string{}, "system": true,
					"created_at": time.Unix(0, 0).UTC(), "updated_at": time.Unix(0, 0).UTC(),
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	api, err := openbox.New(openbox.Options{
		BaseURL:    server.URL,
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runNetwork(context.Background(), api, []string{"profiles", "ls"}, true, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name":"restricted"`) {
		t.Fatalf("stdout=%s", stdout.String())
	}
}
