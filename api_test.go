package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDecodeAPIResponseParsesUnifiedBackendError(t *testing.T) {
	resp := errorResponse(
		http.StatusUpgradeRequired,
		`{"error":{"code":"CLI_VERSION_UNSUPPORTED","params":{"min_cli_version":"0.8.0","cli_version":"0.7.0","reason":"version_too_old"}}}`,
	)

	err := decodeAPIResponse(resp, nil)
	var cliErr *cliError
	if !errors.As(err, &cliErr) {
		t.Fatalf("decodeAPIResponse() error = %T, want *cliError", err)
	}
	if cliErr.Code != "CLI_VERSION_UNSUPPORTED" {
		t.Errorf("Code = %q, want CLI_VERSION_UNSUPPORTED", cliErr.Code)
	}
	if cliErr.Message != "CLI_VERSION_UNSUPPORTED" {
		t.Errorf("Message = %q, want CLI_VERSION_UNSUPPORTED", cliErr.Message)
	}
	if cliErr.Recoverable {
		t.Error("Recoverable = true, want false")
	}
	params, ok := cliErr.Params.(map[string]any)
	if !ok {
		t.Fatalf("Params = %T, want map[string]any", cliErr.Params)
	}
	if params["min_cli_version"] != "0.8.0" || params["cli_version"] != "0.7.0" {
		t.Errorf("Params = %#v, want backend version details", params)
	}
}

func TestDecodeAPIResponsePreservesValidationFields(t *testing.T) {
	resp := errorResponse(
		http.StatusBadRequest,
		`{"error":{"code":"VALIDATION_ERROR","fields":{"query":[{"code":"REQUIRED"}]}}}`,
	)

	err := decodeAPIResponse(resp, nil)
	var cliErr *cliError
	if !errors.As(err, &cliErr) {
		t.Fatalf("decodeAPIResponse() error = %T, want *cliError", err)
	}
	if cliErr.Code != "VALIDATION_ERROR" {
		t.Errorf("Code = %q, want VALIDATION_ERROR", cliErr.Code)
	}
	fields, ok := cliErr.Fields.(map[string]any)
	if !ok {
		t.Fatalf("Fields = %T, want map[string]any", cliErr.Fields)
	}
	if _, ok := fields["query"]; !ok {
		t.Errorf("Fields = %#v, want query field", fields)
	}
}

func TestDecodeAPIResponseSupportsLegacyStringError(t *testing.T) {
	resp := errorResponse(http.StatusBadRequest, `{"error":"invalid request"}`)

	err := decodeAPIResponse(resp, nil)
	var cliErr *cliError
	if !errors.As(err, &cliErr) {
		t.Fatalf("decodeAPIResponse() error = %T, want *cliError", err)
	}
	if cliErr.Code != "HTTP_400" {
		t.Errorf("Code = %q, want HTTP_400", cliErr.Code)
	}
	if cliErr.Message != "invalid request" {
		t.Errorf("Message = %q, want invalid request", cliErr.Message)
	}
}

func TestDecodeAPIResponseMarksRetryableStatus(t *testing.T) {
	resp := errorResponse(
		http.StatusTooManyRequests,
		`{"error":{"code":"THROTTLED"}}`,
	)

	err := decodeAPIResponse(resp, nil)
	var cliErr *cliError
	if !errors.As(err, &cliErr) {
		t.Fatalf("decodeAPIResponse() error = %T, want *cliError", err)
	}
	if !cliErr.Recoverable {
		t.Error("Recoverable = false, want true")
	}
}

func TestErrorEnvelopePreservesBackendDetails(t *testing.T) {
	params := map[string]any{"min_cli_version": "0.8.0"}
	fields := map[string]any{"query": []any{map[string]any{"code": "REQUIRED"}}}
	env := buildErrorEnvelope(&cliError{
		Code:    "VALIDATION_ERROR",
		Message: "VALIDATION_ERROR",
		Params:  params,
		Fields:  fields,
	})

	if env.Error.Params == nil {
		t.Fatal("Params = nil, want backend params")
	}
	if env.Error.Fields == nil {
		t.Fatal("Fields = nil, want backend fields")
	}
}

func errorResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
