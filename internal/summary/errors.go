// Package summary provides the protocol-neutral application service for
// summarization. REST handlers and future MCP transports call the same
// Service, keeping business logic in one place.
package summary

// ErrorCategory classifies service errors for transport-level status mapping.
type ErrorCategory string

const (
	CategoryInvalidInput       ErrorCategory = "invalid_input"
	CategoryNotFound           ErrorCategory = "not_found"
	CategoryServiceUnavailable ErrorCategory = "service_unavailable"
	CategoryInternal           ErrorCategory = "internal"
)

// Error is a typed service error carrying a category for status mapping.
type Error struct {
	Category ErrorCategory
	Message  string
}

func (e *Error) Error() string { return e.Message }

func invalidInput(msg string) *Error {
	return &Error{Category: CategoryInvalidInput, Message: msg}
}

func notFound(msg string) *Error {
	return &Error{Category: CategoryNotFound, Message: msg}
}

func serviceUnavailable(msg string) *Error {
	return &Error{Category: CategoryServiceUnavailable, Message: msg}
}

func internalErr(msg string) *Error {
	return &Error{Category: CategoryInternal, Message: msg}
}
