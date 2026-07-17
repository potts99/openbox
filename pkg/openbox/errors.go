// SPDX-License-Identifier: AGPL-3.0-only

package openbox

import "fmt"

type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Field      string `json:"field,omitempty"`
	Retryable  bool   `json:"retryable"`
	RequestID  string `json:"request_id,omitempty"`
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = e.Code
	}
	if e.Field != "" {
		return fmt.Sprintf("%s (%s): %s", message, e.Code, e.Field)
	}
	return fmt.Sprintf("%s (%s)", message, e.Code)
}

type VersionError struct {
	Wanted    string
	Supported []string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("server does not support API %s (supported: %v)", e.Wanted, e.Supported)
}
