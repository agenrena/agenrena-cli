package main

import (
	"context"
	"fmt"
	"os"

	"github.com/agenrena/agenrena-cli/internal/codexbridge"
)

func runCodex(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "bridge" {
		return usageError("usage: agenrena codex bridge mcp")
	}
	switch args[1] {
	case "guidance":
		if len(args) != 2 {
			return usageError("usage: agenrena codex bridge guidance")
		}
		guide, err := codexbridge.Guidance()
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(os.Stdout, guide)
		return err
	case "mcp":
		if len(args) != 2 {
			return usageError("usage: agenrena codex bridge mcp")
		}
		if err := codexbridge.RunMCP(ctx, os.Stdin, os.Stdout); err != nil {
			return &silentExitError{err: err}
		}
		return nil
	case "daemon":
		if len(args) != 2 {
			return usageError("usage: agenrena codex bridge daemon")
		}
		if err := codexbridge.RunDaemon(ctx); err != nil {
			return &silentExitError{err: err}
		}
		return nil
	default:
		return usageError(fmt.Sprintf("unknown codex bridge command %q", args[1]))
	}
}
