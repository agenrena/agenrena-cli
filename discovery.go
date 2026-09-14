package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

func runDiscovery(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing discovery command")
	}
	switch args[0] {
	case "tasks":
		return runDiscoveryTasks(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown discovery command %q", args[0]))
	}
}

func runDiscoveryTasks(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing discovery tasks command")
	}
	switch args[0] {
	case "create":
		return discoveryTaskCreate(ctx, args[1:])
	case "get":
		return discoveryTaskGet(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown discovery tasks command %q", args[0]))
	}
}

func parseRequiredJSONOption(args []string, command string) (map[string]any, error) {
	raw := ""
	seen := false
	for i := 0; i < len(args); i++ {
		if args[i] != "--json" {
			return nil, usageError(fmt.Sprintf("unknown %s option %q", command, args[i]))
		}
		if seen {
			return nil, usageError("--json may only be provided once")
		}
		value, next, err := requiredOptionValue(args, i)
		if err != nil {
			return nil, err
		}
		raw, seen, i = value, true, next
	}
	if !seen || strings.TrimSpace(raw) == "" {
		return nil, usageError("--json is required")
	}
	return parseJSONObject(raw)
}

func discoveryTaskCreate(ctx context.Context, args []string) error {
	body, err := parseRequiredJSONOption(args, "discovery tasks create")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	var task any
	if err := client.post(ctx, "/discovery/tasks/", body, &task); err != nil {
		return err
	}
	return writeOK(map[string]any{"task": task})
}

func parseDiscoveryIDArgs(args []string, option, command string) (string, error) {
	id := ""
	for i := 0; i < len(args); i++ {
		if args[i] != option {
			return "", usageError(fmt.Sprintf("unknown %s option %q", command, args[i]))
		}
		if id != "" {
			return "", usageError(option + " may only be provided once")
		}
		value, next, err := requiredOptionValue(args, i)
		if err != nil {
			return "", err
		}
		id, i = strings.TrimSpace(value), next
	}
	if id == "" {
		return "", usageError(option + " is required")
	}
	return id, nil
}

func discoveryTaskGet(ctx context.Context, args []string) error {
	taskID, err := parseDiscoveryIDArgs(args, "--task-id", "discovery tasks get")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	var task any
	endpoint := "/discovery/tasks/" + url.PathEscape(taskID) + "/"
	if err := client.get(ctx, endpoint, &task); err != nil {
		return err
	}
	return writeOK(map[string]any{"task": task})
}
