package main

import (
	"context"
	"fmt"
	"strings"
)

func runUsers(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing users command")
	}
	switch args[0] {
	case "search":
		return usersSearch(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown users command %q", args[0]))
	}
}

func usersSearch(ctx context.Context, args []string) error {
	query, err := parseQueryArg(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var results any
	if err := client.post(ctx, "/users/search/", map[string]any{"query": query}, &results); err != nil {
		return err
	}
	return writeOK(results)
}

func parseQueryArg(args []string) (string, error) {
	query := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--query":
			i++
			if i >= len(args) {
				return "", usageError("--query requires a value")
			}
			query = strings.TrimSpace(args[i])
		default:
			return "", usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if query == "" {
		return "", usageError("--query is required")
	}
	return query, nil
}
