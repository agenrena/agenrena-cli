package codexbridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type processFiles struct {
	Root        string
	ProcessFile string
	LogFile     string
	StateFile   string
}

type processRecord struct {
	PID       int    `json:"pid,omitempty"`
	State     string `json:"state,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ProcessStatus struct {
	Running    bool   `json:"running"`
	PID        any    `json:"pid"`
	State      string `json:"state"`
	StartedAt  any    `json:"startedAt"`
	Error      any    `json:"error"`
	RuntimeDir string `json:"runtimeDir"`
	LogFile    string `json:"logFile"`
}

var daemonCommand = func() (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(executable, "codex", "bridge", "daemon"), nil
}

func ProcessFiles() processFiles {
	root := StateDir()
	return processFiles{
		Root: root, ProcessFile: filepath.Join(root, "process.json"),
		LogFile: filepath.Join(root, "bridge.log"), StateFile: filepath.Join(root, "state.json"),
	}
}

func CurrentProcessStatus() (ProcessStatus, error) {
	paths := ProcessFiles()
	var record processRecord
	if err := readJSON(paths.ProcessFile, &record); err != nil {
		return ProcessStatus{}, err
	}
	running := pidIsAlive(record.PID)
	state := "stopped"
	if running {
		state = record.State
		if state == "" {
			state = "running"
		}
	} else if record.State == "fatal" {
		state = "fatal"
	}
	status := ProcessStatus{
		Running: running, PID: nil, State: state, StartedAt: nil, Error: nil,
		RuntimeDir: paths.Root, LogFile: paths.LogFile,
	}
	if running {
		status.PID = record.PID
	}
	if record.StartedAt != "" {
		status.StartedAt = record.StartedAt
	}
	if record.Error != "" {
		status.Error = record.Error
	}
	return status, nil
}

func StartDaemon() (ProcessStatus, error) {
	current, err := CurrentProcessStatus()
	if err != nil {
		return ProcessStatus{}, err
	}
	if current.Running {
		return current, nil
	}
	settings, err := LoadSettings()
	if err != nil {
		return ProcessStatus{}, err
	}
	if err := ValidateRuntime(settings); err != nil {
		return ProcessStatus{}, err
	}
	paths := ProcessFiles()
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return ProcessStatus{}, err
	}
	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ProcessStatus{}, err
	}
	defer logFile.Close()
	cmd, err := daemonCommand()
	if err != nil {
		return ProcessStatus{}, err
	}
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "AGENRENA_BIN="+settings.AgenrenaBin)
	detachCommand(cmd)
	if err := cmd.Start(); err != nil {
		return ProcessStatus{}, err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		status, statusErr := CurrentProcessStatus()
		if statusErr != nil {
			return ProcessStatus{}, statusErr
		}
		if status.Running && status.State == "connected" {
			return status, nil
		}
		if status.State == "fatal" {
			if message, ok := status.Error.(string); ok && message != "" {
				return ProcessStatus{}, errors.New(message)
			}
			return ProcessStatus{}, fmt.Errorf("Agenrena bridge exited during startup. Check %s", paths.LogFile)
		}
	}
	return ProcessStatus{}, fmt.Errorf("Agenrena bridge did not connect. Check %s", paths.LogFile)
}

func StopDaemon() (ProcessStatus, error) {
	current, err := CurrentProcessStatus()
	if err != nil {
		return ProcessStatus{}, err
	}
	if !current.Running {
		return current, nil
	}
	pid, _ := strconv.Atoi(fmt.Sprint(current.PID))
	_ = terminatePID(pid)
	for index := 0; index < 50; index++ {
		time.Sleep(100 * time.Millisecond)
		status, statusErr := CurrentProcessStatus()
		if statusErr == nil && !status.Running {
			break
		}
	}
	status, err := CurrentProcessStatus()
	if err != nil {
		return ProcessStatus{}, err
	}
	if status.Running {
		_ = killPID(pid)
	}
	_ = os.Remove(ProcessFiles().ProcessFile)
	return CurrentProcessStatus()
}

func writeDaemonStatus(record processRecord) error {
	return atomicWriteJSON(ProcessFiles().ProcessFile, record)
}

func RunDaemon(parent context.Context) error {
	settings, err := LoadSettings()
	if err != nil {
		return err
	}
	if err := ValidateRuntime(settings); err != nil {
		return err
	}
	ctx, cancel := daemonSignalContext(parent)
	defer cancel()
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	writeStatus := func(state, message string) {
		_ = writeDaemonStatus(processRecord{PID: os.Getpid(), State: state, StartedAt: startedAt, Error: message})
	}
	writeStatus("connecting", "")
	bridge, err := startAgentBridge(settings)
	if err != nil {
		writeStatus("fatal", err.Error())
		return err
	}
	defer bridge.Shutdown()
	service := newBridgeService(bridge, codexRunner{settings: settings}, NewStateStore(ProcessFiles().StateFile), newCallManager(bridge, settings), func(value map[string]any) {
		state := stringValue(value["state"])
		if state == "" {
			state = "connected"
		}
		message := ""
		if rpcErr := mapValue(value["error"]); len(rpcErr) > 0 {
			message = stringValue(rpcErr["message"])
		}
		writeStatus(state, message)
	})
	if err := service.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		writeStatus("fatal", err.Error())
		return err
	}
	_ = writeDaemonStatus(processRecord{State: "stopped", StartedAt: startedAt})
	return nil
}
