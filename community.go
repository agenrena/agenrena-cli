package main

import (
	"context"
	"fmt"
	"os"
)

func runCommunity(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing community command")
	}
	switch args[0] {
	case "drafts":
		return runCommunityDrafts(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown community command %q", args[0]))
	}
}

func runCommunityDrafts(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing community drafts command")
	}
	switch args[0] {
	case "list":
		return communityDraftsList(ctx, args[1:])
	case "get":
		return communityDraftsGet(ctx, args[1:])
	case "add-image":
		return communityDraftsAddImage(ctx, args[1:])
	case "update-text":
		return communityDraftsUpdateText(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown community drafts command %q", args[0]))
	}
}

func communityDraftsAddImage(ctx context.Context, args []string) error {
	opts, err := parseAddImageArgs(args)
	if err != nil {
		return err
	}
	prepared, err := prepareDraftImage(opts.file)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	draft, err := fetchCommunityDraft(ctx, client, opts.draftID)
	if err != nil {
		return err
	}
	revisionValue, ok := numberFromMap(draft, "revision")
	if !ok {
		return apiError("DRAFT_RESPONSE_INVALID", "draft detail did not include revision", false)
	}
	statusValue, _ := stringFromMap(draft, "status")
	if statusValue != "" && statusValue != "draft" {
		return &cliError{
			Code:        "POST_DRAFT_NOT_EDITABLE",
			Message:     fmt.Sprintf("draft status is %q, not draft", statusValue),
			Recoverable: true,
		}
	}

	body := map[string]any{
		"base_revision": int(revisionValue),
		"images": []map[string]any{
			{
				"width":            prepared.Width,
				"height":           prepared.Height,
				"thumbnail_width":  prepared.ThumbnailWidth,
				"thumbnail_height": prepared.ThumbnailHeight,
				"size_bytes":       prepared.SizeBytes,
				"mime_type":        prepared.MimeType,
			},
		},
	}

	var presign draftImagePresignResponse
	if err := client.post(ctx, fmt.Sprintf("/community/drafts/%s/images/presign/", opts.draftID), body, &presign); err != nil {
		return err
	}
	if len(presign.Images) != 1 {
		return apiError("PRESIGN_RESPONSE_INVALID", "server did not return exactly one image upload target", false)
	}
	target := presign.Images[0]
	if target.ImageUploadURL == "" || target.ThumbnailUploadURL == "" {
		return apiError("PRESIGN_RESPONSE_INVALID", "server response did not include upload URLs", false)
	}
	if err := uploadPUT(ctx, target.ImageUploadURL, prepared.MimeType, prepared.ImageBytes); err != nil {
		return err
	}
	if err := uploadPUT(ctx, target.ThumbnailUploadURL, prepared.MimeType, prepared.ThumbnailBytes); err != nil {
		return err
	}

	return writeOK(map[string]any{
		"image": target,
		"processed": map[string]any{
			"source_path":         opts.file,
			"width":               prepared.Width,
			"height":              prepared.Height,
			"thumbnail_width":     prepared.ThumbnailWidth,
			"thumbnail_height":    prepared.ThumbnailHeight,
			"size_bytes":          prepared.SizeBytes,
			"thumbnail_size":      len(prepared.ThumbnailBytes),
			"mime_type":           prepared.MimeType,
			"resized":             prepared.Resized,
			"jpeg_quality":        prepared.Quality,
			"max_long_edge":       draftImageMaxLongEdge,
			"thumbnail_long_edge": draftThumbMaxLongEdge,
		},
		"base_revision": int(revisionValue),
	})
}

func communityDraftsList(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("community drafts list does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var drafts any
	if err := client.get(ctx, "/community/drafts/", &drafts); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"drafts": drafts,
	})
}

func communityDraftsGet(ctx context.Context, args []string) error {
	opts, err := parseDraftIDArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var draft any
	if err := client.get(ctx, fmt.Sprintf("/community/drafts/%s/", opts.draftID), &draft); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"draft": draft,
	})
}

func communityDraftsUpdateText(ctx context.Context, args []string) error {
	opts, err := parseUpdateTextArgs(args)
	if err != nil {
		return err
	}
	textBytes, err := os.ReadFile(opts.textFile)
	if err != nil {
		return wrapError("TEXT_FILE_READ_FAILED", "failed to read text file", err)
	}
	if len(textBytes) > 20000 {
		return &cliError{
			Code:        "TEXT_FILE_TOO_LARGE",
			Message:     "text file is unexpectedly large for a community draft",
			Recoverable: true,
		}
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	draft, err := fetchCommunityDraft(ctx, client, opts.draftID)
	if err != nil {
		return err
	}
	revisionValue, ok := numberFromMap(draft, "revision")
	if !ok {
		return apiError("DRAFT_RESPONSE_INVALID", "draft detail did not include revision", false)
	}
	statusValue, _ := stringFromMap(draft, "status")
	if statusValue != "" && statusValue != "draft" {
		return &cliError{
			Code:        "POST_DRAFT_NOT_EDITABLE",
			Message:     fmt.Sprintf("draft status is %q, not draft", statusValue),
			Recoverable: true,
		}
	}

	body := map[string]any{
		"base_revision": int(revisionValue),
		"text":          string(textBytes),
	}
	var updated any
	if err := client.patch(ctx, fmt.Sprintf("/community/drafts/%s/", opts.draftID), body, &updated); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"draft":         updated,
		"base_revision": int(revisionValue),
	})
}

func fetchCommunityDraft(ctx context.Context, client *APIClient, draftID string) (map[string]any, error) {
	var draft map[string]any
	if err := client.get(ctx, fmt.Sprintf("/community/drafts/%s/", draftID), &draft); err != nil {
		return nil, err
	}
	return draft, nil
}

type draftIDOptions struct {
	draftID string
}

func parseDraftIDArgs(args []string) (*draftIDOptions, error) {
	opts := &draftIDOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--draft-id":
			i++
			if i >= len(args) {
				return nil, usageError("--draft-id requires a value")
			}
			opts.draftID = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.draftID == "" {
		return nil, usageError("--draft-id is required")
	}
	return opts, nil
}

type updateTextOptions struct {
	draftID  string
	textFile string
}

type addImageOptions struct {
	draftID string
	file    string
}

type draftImagePresignResponse struct {
	Images []draftImageUploadTarget `json:"images"`
}

type draftImageUploadTarget struct {
	ID                 string `json:"id"`
	ImageUploadURL     string `json:"image_upload_url"`
	ThumbnailUploadURL string `json:"thumbnail_upload_url"`
	SortOrder          int    `json:"sort_order"`
}

func parseUpdateTextArgs(args []string) (*updateTextOptions, error) {
	opts := &updateTextOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--draft-id":
			i++
			if i >= len(args) {
				return nil, usageError("--draft-id requires a value")
			}
			opts.draftID = args[i]
		case "--text-file":
			i++
			if i >= len(args) {
				return nil, usageError("--text-file requires a path")
			}
			opts.textFile = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.draftID == "" {
		return nil, usageError("--draft-id is required")
	}
	if opts.textFile == "" {
		return nil, usageError("--text-file is required")
	}
	return opts, nil
}

func parseAddImageArgs(args []string) (*addImageOptions, error) {
	opts := &addImageOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--draft-id":
			i++
			if i >= len(args) {
				return nil, usageError("--draft-id requires a value")
			}
			opts.draftID = args[i]
		case "--file":
			i++
			if i >= len(args) {
				return nil, usageError("--file requires a path")
			}
			opts.file = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.draftID == "" {
		return nil, usageError("--draft-id is required")
	}
	if opts.file == "" {
		return nil, usageError("--file is required")
	}
	if _, err := os.Stat(opts.file); err != nil {
		return nil, wrapError("FILE_NOT_FOUND", "image file is not readable", err)
	}
	return opts, nil
}

func authenticatedClient() (*APIClient, error) {
	creds, err := loadCredentials()
	if err != nil {
		return nil, err
	}
	return newAPIClient(creds), nil
}

func numberFromMap(values map[string]any, key string) (float64, bool) {
	value, ok := values[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

func stringFromMap(values map[string]any, key string) (string, bool) {
	value, ok := values[key]
	if !ok {
		return "", false
	}
	typed, ok := value.(string)
	return typed, ok
}
