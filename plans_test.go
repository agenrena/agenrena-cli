package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPlanCreateRequestBody(t *testing.T) {
	opts, err := parsePlanCreateArgs([]string{
		"--json", `{"title":"Trip","items":[{"source_mode":"external_note","title":"Coffee"}]}`,
	})
	if err != nil {
		t.Fatalf("parsePlanCreateArgs returned error: %v", err)
	}
	body, err := opts.requestBody()
	if err != nil {
		t.Fatalf("requestBody returned error: %v", err)
	}
	if body["title"] != "Trip" {
		t.Fatalf("title = %#v, want Trip", body["title"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want one item", body["items"])
	}

	empty, err := parsePlanCreateArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err = empty.requestBody()
	if err != nil || len(body) != 0 {
		t.Fatalf("empty create body = %#v, err = %v", body, err)
	}
}

func TestPlanItemMutationRequiresExpectedRevision(t *testing.T) {
	if _, err := parsePlanItemMutationArgs([]string{
		"--plan-id", "plan-id",
		"--json", `{"title":"Coffee"}`,
	}, "add", false, true); err == nil {
		t.Fatal("expected missing revision error")
	}

	opts, err := parsePlanItemMutationArgs([]string{
		"--plan-id", "plan-id",
		"--expected-revision", "0",
		"--json", `{"source_mode":"external_note","title":"Coffee"}`,
	}, "add", false, true)
	if err != nil {
		t.Fatalf("parsePlanItemMutationArgs returned error: %v", err)
	}
	body, err := opts.requestBody(true)
	if err != nil {
		t.Fatalf("requestBody returned error: %v", err)
	}
	if body["expected_revision"] != 0 {
		t.Fatalf("expected_revision = %#v, want 0", body["expected_revision"])
	}
}

func TestPlanItemMutationRejectsRevisionInsideJSON(t *testing.T) {
	opts, err := parsePlanItemMutationArgs([]string{
		"--plan-id", "plan-id",
		"--expected-revision", "2",
		"--json", `{"expected_revision":1,"title":"Coffee"}`,
	}, "add", false, true)
	if err != nil {
		t.Fatalf("parsePlanItemMutationArgs returned error: %v", err)
	}
	if _, err := opts.requestBody(true); err == nil {
		t.Fatal("expected embedded revision error")
	}
}

func TestPlanItemUpdateAndDeleteArguments(t *testing.T) {
	if _, err := parsePlanItemMutationArgs([]string{
		"--plan-id", "plan-id",
		"--expected-revision", "1",
		"--json", `{"note":"Later"}`,
	}, "update", true, true); err == nil {
		t.Fatal("expected missing item id error")
	}

	opts, err := parsePlanItemMutationArgs([]string{
		"--plan-id", "plan-id",
		"--item-id", "item-id",
		"--expected-revision", "3",
	}, "delete", true, false)
	if err != nil {
		t.Fatalf("delete arguments returned error: %v", err)
	}
	body, err := opts.requestBody(false)
	if err != nil {
		t.Fatalf("delete requestBody returned error: %v", err)
	}
	if len(body) != 1 || body["expected_revision"] != 3 {
		t.Fatalf("delete body = %#v", body)
	}
}

func TestPlanItemsReorderInput(t *testing.T) {
	opts, err := parsePlanItemsReorderArgs([]string{
		"--plan-id", "plan-id",
		"--expected-revision", "4",
		"--json", `[{"id":"item-a","day_index":0},{"id":"item-b","day_index":null}]`,
	})
	if err != nil {
		t.Fatalf("parsePlanItemsReorderArgs returned error: %v", err)
	}
	items, err := parseJSONArray(*opts.json)
	if err != nil {
		t.Fatalf("array returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items length = %d, want 2", len(items))
	}
}

func TestAPIClientPutAndDeleteSendJSONBodies(t *testing.T) {
	methods := []string{}
	bodies := []map[string]any{}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		bodies = append(bodies, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"revision":2}`)),
		}, nil
	})}

	client := &APIClient{baseURL: "https://api.example.com", apiKey: "test", httpClient: httpClient}
	var out any
	if err := client.put(context.Background(), "/plans/id/items/order/", map[string]any{"expected_revision": 0}, &out); err != nil {
		t.Fatalf("put returned error: %v", err)
	}
	if err := client.delete(context.Background(), "/plans/id/items/item/", map[string]any{"expected_revision": 1}, &out); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	if len(methods) != 2 || methods[0] != http.MethodPut || methods[1] != http.MethodDelete {
		t.Fatalf("methods = %#v", methods)
	}
	if bodies[0]["expected_revision"] != float64(0) || bodies[1]["expected_revision"] != float64(1) {
		t.Fatalf("bodies = %#v", bodies)
	}
}
