package main

import (
	"context"
	"fmt"
	"os"
)

const (
	cliVersion     = "0.6.0"
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
	case "businesses":
		return runBusinesses(ctx, args[1:])
	case "community":
		return runCommunity(ctx, args[1:])
	case "doctor":
		return runDoctor(ctx, args[1:])
	case "furriball":
		return runFurriBall(ctx, args[1:])
	case "marketplace":
		return runMarketplace(ctx, args[1:])
	case "pings":
		return runPings(ctx, args[1:])
	case "plans":
		return runPlans(ctx, args[1:])
	case "stickers":
		return runStickers(ctx, args[1:])
	case "themes":
		return runThemes(ctx, args[1:])
	case "users":
		return runUsers(ctx, args[1:])
	case "watches":
		return runWatches(ctx, args[1:])
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
	fmt.Fprintln(out, "  agenrena furriball pets")
	fmt.Fprintln(out, "  agenrena businesses offerings search-options --country-code <code> [--state-code <code>]")
	fmt.Fprintln(out, "  agenrena businesses offerings search --category <category> [options]")
	fmt.Fprintln(out, "  agenrena businesses offerings list --identity-id <uuid>")
	fmt.Fprintln(out, "  agenrena community drafts list")
	fmt.Fprintln(out, "  agenrena community drafts get --draft-id <id>")
	fmt.Fprintln(out, "  agenrena community drafts create --title <title> [--text <text>]")
	fmt.Fprintln(out, "  agenrena community drafts update --draft-id <id> --base-revision <revision> --text <text>")
	fmt.Fprintln(out, "  agenrena community drafts add-image --draft-id <id> --base-revision <revision> --file <path>")
	fmt.Fprintln(out, "  agenrena marketplace watches list")
	fmt.Fprintln(out, "  agenrena marketplace watches scan --id <watch-id>")
	fmt.Fprintln(out, "  agenrena marketplace recommend --id <candidate-id> --text <recommendation-text>")
	fmt.Fprintln(out, "  agenrena pings scan")
	fmt.Fprintln(out, "  agenrena pings recommend --id <id> --reason <reason>")
	fmt.Fprintln(out, "  agenrena plans create [--json <json>]")
	fmt.Fprintln(out, "  agenrena plans get --plan-id <uuid>")
	fmt.Fprintln(out, "  agenrena plans items add --plan-id <uuid> --expected-revision <revision> --json <json>")
	fmt.Fprintln(out, "  agenrena plans items update --plan-id <uuid> --item-id <uuid> --expected-revision <revision> --json <json>")
	fmt.Fprintln(out, "  agenrena plans items delete --plan-id <uuid> --item-id <uuid> --expected-revision <revision>")
	fmt.Fprintln(out, "  agenrena plans items reorder --plan-id <uuid> --expected-revision <revision> --json <json>")
	fmt.Fprintln(out, "  agenrena stickers packs")
	fmt.Fprintln(out, "  agenrena stickers upload --pack-id <id> --file <path> [--keyword <keyword>]")
	fmt.Fprintln(out, "  agenrena themes card drafts")
	fmt.Fprintln(out, "  agenrena themes card update --theme-id <id> --theme-file <path>")
	fmt.Fprintln(out, "  agenrena themes chat drafts")
	fmt.Fprintln(out, "  agenrena themes chat update --theme-id <id> --theme-file <path>")
	fmt.Fprintln(out, "  agenrena themes chat upload-background --theme-id <id> --variant <light|dark> --file <path>")
	fmt.Fprintln(out, "  agenrena users search --query <query>")
	fmt.Fprintln(out, "  agenrena watches list")
	fmt.Fprintln(out, "  agenrena watches scan --id <id>")
}
