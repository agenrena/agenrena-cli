package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	testSpaceID   = "11111111-1111-1111-1111-111111111111"
	testSectionID = "22222222-2222-2222-2222-222222222222"
)

func TestRunSpacesRejectsMissingAndUnknownCommands(t *testing.T) {
	commands := []func(context.Context, []string) error{
		runSpaces,
		runSpacePosts,
		runSpaceKnowledge,
		runSpaceKnowledgeSections,
	}
	for _, command := range commands {
		if err := command(context.Background(), nil); err == nil {
			t.Fatal("missing command error = nil")
		}
		if err := command(context.Background(), []string{"unknown"}); err == nil {
			t.Fatal("unknown command error = nil")
		}
	}
	for _, removed := range []string{"revisions", "restore"} {
		if err := runSpaceKnowledgeSections(context.Background(), []string{removed}); err == nil {
			t.Errorf("removed command %q error = nil", removed)
		}
	}
}

func TestParseSpaceIDArgs(t *testing.T) {
	spaceID, err := parseSpaceIDArgs([]string{"--space-id", testSpaceID}, "get")
	if err != nil {
		t.Fatalf("parseSpaceIDArgs() error = %v", err)
	}
	if spaceID != testSpaceID {
		t.Errorf("space ID = %q, want %q", spaceID, testSpaceID)
	}

	invalid := [][]string{
		nil,
		{"--space-id", "not-a-uuid"},
		{"--space-id", "../agents/me"},
		{"--space-id", testSpaceID, "--space-id", testSpaceID},
		{"--unknown", "value"},
	}
	for _, args := range invalid {
		if _, err := parseSpaceIDArgs(args, "get"); err == nil {
			t.Errorf("parseSpaceIDArgs(%#v) error = nil", args)
		}
	}
}

func TestParseSpacePostsListArgsAndEndpoint(t *testing.T) {
	opts, err := parseSpacePostsListArgs([]string{
		"--space-id", testSpaceID,
		"--after", "2026-08-11T10:20:30+08:00",
		"--cursor", "opaque+cursor=",
	})
	if err != nil {
		t.Fatalf("parseSpacePostsListArgs() error = %v", err)
	}
	wantEndpoint := "/spaces/" + testSpaceID + "/posts/?after=2026-08-11T10%3A20%3A30%2B08%3A00&cursor=opaque%2Bcursor%3D"
	if got := opts.endpoint(); got != wantEndpoint {
		t.Errorf("endpoint() = %q, want %q", got, wantEndpoint)
	}

	invalid := [][]string{
		nil,
		{"--space-id", testSpaceID, "--after", "not-a-datetime"},
		{"--space-id", testSpaceID, "--cursor", ""},
		{"--space-id", testSpaceID, "--cursor", "one", "--cursor", "two"},
		{"--space-id", testSpaceID, "--unknown", "value"},
	}
	for _, args := range invalid {
		if _, err := parseSpacePostsListArgs(args); err == nil {
			t.Errorf("parseSpacePostsListArgs(%#v) error = nil", args)
		}
	}
}

func TestParseSpaceJSONArgs(t *testing.T) {
	opts, err := parseSpaceJSONArgs([]string{
		"--space-id", testSpaceID,
		"--json", `{"posts_reviewed_through_at":"2026-08-11T10:20:30+08:00"}`,
	}, "knowledge update")
	if err != nil {
		t.Fatalf("parseSpaceJSONArgs() error = %v", err)
	}
	body, err := opts.requestBody("knowledge")
	if err != nil {
		t.Fatalf("requestBody() error = %v", err)
	}
	if body["posts_reviewed_through_at"] != "2026-08-11T10:20:30+08:00" {
		t.Errorf("request body = %#v", body)
	}
	if _, err := opts.knowledgeUpdateBody(); err != nil {
		t.Fatalf("knowledgeUpdateBody() error = %v", err)
	}

	invalid := [][]string{
		nil,
		{"--space-id", testSpaceID},
		{"--space-id", testSpaceID, "--json", ""},
		{"--space-id", testSpaceID, "--json", `{}`, "--json", `{}`},
	}
	for _, args := range invalid {
		if _, err := parseSpaceJSONArgs(args, "knowledge update"); err == nil {
			t.Errorf("parseSpaceJSONArgs(%#v) error = nil", args)
		}
	}

	empty, err := parseSpaceJSONArgs([]string{
		"--space-id", testSpaceID,
		"--json", `{}`,
	}, "knowledge update")
	if err != nil {
		t.Fatalf("empty JSON arguments error = %v", err)
	}
	if _, err := empty.requestBody("knowledge"); err == nil {
		t.Fatal("empty JSON object error = nil")
	}

	invalidBodies := []string{
		`{"agent_update_instructions":"not allowed"}`,
		`{"overview_markdown":"removed"}`,
		`{"posts_reviewed_through_at":"not-a-datetime"}`,
		`{"posts_reviewed_through_at":123}`,
		`{"posts_reviewed_through_at":null,"extra":true}`,
	}
	for _, raw := range invalidBodies {
		candidate, err := parseSpaceJSONArgs([]string{
			"--space-id", testSpaceID,
			"--json", raw,
		}, "knowledge update")
		if err != nil {
			t.Fatalf("parse invalid body arguments: %v", err)
		}
		if _, err := candidate.knowledgeUpdateBody(); err == nil {
			t.Errorf("knowledgeUpdateBody(%s) error = nil", raw)
		}
	}
	nullCursor, err := parseSpaceJSONArgs([]string{
		"--space-id", testSpaceID,
		"--json", `{"posts_reviewed_through_at":null}`,
	}, "knowledge update")
	if err != nil {
		t.Fatalf("null cursor arguments error = %v", err)
	}
	if _, err := nullCursor.knowledgeUpdateBody(); err != nil {
		t.Fatalf("null cursor body error = %v", err)
	}
}

func TestParseSpaceSectionIDArgs(t *testing.T) {
	opts, err := parseSpaceSectionIDArgs([]string{
		"--space-id", testSpaceID,
		"--section-id", testSectionID,
	}, "knowledge sections get")
	if err != nil {
		t.Fatalf("parseSpaceSectionIDArgs() error = %v", err)
	}
	if opts.spaceID != testSpaceID || opts.sectionID != testSectionID {
		t.Errorf("options = %#v", opts)
	}
	if _, err := parseSpaceSectionIDArgs([]string{"--space-id", testSpaceID}, "knowledge sections get"); err == nil {
		t.Fatal("missing --section-id error = nil")
	}
	if _, err := parseSpaceSectionIDArgs([]string{
		"--space-id", testSpaceID,
		"--section-id", "../knowledge",
	}, "knowledge sections get"); err == nil {
		t.Fatal("invalid --section-id error = nil")
	}
}

func TestSpaceSectionUpdateBody(t *testing.T) {
	opts, err := parseSpaceSectionUpdateArgs([]string{
		"--space-id", testSpaceID,
		"--section-id", testSectionID,
		"--base-version", "3",
		"--json", `{"body_markdown":"Updated"}`,
	})
	if err != nil {
		t.Fatalf("parseSpaceSectionUpdateArgs() error = %v", err)
	}
	body, err := opts.requestBody()
	if err != nil {
		t.Fatalf("requestBody() error = %v", err)
	}
	if body["base_version"] != 3 || body["body_markdown"] != "Updated" {
		t.Errorf("request body = %#v", body)
	}

	invalid := [][]string{
		{"--space-id", testSpaceID, "--section-id", testSectionID, "--json", `{"title":"Rules"}`},
		{"--space-id", testSpaceID, "--section-id", testSectionID, "--base-version", "0", "--json", `{}`},
		{"--space-id", testSpaceID, "--section-id", testSectionID, "--base-version", "1"},
	}
	for _, args := range invalid {
		if _, err := parseSpaceSectionUpdateArgs(args); err == nil {
			t.Errorf("parseSpaceSectionUpdateArgs(%#v) error = nil", args)
		}
	}

	embedded, err := parseSpaceSectionUpdateArgs([]string{
		"--space-id", testSpaceID,
		"--section-id", testSectionID,
		"--base-version", "2",
		"--json", `{"base_version":1,"body_markdown":"stale"}`,
	})
	if err != nil {
		t.Fatalf("embedded version arguments error = %v", err)
	}
	if _, err := embedded.requestBody(); err == nil {
		t.Fatal("embedded base_version error = nil")
	}
}

func TestSpaceAPIContracts(t *testing.T) {
	type observedRequest struct {
		method string
		path   string
		query  string
		body   map[string]any
	}
	var observed []observedRequest

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
			query:  request.URL.RawQuery,
			body:   body,
		})

		responseBody := `{}`
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/agent-api/spaces/":
			responseBody = `[]`
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/posts/"):
			responseBody = `{"next":null,"previous":null,"results":[]}`
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/sections/"+testSectionID+"/"):
			responseBody = `{"id":"` + testSectionID + `","version":2}`
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/sections/"+testSectionID+"/"):
			responseBody = `{"id":"` + testSectionID + `","version":3}`
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/sections/"):
			responseBody = `{"id":"` + testSectionID + `","version":1}`
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/knowledge/"):
			responseBody = `{"overview":{"version":2},"sections":[]}`
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/knowledge/"):
			responseBody = `{"posts_reviewed_through_at":"2026-08-11T10:20:30+08:00"}`
		case request.Method == http.MethodGet && request.URL.Path == "/api/agent-api/spaces/"+testSpaceID+"/":
			responseBody = `{"id":"` + testSpaceID + `"}`
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}, nil
	})}
	client := &APIClient{
		baseURL:    "https://api.example.com/api/agent-api",
		apiKey:     "agr_test",
		httpClient: httpClient,
	}
	ctx := context.Background()

	postsOpts := &spacePostsListOptions{
		spaceID: testSpaceID,
		after:   "2026-08-11T10:20:30+08:00",
		cursor:  "opaque+cursor=",
	}
	requests := []func() error{
		func() error { _, err := spacesListRequest(ctx, client); return err },
		func() error { _, err := spaceGetRequest(ctx, client, testSpaceID); return err },
		func() error { _, err := spacePostsListRequest(ctx, client, postsOpts); return err },
		func() error { _, err := spaceKnowledgeGetRequest(ctx, client, testSpaceID); return err },
		func() error {
			_, err := spaceKnowledgeUpdateRequest(ctx, client, testSpaceID, map[string]any{
				"posts_reviewed_through_at": "2026-08-11T10:20:30+08:00",
			})
			return err
		},
		func() error {
			_, err := spaceKnowledgeSectionCreateRequest(ctx, client, testSpaceID, map[string]any{
				"title": "Rules", "body_markdown": "v1",
			})
			return err
		},
		func() error {
			_, err := spaceKnowledgeSectionGetRequest(ctx, client, testSpaceID, testSectionID)
			return err
		},
		func() error {
			_, err := spaceKnowledgeSectionUpdateRequest(ctx, client, testSpaceID, testSectionID, map[string]any{
				"base_version": 2, "body_markdown": "v3",
			})
			return err
		},
	}
	for i, request := range requests {
		if err := request(); err != nil {
			t.Fatalf("request %d error = %v", i, err)
		}
	}

	wantMethods := []string{
		http.MethodGet,
		http.MethodGet,
		http.MethodGet,
		http.MethodGet,
		http.MethodPatch,
		http.MethodPost,
		http.MethodGet,
		http.MethodPatch,
	}
	wantPaths := []string{
		"/api/agent-api/spaces/",
		"/api/agent-api/spaces/" + testSpaceID + "/",
		"/api/agent-api/spaces/" + testSpaceID + "/posts/",
		"/api/agent-api/spaces/" + testSpaceID + "/knowledge/",
		"/api/agent-api/spaces/" + testSpaceID + "/knowledge/",
		"/api/agent-api/spaces/" + testSpaceID + "/knowledge/sections/",
		"/api/agent-api/spaces/" + testSpaceID + "/knowledge/sections/" + testSectionID + "/",
		"/api/agent-api/spaces/" + testSpaceID + "/knowledge/sections/" + testSectionID + "/",
	}
	if len(observed) != len(wantPaths) {
		t.Fatalf("observed %d requests, want %d", len(observed), len(wantPaths))
	}
	for i := range observed {
		if observed[i].method != wantMethods[i] || observed[i].path != wantPaths[i] {
			t.Errorf("request %d = %s %s, want %s %s", i, observed[i].method, observed[i].path, wantMethods[i], wantPaths[i])
		}
	}
	if got := observed[2].query; got != "after=2026-08-11T10%3A20%3A30%2B08%3A00&cursor=opaque%2Bcursor%3D" {
		t.Errorf("posts query = %q", got)
	}
	if observed[4].body["posts_reviewed_through_at"] != "2026-08-11T10:20:30+08:00" {
		t.Errorf("knowledge update body = %#v", observed[4].body)
	}
	if observed[7].body["base_version"] != float64(2) || observed[7].body["body_markdown"] != "v3" {
		t.Errorf("section update body = %#v", observed[7].body)
	}
}
