package codexbridge

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultAPIBase = "https://api.agenrena.com/api/agent-api"

var DefaultUserAgent = "agenrena-codex-bridge/" + Version + " agenrena-hermes-adapter/0.4.0"

type Settings struct {
	APIKey                  string
	APIBase                 string
	WSURL                   string
	CodexBin                string
	CodexWorkspace          string
	CodexModel              string
	CodexSandboxMode        string
	CodexApprovalPolicy     string
	CodexTurnTimeoutSeconds int
	StateDir                string
	LogLevel                string
	UserAgent               string
}

type environ map[string]string

func currentEnvironment() environ {
	result := environ{}
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func envOrCurrent(values map[string]string) environ {
	if values == nil {
		return currentEnvironment()
	}
	return environ(values)
}

func homeDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	value, _ := os.UserHomeDir()
	return value
}

func BridgeConfigDir(env map[string]string, home string) string {
	e := envOrCurrent(env)
	if value := strings.TrimSpace(e["AGENRENA_BRIDGE_CONFIG_DIR"]); value != "" {
		return expandPath(value, homeDir(home))
	}
	if value := strings.TrimSpace(e["XDG_CONFIG_HOME"]); value != "" {
		return filepath.Join(expandPath(value, homeDir(home)), "agenrena-codex-bridge")
	}
	return filepath.Join(homeDir(home), ".config", "agenrena-codex-bridge")
}

func BridgeConfigPath(env map[string]string, home string) string {
	e := envOrCurrent(env)
	if value := strings.TrimSpace(e["AGENRENA_BRIDGE_CONFIG_FILE"]); value != "" {
		return expandPath(value, homeDir(home))
	}
	return filepath.Join(BridgeConfigDir(env, home), "config.json")
}

func LoadBridgeConfig(env map[string]string, home string) (map[string]any, error) {
	path := BridgeConfigPath(env, home)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read bridge config at %s: %w", path, err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("bridge config at %s is not valid JSON", path)
	}
	if result == nil {
		return nil, fmt.Errorf("bridge config at %s must be a JSON object", path)
	}
	return result, nil
}

func BridgeStateDir(env map[string]string, home string, config map[string]any) string {
	e := envOrCurrent(env)
	if value := strings.TrimSpace(e["BRIDGE_STATE_DIR"]); value != "" {
		return expandPath(value, homeDir(home))
	}
	if value := configString(config, "state_dir"); value != "" {
		return expandPath(value, homeDir(home))
	}
	if value := strings.TrimSpace(e["XDG_STATE_HOME"]); value != "" {
		return filepath.Join(expandPath(value, homeDir(home)), "agenrena-codex-bridge")
	}
	return filepath.Join(homeDir(home), ".local", "state", "agenrena-codex-bridge")
}

func CredentialsPath(env map[string]string, home string, config map[string]any) string {
	e := envOrCurrent(env)
	baseHome := homeDir(home)
	if value := strings.TrimSpace(e["AGENRENA_CREDENTIALS_FILE"]); value != "" {
		return expandPath(value, baseHome)
	}
	if value := configString(config, "credentials_file"); value != "" {
		return expandPath(value, baseHome)
	}
	if value := strings.TrimSpace(e["AGENRENA_CONFIG_DIR"]); value != "" {
		return filepath.Join(expandPath(value, baseHome), "credentials.json")
	}
	if value := configString(config, "credentials_dir"); value != "" {
		return filepath.Join(expandPath(value, baseHome), "credentials.json")
	}
	if value := strings.TrimSpace(e["XDG_CONFIG_HOME"]); value != "" {
		return filepath.Join(expandPath(value, baseHome), "agenrena", "credentials.json")
	}
	return filepath.Join(baseHome, ".config", "agenrena", "credentials.json")
}

func LoadAPIKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("Agenrena credentials were not found at %s. Run `agenrena auth login` or set AGENRENA_CONFIG_DIR", path)
	}
	if err != nil {
		return "", fmt.Errorf("could not read Agenrena credentials at %s: %w", path, err)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return "", fmt.Errorf("Agenrena credentials at %s are not valid JSON", path)
	}
	key := configString(value, "api_key")
	if key == "" {
		return "", fmt.Errorf("Agenrena credentials at %s do not contain api_key", path)
	}
	if !strings.HasPrefix(key, "agr_") {
		return "", fmt.Errorf("Agenrena credentials at %s contain an invalid API key", path)
	}
	return key, nil
}

func DeriveWSURL(apiBase string) (string, error) {
	parsed, err := url.Parse(apiBase)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("AGENRENA_API_BASE must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("AGENRENA_API_BASE must use http or https")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "/ws/agent/events/", "", "", ""
	return parsed.String(), nil
}

func LoadSettings(env map[string]string, cwd, home string) (Settings, error) {
	e := envOrCurrent(env)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	config, err := LoadBridgeConfig(env, home)
	if err != nil {
		return Settings{}, err
	}
	apiKey, err := LoadAPIKey(CredentialsPath(env, home, config))
	if err != nil {
		return Settings{}, err
	}
	apiBase := strings.TrimRight(first(e["AGENRENA_API_BASE"], configString(config, "api_base"), DefaultAPIBase), "/")
	wsURL := first(e["AGENRENA_WS_URL"], configString(config, "ws_url"))
	if wsURL == "" {
		wsURL, err = DeriveWSURL(apiBase)
		if err != nil {
			return Settings{}, err
		}
	}
	parsedWS, err := url.Parse(strings.TrimSpace(wsURL))
	if err != nil || parsedWS.Scheme != "wss" || parsedWS.Hostname() == "" {
		return Settings{}, fmt.Errorf("AGENRENA_WS_URL must be an absolute wss:// URL")
	}

	workspace := expandPath(first(e["CODEX_WORKSPACE"], configString(config, "workspace"), cwd), homeDir(home))
	workspace, _ = filepath.Abs(workspace)
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return Settings{}, fmt.Errorf("CODEX_WORKSPACE is not a directory: %s", workspace)
	}
	timeoutText := first(e["CODEX_TURN_TIMEOUT_SECONDS"], configString(config, "codex_turn_timeout_seconds"), "900")
	timeout, err := strconv.Atoi(timeoutText)
	if err != nil {
		return Settings{}, fmt.Errorf("CODEX_TURN_TIMEOUT_SECONDS must be an integer")
	}
	if timeout <= 0 {
		return Settings{}, fmt.Errorf("CODEX_TURN_TIMEOUT_SECONDS must be greater than zero")
	}
	return Settings{
		APIKey: apiKey, APIBase: apiBase, WSURL: strings.TrimSpace(wsURL),
		CodexBin:                first(e["CODEX_BIN"], configString(config, "codex_bin"), "codex"),
		CodexWorkspace:          workspace,
		CodexModel:              strings.TrimSpace(first(e["CODEX_MODEL"], configString(config, "codex_model"))),
		CodexSandboxMode:        strings.TrimSpace(first(e["CODEX_SANDBOX_MODE"], configString(config, "codex_sandbox_mode"), "read-only")),
		CodexApprovalPolicy:     strings.TrimSpace(first(e["CODEX_APPROVAL_POLICY"], configString(config, "codex_approval_policy"), "never")),
		CodexTurnTimeoutSeconds: timeout,
		StateDir:                BridgeStateDir(env, home, config),
		LogLevel:                strings.ToUpper(first(e["LOG_LEVEL"], configString(config, "log_level"), "INFO")),
		UserAgent:               first(e["AGENRENA_USER_AGENT"], configString(config, "user_agent"), DefaultUserAgent),
	}, nil
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func configString(config map[string]any, key string) string {
	if config == nil || config[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(config[key]))
}

func expandPath(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
