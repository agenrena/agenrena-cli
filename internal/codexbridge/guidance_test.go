package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in: real packaged Codex, local Responses fixture, no account or inference.
func TestBridgeGuidanceBundledCodex(t *testing.T) {
	bin := os.Getenv("BRIDGE_TEST_CODEX")
	if bin == "" {
		t.Skip("set BRIDGE_TEST_CODEX to the bundled binary")
	}
	runner, root := guidanceFixture(t)
	t.Setenv("ACCESS_TOKEN", "local-guidance-fixture")
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[]}`)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		mu.Lock()
		requests = append(requests, string(body))
		index := len(requests)
		mu.Unlock()
		item := map[string]any{"id": fmt.Sprintf("msg_%d", index), "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "GUIDANCE_FIXTURE_OK", "annotations": []any{}}}}
		response := map[string]any{"id": fmt.Sprintf("resp_%d", index), "object": "response", "status": "completed", "model": "gpt-5.4", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
		w.Header().Set("Content-Type", "text/event-stream")
		seq := 0
		send := func(event map[string]any) {
			event["sequence_number"] = seq
			seq++
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": response["id"], "status": "in_progress", "output": []any{}}})
		send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": item["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
		send(map[string]any{"type": "response.content_part.added", "item_id": item["id"], "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		send(map[string]any{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": "GUIDANCE_FIXTURE_OK"})
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": response})
	}))
	defer server.Close()
	runner.settings.TurnTimeout = 30 * time.Second
	runner.settings.Model = "gpt-5.4"
	runner.settings.CodexCommand = []string{bin, "app-server"}
	for _, option := range []string{
		`model="gpt-5.4"`, `model_provider="guidance_fixture"`,
		`model_providers.guidance_fixture.name="Local fixture"`,
		fmt.Sprintf("model_providers.guidance_fixture.base_url=%q", server.URL+"/v1"),
		`model_providers.guidance_fixture.env_key="ACCESS_TOKEN"`,
		`model_providers.guidance_fixture.wire_api="responses"`,
		`model_providers.guidance_fixture.requires_openai_auth=false`,
		`model_providers.guidance_fixture.supports_websockets=false`,
		`approval_policy="never"`, `sandbox_mode="read-only"`,
		`features.apps=false`, `features.browser_use=false`, `features.computer_use=false`,
		`features.image_generation=false`, `features.multi_agent=false`, `features.shell_snapshot=false`,
		`web_search="disabled"`, `shell_environment_policy.exclude=["ACCESS_TOKEN"]`,
	} {
		runner.settings.CodexCommand = append(runner.settings.CodexCommand, "-c", option)
	}
	// First turn predates tracking, as an existing user's conversation would.
	store := runner.guidanceStore
	runner.guidanceStore = nil
	first, err := runner.RunTurn(context.Background(), InboundMessage{ID: "00000000-0000-4000-8000-000000000001", Route: "merchant", Sender: Sender{ID: "customer"}, Text: "HISTORY_BEFORE_GUIDANCE_UPDATE"}, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner.guidanceStore = store
	writeGuidanceFixture(t, filepath.Join(root, "workspace/AGENTS.md"), "Changed workspace guidance")
	second, err := runner.RunTurn(context.Background(), InboundMessage{ID: "00000000-0000-4000-8000-000000000002", Route: "merchant", Sender: Sender{ID: "customer"}, Text: "Continue the same conversation"}, first.ThreadID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ThreadID != first.ThreadID {
		t.Fatal("thread changed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	if !strings.Contains(requests[1], "HISTORY_BEFORE_GUIDANCE_UPDATE") || !strings.Contains(requests[1], "Local guidance is new or has changed") || !strings.Contains(requests[1], "workspace AGENTS.md") {
		t.Fatal("history or guidance notification did not reach resumed Codex request")
	}
	if len(store.guidanceHashes(first.ThreadID)) != 4 {
		t.Fatal("notification was not persisted")
	}
}

func guidanceFixture(t *testing.T) (codexRunner, string) {
	t.Helper()
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	for _, dir := range []string{"resources/skills/agenrena-skill/references", "codex", "workspace/.agents/skills", "bridge"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		"resources/skills/agenrena-skill/SKILL.md":            "# Skill v1",
		"resources/skills/agenrena-skill/references/guide.md": "Reference v1",
		"codex/AGENTS.md": "Global instructions", "workspace/AGENTS.md": "Workspace instructions",
	} {
		writeGuidanceFixture(t, filepath.Join(root, path), content)
	}
	t.Setenv("AGENRENA_CODEX_BRIDGE_SKILL_DIR", "")
	if err := os.Symlink(filepath.Join(resources, "skills/agenrena-skill"), filepath.Join(root, "workspace/.agents/skills/agenrena-skill")); err != nil {
		// Windows may not grant symlink privileges; exercise the explicit path instead.
		t.Setenv("AGENRENA_CODEX_BRIDGE_SKILL_DIR", filepath.Join(resources, "skills/agenrena-skill"))
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	store := NewStateStore(filepath.Join(root, "bridge/state.json"))
	return codexRunner{settings: Settings{Workspace: filepath.Join(root, "workspace"), ApprovalPolicy: "never", SandboxMode: "read-only", TurnTimeout: 10 * time.Second}, guidanceStore: store}, root
}

func writeGuidanceFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeGuidanceContentChangesAndPersistence(t *testing.T) {
	runner, root := guidanceFixture(t)
	update, err := runner.guidanceUpdate("old-thread")
	if err != nil || update == nil || len(update.hashes) != 4 {
		t.Fatalf("first update: %v %v", update, err)
	}
	if _, err := runner.guidanceStore.Record(Reply{Route: "merchant", ThreadID: "old-thread", InboundMessageID: "pending", Text: "pending reply"}); err != nil {
		t.Fatal(err)
	}
	if err := runner.guidanceStore.rememberGuidance("old-thread", update.hashes); err != nil {
		t.Fatal(err)
	}
	runner.guidanceStore = NewStateStore(filepath.Join(root, "bridge/state.json"))
	if err := runner.guidanceStore.Load(); err != nil {
		t.Fatal(err)
	}
	if runner.guidanceStore.ThreadID("merchant") != "old-thread" {
		t.Fatal("session was reset")
	}
	if _, ok := runner.guidanceStore.Pending("pending"); !ok {
		t.Fatal("pending reply lost")
	}
	if next, err := runner.guidanceUpdate("old-thread"); err != nil || next != nil {
		t.Fatal("unchanged guidance repeated", err)
	}
	if next, err := runner.guidanceUpdate("another-thread"); err != nil || next == nil {
		t.Fatal("notification leaked across sessions", err)
	}
	for _, path := range []string{"resources/skills/agenrena-skill/references/guide.md", "codex/AGENTS.md", "workspace/AGENTS.md"} {
		writeGuidanceFixture(t, filepath.Join(root, path), "changed bytes")
		next, err := runner.guidanceUpdate("old-thread")
		if err != nil || next == nil {
			t.Fatal("edit not detected", path, err)
		}
		if err := runner.guidanceStore.rememberGuidance("old-thread", next.hashes); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "workspace/AGENTS.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	next, err := runner.guidanceUpdate("old-thread")
	if err != nil || next == nil || !strings.Contains(next.notice, `"missing":true`) {
		t.Fatal("deletion not reported", err)
	}
	if err := runner.guidanceStore.rememberGuidance("old-thread", next.hashes); err != nil {
		t.Fatal(err)
	}
	writeGuidanceFixture(t, path, "")
	if next, err := runner.guidanceUpdate("old-thread"); err != nil || next == nil {
		t.Fatal("empty recreated file not detected", err)
	}
}

func TestBridgeGuidanceLinkedFiles(t *testing.T) {
	for _, path := range []string{
		"workspace/AGENTS.md", "codex/AGENTS.md",
		"resources/skills/agenrena-skill/SKILL.md",
		"resources/skills/agenrena-skill/references/guide.md",
	} {
		t.Run(path, func(t *testing.T) {
			runner, root := guidanceFixture(t)
			link := filepath.Join(root, path)
			target := filepath.Join(root, "shared.md")
			if err := os.Rename(link, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			update, err := runner.guidanceUpdate("thread")
			if err != nil || update == nil {
				t.Fatalf("linked file rejected: %v", err)
			}
			if err := runner.guidanceStore.rememberGuidance("thread", update.hashes); err != nil {
				t.Fatal(err)
			}
			if next, err := runner.guidanceUpdate("thread"); err != nil || next != nil {
				t.Fatalf("unchanged link repeated: %v", err)
			}
			writeGuidanceFixture(t, target, "Changed linked guidance")
			if next, err := runner.guidanceUpdate("thread"); err != nil || next == nil {
				t.Fatalf("linked content edit not detected: %v", err)
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			file, err := guidanceFile("linked", link)
			if err != nil || !file.Missing {
				t.Fatalf("dangling link should be missing: %v", err)
			}
		})
	}
}

func TestBridgeGuidanceFailedSaveRetries(t *testing.T) {
	runner, root := guidanceFixture(t)
	update, err := runner.guidanceUpdate("thread")
	if err != nil {
		t.Fatal(err)
	}
	runner.guidanceStore.path = root // Renaming the state file onto a directory must fail.
	if err := runner.guidanceStore.rememberGuidance("thread", update.hashes); err == nil {
		t.Fatal("expected failed save")
	}
	if len(runner.guidanceStore.guidanceHashes("thread")) != 0 {
		t.Fatal("failed save changed in-memory state")
	}
}

func TestBridgeGuidanceRunnerResumeAndFailure(t *testing.T) {
	runner, root := guidanceFixture(t)
	capture := filepath.Join(root, "requests.jsonl")
	t.Setenv("BRIDGE_GUIDANCE_HELPER", "1")
	t.Setenv("BRIDGE_GUIDANCE_CAPTURE", capture)
	runner.settings.CodexCommand = []string{os.Args[0], "-test.run=^TestBridgeGuidanceHelperProcess$"}
	message := InboundMessage{ID: "message", Route: "merchant", Text: "Customer message"}
	t.Setenv("BRIDGE_GUIDANCE_FAIL", "1")
	if _, err := runner.RunTurn(context.Background(), message, "existing-thread", nil, nil); err == nil {
		t.Fatal("expected rejected turn")
	}
	if len(runner.guidanceStore.guidanceHashes("existing-thread")) != 0 {
		t.Fatal("failed turn was marked notified")
	}
	t.Setenv("BRIDGE_GUIDANCE_FAIL", "")
	for i := 0; i < 2; i++ {
		result, err := runner.RunTurn(context.Background(), message, "existing-thread", nil, nil)
		if err != nil || result.ThreadID != "existing-thread" {
			t.Fatal("resume failed", err)
		}
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	notices, turns := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		if request["method"] == "thread/start" {
			t.Fatal("existing thread recreated")
		}
		if request["method"] != "turn/start" {
			continue
		}
		turns++
		params := mapValue(request["params"])
		if strings.Contains(stringValue(mapValue(params["input"].([]any)[0])["text"]), "guidance") {
			t.Fatal("notice inserted into customer text")
		}
		context := mapValue(params["additionalContext"])
		if context["agenrena_transport_metadata"] == nil {
			t.Fatal("sender context lost")
		}
		if entry := mapValue(context["agenrena_guidance_update"]); len(entry) > 0 {
			if entry["kind"] != "application" {
				t.Fatal("incorrect context kind")
			}
			notices++
		}
	}
	if turns != 3 || notices != 2 {
		t.Fatalf("turns=%d notices=%d", turns, notices)
	}
}

func TestBridgeGuidanceHelperProcess(t *testing.T) {
	if os.Getenv("BRIDGE_GUIDANCE_HELPER") == "" {
		return
	}
	scanner, encoder := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		f, _ := os.OpenFile(os.Getenv("BRIDGE_GUIDANCE_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		_, _ = f.Write(append(scanner.Bytes(), '\n'))
		_ = f.Close()
		id := request["id"]
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/resume":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "existing-thread"}}})
		case "turn/start":
			if os.Getenv("BRIDGE_GUIDANCE_FAIL") != "" {
				_ = encoder.Encode(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": "fixture rejection"}})
				continue
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}})
			writeCompletedTurn(encoder, "fixture reply", false)
		}
	}
}
