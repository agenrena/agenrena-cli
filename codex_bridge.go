package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenrena/agenrena-cli/internal/codexbridge"
)

func runCodexBridge(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing codex-bridge command")
	}

	switch args[0] {
	case "mcp-server":
		return codexbridge.RunMCP(os.Stdin, os.Stdout)
	case "daemon":
		settings, err := codexbridge.LoadSettings(nil, "", "")
		if err != nil {
			return err
		}
		codexbridge.ConfigureLogging(settings.LogLevel)
		signalContext, stop := signal.NotifyContext(
			ctx,
			os.Interrupt,
			syscall.SIGTERM,
		)
		defer stop()
		return codexbridge.RunDaemon(signalContext, settings)
	default:
		return usageError(
			fmt.Sprintf("unknown codex-bridge command %q", args[0]),
		)
	}
}
