package main

import "testing"

func TestParseDiscoveryTaskCreateRequiresJSONObject(t *testing.T) {
	body, err := parseRequiredJSONOption([]string{"--json", `{"client_task_id":"one"}`}, "discovery tasks create")
	if err != nil || body["client_task_id"] != "one" {
		t.Fatalf("body=%v err=%v", body, err)
	}
	for _, args := range [][]string{nil, {"--json", "[]"}, {"--json", "{}", "--json", "{}"}} {
		if _, err := parseRequiredJSONOption(args, "discovery tasks create"); err == nil {
			t.Fatalf("args=%v error=nil", args)
		}
	}
}

func TestParseDiscoveryTaskID(t *testing.T) {
	id, err := parseDiscoveryIDArgs([]string{"--task-id", " task-1 "}, "--task-id", "discovery tasks get")
	if err != nil || id != "task-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if _, err := parseDiscoveryIDArgs(nil, "--task-id", "discovery tasks get"); err == nil {
		t.Fatal("missing task id error=nil")
	}
}
