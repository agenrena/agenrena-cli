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
	case "create":
		return communityDraftsCreate(ctx, args[1:])
	case "update":
		return communityDraftsUpdate(ctx, args[1:])
	case "add-image":
		return communityDraftsAddImage(ctx, args[1:])
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

	body := map[string]any{
		"base_revision": *opts.baseRevision,
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
		"base_revision": *opts.baseRevision,
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

func communityDraftsCreate(ctx context.Context, args []string) error {
	opts, err := parseCreateDraftArgs(args)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := map[string]any{
		"title": opts.title,
		"text":  opts.text,
	}
	var created any
	if err := client.post(ctx, "/community/drafts/", body, &created); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"draft": created,
	})
}

func communityDraftsUpdate(ctx context.Context, args []string) error {
	opts, err := parseUpdateArgs(args)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := map[string]any{
		"base_revision": *opts.baseRevision,
		"text":          opts.text,
	}
	var updated any
	if err := client.patch(ctx, fmt.Sprintf("/community/drafts/%s/", opts.draftID), body, &updated); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"draft":         updated,
		"base_revision": *opts.baseRevision,
	})
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

type createDraftOptions struct {
	title string
	text  string
}

type updateOptions struct {
	draftID      string
	baseRevision *int
	text         string
}

type addImageOptions struct {
	draftID      string
	baseRevision *int
	file         string
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

func parseCreateDraftArgs(args []string) (*createDraftOptions, error) {
	opts := &createDraftOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			i++
			if i >= len(args) {
				return nil, usageError("--title requires a value")
			}
			opts.title = args[i]
		case "--text":
			i++
			if i >= len(args) {
				return nil, usageError("--text requires a value")
			}
			opts.text = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.title == "" {
		return nil, usageError("--title is required")
	}
	return opts, nil
}

func parseUpdateArgs(args []string) (*updateOptions, error) {
	opts := &updateOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--draft-id":
			i++
			if i >= len(args) {
				return nil, usageError("--draft-id requires a value")
			}
			opts.draftID = args[i]
		case "--base-revision":
			i++
			if i >= len(args) {
				return nil, usageError("--base-revision requires a value")
			}
			parsed, err := parseIntOption("--base-revision", args[i], 0)
			if err != nil {
				return nil, err
			}
			opts.baseRevision = &parsed
		case "--text":
			i++
			if i >= len(args) {
				return nil, usageError("--text requires a value")
			}
			opts.text = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.draftID == "" {
		return nil, usageError("--draft-id is required")
	}
	if opts.baseRevision == nil {
		return nil, usageError("--base-revision is required")
	}
	if opts.text == "" {
		return nil, usageError("--text is required")
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
		case "--base-revision":
			i++
			if i >= len(args) {
				return nil, usageError("--base-revision requires a value")
			}
			parsed, err := parseIntOption("--base-revision", args[i], 0)
			if err != nil {
				return nil, err
			}
			opts.baseRevision = &parsed
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
	if opts.baseRevision == nil {
		return nil, usageError("--base-revision is required")
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
