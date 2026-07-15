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
	// A JSON string is also a valid YAML scalar, preventing a key comment or
	// newline from changing the cloud-config document structure.
	quoted, err := json.Marshal(publicKey)
	if err != nil {
		return "", err
	}
	return "#cloud-config\nusers:\n  - name: root\n    ssh_authorized_keys:\n      - " + string(quoted) + "\n", nil
}
