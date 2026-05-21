package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Credentials struct {
	Version  int    `json:"version,omitempty"`
	AuthType string `json:"auth_type,omitempty"`
	APIKey   string `json:"api_key"`
	APIBase  string `json:"api_base,omitempty"`
	Account  any    `json:"account,omitempty"`
}

func configDir() (string, error) {
	if dir := os.Getenv("AGENRENA_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agenrena"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "agenrena"), nil
}

func credentialsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

func loadCredentials() (*Credentials, error) {
	if key := os.Getenv("AGENRENA_API_KEY"); key != "" {
		return &Credentials{
			Version:  1,
			AuthType: "api_key",
			APIKey:   key,
			APIBase:  apiBaseFromEnv(),
		}, nil
	}

	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, authError("not logged in; run `agenrena auth login`")
		}
		return nil, err
	}

	var creds Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return nil, wrapError("CREDENTIALS_INVALID", "failed to parse credentials", err)
	}
	if creds.APIKey == "" {
		return nil, authError("credentials file does not contain api_key")
	}
	if creds.APIBase == "" {
		creds.APIBase = defaultAPIBase
	}
	return &creds, nil
}

func saveCredentials(creds *Credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	creds.Version = 1
	creds.AuthType = "api_key"
	if creds.APIBase == "" {
		creds.APIBase = defaultAPIBase
	}

	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0600)
}

func removeCredentials() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func apiBaseFromEnv() string {
	if base := os.Getenv("AGENRENA_API_BASE"); base != "" {
		return base
	}
	return defaultAPIBase
}
