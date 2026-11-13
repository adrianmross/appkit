// Package apperr is the structured error every transport returns. It knows
// nothing about an app's entities.
package apperr

import (
	"errors"
	"fmt"
	"strings"
)

// Error is a structured failure safe to show to an agent.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	Status  int            `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func (e *Error) WithHint(h string) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Hint = h
	return &cp
}

func (e *Error) WithDetails(d map[string]any) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Details = d
	return &cp
}

func Validation(format string, args ...any) *Error {
	return &Error{Code: "validation", Message: fmt.Sprintf(format, args...), Status: 400, Hint: "see /openapi.json for the request shape"}
}

func NotFound(entity string) *Error {
	return &Error{Code: "not_found", Message: entity + " not found", Hint: "check the id with the matching list command", Status: 404}
}

func Conflict(message, hint string) *Error {
	return &Error{Code: "conflict", Message: message, Hint: hint, Status: 409}
}

func VersionConflict(entity string, current int) *Error {
	return Conflict(entity+" was modified", "fetch the current "+entity+" and retry with its version").WithDetails(map[string]any{"current_version": current})
}

func Forbidden(scope string) *Error {
	return &Error{Code: "forbidden", Message: "token is missing the " + scope + " scope", Hint: "create a token with --scope " + scope, Status: 403}
}

func RateLimited(hint string) *Error {
	return &Error{Code: "rate_limited", Message: "too many requests", Hint: hint, Status: 429}
}

// FeatureUnavailable is a feature this user can't use right now. reason is
// "not_configured" (the deployment lacks it), "not_entitled" (needs a plan),
// or "disabled" (the user turned it off).
func FeatureUnavailable(feature, reason, hint string) *Error {
	return &Error{Code: "feature_unavailable", Message: feature + " is unavailable: " + strings.ReplaceAll(reason, "_", " "), Hint: hint, Status: 403,
		Details: map[string]any{"feature": feature, "reason": reason}}
}

func IdempotencyMismatch() *Error {
	return &Error{Code: "idempotency_mismatch", Message: "idempotency key was reused with a different request", Hint: "reuse a key only with an identical request, or send a new key", Status: 409}
}

func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e != nil && e.Code == code
}

func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}
