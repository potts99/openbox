// SPDX-License-Identifier: AGPL-3.0-only

// Package cloudinit builds structured cloud-init input without shell strings.
package cloudinit

import (
	"encoding/json"
	"errors"
	"strings"
)

func OwnerKey(publicKey string) (string, error) {
	if strings.TrimSpace(publicKey) == "" {
		return "", errors.New("owner public key is required")
	}
	// Each newline-delimited key becomes its own JSON-quoted YAML scalar. This
	// lets OpenBox add its separate internal gateway key without exposing the
	// corresponding private key or allowing key comments to alter YAML.
	var body strings.Builder
	body.WriteString("#cloud-config\nusers:\n  - name: root\n    ssh_authorized_keys:\n")
	for _, value := range strings.Split(publicKey, "\n") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		body.WriteString("      - ")
		body.Write(quoted)
		body.WriteByte('\n')
	}
	return body.String(), nil
}
