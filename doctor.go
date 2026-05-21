package main

import (
	"context"
)

func runDoctor(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("doctor does not accept arguments")
	}

	update := checkForUpdate(ctx)
	result := map[string]any{
		"cli_version": cliVersion,
		"api_base":    apiBaseFromEnv(),
		"update":      update,
	}

	path, pathErr := credentialsPath()
	if pathErr == nil {
		result["credentials_path"] = path
	}

	creds, err := loadCredentials()
	if err != nil {
		result["auth"] = map[string]any{
			"logged_in": false,
			"error":     err.Error(),
		}
		return writeOK(result)
	}

	client := newAPIClient(creds)
	result["api_base"] = client.baseURL
	result["auth"] = map[string]any{
		"logged_in": true,
		"source":    credentialsSource(),
	}

	account, err := fetchMe(ctx, client)
	if err != nil {
		result["api_reachable"] = false
		result["auth_valid"] = false
		result["auth_error"] = err.Error()
		return writeOK(result)
	}

	result["api_reachable"] = true
	result["auth_valid"] = true
	result["account"] = account
	return writeOK(result)
}
