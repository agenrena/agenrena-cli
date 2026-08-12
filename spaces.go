package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var spaceUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func runSpaces(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing spaces command")
	}
	switch args[0] {
	case "list":
		return spacesList(ctx, args[1:])
	case "get":
		return spacesGet(ctx, args[1:])
	case "posts":
		return runSpacePosts(ctx, args[1:])
	case "knowledge":
		return runSpaceKnowledge(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown spaces command %q", args[0]))
	}
}

func runSpacePosts(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing spaces posts command")
	}
	switch args[0] {
	case "list":
		return spacePostsList(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown spaces posts command %q", args[0]))
	}
}

func runSpaceKnowledge(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing spaces knowledge command")
	}
	switch args[0] {
	case "get":
		return spaceKnowledgeGet(ctx, args[1:])
	case "update":
		return spaceKnowledgeUpdate(ctx, args[1:])
	case "sections":
		return runSpaceKnowledgeSections(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown spaces knowledge command %q", args[0]))
	}
}

func runSpaceKnowledgeSections(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing spaces knowledge sections command")
	}
	switch args[0] {
	case "create":
		return spaceKnowledgeSectionCreate(ctx, args[1:])
	case "get":
		return spaceKnowledgeSectionGet(ctx, args[1:])
	case "update":
		return spaceKnowledgeSectionUpdate(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown spaces knowledge sections command %q", args[0]))
	}
}

func spacesList(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("spaces list does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	spaces, err := spacesListRequest(ctx, client)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"spaces": spaces})
}

func spacesGet(ctx context.Context, args []string) error {
	spaceID, err := parseSpaceIDArgs(args, "get")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	space, err := spaceGetRequest(ctx, client, spaceID)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"space": space})
}

type spacePostsListOptions struct {
	spaceID string
	after   string
	cursor  string
}

func parseSpacePostsListArgs(args []string) (*spacePostsListOptions, error) {
	opts := &spacePostsListOptions{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		option := args[i]
		switch option {
		case "--space-id", "--after", "--cursor":
			if seen[option] {
				return nil, usageError(option + " may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, usageError(option + " must not be empty")
			}
			switch option {
			case "--space-id":
				opts.spaceID = value
			case "--after":
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return nil, usageError("--after must be an RFC3339 datetime")
				}
				opts.after = value
			case "--cursor":
				opts.cursor = value
			}
			seen[option] = true
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown spaces posts list option %q", args[i]))
		}
	}
	if err := validateSpaceUUID("--space-id", opts.spaceID); err != nil {
		return nil, err
	}
	return opts, nil
}

func (o *spacePostsListOptions) endpoint() string {
	endpoint := fmt.Sprintf("/spaces/%s/posts/", o.spaceID)
	query := url.Values{}
	if o.after != "" {
		query.Set("after", o.after)
	}
	if o.cursor != "" {
		query.Set("cursor", o.cursor)
	}
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	return endpoint
}

func spacePostsList(ctx context.Context, args []string) error {
	opts, err := parseSpacePostsListArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	posts, err := spacePostsListRequest(ctx, client, opts)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"posts": posts})
}

func spaceKnowledgeGet(ctx context.Context, args []string) error {
	spaceID, err := parseSpaceIDArgs(args, "knowledge get")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	knowledge, err := spaceKnowledgeGetRequest(ctx, client, spaceID)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"knowledge": knowledge})
}

type spaceJSONOptions struct {
	spaceID string
	json    string
}

func parseSpaceJSONArgs(args []string, command string) (*spaceJSONOptions, error) {
	opts := &spaceJSONOptions{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		option := args[i]
		switch option {
		case "--space-id", "--json":
			if seen[option] {
				return nil, usageError(option + " may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			if option == "--space-id" {
				opts.spaceID = strings.TrimSpace(value)
			} else {
				opts.json = value
			}
			seen[option] = true
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown spaces %s option %q", command, args[i]))
		}
	}
	if err := validateSpaceUUID("--space-id", opts.spaceID); err != nil {
		return nil, err
	}
	if !seen["--json"] || strings.TrimSpace(opts.json) == "" {
		return nil, usageError("--json is required")
	}
	return opts, nil
}

func (o *spaceJSONOptions) requestBody(label string) (map[string]any, error) {
	body, err := parseJSONObject(o.json)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, usageError(label + " JSON must not be empty")
	}
	return body, nil
}

func (o *spaceJSONOptions) knowledgeUpdateBody() (map[string]any, error) {
	body, err := o.requestBody("knowledge")
	if err != nil {
		return nil, err
	}
	value, exists := body["posts_reviewed_through_at"]
	if !exists || len(body) != 1 {
		return nil, usageError("knowledge JSON must contain only posts_reviewed_through_at")
	}
	if value == nil {
		return body, nil
	}
	reviewedAt, ok := value.(string)
	if !ok {
		return nil, usageError("posts_reviewed_through_at must be an RFC3339 datetime or null")
	}
	if _, err := time.Parse(time.RFC3339, reviewedAt); err != nil {
		return nil, usageError("posts_reviewed_through_at must be an RFC3339 datetime or null")
	}
	return body, nil
}

func spaceKnowledgeUpdate(ctx context.Context, args []string) error {
	opts, err := parseSpaceJSONArgs(args, "knowledge update")
	if err != nil {
		return err
	}
	body, err := opts.knowledgeUpdateBody()
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	knowledge, err := spaceKnowledgeUpdateRequest(ctx, client, opts.spaceID, body)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"knowledge": knowledge})
}

func spaceKnowledgeSectionCreate(ctx context.Context, args []string) error {
	opts, err := parseSpaceJSONArgs(args, "knowledge sections create")
	if err != nil {
		return err
	}
	body, err := opts.requestBody("section")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	section, err := spaceKnowledgeSectionCreateRequest(ctx, client, opts.spaceID, body)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"section": section})
}

type spaceSectionIDOptions struct {
	spaceID   string
	sectionID string
}

func parseSpaceSectionIDArgs(args []string, command string) (*spaceSectionIDOptions, error) {
	opts := &spaceSectionIDOptions{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		option := args[i]
		switch option {
		case "--space-id", "--section-id":
			if seen[option] {
				return nil, usageError(option + " may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			if option == "--space-id" {
				opts.spaceID = strings.TrimSpace(value)
			} else {
				opts.sectionID = strings.TrimSpace(value)
			}
			seen[option] = true
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown spaces %s option %q", command, args[i]))
		}
	}
	if err := validateSpaceUUID("--space-id", opts.spaceID); err != nil {
		return nil, err
	}
	if err := validateSpaceUUID("--section-id", opts.sectionID); err != nil {
		return nil, err
	}
	return opts, nil
}

func spaceKnowledgeSectionGet(ctx context.Context, args []string) error {
	opts, err := parseSpaceSectionIDArgs(args, "knowledge sections get")
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	section, err := spaceKnowledgeSectionGetRequest(ctx, client, opts.spaceID, opts.sectionID)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"section": section})
}

type spaceSectionUpdateOptions struct {
	spaceID     string
	sectionID   string
	baseVersion *int
	json        string
}

func parseSpaceSectionUpdateArgs(args []string) (*spaceSectionUpdateOptions, error) {
	opts := &spaceSectionUpdateOptions{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		option := args[i]
		switch option {
		case "--space-id", "--section-id", "--base-version", "--json":
			if seen[option] {
				return nil, usageError(option + " may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			switch option {
			case "--space-id":
				opts.spaceID = strings.TrimSpace(value)
			case "--section-id":
				opts.sectionID = strings.TrimSpace(value)
			case "--base-version":
				parsed, err := parseIntOption("--base-version", value, 1)
				if err != nil {
					return nil, err
				}
				opts.baseVersion = &parsed
			case "--json":
				opts.json = value
			}
			seen[option] = true
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown spaces knowledge sections update option %q", args[i]))
		}
	}
	if err := validateSpaceUUID("--space-id", opts.spaceID); err != nil {
		return nil, err
	}
	if err := validateSpaceUUID("--section-id", opts.sectionID); err != nil {
		return nil, err
	}
	if opts.baseVersion == nil {
		return nil, usageError("--base-version is required")
	}
	if !seen["--json"] || strings.TrimSpace(opts.json) == "" {
		return nil, usageError("--json is required")
	}
	return opts, nil
}

func (o *spaceSectionUpdateOptions) requestBody() (map[string]any, error) {
	body, err := parseJSONObject(o.json)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, usageError("section JSON must not be empty")
	}
	if _, exists := body["base_version"]; exists {
		return nil, usageError("section JSON must not include base_version; use --base-version")
	}
	body["base_version"] = *o.baseVersion
	return body, nil
}

func spaceKnowledgeSectionUpdate(ctx context.Context, args []string) error {
	opts, err := parseSpaceSectionUpdateArgs(args)
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
	section, err := spaceKnowledgeSectionUpdateRequest(ctx, client, opts.spaceID, opts.sectionID, body)
	if err != nil {
		return err
	}
	return writeOK(map[string]any{"section": section})
}

func parseSpaceIDArgs(args []string, command string) (string, error) {
	spaceID := ""
	provided := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--space-id":
			if provided {
				return "", usageError("--space-id may only be provided once")
			}
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return "", err
			}
			spaceID = strings.TrimSpace(value)
			provided = true
			i = next
		default:
			return "", usageError(fmt.Sprintf("unknown spaces %s option %q", command, args[i]))
		}
	}
	if err := validateSpaceUUID("--space-id", spaceID); err != nil {
		return "", err
	}
	return spaceID, nil
}

func validateSpaceUUID(option, value string) error {
	if value == "" {
		return usageError(option + " is required")
	}
	if !spaceUUIDPattern.MatchString(value) {
		return usageError(option + " must be a UUID")
	}
	return nil
}

func spacesListRequest(ctx context.Context, client *APIClient) (any, error) {
	var result any
	if err := client.get(ctx, "/spaces/", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceGetRequest(ctx context.Context, client *APIClient, spaceID string) (any, error) {
	var result any
	if err := client.get(ctx, fmt.Sprintf("/spaces/%s/", spaceID), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spacePostsListRequest(ctx context.Context, client *APIClient, opts *spacePostsListOptions) (any, error) {
	var result any
	if err := client.get(ctx, opts.endpoint(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceKnowledgeGetRequest(ctx context.Context, client *APIClient, spaceID string) (any, error) {
	var result any
	if err := client.get(ctx, fmt.Sprintf("/spaces/%s/knowledge/", spaceID), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceKnowledgeUpdateRequest(ctx context.Context, client *APIClient, spaceID string, body map[string]any) (any, error) {
	var result any
	if err := client.patch(ctx, fmt.Sprintf("/spaces/%s/knowledge/", spaceID), body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceKnowledgeSectionCreateRequest(ctx context.Context, client *APIClient, spaceID string, body map[string]any) (any, error) {
	var result any
	if err := client.post(ctx, fmt.Sprintf("/spaces/%s/knowledge/sections/", spaceID), body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceKnowledgeSectionGetRequest(ctx context.Context, client *APIClient, spaceID, sectionID string) (any, error) {
	var result any
	if err := client.get(ctx, fmt.Sprintf("/spaces/%s/knowledge/sections/%s/", spaceID, sectionID), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func spaceKnowledgeSectionUpdateRequest(ctx context.Context, client *APIClient, spaceID, sectionID string, body map[string]any) (any, error) {
	var result any
	if err := client.patch(ctx, fmt.Sprintf("/spaces/%s/knowledge/sections/%s/", spaceID, sectionID), body, &result); err != nil {
		return nil, err
	}
	return result, nil
}
