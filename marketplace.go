package main

import (
	"context"
	"fmt"
)

func runMarketplace(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing marketplace command")
	}
	switch args[0] {
	case "watches":
		return runMarketplaceWatches(ctx, args[1:])
	case "recommend":
		return marketplaceRecommend(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown marketplace command %q", args[0]))
	}
}

func runMarketplaceWatches(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing marketplace watches command")
	}
	switch args[0] {
	case "list":
		return marketplaceWatchesList(ctx, args[1:])
	case "scan":
		return marketplaceWatchesScan(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown marketplace watches command %q", args[0]))
	}
}

func marketplaceWatchesList(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("marketplace watches list does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var result any
	if err := client.get(ctx, "/marketplace/watches/", &result); err != nil {
		return err
	}
	return writeOK(result)
}

func marketplaceWatchesScan(ctx context.Context, args []string) error {
	watchID, err := parseMarketplaceWatchScanArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var result any
	if err := client.post(ctx, fmt.Sprintf("/marketplace/watches/%s/candidates/", watchID), nil, &result); err != nil {
		return err
	}
	return writeOK(result)
}

func marketplaceRecommend(ctx context.Context, args []string) error {
	id, text, err := parseMarketplaceRecommendArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := map[string]any{
		"recommendation_text": text,
	}
	var result any
	if err := client.post(ctx, fmt.Sprintf("/marketplace/recommendations/%s/", id), body, &result); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"recommendation": result,
	})
}

func parseMarketplaceWatchScanArgs(args []string) (string, error) {
	watchID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--watch-id":
			i++
			if i >= len(args) {
				return "", usageError(args[i-1] + " requires a value")
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

func parseMarketplaceRecommendArgs(args []string) (string, string, error) {
	var id, text string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--candidate-id":
			i++
			if i >= len(args) {
				return "", "", usageError(args[i-1] + " requires a value")
			}
			id = args[i]
		case "--text", "--recommendation-text":
			i++
			if i >= len(args) {
				return "", "", usageError(args[i-1] + " requires a value")
			}
			text = args[i]
		default:
			return "", "", usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if id == "" {
		return "", "", usageError("--id is required")
	}
	if text == "" {
		return "", "", usageError("--text is required")
	}
	return id, text, nil
}
