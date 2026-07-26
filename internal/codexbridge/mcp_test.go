package codexbridge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPInitializeAndToolsList(t *testing.T) {
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\"}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n",
	)
	var output bytes.Buffer
	if err := RunMCP(input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("output=%s", output.String())
	}
	var response map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &response)
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("tools=%#v", tools)
	}
}
