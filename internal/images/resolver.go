// SPDX-License-Identifier: AGPL-3.0-only

// Package images resolves mutable runtime image aliases to immutable fingerprints.
package images

import (
	"fmt"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type NotFoundError struct{ Reference string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("image %q was not found", e.Reference) }

// TypeMismatchError means the alias exists but not for the requested runtime type.
// Incus aliases are unique by name, so container and VM images need distinct aliases
// (OpenBox uses a "/vm" suffix for virtual-machine curated images).
type TypeMismatchError struct {
	Reference string
	WantType  string
	HaveType  string
}

func (e *TypeMismatchError) Error() string {
	return fmt.Sprintf("image %q is type %q, need %q", e.Reference, e.HaveType, e.WantType)
}

type AmbiguousError struct{ Reference string }

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("image %q resolves to more than one fingerprint", e.Reference)
}

func Resolve(reference string, available []runtimeapi.Image) (runtimeapi.Image, error) {
	return ResolveForType(reference, "", available)
}

// ResolveForType resolves an alias or fingerprint only among images compatible
// with the selected runtime type. The returned fingerprint is immutable.
//
// When imageType is virtual-machine, the base alias also tries reference+"/vm"
// and reference+"/virtual-machine" so callers can keep using the workflow alias
// while Incus stores a distinct VM alias (Incus alias names are globally unique).
func ResolveForType(reference, imageType string, available []runtimeapi.Image) (runtimeapi.Image, error) {
	candidates := aliasCandidates(reference, imageType)
	var wrongType string
	for _, candidate := range candidates {
		matches, otherType := matchImages(candidate, imageType, available)
		switch len(matches) {
		case 0:
			if otherType != "" && wrongType == "" {
				wrongType = otherType
			}
			continue
		case 1:
			return matches[0], nil
		default:
			return runtimeapi.Image{}, &AmbiguousError{Reference: candidate}
		}
	}
	if wrongType != "" && imageType != "" {
		return runtimeapi.Image{}, &TypeMismatchError{Reference: reference, WantType: imageType, HaveType: wrongType}
	}
	return runtimeapi.Image{}, &NotFoundError{Reference: reference}
}

func aliasCandidates(reference, imageType string) []string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil
	}
	candidates := []string{reference}
	if imageType != "virtual-machine" {
		return candidates
	}
	if strings.HasSuffix(reference, "/vm") || strings.HasSuffix(reference, "/virtual-machine") {
		return candidates
	}
	return append(candidates, reference+"/vm", reference+"/virtual-machine")
}

func matchImages(reference, imageType string, available []runtimeapi.Image) (matches []runtimeapi.Image, otherType string) {
	for _, image := range available {
		if image.Fingerprint != reference && !contains(image.Aliases, reference) {
			continue
		}
		if imageType != "" && image.Type != imageType {
			if otherType == "" {
				otherType = image.Type
			}
			continue
		}
		matches = append(matches, image)
	}
	return matches, otherType
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
