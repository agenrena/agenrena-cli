package agentbridge

import "fmt"

type RPCError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Recoverable  bool   `json:"recoverable"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
	Fields       any    `json:"fields,omitempty"`
	Cause        error  `json:"-"`
}

func (err *RPCError) Error() string {
	if err.Cause != nil {
		return fmt.Sprintf("%s: %v", err.Message, err.Cause)
	}
	return err.Message
}

func bridgeError(code, message string, recoverable bool) *RPCError {
	return &RPCError{Code: code, Message: message, Recoverable: recoverable}
}

func wrapBridgeError(code, message string, recoverable bool, cause error) *RPCError {
	return &RPCError{Code: code, Message: message, Recoverable: recoverable, Cause: cause}
}
