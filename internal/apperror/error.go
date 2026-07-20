// Package apperror defines stable process-level error categories and exit codes.
package apperror

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ExitCode is a stable public process exit status.
type ExitCode int

const (
	ExitSuccess     ExitCode = 0
	ExitFailure     ExitCode = 1
	ExitUsage       ExitCode = 64
	ExitData        ExitCode = 65
	ExitUnavailable ExitCode = 69
	ExitTemporary   ExitCode = 75
	ExitPolicy      ExitCode = 78
)

// Error carries a stable machine-readable kind without exposing internal causes.
type Error struct {
	Code    ExitCode
	Kind    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

// New creates a public application error.
func New(code ExitCode, kind, message string) *Error {
	return &Error{Code: code, Kind: kind, Message: message}
}

// Wrap creates a public application error backed by an internal cause.
func Wrap(code ExitCode, kind, message string, cause error) *Error {
	return &Error{Code: code, Kind: kind, Message: message, Cause: cause}
}

// Code returns the stable exit status for err. Unknown errors are failures.
func Code(err error) ExitCode {
	if err == nil {
		return ExitSuccess
	}
	var applicationError *Error
	if errors.As(err, &applicationError) {
		return applicationError.Code
	}
	return ExitFailure
}

// Write renders an error for either human or JSON CLI output.
func Write(writer io.Writer, err error, jsonMode bool) ExitCode {
	if err == nil {
		return ExitSuccess
	}
	code := Code(err)
	var applicationError *Error
	kind := "runtime_failure"
	message := err.Error()
	if errors.As(err, &applicationError) {
		kind = applicationError.Kind
		message = applicationError.Message
	}
	if jsonMode {
		payload := struct {
			Error struct {
				Code    int    `json:"code"`
				Kind    string `json:"kind"`
				Message string `json:"message"`
			} `json:"error"`
		}{}
		payload.Error.Code = int(code)
		payload.Error.Kind = kind
		payload.Error.Message = message
		if encodeErr := json.NewEncoder(writer).Encode(payload); encodeErr != nil {
			return ExitFailure
		}
		return code
	}
	fmt.Fprintf(writer, "ovpn: %s\n", message)
	return code
}
