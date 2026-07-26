package codexbridge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ProcessStatus struct {
	Running    bool   `json:"running"`
	PID        any    `json:"pid"`
	StartedAt  any    `json:"started_at"`
	RuntimeDir string `json:"runtime_dir"`
	LogFile    string `json:"log_file"`
}

func runtimePaths(env map[string]string, home string) (string, string, string, error) {
	config, err := LoadBridgeConfig(env, home)
	if err != nil {
		return "", "", "", err
	}
	directory := BridgeStateDir(env, home, config)
	return directory, filepath.Join(directory, "process.json"), filepath.Join(directory, "bridge.log"), nil
}

func readProcessFile(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return map[string]any{}
	}
	return result
}

func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func pidIsBridge(pid int) bool {
	if !pidRunning(pid) {
		return false
	}
	result, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return true
	}
	return strings.Contains(
		string(result),
		" codex-bridge daemon",
	)
}

func GetProcessStatus(env map[string]string, home string) (ProcessStatus, error) {
	runtimeDir, processFile, logFile, err := runtimePaths(env, home)
	if err != nil {
		return ProcessStatus{}, err
	}
	state := readProcessFile(processFile)
	pid, _ := strconv.Atoi(stringValue(state["pid"]))
	running := pidIsBridge(pid)
	if !running && len(state) > 0 {
		_ = os.Remove(processFile)
	}
	status := ProcessStatus{Running: running, RuntimeDir: runtimeDir, LogFile: logFile}
	if running {
		status.PID, status.StartedAt = pid, state["started_at"]
	}
	return status, nil
}

func validateRuntime(settings Settings) error {
	path := settings.CodexBin
	if !filepath.IsAbs(path) {
		path, _ = exec.LookPath(path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("Codex executable was not found: %s", settings.CodexBin)
	}
	return nil
}

func StartDaemon(env map[string]string, home string) (ProcessStatus, error) {
	current, err := GetProcessStatus(env, home)
	if err != nil || current.Running {
		return current, err
	}
	executable, err := os.Executable()
	if err != nil {
		return ProcessStatus{}, err
	}
	cwd, _ := os.Getwd()
	settings, err := LoadSettings(env, cwd, home)
	if err != nil {
		return ProcessStatus{}, err
	}
	if err := validateRuntime(settings); err != nil {
		return ProcessStatus{}, err
	}
	runtimeDir, processFile, logFile, err := runtimePaths(env, home)
	if err != nil {
		return ProcessStatus{}, err
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return ProcessStatus{}, err
	}
	logHandle, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ProcessStatus{}, err
	}
	defer logHandle.Close()
	command := exec.Command(executable, "codex-bridge", "daemon")
	command.Dir = settings.CodexWorkspace
	command.Stdin = nil
	command.Stdout, command.Stderr = logHandle, logHandle
	command.Env = mergedEnvironment(env, map[string]string{
		"AGENRENA_BRIDGE_CONFIG_FILE": BridgeConfigPath(env, home),
	})
	command.SysProcAttr = daemonSysProcAttr()
	if err := command.Start(); err != nil {
		return ProcessStatus{}, err
	}
	go func() { _ = command.Wait() }()
	if err := atomicWriteJSON(processFile, map[string]any{
		"pid": command.Process.Pid, "started_at": time.Now().UTC().Format(time.RFC3339Nano),
		"workspace": settings.CodexWorkspace,
	}, 0o600); err != nil {
		_ = command.Process.Kill()
		return ProcessStatus{}, err
	}
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if !pidRunning(command.Process.Pid) {
			tail := TailLog(logFile, 20)
			_ = os.Remove(processFile)
			return ProcessStatus{}, fmt.Errorf("Agenrena bridge exited during startup%s", optionalTail(tail))
		}
		status, _ := GetProcessStatus(env, home)
		if status.Running {
			return status, nil
		}
	}
	return ProcessStatus{}, fmt.Errorf("Agenrena bridge did not report startup. Check %s", logFile)
}

func StopDaemon(env map[string]string, home string) (ProcessStatus, error) {
	current, err := GetProcessStatus(env, home)
	if err != nil || !current.Running {
		return current, err
	}
	pid := current.PID.(int)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for range 50 {
		time.Sleep(100 * time.Millisecond)
		if !pidIsBridge(pid) {
			return GetProcessStatus(env, home)
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return GetProcessStatus(env, home)
}

func mergedEnvironment(overrides map[string]string, additions map[string]string) []string {
	values := currentEnvironment()
	for key, value := range overrides {
		values[key] = value
	}
	for key, value := range additions {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func TailLog(path string, lines int) string {
	handle, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer handle.Close()
	raw, _ := io.ReadAll(handle)
	parts := strings.Split(string(raw), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func optionalTail(tail string) string {
	if strings.TrimSpace(tail) == "" {
		return ""
	}
	return "\n\n" + tail
}
