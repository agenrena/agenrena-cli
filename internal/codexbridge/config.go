package codexbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	Version               = "1.0.0"
	ProtocolVersion       = 1
	configVersion         = 2
	threadToolsVersion    = 1
	maxCompletedIDs       = 5000
	maxOutboundMediaCount = 9
	maxOutboundMediaBytes = 20 * 1024 * 1024
	maxTotalOutboundMedia = 50 * 1024 * 1024
	defaultTurnTimeout    = 15 * time.Minute
	handoffToolName       = "handoff_to_human"
)

type fileConfig struct {
	Version   int    `json:"version"`
	Workspace string `json:"workspace"`
}

type Settings struct {
	Workspace       string
	CodexBin        string
	AgenrenaBin     string
	CodexCommand    []string
	AgenrenaCommand []string
	Model           string
	SandboxMode     string
	ApprovalPolicy  string
	TurnTimeout     time.Duration
	CallsEnabled    bool
	RealtimeVersion string
	RealtimeModel   string
	RealtimeVoice   string
}

type PublicConfig struct {
	Configured      bool   `json:"configured"`
	ConfigFile      string `json:"configFile"`
	Workspace       any    `json:"workspace"`
	SandboxMode     string `json:"sandboxMode"`
	ApprovalPolicy  string `json:"approvalPolicy"`
	CallsEnabled    bool   `json:"callsEnabled"`
	RealtimeVersion string `json:"realtimeVersion,omitempty"`
	RealtimeModel   any    `json:"realtimeModel,omitempty"`
}

func expandPath(value string) string {
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	if value == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~/"))
}

func ConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("AGENRENA_CODEX_BRIDGE_CONFIG_FILE")); value != "" {
		return expandPath(value)
	}
	root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(expandPath(root), "agenrena-codex-bridge", "config.json")
}

func StateDir() string {
	if value := strings.TrimSpace(os.Getenv("AGENRENA_CODEX_BRIDGE_STATE_DIR")); value != "" {
		return expandPath(value)
	}
	root := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(expandPath(root), "agenrena-codex-bridge")
}

func readJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read JSON at %s: %w", path, err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("could not read JSON at %s: %w", path, err)
	}
	return nil
}

func atomicWriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%d.%d.tmp", os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func loadConfig() (fileConfig, error) {
	var config fileConfig
	err := readJSON(ConfigPath(), &config)
	return config, err
}

func Configure(workspace string) (map[string]any, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	resolved, err := filepath.Abs(expandPath(workspace))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("the requested Codex workspace is not a directory: %s", resolved)
	}
	if err := atomicWriteJSON(ConfigPath(), fileConfig{Version: configVersion, Workspace: resolved}); err != nil {
		return nil, err
	}
	return map[string]any{
		"configured": true,
		"configFile": ConfigPath(),
		"workspace":  resolved,
		"nextStep":   "Call agenrena_bridge_start.",
	}, nil
}

func CurrentPublicConfig() (PublicConfig, error) {
	config, err := loadConfig()
	if err != nil {
		return PublicConfig{}, err
	}
	configured := config.Version == configVersion && config.Workspace != ""
	workspace := any(nil)
	if configured {
		workspace = config.Workspace
	}
	callsEnabled := envBool("AGENRENA_CODEX_BRIDGE_CALLS", false)
	realtimeModel := any(nil)
	if callsEnabled {
		if value := strings.TrimSpace(os.Getenv("CODEX_REALTIME_MODEL")); value != "" {
			realtimeModel = value
		}
	}
	return PublicConfig{
		Configured:      configured,
		ConfigFile:      ConfigPath(),
		Workspace:       workspace,
		SandboxMode:     envOr("CODEX_SANDBOX_MODE", "read-only"),
		ApprovalPolicy:  envOr("CODEX_APPROVAL_POLICY", "never"),
		CallsEnabled:    callsEnabled,
		RealtimeVersion: envOr("CODEX_REALTIME_VERSION", "v3"),
		RealtimeModel:   realtimeModel,
	}, nil
}

func LoadSettings() (Settings, error) {
	config, err := loadConfig()
	if err != nil {
		return Settings{}, err
	}
	if config.Version != configVersion || config.Workspace == "" {
		return Settings{}, errors.New("the bridge is not configured for plugin 1.0. Call agenrena_bridge_setup first")
	}
	timeout := defaultTurnTimeout
	if raw := strings.TrimSpace(os.Getenv("CODEX_TURN_TIMEOUT_SECONDS")); raw != "" {
		seconds, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil || seconds <= 0 {
			return Settings{}, errors.New("CODEX_TURN_TIMEOUT_SECONDS must be greater than zero")
		}
		timeout = time.Duration(seconds * float64(time.Second))
	}
	agenrenaBin := strings.TrimSpace(os.Getenv("AGENRENA_BIN"))
	if agenrenaBin == "" {
		agenrenaBin, err = os.Executable()
		if err != nil {
			return Settings{}, fmt.Errorf("resolve Agenrena executable: %w", err)
		}
	}
	workspace, err := filepath.Abs(expandPath(config.Workspace))
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Workspace:       workspace,
		CodexBin:        envOr("CODEX_BIN", "codex"),
		AgenrenaBin:     agenrenaBin,
		Model:           strings.TrimSpace(os.Getenv("CODEX_MODEL")),
		SandboxMode:     envOr("CODEX_SANDBOX_MODE", "read-only"),
		ApprovalPolicy:  envOr("CODEX_APPROVAL_POLICY", "never"),
		TurnTimeout:     timeout,
		CallsEnabled:    envBool("AGENRENA_CODEX_BRIDGE_CALLS", false),
		RealtimeVersion: envOr("CODEX_REALTIME_VERSION", "v3"),
		RealtimeModel:   strings.TrimSpace(os.Getenv("CODEX_REALTIME_MODEL")),
		RealtimeVoice:   strings.TrimSpace(os.Getenv("CODEX_REALTIME_VOICE")),
	}, nil
}

func ValidateRuntime(settings Settings) error {
	if _, err := exec.LookPath(settings.AgenrenaBin); err != nil {
		return fmt.Errorf("Agenrena CLI was not found: %s", settings.AgenrenaBin)
	}
	if _, err := exec.LookPath(settings.CodexBin); err != nil {
		return fmt.Errorf("Codex executable was not found on PATH: %s", settings.CodexBin)
	}
	if settings.TurnTimeout <= 0 {
		return errors.New("CODEX_TURN_TIMEOUT_SECONDS must be greater than zero")
	}
	if settings.CallsEnabled && settings.RealtimeVersion != "v1" && settings.RealtimeVersion != "v2" && settings.RealtimeVersion != "v3" {
		return errors.New("CODEX_REALTIME_VERSION must be v1, v2, or v3 for WebRTC")
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
