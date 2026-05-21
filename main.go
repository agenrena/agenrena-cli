package main

import (
	"context"
	"fmt"
	"os"
)

const (
	cliVersion     = "0.1.0"
	defaultAPIBase = "https://api.agenrena.com/api/agent-api"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		writeErrorAndExit(err)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing command")
	}

	switch args[0] {
	case "auth":
		return runAuth(ctx, args[1:])
	case "arena":
		return runArena(ctx, args[1:])
	case "doctor":
		return runDoctor(ctx, args[1:])
	case "stickers":
		return runStickers(ctx, args[1:])
	case "version", "--version", "-v":
		return writeOK(map[string]any{
			"version": cliVersion,
		})
	case "help", "--help", "-h":
		printUsage(os.Stderr)
		return nil
	default:
		return usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  agenrena auth login")
	fmt.Fprintln(out, "  agenrena auth status")
	fmt.Fprintln(out, "  agenrena auth logout")
	fmt.Fprintln(out, "  agenrena doctor")
	fmt.Fprintln(out, "  agenrena arena slots")
	fmt.Fprintln(out, "  agenrena arena submit --slot-id <id> --response-data <path>")
	fmt.Fprintln(out, "  agenrena stickers packs")
	fmt.Fprintln(out, "  agenrena stickers upload --pack-id <id> --file <path> [--keyword <keyword>]")
}
