// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/httpapi/generated"
)

func TestGeneratedTransportMatchesOpenAPIRevision(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(schema))
	if got != generated.OpenAPISHA256 {
		t.Fatalf("generated transport is stale: schema hash %s, generated hash %s; run go generate ./internal/httpapi/generated", got, generated.OpenAPISHA256)
	}
}

func TestOpenAPIContainsEveryHTTPRoute(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(schema)
	for _, path := range []string{
		"/v1/health:",
		"/v1/capabilities:",
		"/v1/connection:",
		"/v1/images:",
		"/v1/instances:",
		"/v1/instances/{instance_id}:",
		"/v1/instances/{instance_id}/suggested-ports:",
		"/v1/instances/{instance_id}/actions/{action}:",
		"/v1/routes:",
		"/v1/routes/{route_id}:",
		"/v1/routes/{route_id}/publish:",
		"/v1/routes/{route_id}/validate-dns:",
		"/v1/pi-profiles:",
		"/v1/pi-profiles/{profile_id}/versions:",
		"/v1/pi-profiles/{profile_id}/rollback:",
		"/v1/pi-profiles/{profile_id}/apply:",
		"/v1/certificates/allow:",
		"/v1/gateway/auth:",
		"/v1/operations:",
		"/v1/operations/{operation_id}:",
		"/v1/operations/{operation_id}/cancel:",
		"/v1/operations/{operation_id}/events:",
	} {
		if !strings.Contains(contents, "  "+path) {
			t.Errorf("OpenAPI schema missing %s", path)
		}
	}
}
