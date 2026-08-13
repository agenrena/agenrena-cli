package bridgeprotocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

const maxProtocolLineBytes = 8 * 1024 * 1024

type Backend interface {
	Initialize(context.Context, agentbridge.InitializeParams, func(agentbridge.Event)) (agentbridge.InitializeResult, error)
	Send(context.Context, agentbridge.SendParams) (agentbridge.SendResult, error)
	Handoff(context.Context, agentbridge.HandoffParams) (agentbridge.HandoffResult, error)
	AcceptCall(context.Context, agentbridge.AcceptCallParams) (agentbridge.AcceptCallResult, error)
	LeaveCall(context.Context, agentbridge.LeaveCallParams) (agentbridge.LeaveCallResult, error)
	Fatal() <-chan *agentbridge.RPCError
	Close() error
}

type TerminalError struct {
	Err error
}

func (err *TerminalError) Error() string {
	if err.Err == nil {
		return "bridge terminated"
	}
	return err.Err.Error()
}

func (err *TerminalError) Unwrap() error { return err.Err }

type Server struct {
	input   io.Reader
	output  io.Writer
	backend Backend
	writer  protocolWriter
}

func NewServer(input io.Reader, output io.Writer, backend Backend) *Server {
	return &Server{
		input: input, output: output, backend: backend,
		writer: protocolWriter{encoder: json.NewEncoder(output)},
	}
}

func (server *Server) Run(ctx context.Context) error {
	defer server.backend.Close()
	lines := make(chan scanResult, 1)
	go scanLines(server.input, lines)
	initialized := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case fatal := <-server.backend.Fatal():
			if fatal == nil {
				return &TerminalError{Err: errors.New("bridge ended with an unknown fatal error")}
			}
			return &TerminalError{Err: fatal}
		case item, ok := <-lines:
			if !ok {
				return nil
			}
			if item.err != nil {
				_ = server.writer.write(errorResponse(nil, -32700, "Parse error", &agentbridge.RPCError{
					Code: "PROTOCOL_INVALID", Message: item.err.Error(), Recoverable: false,
				}))
				return &TerminalError{Err: item.err}
			}
			request, response := parseRequest(item.line)
			if response != nil {
				_ = server.writer.write(response)
				continue
			}
			if len(request.ID) == 0 {
				continue
			}
			switch request.Method {
			case "initialize":
				if initialized {
					_ = server.writer.write(errorResponse(request.ID, -32000, "Bridge is already initialized", &agentbridge.RPCError{
						Code: "ALREADY_INITIALIZED", Message: "initialize may only be called once", Recoverable: false,
					}))
					continue
				}
				var params agentbridge.InitializeParams
				if err := decodeParams(request.Params, &params); err != nil {
					_ = server.writer.write(errorResponse(request.ID, -32602, "Invalid params", &agentbridge.RPCError{
						Code: "MESSAGE_INVALID", Message: err.Error(), Recoverable: false,
					}))
					continue
				}
				result, err := server.backend.Initialize(ctx, params, server.notify)
				if err != nil {
					rpcErr := toRPCError(err)
					_ = server.writer.write(errorResponse(request.ID, -32000, rpcErr.Message, rpcErr))
					return &TerminalError{Err: rpcErr}
				}
				initialized = true
				if err := server.writer.write(resultResponse(request.ID, result)); err != nil {
					return err
				}
			case "messages/send":
				if !initialized {
					_ = server.writer.write(errorResponse(request.ID, -32000, "Bridge is not initialized", &agentbridge.RPCError{
						Code: "NOT_INITIALIZED", Message: "initialize must succeed before messages/send", Recoverable: false,
					}))
					continue
				}
				var params agentbridge.SendParams
				if err := decodeParams(request.Params, &params); err != nil {
					_ = server.writer.write(errorResponse(request.ID, -32602, "Invalid params", &agentbridge.RPCError{
						Code: "MESSAGE_INVALID", Message: err.Error(), Recoverable: false,
					}))
					continue
				}
				result, err := server.backend.Send(ctx, params)
				if err != nil {
					rpcErr := toRPCError(err)
					_ = server.writer.write(errorResponse(request.ID, -32000, rpcErr.Message, rpcErr))
					continue
				}
				if err := server.writer.write(resultResponse(request.ID, result)); err != nil {
					return err
				}
			case "conversations/handoff":
				if !initialized {
					_ = server.writer.write(errorResponse(request.ID, -32000, "Bridge is not initialized", &agentbridge.RPCError{
						Code: "NOT_INITIALIZED", Message: "initialize must succeed before conversations/handoff", Recoverable: false,
					}))
					continue
				}
				var params agentbridge.HandoffParams
				if err := decodeParams(request.Params, &params); err != nil {
					_ = server.writer.write(errorResponse(request.ID, -32602, "Invalid params", &agentbridge.RPCError{
						Code: "MESSAGE_INVALID", Message: err.Error(), Recoverable: false,
					}))
					continue
				}
				result, err := server.backend.Handoff(ctx, params)
				if err != nil {
					rpcErr := toRPCError(err)
					_ = server.writer.write(errorResponse(request.ID, -32000, rpcErr.Message, rpcErr))
					continue
				}
				if err := server.writer.write(resultResponse(request.ID, result)); err != nil {
					return err
				}
			case "calls/accept":
				if !initialized {
					server.writeNotInitialized(request.ID, "calls/accept")
					continue
				}
				var params agentbridge.AcceptCallParams
				if err := decodeParams(request.Params, &params); err != nil {
					server.writeInvalidParams(request.ID, err)
					continue
				}
				result, err := server.backend.AcceptCall(ctx, params)
				if err != nil {
					server.writeBackendError(request.ID, err)
					continue
				}
				if err := server.writer.write(resultResponse(request.ID, result)); err != nil {
					return err
				}
			case "calls/leave":
				if !initialized {
					server.writeNotInitialized(request.ID, "calls/leave")
					continue
				}
				var params agentbridge.LeaveCallParams
				if err := decodeParams(request.Params, &params); err != nil {
					server.writeInvalidParams(request.ID, err)
					continue
				}
				result, err := server.backend.LeaveCall(ctx, params)
				if err != nil {
					server.writeBackendError(request.ID, err)
					continue
				}
				if err := server.writer.write(resultResponse(request.ID, result)); err != nil {
					return err
				}
			case "shutdown":
				if err := server.backend.Close(); err != nil {
					rpcErr := toRPCError(err)
					_ = server.writer.write(errorResponse(request.ID, -32603, rpcErr.Message, rpcErr))
					return &TerminalError{Err: rpcErr}
				}
				return server.writer.write(resultResponse(request.ID, map[string]string{"state": "stopped"}))
			default:
				_ = server.writer.write(errorResponse(request.ID, -32601, "Method not found", &agentbridge.RPCError{
					Code: "METHOD_NOT_FOUND", Message: fmt.Sprintf("unknown bridge method %q", request.Method), Recoverable: false,
				}))
			}
		}
	}
}

func (server *Server) writeNotInitialized(id json.RawMessage, method string) {
	_ = server.writer.write(errorResponse(id, -32000, "Bridge is not initialized", &agentbridge.RPCError{
		Code: "NOT_INITIALIZED", Message: "initialize must succeed before " + method, Recoverable: false,
	}))
}

func (server *Server) writeInvalidParams(id json.RawMessage, err error) {
	_ = server.writer.write(errorResponse(id, -32602, "Invalid params", &agentbridge.RPCError{
		Code: "MESSAGE_INVALID", Message: err.Error(), Recoverable: false,
	}))
}

func (server *Server) writeBackendError(id json.RawMessage, err error) {
	rpcErr := toRPCError(err)
	_ = server.writer.write(errorResponse(id, -32000, rpcErr.Message, rpcErr))
}

func (server *Server) notify(event agentbridge.Event) {
	if event.Method == "" {
		return
	}
	_ = server.writer.write(map[string]any{
		"jsonrpc": "2.0", "method": event.Method, "params": event.Params,
	})
}

type requestEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type scanResult struct {
	line []byte
	err  error
}

func scanLines(input io.Reader, output chan<- scanResult) {
	defer close(output)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxProtocolLineBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) == 0 {
			continue
		}
		output <- scanResult{line: line}
	}
	if err := scanner.Err(); err != nil {
		output <- scanResult{err: err}
	}
}

func parseRequest(line []byte) (*requestEnvelope, any) {
	var raw any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, errorResponse(nil, -32700, "Parse error", &agentbridge.RPCError{
			Code: "PROTOCOL_INVALID", Message: "input line is not valid JSON", Recoverable: false,
		})
	}
	if _, ok := raw.(map[string]any); !ok {
		return nil, errorResponse(nil, -32600, "Invalid Request", &agentbridge.RPCError{
			Code: "PROTOCOL_INVALID", Message: "JSON-RPC request must be an object", Recoverable: false,
		})
	}
	var request requestEnvelope
	if err := json.Unmarshal(line, &request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
		return nil, errorResponse(request.ID, -32600, "Invalid Request", &agentbridge.RPCError{
			Code: "PROTOCOL_INVALID", Message: "jsonrpc must be 2.0 and method must be non-empty", Recoverable: false,
		})
	}
	if len(request.ID) > 0 && !validRequestID(request.ID) {
		return nil, errorResponse(nil, -32600, "Invalid Request", &agentbridge.RPCError{
			Code: "PROTOCOL_INVALID", Message: "request id must be a string or integer", Recoverable: false,
		})
	}
	return &request, nil
}

func validRequestID(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return true
	}
	if raw[0] == '"' {
		var value string
		return json.Unmarshal(raw, &value) == nil
	}
	for index, value := range raw {
		if value == '-' && index == 0 && len(raw) > 1 {
			continue
		}
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return fmt.Errorf("params must be a JSON object")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	return nil
}

func resultResponse(id, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func errorResponse(id any, numericCode int, message string, rpcErr *agentbridge.RPCError) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{
			"code": numericCode, "message": message,
			"data": rpcErr,
		},
	}
}

func toRPCError(err error) *agentbridge.RPCError {
	var rpcErr *agentbridge.RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &agentbridge.RPCError{
		Code: "INTERNAL_ERROR", Message: err.Error(), Recoverable: false, Cause: err,
	}
}

type protocolWriter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (writer *protocolWriter) write(value any) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.encoder.Encode(value)
}
