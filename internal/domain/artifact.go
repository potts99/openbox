// SPDX-License-Identifier: AGPL-3.0-only

package domain

import (
	"regexp"
	"strings"
	"time"
)

var artifactPathSegmentPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type ArtifactID string

type Artifact struct {
	ID          ArtifactID
	InstanceID  InstanceID
	OwnerID     OwnerID
	Path        string
	SizeBytes   int64
	ContentType string
	SHA256      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func ValidateArtifactPath(value string) error {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return newError(CodeInvalidArgument, "path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == ".." || !artifactPathSegmentPattern.MatchString(segment) {
			return newError(CodeInvalidArgument, "path")
		}
	}
	return nil
}
