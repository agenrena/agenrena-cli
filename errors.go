package main

import "fmt"

type cliError struct {
	Code        string
	Message     string
	Recoverable bool
	Params      any
	Fields      any
}

func (e *cliError) Error() string {
	return e.Message
}

func usageError(message string) error {
	return &cliError{Code: "USAGE_ERROR", Message: message, Recoverable: true}
}

func authError(message string) error {
	return &cliError{Code: "AUTH_ERROR", Message: message, Recoverable: true}
}

func apiError(code, message string, recoverable bool) error {
	return &cliError{Code: code, Message: message, Recoverable: recoverable}
}

func wrapError(code, message string, err error) error {
	if err == nil {
		return &cliError{Code: code, Message: message}
	}
	return &cliError{Code: code, Message: fmt.Sprintf("%s: %v", message, err)}
}
