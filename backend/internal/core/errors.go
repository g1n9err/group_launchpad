// Package core defines shared domain error types. Transport maps these to HTTP
// responses in exactly one place; core never imports HTTP concepts.
package core

// Error codes — stable values that are part of the public API contract.
const (
	CodeValidation    = "VALIDATION_ERROR"
	CodeDuplicate     = "DUPLICATE_GROUP"
	CodeConflict      = "CONFLICT"
	CodeNotFound      = "NOT_FOUND"
	CodeNotReady      = "NOT_READY"
	CodeUpstream      = "UPSTREAM_ERROR"
	CodeOAuthExchange = "OAUTH_EXCHANGE_FAILED"
	CodeUnauthorized  = "UNAUTHORIZED"
	CodeForbidden     = "FORBIDDEN"
	CodeRateLimited   = "RATE_LIMITED"
	CodeInternal      = "INTERNAL"
)

// AppError is a domain error carrying everything the API needs to respond:
// a stable machine code, a human message, and optional per-field errors.
type AppError struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
}

func (e *AppError) Error() string { return e.Code + ": " + e.Message }

// Validation reports invalid input, with field-level detail when available.
func Validation(message string, fieldErrors map[string]string) *AppError {
	return &AppError{Code: CodeValidation, Message: message, FieldErrors: fieldErrors}
}

// DuplicateGroup reports a rejected create because the group name exists.
func DuplicateGroup(name string) *AppError {
	return &AppError{
		Code:    CodeDuplicate,
		Message: "a group with this name already exists",
	}
}

// NotFound reports a missing resource of the given kind.
func NotFound(kind, id string) *AppError {
	return &AppError{Code: CodeNotFound, Message: kind + " not found: " + id}
}
