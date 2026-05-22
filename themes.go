package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runThemes(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing themes command")
	}
	switch args[0] {
	case "card":
		return runCardThemes(ctx, args[1:])
	case "chat":
		return runChatThemes(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown themes command %q", args[0]))
	}
}

func runCardThemes(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing themes card command")
	}
	switch args[0] {
	case "drafts":
		return cardThemeDrafts(ctx, args[1:])
	case "update":
		return cardThemeUpdate(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown themes card command %q", args[0]))
	}
}

func cardThemeDrafts(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("themes card drafts does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var drafts any
	if err := client.get(ctx, "/themes/drafts/", &drafts); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"drafts": drafts,
	})
}

func runChatThemes(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing themes chat command")
	}
	switch args[0] {
	case "drafts":
		return chatThemeDrafts(ctx, args[1:])
	case "update":
		return chatThemeUpdate(ctx, args[1:])
	case "upload-background":
		return chatThemeUploadBackground(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown themes chat command %q", args[0]))
	}
}

func chatThemeDrafts(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("themes chat drafts does not accept arguments")
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var drafts any
	if err := client.get(ctx, "/chat-themes/drafts/", &drafts); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"drafts": drafts,
	})
}

func chatThemeUpdate(ctx context.Context, args []string) error {
	opts, err := parseThemeFileUpdateArgs(args)
	if err != nil {
		return err
	}
	body, err := loadChatThemeUpdateBody(opts.themeFile)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var updated any
	if err := client.patch(ctx, fmt.Sprintf("/chat-themes/%s/", opts.themeID), body, &updated); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"theme": updated,
	})
}

func chatThemeUploadBackground(ctx context.Context, args []string) error {
	opts, err := parseChatBackgroundUploadArgs(args)
	if err != nil {
		return err
	}
	prepared, err := prepareChatBackground(opts.file)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := map[string]any{
		"variant":      opts.variant,
		"content_type": prepared.MimeType,
	}
	var target chatBackgroundUploadTarget
	if err := client.post(ctx, fmt.Sprintf("/chat-themes/%s/upload-background/", opts.themeID), body, &target); err != nil {
		return err
	}
	if target.UploadURL == "" {
		return apiError("UPLOAD_TARGET_INVALID", "server response did not include upload_url", false)
	}
	if target.UploadFields == nil {
		target.UploadFields = map[string]string{}
	}
	if _, ok := target.UploadFields["Content-Type"]; !ok {
		target.UploadFields["Content-Type"] = prepared.MimeType
	}

	if err := uploadMultipart(ctx, target.UploadURL, target.UploadFields, "file", "chat-background.jpg", prepared.MimeType, prepared.Bytes); err != nil {
		return err
	}

	return writeOK(map[string]any{
		"background": target,
		"processed": map[string]any{
			"source_path":  opts.file,
			"width":        prepared.Width,
			"height":       prepared.Height,
			"size_bytes":   prepared.SizeBytes,
			"mime_type":    prepared.MimeType,
			"jpeg_quality": prepared.Quality,
		},
	})
}

type chatBackgroundUploadOptions struct {
	themeID string
	variant string
	file    string
}

type chatBackgroundUploadTarget struct {
	Variant      string            `json:"variant"`
	ImageKey     string            `json:"image_key"`
	ImageURL     string            `json:"image_url"`
	UploadURL    string            `json:"upload_url"`
	UploadFields map[string]string `json:"upload_fields"`
}

func cardThemeUpdate(ctx context.Context, args []string) error {
	opts, err := parseCardThemeUpdateArgs(args)
	if err != nil {
		return err
	}
	body, err := loadCardThemeUpdateBody(opts.themeFile)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var updated any
	if err := client.patch(ctx, fmt.Sprintf("/themes/%s/", opts.themeID), body, &updated); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"theme": updated,
	})
}

type cardThemeUpdateOptions struct {
	themeID   string
	themeFile string
}

func parseCardThemeUpdateArgs(args []string) (*cardThemeUpdateOptions, error) {
	return parseThemeFileUpdateArgs(args)
}

func parseThemeFileUpdateArgs(args []string) (*cardThemeUpdateOptions, error) {
	opts := &cardThemeUpdateOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--theme-id":
			i++
			if i >= len(args) {
				return nil, usageError("--theme-id requires a value")
			}
			opts.themeID = args[i]
		case "--theme-file":
			i++
			if i >= len(args) {
				return nil, usageError("--theme-file requires a path")
			}
			opts.themeFile = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown option %q", args[i]))
		}
	}
	if opts.themeID == "" {
		return nil, usageError("--theme-id is required")
	}
	if opts.themeFile == "" {
		return nil, usageError("--theme-file is required")
	}
	if _, err := os.Stat(opts.themeFile); err != nil {
		return nil, wrapError("FILE_NOT_FOUND", "theme file is not readable", err)
	}
	return opts, nil
}

func parseChatBackgroundUploadArgs(args []string) (*chatBackgroundUploadOptions, error) {
	opts := &chatBackgroundUploadOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--theme-id":
			i++
			if i >= len(args) {
				return nil, usageError("--theme-id requires a value")
			}
			opts.themeID = args[i]
		case "--variant":
			i++
			if i >= len(args) {
				return nil, usageError("--variant requires a value")
			}
			opts.variant = strings.TrimSpace(args[i])
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
	if opts.themeID == "" {
		return nil, usageError("--theme-id is required")
	}
	if opts.variant == "" {
		return nil, usageError("--variant is required")
	}
	if opts.variant != "light" && opts.variant != "dark" {
		return nil, usageError("--variant must be light or dark")
	}
	if opts.file == "" {
		return nil, usageError("--file is required")
	}
	if _, err := os.Stat(opts.file); err != nil {
		return nil, wrapError("FILE_NOT_FOUND", "background file is not readable", err)
	}
	return opts, nil
}

func loadCardThemeUpdateBody(themeFile string) (map[string]any, error) {
	raw, err := os.ReadFile(themeFile)
	if err != nil {
		return nil, wrapError("THEME_FILE_READ_FAILED", "failed to read theme file", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, wrapError("THEME_FILE_INVALID_JSON", "theme file must be a JSON object", err)
	}
	if parsed == nil {
		return nil, &cliError{
			Code:        "THEME_FILE_INVALID_TYPE",
			Message:     "theme file must be a JSON object",
			Recoverable: true,
		}
	}
	if _, hasName := parsed["name"]; hasName {
		return nil, &cliError{
			Code:        "THEME_UNSUPPORTED_FIELD",
			Message:     "theme update cannot include name",
			Recoverable: true,
		}
	}

	body := map[string]any{}
	if cardTheme, ok := parsed["card_theme"]; ok {
		body["card_theme"] = cardTheme
		if value, ok := parsed["seed_color"]; ok {
			body["seed_color"] = value
		}
	} else {
		body["card_theme"] = parsed
	}
	if len(body) == 0 {
		return nil, &cliError{
			Code:        "THEME_UPDATE_EMPTY",
			Message:     "theme update must include card_theme or a card theme JSON object",
			Recoverable: true,
		}
	}
	return body, nil
}

func loadChatThemeUpdateBody(themeFile string) (map[string]any, error) {
	raw, err := os.ReadFile(themeFile)
	if err != nil {
		return nil, wrapError("THEME_FILE_READ_FAILED", "failed to read theme file", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, wrapError("THEME_FILE_INVALID_JSON", "theme file must be a JSON object", err)
	}
	if parsed == nil {
		return nil, &cliError{
			Code:        "THEME_FILE_INVALID_TYPE",
			Message:     "theme file must be a JSON object",
			Recoverable: true,
		}
	}
	if _, hasName := parsed["name"]; hasName {
		return nil, &cliError{
			Code:        "THEME_UNSUPPORTED_FIELD",
			Message:     "theme update cannot include name",
			Recoverable: true,
		}
	}
	if chatTheme, ok := parsed["chat_theme"]; ok {
		return map[string]any{"chat_theme": chatTheme}, nil
	}
	return map[string]any{"chat_theme": parsed}, nil
}
