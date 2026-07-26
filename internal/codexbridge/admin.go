package codexbridge

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func ConfigureBridge(workspace, credentialsDir, apiBase, wsURL string, env map[string]string, home string) (map[string]any, error) {
	resolvedWorkspace := expandPath(workspace, homeDir(home))
	resolvedWorkspace, _ = filepath.Abs(resolvedWorkspace)
	info, err := os.Stat(resolvedWorkspace)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("the requested Codex workspace is not a directory: %s", resolvedWorkspace)
	}
	current, err := LoadBridgeConfig(env, home)
	if err != nil {
		return nil, err
	}
	next := current
	next["version"] = 1
	next["workspace"] = resolvedWorkspace
	if credentialsDir != "" {
		value, _ := filepath.Abs(expandPath(credentialsDir, homeDir(home)))
		next["credentials_dir"] = value
		delete(next, "credentials_file")
	}
	if apiBase != "" {
		next["api_base"] = strings.TrimRight(apiBase, "/")
	}
	if wsURL != "" {
		next["ws_url"] = wsURL
	}
	resolvedAPI := strings.TrimRight(first(configString(next, "api_base"), DefaultAPIBase), "/")
	parsedAPI, err := url.Parse(resolvedAPI)
	if err != nil || parsedAPI.Hostname() == "" || (parsedAPI.Scheme != "http" && parsedAPI.Scheme != "https") {
		return nil, fmt.Errorf("the Agenrena API base must be an absolute http:// or https:// URL")
	}
	resolvedWS := configString(next, "ws_url")
	if resolvedWS == "" {
		resolvedWS, err = DeriveWSURL(resolvedAPI)
		if err != nil {
			return nil, err
		}
	}
	parsedWS, err := url.Parse(resolvedWS)
	if err != nil || parsedWS.Scheme != "wss" || parsedWS.Hostname() == "" {
		return nil, fmt.Errorf("the Agenrena WebSocket URL must use wss://")
	}
	next["api_base"], next["ws_url"] = resolvedAPI, resolvedWS
	credentialFile := CredentialsPath(env, home, next)
	if _, err := LoadAPIKey(credentialFile); err != nil {
		return nil, err
	}
	path := BridgeConfigPath(env, home)
	if err := atomicWriteJSON(path, next, 0o600); err != nil {
		return nil, err
	}
	return map[string]any{
		"config_file": path, "workspace": resolvedWorkspace,
		"credentials_file": credentialFile, "api_base": resolvedAPI, "ws_url": resolvedWS,
	}, nil
}

func PublicBridgeConfig(env map[string]string, home string) (map[string]any, error) {
	value, err := LoadBridgeConfig(env, home)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"configured":       configString(value, "workspace") != "",
		"config_file":      BridgeConfigPath(env, home),
		"workspace":        value["workspace"],
		"credentials_dir":  value["credentials_dir"],
		"credentials_file": value["credentials_file"],
		"api_base":         first(configString(value, "api_base"), DefaultAPIBase),
		"ws_url":           value["ws_url"],
		"sandbox_mode":     first(configString(value, "codex_sandbox_mode"), "read-only"),
		"approval_policy":  first(configString(value, "codex_approval_policy"), "never"),
	}, nil
}
