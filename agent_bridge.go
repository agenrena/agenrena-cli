package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
	"github.com/agenrena/agenrena-cli/internal/bridgeprotocol"
)

func runAgent(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing agent command")
	}
	if args[0] != "bridge" {
		return usageError(fmt.Sprintf("unknown agent command %q", args[0]))
	}
	if len(args) != 2 || args[1] != "--stdio" {
		return usageError("usage: agenrena agent bridge --stdio")
	}
	return runAgentBridge(ctx)
}

func runAgentBridge(ctx context.Context) error {
	apiBase := strings.TrimRight(apiBaseFromEnv(), "/")
	wsURL := strings.TrimSpace(os.Getenv("AGENRENA_WS_URL"))
	if wsURL == "" {
		var err error
		wsURL, err = agentbridge.DeriveWebSocketURL(apiBase)
		if err != nil {
			return &silentExitError{err: err}
		}
	} else if err := validateAgentBridgeWebSocketURL(wsURL); err != nil {
		return &silentExitError{err: err}
	}
	stateDir, err := agentBridgeStateDir()
	if err != nil {
		return &silentExitError{err: err}
	}
	parsedAPI, _ := url.Parse(apiBase)
	localDevelopment := parsedAPI != nil && isLoopbackAddress(parsedAPI.Hostname())
	service := agentbridge.NewService(agentbridge.Config{
		APIBase: apiBase, WSURL: wsURL, StateDir: stateDir,
		ServerVersion: cliVersion, UserAgent: "agenrena-agent-bridge/" + cliVersion,
		APIKeyLoader: func() (string, error) {
			credentials, err := loadCredentials()
			if err != nil {
				return "", err
			}
			return credentials.APIKey, nil
		},
		AllowPrivateMedia: localDevelopment,
		AllowHTTPMedia:    localDevelopment,
	})
	signalContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := bridgeprotocol.NewServer(os.Stdin, os.Stdout, service)
	if err := server.Run(signalContext); err != nil {
		return &silentExitError{err: err}
	}
	return nil
}

func validateAgentBridgeWebSocketURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return usageError("AGENRENA_WS_URL must be an absolute ws:// or wss:// URL")
	}
	if parsed.Scheme == "ws" && !isLoopbackAddress(parsed.Hostname()) {
		return usageError("insecure AGENRENA_WS_URL is only allowed for loopback hosts")
	}
	return nil
}

func isLoopbackAddress(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func agentBridgeStateDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENRENA_AGENT_BRIDGE_STATE_DIR")); value != "" {
		return filepath.Abs(value)
	}
	if value := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); value != "" {
		return filepath.Join(value, "agenrena", "agent-bridge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agenrena", "agent-bridge"), nil
}
