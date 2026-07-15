// SPDX-License-Identifier: AGPL-3.0-only

package domain

import "fmt"

// ErrorCode is a stable, machine-readable domain failure code.
type ErrorCode string

const (
	CodeInvalidArgument       ErrorCode = "invalid_argument"
	CodeInvalidTransition     ErrorCode = "invalid_transition"
	CodeExpiryRequired        ErrorCode = "expiry_required"
	CodeProtectedBase         ErrorCode = "protected_base"
	CodeConflict              ErrorCode = "conflict"
	CodeNotFound              ErrorCode = "not_found"
	CodeIdempotencyConflict   ErrorCode = "idempotency_conflict"
	CodePersistenceCorruption ErrorCode = "persistence_corruption"
	CodeRuntimeMissing        ErrorCode = "runtime_missing"
	CodeOperationCanceled     ErrorCode = "operation_canceled"
	CodeCancellationUnsafe    ErrorCode = "cancellation_unsafe"
	CodeUnavailable           ErrorCode = "unavailable"
	CodeUnauthenticated       ErrorCode = "unauthenticated"
	CodeForbidden             ErrorCode = "forbidden"
	CodeNotImplemented        ErrorCode = "not_implemented"
)

// Error carries a code and field without coupling the domain to UI wording.
type Error struct {
	Code  ErrorCode
	Field string
	Cause error
}

func (e *Error) Error() string {
	if e.Field == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Field)
}

func (e *Error) Unwrap() error { return e.Cause }

func newError(code ErrorCode, field string) error {
	return &Error{Code: code, Field: field}
}
