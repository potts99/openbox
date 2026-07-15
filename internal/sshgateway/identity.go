// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway

import (
	"errors"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
)

type sessionKind uint8

const (
	sessionControl sessionKind = iota + 1
	sessionInstance
)

func parseUsername(username string) (sessionKind, string, error) {
	if username == "openbox" {
		return sessionControl, "", nil
	}
	name := username
	if strings.HasSuffix(name, ".openbox") {
		name = strings.TrimSuffix(name, ".openbox")
	}
	if name == "" || domain.ValidateInstanceName(name) != nil {
		return 0, "", errors.New("invalid SSH username")
	}
	return sessionInstance, name, nil
}

func auditCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	if len(fields[0]) > 64 {
		return fields[0][:64]
	}
	return fields[0]
}
