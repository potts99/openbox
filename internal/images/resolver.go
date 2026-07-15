// SPDX-License-Identifier: AGPL-3.0-only

// Package images resolves mutable runtime image aliases to immutable fingerprints.
package images

import (
	"fmt"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type NotFoundError struct{ Reference string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("image %q was not found", e.Reference) }

type AmbiguousError struct{ Reference string }

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("image %q resolves to more than one fingerprint", e.Reference)
}

func Resolve(reference string, available []runtimeapi.Image) (runtimeapi.Image, error) {
	var matches []runtimeapi.Image
	for _, image := range available {
		if image.Fingerprint == reference || contains(image.Aliases, reference) {
			matches = append(matches, image)
		}
	}
	switch len(matches) {
	case 0:
		return runtimeapi.Image{}, &NotFoundError{Reference: reference}
	case 1:
		return matches[0], nil
	default:
		return runtimeapi.Image{}, &AmbiguousError{Reference: reference}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
