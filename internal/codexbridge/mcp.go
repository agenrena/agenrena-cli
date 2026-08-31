package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var mcpTools = []mcpTool{
	{
		Name:        "agenrena_bridge_setup",
		Description: "Configure the Agenrena-to-Codex bridge for one explicit local Codex workspace. Authentication remains owned by the Agenrena CLI.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"workspace"},
			"properties": map[string]any{"workspace": map[string]any{"type": "string", "description": "Absolute local directory Codex should use for Agenrena conversations."}},
		},
	},
	{Name: "agenrena_bridge_start", Description: "Start the native Agenrena Codex Bridge background daemon.", InputSchema: emptyObjectSchema()},
	{Name: "agenrena_bridge_status", Description: "Show bridge configuration and background process status.", InputSchema: emptyObjectSchema()},
	{Name: "agenrena_bridge_stop", Description: "Stop the native Agenrena Codex Bridge background daemon.", InputSchema: emptyObjectSchema()},
}

func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
}

func RunMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReaderSize(input, 64*1024)
	encoder := json.NewEncoder(output)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > maxRPCLineBytes {
			return errors.New("MCP client emitted an oversized JSON line")
		}
		if len(line) > 0 {
			var request rpcMessage
			if json.Unmarshal(line, &request) == nil && len(request.ID) > 0 {
				response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID)}
				result, handleErr := handleMCP(ctx, request)
				if handleErr != nil {
					response["error"] = map[string]any{"code": -32601, "message": handleErr.Error()}
				} else {
					response["result"] = result
				}
				if encodeErr := encoder.Encode(response); encodeErr != nil {
					return encodeErr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func handleMCP(_ context.Context, request rpcMessage) (any, error) {
	switch request.Method {
	case "initialize":
		params := decodeMap(request.Params)
		protocolVersion := stringValue(params["protocolVersion"])
		if protocolVersion == "" {
			protocolVersion = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agenrena-codex-bridge", "title": "Agenrena Codex Bridge", "version": Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		params := decodeMap(request.Params)
		arguments := mapValue(params["arguments"])
		return callMCPTool(stringValue(params["name"]), arguments), nil
	default:
		return nil, fmt.Errorf("unsupported MCP method: %s", request.Method)
	}
}

func callMCPTool(name string, arguments map[string]any) map[string]any {
	var value any
	var err error
	switch name {
	case "agenrena_bridge_setup":
		var status ProcessStatus
		status, err = CurrentProcessStatus()
		if err == nil && status.Running {
			err = errors.New("stop the Agenrena bridge before changing its workspace")
		} else if err == nil {
			value, err = Configure(stringValue(arguments["workspace"]))
		}
	case "agenrena_bridge_start":
		value, err = StartDaemon()
		if err == nil {
			encoded := map[string]any{}
			data, _ := json.Marshal(value)
			_ = json.Unmarshal(data, &encoded)
			message := "Agenrena messages will be answered through Codex while this bridge is running."
			if envBool("AGENRENA_CODEX_BRIDGE_CALLS", false) {
				message = "Agenrena messages and incoming voice calls will be handled through Codex while this bridge is running."
			}
			encoded["message"] = message
			value = encoded
		}
	case "agenrena_bridge_status":
		var config PublicConfig
		var status ProcessStatus
		config, err = CurrentPublicConfig()
		if err == nil {
			status, err = CurrentProcessStatus()
		}
		if err == nil {
			value = map[string]any{"config": config, "process": status}
		}
	case "agenrena_bridge_stop":
		value, err = StopDaemon()
	default:
		err = fmt.Errorf("unknown tool: %s", name)
	}
	if err != nil {
		return textResult(err.Error(), true)
	}
	return textResult(value, false)
}

func textResult(value any, isError bool) map[string]any {
	text, ok := value.(string)
	if !ok {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			text = err.Error()
			isError = true
		} else {
			text = string(data)
		}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}
