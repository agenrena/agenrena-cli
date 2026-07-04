package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseUpdateArgsRequiresBaseRevision(t *testing.T) {
	if _, err := parseUpdateArgs([]string{
		"--draft-id", "draft-id",
		"--text", "updated",
	}); err == nil {
		t.Fatal("expected missing base revision error")
	}

	opts, err := parseUpdateArgs([]string{
		"--draft-id", "draft-id",
		"--base-revision", "0",
		"--text", "updated",
	})
	if err != nil {
		t.Fatalf("parseUpdateArgs returned error: %v", err)
	}
	if opts.baseRevision == nil || *opts.baseRevision != 0 {
		t.Fatalf("baseRevision = %v, want 0", opts.baseRevision)
	}

	if _, err := parseUpdateArgs([]string{
		"--draft-id", "draft-id",
		"--base-revision", "-1",
		"--text", "updated",
	}); err == nil {
		t.Fatal("expected negative base revision error")
	}
}

func TestParseAddImageArgsRequiresBaseRevision(t *testing.T) {
	file := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := parseAddImageArgs([]string{
		"--draft-id", "draft-id",
		"--file", file,
	}); err == nil {
		t.Fatal("expected missing base revision error")
	}

	opts, err := parseAddImageArgs([]string{
		"--draft-id", "draft-id",
		"--base-revision", "7",
		"--file", file,
	})
	if err != nil {
		t.Fatalf("parseAddImageArgs returned error: %v", err)
	}
	if opts.baseRevision == nil || *opts.baseRevision != 7 {
		t.Fatalf("baseRevision = %v, want 7", opts.baseRevision)
	}
}
