package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const stickerMaxBytes = 500 * 1024

type stickerUploadTarget struct {
	ID           string            `json:"id"`
	ImageKey     string            `json:"image_key"`
	UploadURL    string            `json:"upload_url"`
	UploadFields map[string]string `json:"upload_fields"`
	SortOrder    int               `json:"sort_order"`
	Keyword      string            `json:"keyword"`
}

func runStickers(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing stickers command")
	}
	switch args[0] {
	case "packs":
		return stickersPacks(ctx, args[1:])
	case "upload":
		return stickersUpload(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown stickers command %q", args[0]))
	}
}

func stickersPacks(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("stickers packs does not accept arguments")
	}
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	client := newAPIClient(creds)

	var packs any
	if err := client.get(ctx, "/stickers/packs/drafts/", &packs); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"packs": packs,
	})
}

func stickersUpload(ctx context.Context, args []string) error {
	opts, err := parseStickerUploadArgs(args)
	if err != nil {
		return err
	}
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	client := newAPIClient(creds)

	content, info, err := prepareStickerPNG(opts.file)
	if err != nil {
		return err
	}
	if len(content) > stickerMaxBytes {
		return &cliError{
			Code:        "STICKER_TOO_LARGE",
			Message:     fmt.Sprintf("processed sticker is %d bytes; limit is %d bytes", len(content), stickerMaxBytes),
			Recoverable: true,
		}
	}
	warnings := stickerWarnings(info)

	var target stickerUploadTarget
	body := map[string]any{}
	if opts.keyword != "" {
		body["keyword"] = opts.keyword
	}
	if err := client.post(ctx, fmt.Sprintf("/stickers/packs/%s/stickers/", opts.packID), body, &target); err != nil {
		return err
	}
	if target.UploadURL == "" {
		return apiError("UPLOAD_TARGET_INVALID", "server response did not include upload_url", false)
	}
	if target.UploadFields == nil {
		target.UploadFields = map[string]string{}
	}
	if _, ok := target.UploadFields["Content-Type"]; !ok {
		target.UploadFields["Content-Type"] = "image/png"
	}

	fileName := filepath.Base(opts.file)
	if err := uploadMultipart(ctx, target.UploadURL, target.UploadFields, "file", fileName, "image/png", content); err != nil {
		return err
	}

	return writeOKWithWarnings(map[string]any{
		"sticker": target,
		"image": map[string]any{
			"source_path":       opts.file,
			"source_width":      info.SourceWidth,
			"source_height":     info.SourceHeight,
			"processed_width":   info.ProcessedWidth,
			"processed_height":  info.ProcessedHeight,
			"processed_size":    len(content),
			"resized":           info.Resized,
			"has_alpha":         info.HasAlpha,
			"content_type":      "image/png",
			"max_allowed_bytes": stickerMaxBytes,
		},
	}, warnings)
}

func stickerWarnings(info StickerImageInfo) []string {
	var warnings []string
	if !info.HasAlpha {
		warnings = append(warnings, "Sticker PNG has no transparent pixels. If the image appears to have a checkerboard background, that checkerboard is part of the image.")
	}
	return warnings
}

type stickerUploadOptions struct {
	packID  string
	file    string
	keyword string
}

func parseStickerUploadArgs(args []string) (*stickerUploadOptions, error) {
	opts := &stickerUploadOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pack-id":
			i++
			if i >= len(args) {
				return nil, usageError("--pack-id requires a value")
			}
			opts.packID = args[i]
		case "--file":
			i++
			if i >= len(args) {
				return nil, usageError("--file requires a value")
			}
			opts.file = args[i]
		case "--keyword":
			i++
			if i >= len(args) {
				return nil, usageError("--keyword requires a value")
			}
			opts.keyword = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown stickers upload option %q", args[i]))
		}
	}
	if opts.packID == "" {
		return nil, usageError("--pack-id is required")
	}
	if opts.file == "" {
		return nil, usageError("--file is required")
	}
	if _, err := os.Stat(opts.file); err != nil {
		return nil, wrapError("FILE_NOT_FOUND", "sticker file is not readable", err)
	}
	return opts, nil
}
