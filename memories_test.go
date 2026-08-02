package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseMemoryCreateArgs(t *testing.T) {
	raw, err := parseMemoryCreateArgs([]string{
		"--json", `{"memory_text":"Remember this","keywords":["one","two","three","four","five"]}`,
	})
	if err != nil {
		t.Fatalf("parseMemoryCreateArgs() error = %v", err)
	}
	body, err := parseJSONObject(raw)
	if err != nil {
		t.Fatalf("parseJSONObject() error = %v", err)
	}
	if body["memory_text"] != "Remember this" {
		t.Errorf("memory_text = %#v, want Remember this", body["memory_text"])
	}

	if _, err := parseMemoryCreateArgs(nil); err == nil {
		t.Fatal("parseMemoryCreateArgs() missing --json error = nil")
	}
	if _, err := parseMemoryCreateArgs([]string{"--json", `{}`, "--json", `{}`}); err == nil {
		t.Fatal("parseMemoryCreateArgs() duplicate --json error = nil")
	}
}

func TestParseMemorySearchArgs(t *testing.T) {
	opts, err := parseMemorySearchArgs([]string{
		"--keyword", "restaurant",
		"--keyword", "taipei",
		"--cursor", "opaque-cursor",
	})
	if err != nil {
		t.Fatalf("parseMemorySearchArgs() error = %v", err)
	}
	if len(opts.keywords) != 2 || opts.keywords[0] != "restaurant" || opts.keywords[1] != "taipei" {
		t.Errorf("keywords = %#v, want restaurant and taipei", opts.keywords)
	}
	if opts.cursor != "opaque-cursor" {
		t.Errorf("cursor = %q, want opaque-cursor", opts.cursor)
	}

	if _, err := parseMemorySearchArgs(nil); err == nil {
		t.Fatal("parseMemorySearchArgs() missing keyword error = nil")
	}
	tooMany := make([]string, 0, (memorySearchKeywordMax+1)*2)
	for i := 0; i <= memorySearchKeywordMax; i++ {
		tooMany = append(tooMany, "--keyword", "keyword")
	}
	if _, err := parseMemorySearchArgs(tooMany); err == nil {
		t.Fatal("parseMemorySearchArgs() excessive keywords error = nil")
	}
}

func TestParseMemoryReadAndForgetArgs(t *testing.T) {
	const (
		memoryID1 = "11111111-1111-1111-1111-111111111111"
		memoryID2 = "22222222-2222-2222-2222-222222222222"
	)
	read, err := parseMemoryReadArgs([]string{
		"--memory-id", memoryID1,
		"--memory-id", memoryID2,
	})
	if err != nil {
		t.Fatalf("parseMemoryReadArgs() error = %v", err)
	}
	if len(read.memoryIDs) != 2 || read.memoryIDs[1] != memoryID2 {
		t.Errorf("memory IDs = %#v, want supplied UUIDs", read.memoryIDs)
	}
	if _, err := parseMemoryReadArgs(nil); err == nil {
		t.Fatal("parseMemoryReadArgs() missing ID error = nil")
	}
	if _, err := parseMemoryReadArgs([]string{"--memory-id", "../agents/me"}); err == nil {
		t.Fatal("parseMemoryReadArgs() invalid UUID error = nil")
	}
	if _, err := parseMemoryReadArgs([]string{
		"--memory-id", "00000000-0000-0000-0000-000000000001",
		"--memory-id", "00000000-0000-0000-0000-000000000002",
		"--memory-id", "00000000-0000-0000-0000-000000000003",
		"--memory-id", "00000000-0000-0000-0000-000000000004",
		"--memory-id", "00000000-0000-0000-0000-000000000005",
		"--memory-id", "00000000-0000-0000-0000-000000000006",
	}); err == nil {
		t.Fatal("parseMemoryReadArgs() excessive IDs error = nil")
	}

	memoryID, err := parseMemoryForgetArgs([]string{"--memory-id", memoryID1})
	if err != nil {
		t.Fatalf("parseMemoryForgetArgs() error = %v", err)
	}
	if memoryID != memoryID1 {
		t.Errorf("memory ID = %q, want %s", memoryID, memoryID1)
	}
	if _, err := parseMemoryForgetArgs([]string{
		"--memory-id", memoryID1,
		"--memory-id", memoryID2,
	}); err == nil {
		t.Fatal("parseMemoryForgetArgs() duplicate ID error = nil")
	}
	if _, err := parseMemoryForgetArgs([]string{"--memory-id", "../agents/me"}); err == nil {
		t.Fatal("parseMemoryForgetArgs() invalid UUID error = nil")
	}
}

func TestMemoryAPIContracts(t *testing.T) {
	const memoryID = "11111111-1111-1111-1111-111111111111"
	type observedRequest struct {
		method string
		path   string
		body   map[string]any
	}
	var observed []observedRequest
	responses := map[string]struct {
		status int
		body   string
	}{
		"/api/agent-api/memories/": {
			status: http.StatusCreated,
			body:   `{"memory":{"memory_id":"11111111-1111-1111-1111-111111111111"}}`,
		},
		"/api/agent-api/memories/search/": {
			status: http.StatusOK,
			body:   `{"results":[{"memory_id":"11111111-1111-1111-1111-111111111111"}],"next_cursor":null}`,
		},
		"/api/agent-api/memories/read/": {
			status: http.StatusOK,
			body:   `{"memories":[{"memory_id":"11111111-1111-1111-1111-111111111111","memory_text":"Remember this"}]}`,
		},
		"/api/agent-api/memories/11111111-1111-1111-1111-111111111111/": {
			status: http.StatusNoContent,
			body:   "",
		},
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer agr_test" {
			t.Errorf("Authorization = %q, want Bearer agr_test", request.Header.Get("Authorization"))
		}
		if request.Header.Get("User-Agent") != "agenrena-cli/"+cliVersion {
			t.Errorf("User-Agent = %q, want agenrena-cli/%s", request.Header.Get("User-Agent"), cliVersion)
		}

		var body map[string]any
		if request.Body != nil {
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode %s body: %v", request.URL.Path, err)
			}
		}
		observed = append(observed, observedRequest{
			method: request.Method,
			path:   request.URL.Path,
			body:   body,
		})

		response, ok := responses[request.URL.Path]
		if !ok {
			t.Fatalf("unexpected request path %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: response.status,
			Status:     http.StatusText(response.status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(response.body)),
		}, nil
	})}
	client := &APIClient{
		baseURL:    "https://api.example.com/api/agent-api",
		apiKey:     "agr_test",
		httpClient: httpClient,
	}

	if _, err := memoryCreateRequest(context.Background(), client, map[string]any{
		"memory_text": "Remember this",
		"keywords":    []string{"one", "two", "three", "four", "five"},
	}); err != nil {
		t.Fatalf("memoryCreateRequest() error = %v", err)
	}
	if _, err := memorySearchRequest(context.Background(), client, map[string]any{
		"keywords": []string{"one", "two"},
		"cursor":   "opaque-cursor",
	}); err != nil {
		t.Fatalf("memorySearchRequest() error = %v", err)
	}
	if _, err := memoryReadRequest(context.Background(), client, map[string]any{
		"memory_ids": []string{memoryID},
	}); err != nil {
		t.Fatalf("memoryReadRequest() error = %v", err)
	}
	if err := memoryForgetRequest(context.Background(), client, memoryID); err != nil {
		t.Fatalf("memoryForgetRequest() error = %v", err)
	}

	wantMethods := []string{http.MethodPost, http.MethodPost, http.MethodPost, http.MethodDelete}
	wantPaths := []string{
		"/api/agent-api/memories/",
		"/api/agent-api/memories/search/",
		"/api/agent-api/memories/read/",
		"/api/agent-api/memories/11111111-1111-1111-1111-111111111111/",
	}
	if len(observed) != len(wantPaths) {
		t.Fatalf("observed %d requests, want %d", len(observed), len(wantPaths))
	}
	for i := range observed {
		if observed[i].method != wantMethods[i] || observed[i].path != wantPaths[i] {
			t.Errorf("request %d = %s %s, want %s %s", i, observed[i].method, observed[i].path, wantMethods[i], wantPaths[i])
		}
	}
	if observed[1].body["cursor"] != "opaque-cursor" {
		t.Errorf("search body = %#v, want cursor", observed[1].body)
	}
	memoryIDs, ok := observed[2].body["memory_ids"].([]any)
	if !ok || len(memoryIDs) != 1 || memoryIDs[0] != memoryID {
		t.Errorf("read body = %#v, want %s", observed[2].body, memoryID)
	}
	if observed[3].body != nil {
		t.Errorf("forget body = %#v, want nil", observed[3].body)
	}
}
