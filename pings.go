package main

import (
	"context"
	"fmt"
)

func runPings(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing pings command")
	}
	switch args[0] {
	case "scan":
		return pingsScan(ctx, args[1:])
	case "recommend":
		return pingsRecommend(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown pings command %q", args[0]))
	}
}

func pingsScan(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("pings scan does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var result any
	if err := client.post(ctx, "/pings/candidates/", nil, &result); err != nil {
		return err
	}
	return writeOK(result)
}

func pingsRecommend(ctx context.Context, args []string) error {
	id, reason, err := parsePingsRecommendArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := map[string]any{
		"reason": reason,
	}
	var result any
	if err := client.post(ctx, fmt.Sprintf("/pings/recommendations/%s/", id), body, &result); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"recommendation": result,
	})
}

func parsePingsRecommendArgs(args []string) (string, string, error) {
	var id, reason string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			i++
			if i >= len(args) {
				return "", "", usageError("--id requires a value")
			}
			id = args[i]
		case "--reason":
			i++
			if i >= len(args) {
				return "", "", usageError("--reason requires a value")
			}
			reason = args[i]
		default:
			return "", "", usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if id == "" {
		return "", "", usageError("--id is required")
	}
	if reason == "" {
		return "", "", usageError("--reason is required")
	}
	return id, reason, nil
}
