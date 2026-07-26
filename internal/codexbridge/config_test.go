package codexbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPreserveConfigPrecedenceAndDoNotExposeKey(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "bridge")
	credentialsDir := filepath.Join(root, "agenrena")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(credentialsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONTest(t, filepath.Join(credentialsDir, "credentials.json"), map[string]any{"api_key": "agr_secret"})
	writeJSONTest(t, filepath.Join(configDir, "config.json"), map[string]any{
		"workspace": root, "credentials_dir": credentialsDir,
		"api_base": "https://example.com/api/agent-api",
	})
	env := map[string]string{
		"AGENRENA_BRIDGE_CONFIG_DIR": configDir,
		"CODEX_SANDBOX_MODE":         "workspace-write",
	}
	settings, err := LoadSettings(env, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if settings.APIKey != "agr_secret" || settings.WSURL != "wss://example.com/ws/agent/events/" ||
		settings.CodexSandboxMode != "workspace-write" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	public, err := PublicBridgeConfig(env, root)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	if string(raw) == "" || contains(string(raw), "agr_secret") {
		t.Fatalf("public config leaked secret: %s", raw)
	}
}

func TestConfigureBridgeValidatesCredentialsAndWritesPrivateConfig(t *testing.T) {
	root := t.TempDir()
	credentialsDir := filepath.Join(root, "credentials")
	if err := os.MkdirAll(credentialsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONTest(t, filepath.Join(credentialsDir, "credentials.json"), map[string]any{"api_key": "agr_key"})
	env := map[string]string{"AGENRENA_BRIDGE_CONFIG_DIR": filepath.Join(root, "bridge")}
	result, err := ConfigureBridge(root, credentialsDir, "", "", env, root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(result["config_file"].(string))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func writeJSONTest(t *testing.T, path string, value any) {
	t.Helper()
	raw, _ := json.Marshal(value)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
