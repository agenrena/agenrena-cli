package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestCodexBridgeMCPAndDaemonLifecycle(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	binary := filepath.Join(temp, "agenrena")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	credentialsDir := filepath.Join(temp, "credentials")
	if err := os.MkdirAll(credentialsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCodexBridgeJSON(
		t,
		filepath.Join(credentialsDir, "credentials.json"),
		map[string]any{"api_key": "agr_test"},
	)

	command := exec.Command(binary, "codex-bridge", "mcp-server")
	command.Dir = root
	command.Env = append(
		os.Environ(),
		"AGENRENA_BRIDGE_CONFIG_DIR="+filepath.Join(temp, "config"),
		"AGENRENA_CONFIG_DIR="+credentialsDir,
		"BRIDGE_STATE_DIR="+filepath.Join(temp, "state"),
		"CODEX_BIN="+binary,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		if raw, err := os.ReadFile(
			filepath.Join(temp, "state", "process.json"),
		); err == nil {
			var state map[string]any
			_ = json.Unmarshal(raw, &state)
			if pid, err := strconv.Atoi(fmt.Sprint(state["pid"])); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}()

	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(bufio.NewReader(stdout))
	call := func(id int, name string, arguments map[string]any) map[string]any {
		t.Helper()
		err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]any{
				"name": name, "arguments": arguments,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response["result"].(map[string]any)
	}

	setup := call(1, "agenrena_bridge_setup", map[string]any{
		"workspace": root,
		"apiBase":   "https://127.0.0.1:9/api/agent-api",
		"wsUrl":     "wss://127.0.0.1:9/ws/agent/events/",
	})
	if setup["isError"] == true {
		t.Fatalf("setup failed: %#v", setup)
	}
	start := call(2, "agenrena_bridge_start", map[string]any{})
	if start["isError"] == true {
		t.Fatalf("start failed: %#v", start)
	}
	status := call(3, "agenrena_bridge_status", map[string]any{})
	if status["isError"] == true {
		t.Fatalf("status failed: %#v", status)
	}
	stop := call(4, "agenrena_bridge_stop", map[string]any{})
	if stop["isError"] == true {
		t.Fatalf("stop failed: %#v", stop)
	}
}

func writeCodexBridgeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
