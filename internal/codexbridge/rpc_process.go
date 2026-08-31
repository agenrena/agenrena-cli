package codexbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const maxRPCLineBytes = 32 * 1024 * 1024

type rpcError struct {
	Code    int             `json:"code,omitempty"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

type jsonLineProcess struct {
	command string
	cmd     *exec.Cmd
	stdin   io.WriteCloser

	includeJSONRPC bool
	writeMu        sync.Mutex
	pendingMu      sync.Mutex
	pending        map[int]chan rpcReply
	nextID         int

	requests      chan rpcMessage
	notifications chan rpcMessage
	exit          chan error
	exitOnce      sync.Once
	stderrMu      sync.Mutex
	stderr        []byte
}

func startJSONLineProcess(command string, args []string, cwd string, includeJSONRPC bool, stderrForward io.Writer) (*jsonLineProcess, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	process := &jsonLineProcess{
		command: command, cmd: cmd, stdin: stdin, includeJSONRPC: includeJSONRPC,
		pending: make(map[int]chan rpcReply), requests: make(chan rpcMessage, 64),
		notifications: make(chan rpcMessage, 256), exit: make(chan error, 1),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go process.readStdout(stdout)
	go process.readStderr(stderr, stderrForward)
	go func() {
		err := cmd.Wait()
		message := process.stderrText()
		if message != "" {
			err = errors.New(message)
		} else if err == nil {
			err = fmt.Errorf("%s exited unexpectedly", command)
		}
		process.finish(err)
	}()
	return process, nil
}

func (process *jsonLineProcess) Request(ctx context.Context, method string, params any, timeout time.Duration, destination any) error {
	process.pendingMu.Lock()
	process.nextID++
	id := process.nextID
	wait := make(chan rpcReply, 1)
	process.pending[id] = wait
	process.pendingMu.Unlock()

	message := map[string]any{"id": id, "method": method, "params": params}
	if process.includeJSONRPC {
		message["jsonrpc"] = "2.0"
	}
	if err := process.write(message); err != nil {
		process.removePending(id)
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case reply := <-wait:
		if reply.err != nil {
			return reply.err
		}
		if destination == nil || len(reply.result) == 0 || bytes.Equal(reply.result, []byte("null")) {
			return nil
		}
		if err := json.Unmarshal(reply.result, destination); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	case <-timer.C:
		process.removePending(id)
		return fmt.Errorf("%s timed out", method)
	case <-ctx.Done():
		process.removePending(id)
		return ctx.Err()
	}
}

func (process *jsonLineProcess) Respond(id json.RawMessage, result any) error {
	message := map[string]any{"id": json.RawMessage(id), "result": result}
	if process.includeJSONRPC {
		message["jsonrpc"] = "2.0"
	}
	return process.write(message)
}

func (process *jsonLineProcess) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	process.writeMu.Lock()
	defer process.writeMu.Unlock()
	if process.stdin == nil {
		return fmt.Errorf("%s stdin is closed", process.command)
	}
	_, err = process.stdin.Write(data)
	return err
}

func (process *jsonLineProcess) Close(grace time.Duration) {
	process.writeMu.Lock()
	stdin := process.stdin
	process.stdin = nil
	process.writeMu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	select {
	case <-process.exit:
		return
	case <-time.After(grace):
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Kill()
		}
	}
}

func (process *jsonLineProcess) readStdout(reader io.Reader) {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	for {
		line, err := buffered.ReadBytes('\n')
		if len(line) > maxRPCLineBytes {
			if process.cmd.Process != nil {
				_ = process.cmd.Process.Kill()
			}
			process.finish(fmt.Errorf("%s emitted an oversized JSON line", process.command))
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var message rpcMessage
			if json.Unmarshal(line, &message) == nil {
				process.dispatch(message)
			}
		}
		if err != nil {
			if err != io.EOF {
				process.finish(err)
			}
			return
		}
	}
}

func (process *jsonLineProcess) readStderr(reader io.Reader, forward io.Writer) {
	buffer := make([]byte, 4096)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			if forward != nil {
				_, _ = forward.Write(buffer[:count])
			}
			process.stderrMu.Lock()
			process.stderr = append(process.stderr, buffer[:count]...)
			if len(process.stderr) > 20_000 {
				process.stderr = append([]byte(nil), process.stderr[len(process.stderr)-20_000:]...)
			}
			process.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (process *jsonLineProcess) dispatch(message rpcMessage) {
	if message.Method != "" && len(message.ID) > 0 {
		process.requests <- message
		return
	}
	if len(message.ID) > 0 {
		var id int
		if json.Unmarshal(message.ID, &id) != nil {
			return
		}
		process.pendingMu.Lock()
		wait := process.pending[id]
		delete(process.pending, id)
		process.pendingMu.Unlock()
		if wait == nil {
			return
		}
		if message.Error != nil {
			wait <- rpcReply{err: errors.New(message.Error.Message)}
		} else {
			wait <- rpcReply{result: message.Result}
		}
		return
	}
	if message.Method != "" {
		process.notifications <- message
	}
}

func (process *jsonLineProcess) removePending(id int) {
	process.pendingMu.Lock()
	delete(process.pending, id)
	process.pendingMu.Unlock()
}

func (process *jsonLineProcess) finish(err error) {
	process.exitOnce.Do(func() {
		process.pendingMu.Lock()
		for id, wait := range process.pending {
			wait <- rpcReply{err: err}
			delete(process.pending, id)
		}
		process.pendingMu.Unlock()
		process.exit <- err
	})
}

func (process *jsonLineProcess) stderrText() string {
	process.stderrMu.Lock()
	defer process.stderrMu.Unlock()
	return string(bytes.TrimSpace(process.stderr))
}
