package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func runPlans(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing plans command")
	}
	switch args[0] {
	case "create":
		return plansCreate(ctx, args[1:])
	case "get":
		return plansGet(ctx, args[1:])
	case "items":
		return runPlanItems(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown plans command %q", args[0]))
	}
}

func runPlanItems(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing plans items command")
	}
	switch args[0] {
	case "add":
		return planItemsAdd(ctx, args[1:])
	case "update":
		return planItemsUpdate(ctx, args[1:])
	case "delete":
		return planItemsDelete(ctx, args[1:])
	case "reorder":
		return planItemsReorder(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown plans items command %q", args[0]))
	}
}

func parseJSONObject(raw string) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, wrapError("JSON_INVALID", "input must be a JSON object", err)
	}
	if value == nil {
		return nil, &cliError{Code: "JSON_INVALID_TYPE", Message: "input must be a JSON object", Recoverable: true}
	}
	return value, nil
}

func parseJSONArray(raw string) ([]any, error) {
	var value []any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, wrapError("JSON_INVALID", "input must be a JSON array", err)
	}
	if value == nil {
		return nil, &cliError{Code: "JSON_INVALID_TYPE", Message: "input must be a JSON array", Recoverable: true}
	}
	return value, nil
}

type planCreateOptions struct {
	json *string
}

func parsePlanCreateArgs(args []string) (*planCreateOptions, error) {
	opts := &planCreateOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.json = &value
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown plans create option %q", args[i]))
		}
	}
	return opts, nil
}

func (o *planCreateOptions) requestBody() (map[string]any, error) {
	if o.json == nil {
		return map[string]any{}, nil
	}
	return parseJSONObject(*o.json)
}

func plansCreate(ctx context.Context, args []string) error {
	opts, err := parsePlanCreateArgs(args)
	if err != nil {
		return err
	}
	body, err := opts.requestBody()
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	var plan any
	if err := client.post(ctx, "/plans/", body, &plan); err != nil {
		return err
	}
	return writeOK(map[string]any{"plan": plan})
}

func parsePlanIDArgs(args []string) (string, error) {
	planID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return "", err
			}
			planID = value
			i = next
		default:
			return "", usageError(fmt.Sprintf("unknown plans get option %q", args[i]))
		}
	}
	if planID == "" {
		return "", usageError("--plan-id is required")
	}
	return planID, nil
}

func plansGet(ctx context.Context, args []string) error {
	planID, err := parsePlanIDArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	var plan any
	if err := client.get(ctx, fmt.Sprintf("/plans/%s/", planID), &plan); err != nil {
		return err
	}
	return writeOK(map[string]any{"plan": plan})
}

type planItemMutationOptions struct {
	planID           string
	itemID           string
	expectedRevision *int
	json             *string
}

func parsePlanItemMutationArgs(args []string, command string, needsItemID, needsInput bool) (*planItemMutationOptions, error) {
	opts := &planItemMutationOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.planID = value
			i = next
		case "--item-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.itemID = value
			i = next
		case "--expected-revision":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			parsed, err := parseIntOption("--expected-revision", value, 0)
			if err != nil {
				return nil, err
			}
			opts.expectedRevision = &parsed
			i = next
		case "--json":
			if !needsInput {
				return nil, usageError(fmt.Sprintf("unknown plans items %s option %q", command, args[i]))
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.json = &value
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown plans items %s option %q", command, args[i]))
		}
	}
	if opts.planID == "" {
		return nil, usageError("--plan-id is required")
	}
	if needsItemID && opts.itemID == "" {
		return nil, usageError("--item-id is required")
	}
	if !needsItemID && opts.itemID != "" {
		return nil, usageError("--item-id is not accepted")
	}
	if opts.expectedRevision == nil {
		return nil, usageError("--expected-revision is required")
	}
	if needsInput && opts.json == nil {
		return nil, usageError("--json is required")
	}
	return opts, nil
}

func (o *planItemMutationOptions) requestBody(requireContent bool) (map[string]any, error) {
	body := map[string]any{}
	if o.json != nil {
		parsed, err := parseJSONObject(*o.json)
		if err != nil {
			return nil, err
		}
		body = parsed
	}
	if _, exists := body["expected_revision"]; exists {
		return nil, usageError("item JSON must not include expected_revision; use --expected-revision")
	}
	if requireContent && len(body) == 0 {
		return nil, usageError("item JSON must not be empty")
	}
	body["expected_revision"] = *o.expectedRevision
	return body, nil
}

func planItemsAdd(ctx context.Context, args []string) error {
	opts, err := parsePlanItemMutationArgs(args, "add", false, true)
	if err != nil {
		return err
	}
	body, err := opts.requestBody(true)
	if err != nil {
		return err
	}
	return mutatePlan(ctx, httpPlanMutation{method: http.MethodPost, endpoint: fmt.Sprintf("/plans/%s/items/", opts.planID), body: body})
}

func planItemsUpdate(ctx context.Context, args []string) error {
	opts, err := parsePlanItemMutationArgs(args, "update", true, true)
	if err != nil {
		return err
	}
	body, err := opts.requestBody(true)
	if err != nil {
		return err
	}
	return mutatePlan(ctx, httpPlanMutation{method: http.MethodPatch, endpoint: fmt.Sprintf("/plans/%s/items/%s/", opts.planID, opts.itemID), body: body})
}

func planItemsDelete(ctx context.Context, args []string) error {
	opts, err := parsePlanItemMutationArgs(args, "delete", true, false)
	if err != nil {
		return err
	}
	body, err := opts.requestBody(false)
	if err != nil {
		return err
	}
	return mutatePlan(ctx, httpPlanMutation{method: http.MethodDelete, endpoint: fmt.Sprintf("/plans/%s/items/%s/", opts.planID, opts.itemID), body: body})
}

type planItemsReorderOptions struct {
	planID           string
	expectedRevision *int
	json             *string
}

func parsePlanItemsReorderArgs(args []string) (*planItemsReorderOptions, error) {
	opts := &planItemsReorderOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.planID = value
			i = next
		case "--expected-revision":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			parsed, err := parseIntOption("--expected-revision", value, 0)
			if err != nil {
				return nil, err
			}
			opts.expectedRevision = &parsed
			i = next
		case "--json":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.json = &value
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown plans items reorder option %q", args[i]))
		}
	}
	if opts.planID == "" {
		return nil, usageError("--plan-id is required")
	}
	if opts.expectedRevision == nil {
		return nil, usageError("--expected-revision is required")
	}
	if opts.json == nil {
		return nil, usageError("--json is required")
	}
	return opts, nil
}

func planItemsReorder(ctx context.Context, args []string) error {
	opts, err := parsePlanItemsReorderArgs(args)
	if err != nil {
		return err
	}
	items, err := parseJSONArray(*opts.json)
	if err != nil {
		return err
	}
	body := map[string]any{
		"expected_revision": *opts.expectedRevision,
		"items":             items,
	}
	return mutatePlan(ctx, httpPlanMutation{method: http.MethodPut, endpoint: fmt.Sprintf("/plans/%s/items/order/", opts.planID), body: body})
}

type httpPlanMutation struct {
	method   string
	endpoint string
	body     map[string]any
}

func mutatePlan(ctx context.Context, mutation httpPlanMutation) error {
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	var plan any
	switch mutation.method {
	case http.MethodPost:
		err = client.post(ctx, mutation.endpoint, mutation.body, &plan)
	case http.MethodPatch:
		err = client.patch(ctx, mutation.endpoint, mutation.body, &plan)
	case http.MethodPut:
		err = client.put(ctx, mutation.endpoint, mutation.body, &plan)
	case http.MethodDelete:
		err = client.delete(ctx, mutation.endpoint, mutation.body, &plan)
	default:
		return fmt.Errorf("unsupported plan mutation method %q", mutation.method)
	}
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"plan": plan})
}
