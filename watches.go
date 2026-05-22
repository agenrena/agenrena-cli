package main

import (
	"context"
	"fmt"
)

func runWatches(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing watches command")
	}
	switch args[0] {
	case "list":
		return watchesList(ctx, args[1:])
	case "scan":
		return watchesScan(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown watches command %q", args[0]))
	}
}

func watchesList(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("watches list does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var watches any
	if err := client.get(ctx, "/community/topic-watches/", &watches); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"watches": watches,
	})
}

func watchesScan(ctx context.Context, args []string) error {
	watchID, err := parseWatchScanArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var scan any
	if err := client.post(ctx, fmt.Sprintf("/community/topic-watches/%s/candidates/", watchID), nil, &scan); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"watch_id": watchID,
		"scan":     scan,
	})
}

func parseWatchScanArgs(args []string) (string, error) {
	watchID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			i++
			if i >= len(args) {
				return "", usageError("--id requires a value")
			}
			watchID = args[i]
		default:
			return "", usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if watchID == "" {
		return "", usageError("--id is required")
	}
	return watchID, nil
}
