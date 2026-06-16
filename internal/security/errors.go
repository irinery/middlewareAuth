package security

import (
	"errors"
	"fmt"
	"net/http"
)

type ErrorDetail struct {
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

type AppError struct {
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	Details    []ErrorDetail `json:"details,omitempty"`
	StatusCode int           `json:"-"`
	Cause      error         `json:"-"`
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return e.Code + ": " + e.Message
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewError(code, message string, status int) *AppError {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return &AppError{Code: code, Message: message, StatusCode: status}
}

func Wrap(code, message string, status int, cause error) *AppError {
	err := NewError(code, message, status)
	err.Cause = cause
	return err
}

func WithDetail(err *AppError, field, reason string) *AppError {
	if err == nil {
		return nil
	}
	err.Details = append(err.Details, ErrorDetail{Field: field, Reason: reason})
	return err
}

func StatusCode(err error) int {
	var appErr *AppError
	if errors.As(err, &appErr) && appErr.StatusCode != 0 {
		return appErr.StatusCode
	}
	return http.StatusInternalServerError
}

func Code(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return "ERR_INTERNAL"
}

func Public(err error) *AppError {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return &AppError{
			Code:       appErr.Code,
			Message:    appErr.Message,
			Details:    appErr.Details,
			StatusCode: appErr.StatusCode,
		}
	}
	return NewError("ERR_INTERNAL", "erro interno", http.StatusInternalServerError)
}
