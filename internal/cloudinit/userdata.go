// SPDX-License-Identifier: AGPL-3.0-only

// Package cloudinit builds structured cloud-init input without shell strings.
package cloudinit

import (
	"encoding/json"
	"errors"
	"strings"
)

// OwnerKey builds cloud-config that only installs the owner's SSH authorized
// keys. Sandbox/container images are expected to already include openssh-server
// (see baked OpenBox aliases). Skipping apt on every create keeps cold starts fast.
func OwnerKey(publicKey string) (string, error) {
	return ownerKey(publicKey, false)
}

// OwnerKeyBootstrap is for one-time golden / image-bake boots from upstream
// images that may omit openssh-server. It runs apt to install the package.
func OwnerKeyBootstrap(publicKey string) (string, error) {
	return ownerKey(publicKey, true)
}

func ownerKey(publicKey string, installOpenSSH bool) (string, error) {
	if strings.TrimSpace(publicKey) == "" {
		return "", errors.New("owner public key is required")
	}
	// Each newline-delimited key becomes its own JSON-quoted YAML scalar. This
	// lets OpenBox add its separate internal gateway key without exposing the
	// corresponding private key or allowing key comments to alter YAML.
	var body strings.Builder
	body.WriteString("#cloud-config\n")
	if installOpenSSH {
		// linuxcontainers cloud images may omit openssh-server; golden first
		// boot installs it once before the pool-ready snapshot / image publish.
		body.WriteString("package_update: true\npackages:\n  - openssh-server\n")
	}
	body.WriteString("users:\n  - name: root\n    ssh_authorized_keys:\n")
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
