package codexbridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

var mcpTools = []map[string]any{
	{
		"name":        "agenrena_bridge_setup",
		"description": "Configure the local Agenrena-to-Codex bridge for one explicit Codex workspace. This reads an existing Agenrena CLI credentials file; it does not accept or store an API key.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"workspace"},
			"properties": map[string]any{
				"workspace":      map[string]any{"type": "string", "description": "Absolute local directory that Codex should use as cwd for Agenrena conversations."},
				"credentialsDir": map[string]any{"type": "string", "description": "Optional directory containing Agenrena credentials.json. Omit to use the Agenrena CLI default."},
				"apiBase":        map[string]any{"type": "string", "description": "Optional Agent API base, for example http://localhost:8020/api/agent-api."},
				"wsUrl":          map[string]any{"type": "string", "description": "Optional wss:// Agent WebSocket endpoint. It must not contain the API key; the bridge adds the token."},
			},
		},
	},
	{
		"name":        "agenrena_bridge_start",
		"description": "Start the configured background bridge. It receives Agenrena text, image, and sticker events, runs Codex app-server, and replies to the same conversation.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
	},
	{
		"name":        "agenrena_bridge_status",
		"description": "Show the local bridge configuration and whether its background process is running. Never returns the API key.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
	},
	{
		"name":        "agenrena_bridge_stop",
		"description": "Stop the local Agenrena Codex Bridge background process.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
	},
}

func textResult(value any, isError bool) map[string]any {
	var text string
	if typed, ok := value.(string); ok {
		text = typed
	} else {
		raw, _ := json.MarshalIndent(value, "", "  ")
		text = string(raw)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}

func CallTool(name string, arguments map[string]any) map[string]any {
	var value any
	var err error
	switch name {
	case "agenrena_bridge_setup":
		var status ProcessStatus
		status, err = GetProcessStatus(nil, "")
		if err == nil && status.Running {
			err = fmt.Errorf("stop the Agenrena bridge before changing its workspace or endpoints")
			break
		}
		var configured map[string]any
		configured, err = ConfigureBridge(
			stringValue(arguments["workspace"]), stringValue(arguments["credentialsDir"]),
			stringValue(arguments["apiBase"]), stringValue(arguments["wsUrl"]), nil, "",
		)
		if err == nil {
			configured["configured"] = true
			configured["next_step"] = "Call agenrena_bridge_start."
			value = configured
		}
	case "agenrena_bridge_start":
		var status ProcessStatus
		status, err = StartDaemon(nil, "")
		if err == nil {
			raw, _ := json.Marshal(status)
			result := map[string]any{}
			_ = json.Unmarshal(raw, &result)
			result["message"] = "Agenrena text, image, and sticker messages will be answered through Codex while this bridge is running."
			value = result
		}
	case "agenrena_bridge_status":
		var config map[string]any
		var process ProcessStatus
		config, err = PublicBridgeConfig(nil, "")
		if err == nil {
			process, err = GetProcessStatus(nil, "")
		}
		value = map[string]any{"config": config, "process": process}
	case "agenrena_bridge_stop":
		value, err = StopDaemon(nil, "")
	default:
		err = fmt.Errorf("unknown tool: %s", name)
	}
	if err != nil {
		return textResult(err.Error(), true)
	}
	return textResult(value, false)
}

func handleMCPRequest(message map[string]any) (any, error) {
	method := stringValue(message["method"])
	params, _ := message["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": first(stringValue(params["protocolVersion"]), "2025-06-18"),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agenrena-codex-bridge", "title": "Agenrena Codex Bridge", "version": Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		arguments, _ := params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		return CallTool(stringValue(params["name"]), arguments), nil
	default:
		return nil, fmt.Errorf("unsupported MCP method: %s", method)
	}
}

func RunMCP(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message == nil {
			continue
		}
		requestID, exists := message["id"]
		if !exists {
			continue
		}
		result, err := handleMCPRequest(message)
		response := map[string]any{"jsonrpc": "2.0", "id": requestID}
		if err != nil {
			response["error"] = map[string]any{"code": -32601, "message": err.Error()}
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
