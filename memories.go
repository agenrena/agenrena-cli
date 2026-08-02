package main

import (
	"context"
	"fmt"
	"regexp"
)

const (
	memorySearchKeywordMax = 30
	memoryReadBatchMax     = 5
)

var memoryUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func runMemories(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing memories command")
	}

	switch args[0] {
	case "create":
		return memoriesCreate(ctx, args[1:])
	case "search":
		return memoriesSearch(ctx, args[1:])
	case "read":
		return memoriesRead(ctx, args[1:])
	case "forget":
		return memoriesForget(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown memories command %q", args[0]))
	}
}

func memoriesCreate(ctx context.Context, args []string) error {
	rawJSON, err := parseMemoryCreateArgs(args)
	if err != nil {
		return err
	}
	body, err := parseJSONObject(rawJSON)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	result, err := memoryCreateRequest(ctx, client, body)
	if err != nil {
		return err
	}
	return writeOK(result)
}

func parseMemoryCreateArgs(args []string) (string, error) {
	rawJSON := ""
	jsonProvided := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			if jsonProvided {
				return "", usageError("--json may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return "", err
			}
			rawJSON = value
			jsonProvided = true
			i = next
		default:
			return "", usageError(fmt.Sprintf("unknown memories create option %q", args[i]))
		}
	}
	if rawJSON == "" {
		return "", usageError("--json is required")
	}
	return rawJSON, nil
}

type memorySearchOptions struct {
	keywords  []string
	cursor    string
	hasCursor bool
}

func memoriesSearch(ctx context.Context, args []string) error {
	opts, err := parseMemorySearchArgs(args)
	if err != nil {
		return err
	}
	body := map[string]any{"keywords": opts.keywords}
	if opts.cursor != "" {
		body["cursor"] = opts.cursor
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	result, err := memorySearchRequest(ctx, client, body)
	if err != nil {
		return err
	}
	return writeOK(result)
}

func parseMemorySearchArgs(args []string) (*memorySearchOptions, error) {
	opts := &memorySearchOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--keyword":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.keywords = append(opts.keywords, value)
			i = next
		case "--cursor":
			if opts.hasCursor {
				return nil, usageError("--cursor may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.cursor = value
			opts.hasCursor = true
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown memories search option %q", args[i]))
		}
	}
	if len(opts.keywords) == 0 {
		return nil, usageError("at least one --keyword is required")
	}
	if len(opts.keywords) > memorySearchKeywordMax {
		return nil, usageError(fmt.Sprintf("at most %d --keyword values are allowed", memorySearchKeywordMax))
	}
	return opts, nil
}

type memoryReadOptions struct {
	memoryIDs []string
}

func memoriesRead(ctx context.Context, args []string) error {
	opts, err := parseMemoryReadArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	result, err := memoryReadRequest(ctx, client, map[string]any{"memory_ids": opts.memoryIDs})
	if err != nil {
		return err
	}
	return writeOK(result)
}

func parseMemoryReadArgs(args []string) (*memoryReadOptions, error) {
	opts := &memoryReadOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--memory-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			if !memoryUUIDPattern.MatchString(value) {
				return nil, usageError("--memory-id must be a UUID")
			}
			opts.memoryIDs = append(opts.memoryIDs, value)
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown memories read option %q", args[i]))
		}
	}
	if len(opts.memoryIDs) == 0 {
		return nil, usageError("at least one --memory-id is required")
	}
	if len(opts.memoryIDs) > memoryReadBatchMax {
		return nil, usageError(fmt.Sprintf("at most %d --memory-id values are allowed", memoryReadBatchMax))
	}
	return opts, nil
}

func memoriesForget(ctx context.Context, args []string) error {
	memoryID, err := parseMemoryForgetArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	if err := memoryForgetRequest(ctx, client, memoryID); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"memory_id": memoryID,
		"forgotten": true,
	})
}

func parseMemoryForgetArgs(args []string) (string, error) {
	memoryID := ""
	memoryIDProvided := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--memory-id":
			if memoryIDProvided {
				return "", usageError("--memory-id may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return "", err
			}
			if !memoryUUIDPattern.MatchString(value) {
				return "", usageError("--memory-id must be a UUID")
			}
			memoryID = value
			memoryIDProvided = true
			i = next
		default:
			return "", usageError(fmt.Sprintf("unknown memories forget option %q", args[i]))
		}
	}
	if memoryID == "" {
		return "", usageError("--memory-id is required")
	}
	return memoryID, nil
}

func memoryCreateRequest(ctx context.Context, client *APIClient, body map[string]any) (map[string]any, error) {
	var result map[string]any
	if err := client.post(ctx, "/memories/", body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func memorySearchRequest(ctx context.Context, client *APIClient, body map[string]any) (map[string]any, error) {
	var result map[string]any
	if err := client.post(ctx, "/memories/search/", body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func memoryReadRequest(ctx context.Context, client *APIClient, body map[string]any) (map[string]any, error) {
	var result map[string]any
	if err := client.post(ctx, "/memories/read/", body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func memoryForgetRequest(ctx context.Context, client *APIClient, memoryID string) error {
	return client.delete(ctx, fmt.Sprintf("/memories/%s/", memoryID), nil, nil)
}
