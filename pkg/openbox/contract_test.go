// SPDX-License-Identifier: AGPL-3.0-only

package openbox_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// supportedSDKMethods maps pkg/openbox Client methods to OpenAPI operationIds.
// Keep this list the agent-facing freeze surface for SDK v0.
var supportedSDKMethods = map[string]string{
	"Negotiate":           "getHealth",
	"Health":              "getHealth",
	"Capabilities":        "getCapabilities",
	"ListImages":          "listImages",
	"ListInstances":       "listInstances",
	"GetInstance":         "getInstance",
	"CreateInstance":      "createInstance",
	"StartInstance":       "mutateInstance",
	"StopInstance":        "mutateInstance",
	"RestartInstance":     "mutateInstance",
	"DeleteInstance":      "deleteInstance",
	"ExtendInstance":      "extendInstanceExpiry",
	"ExecInstance":        "execInstance",
	"ListSnapshots":       "listInstanceSnapshots",
	"CreateSnapshot":      "createInstanceSnapshot",
	"GetSnapshot":         "getSnapshot",
	"DeleteSnapshot":      "deleteSnapshot",
	"RestoreSnapshot":     "restoreSnapshotAsNew",
	"CloneInstance":       "cloneInstance",
	"ListOperations":      "listOperations",
	"GetOperation":        "getOperation",
	"CancelOperation":     "cancelOperation",
	"WatchOperation":      "watchOperation",
	"ListRoutes":          "listRoutes",
	"CreateRoute":         "createRoute",
	"DeleteRoute":         "deleteRoute",
	"PublishRoute":        "publishRoute",
	"ListEgressProfiles":  "listEgressProfiles",
	"GetEgressProfile":    "getEgressProfile",
	"CreateEgressProfile": "createEgressProfile",
	"UpdateEgressProfile": "updateEgressProfile",
	"DeleteEgressProfile": "deleteEgressProfile",
	"AttachEgressProfile": "attachInstanceEgressProfile",
	"ListAuditEvents":     "listAuditEvents",
	"ListSuggestedPorts":  "listSuggestedPorts",
}

func TestSupportedSDKMethodsExistInOpenAPI(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(schema)
	opID := regexp.MustCompile(`(?m)^\s+operationId:\s+(\S+)\s*$`)
	found := map[string]bool{}
	for _, match := range opID.FindAllStringSubmatch(contents, -1) {
		found[match[1]] = true
	}
	for method, operationID := range supportedSDKMethods {
		if !found[operationID] {
			t.Errorf("SDK method %s maps to missing OpenAPI operationId %s", method, operationID)
		}
	}
}

func TestSupportedSDKMethodsAreDocumented(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("../../docs/api/go-sdk.md")
	if err != nil {
		// Docs are local/gitignored; skip when absent in CI checkouts that omit /docs.
		if os.IsNotExist(err) {
			t.Skip("docs/api/go-sdk.md not present")
		}
		t.Fatal(err)
	}
	text := string(doc)
	for method := range supportedSDKMethods {
		if !strings.Contains(text, method) {
			t.Errorf("docs/api/go-sdk.md missing SDK method %s", method)
		}
	}
}
