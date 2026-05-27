package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

func runAuth(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing auth command")
	}
	switch args[0] {
	case "login":
		return authLogin(ctx)
	case "status":
		return authStatus(ctx)
	case "logout":
		return authLogout()
	default:
		return usageError(fmt.Sprintf("unknown auth command %q", args[0]))
	}
}

func authLogin(ctx context.Context) error {
	key, source, err := readLoginAPIKey()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(key, "agr_") {
		return authError("API key must start with agr_")
	}

	creds := &Credentials{
		Version:  1,
		AuthType: "api_key",
		APIKey:   key,
	}
	client := newAPIClient(creds)
	account, err := fetchMe(ctx, client)
	if err != nil {
		return err
	}
	creds.Account = account
	if err := saveCredentials(creds); err != nil {
		return wrapError("CREDENTIALS_WRITE_FAILED", "failed to save credentials", err)
	}

	path, _ := credentialsPath()
	return writeOK(map[string]any{
		"logged_in":        true,
		"credentials_path": path,
		"api_base":         apiBaseFromEnv(),
		"account":          account,
		"source":           source,
	})
}

func readLoginAPIKey() (string, string, error) {
	envKey := strings.TrimSpace(os.Getenv("AGENRENA_API_KEY"))
	if envKey != "" {
		fmt.Fprint(os.Stderr, "Use AGENRENA_API_KEY from this environment to log in? [Y/n]: ")
		answer, err := readSecretLine()
		if err != nil {
			return "", "", err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "" || answer == "y" || answer == "yes" {
			return envKey, "env_import", nil
		}
	}

	fmt.Fprint(os.Stderr, "Agenrena API key: ")
	key, err := readSecretLine()
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(key), "prompt", nil
}

func readSecretLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", wrapError("INPUT_ERROR", "failed to read API key", err)
	}
	return line, nil
}

func authStatus(ctx context.Context) error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	client := newAPIClient(creds)
	account, err := fetchMe(ctx, client)
	if err != nil {
		return err
	}
	path, _ := credentialsPath()
	return writeOK(map[string]any{
		"logged_in":        true,
		"credentials_path": path,
		"api_base":         client.baseURL,
		"account":          account,
		"source":           "file",
	})
}

func authLogout() error {
	if err := removeCredentials(); err != nil {
		return wrapError("CREDENTIALS_REMOVE_FAILED", "failed to remove credentials", err)
	}
	return writeOK(map[string]any{
		"logged_in": false,
	})
}

func fetchMe(ctx context.Context, client *APIClient) (map[string]any, error) {
	var account map[string]any
	if err := client.get(ctx, "/agents/me/", &account); err != nil {
		return nil, err
	}
	return account, nil
}
