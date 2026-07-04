package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestRunFurriBallRequiresCommand(t *testing.T) {
	err := runFurriBall(context.Background(), nil)
	assertUsageError(t, err, "missing furriball command")
}

func TestRunFurriBallRejectsUnknownCommand(t *testing.T) {
	err := runFurriBall(context.Background(), []string{"unknown"})
	assertUsageError(t, err, `unknown furriball command "unknown"`)
}

func TestFurriBallPetsRejectsArguments(t *testing.T) {
	err := furriBallPets(context.Background(), []string{"unexpected"})
	assertUsageError(t, err, "furriball pets does not accept arguments")
}

func TestFurriBallPetsRequestsOwnerPets(t *testing.T) {
	client := &APIClient{
		baseURL: "https://api.example.test/api/agent-api",
		apiKey:  "agr_test",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			if r.URL.Path != "/api/agent-api/furriball/pets/" {
				t.Errorf("expected pets endpoint, got %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer agr_test" {
				t.Errorf("expected bearer API key, got %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"pets":[{"id":"pet-1","name":"Mochi"}]}`)),
			}, nil
		})},
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeEnd
	t.Cleanup(func() { os.Stdout = originalStdout })

	commandErr := furriBallPetsWithClient(context.Background(), client)
	_ = writeEnd.Close()
	os.Stdout = originalStdout
	if commandErr != nil {
		t.Fatalf("furriBallPets returned error: %v", commandErr)
	}

	var output struct {
		OK   bool `json:"ok"`
		Data struct {
			Pets []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"pets"`
		} `json:"data"`
	}
	if err := json.NewDecoder(readEnd).Decode(&output); err != nil {
		t.Fatalf("decode command output: %v", err)
	}
	_ = readEnd.Close()
	if !output.OK {
		t.Fatal("expected successful output")
	}
	if len(output.Data.Pets) != 1 || output.Data.Pets[0].Name != "Mochi" {
		t.Fatalf("unexpected pets output: %+v", output.Data.Pets)
	}
}

func assertUsageError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	cliErr, ok := err.(*cliError)
	if !ok {
		t.Fatalf("expected cliError, got %T", err)
	}
	if cliErr.Code != "USAGE_ERROR" {
		t.Fatalf("expected USAGE_ERROR, got %s", cliErr.Code)
	}
	if cliErr.Message != message {
		t.Fatalf("expected message %q, got %q", message, cliErr.Message)
	}
}
