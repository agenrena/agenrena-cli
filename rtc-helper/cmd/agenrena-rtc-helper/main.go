package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenrena/agenrena-cli/rtc-helper/internal/livekitrtc"
	"github.com/agenrena/agenrena-cli/rtc-helper/internal/mediaipc"
	"github.com/livekit/protocol/logger"
)

const configLimitBytes = 64 * 1024

type helperConfig struct {
	ProtocolVersion  int    `json:"protocolVersion"`
	CallID           string `json:"callId"`
	ServerURL        string `json:"serverUrl"`
	ParticipantToken string `json:"participantToken"`
	SocketPath       string `json:"socketPath"`
	SampleRateHz     int    `json:"sampleRateHz"`
}

type readyEvent struct {
	Type            string `json:"type"`
	ProtocolVersion int    `json:"protocolVersion"`
	SocketPath      string `json:"socketPath"`
	Format          string `json:"format"`
	SampleRateHz    int    `json:"sampleRateHz"`
	Channels        int    `json:"channels"`
	FrameDurationMS int    `json:"frameDurationMs"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("agenrena-rtc-helper: ")
	logger.InitFromConfig(&logger.Config{Level: "warn"}, "agenrena-rtc-helper")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdin, os.Stdout); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer) error {
	config, err := readConfig(input)
	if err != nil {
		return err
	}
	server, err := mediaipc.Listen(config.SocketPath)
	if err != nil {
		return fmt.Errorf("open local media socket: %w", err)
	}
	defer server.Close()

	rtc, err := livekitrtc.New(livekitrtc.Config{
		ServerURL: config.ServerURL, ParticipantToken: config.ParticipantToken,
		SampleRateHz: config.SampleRateHz, Sink: server,
	})
	if err != nil {
		return err
	}
	defer rtc.Close()
	if err := rtc.Connect(); err != nil {
		return err
	}

	if err := json.NewEncoder(output).Encode(readyEvent{
		Type: "ready", ProtocolVersion: mediaipc.ProtocolVersion,
		SocketPath: config.SocketPath, Format: "pcm_s16le",
		SampleRateHz: config.SampleRateHz, Channels: livekitrtc.Channels,
		FrameDurationMS: 20,
	}); err != nil {
		return fmt.Errorf("write ready event: %w", err)
	}

	mediaDone := make(chan error, 1)
	go func() { mediaDone <- server.Serve(ctx, config.CallID, rtc) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-rtc.Done():
		return err
	case err := <-mediaDone:
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("local media connection ended: %w", err)
	}
}

func readConfig(input io.Reader) (helperConfig, error) {
	reader := bufio.NewReader(io.LimitReader(input, configLimitBytes+1))
	decoder := json.NewDecoder(reader)
	var config helperConfig
	if err := decoder.Decode(&config); err != nil {
		return helperConfig{}, fmt.Errorf("read helper configuration: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return helperConfig{}, errors.New("helper configuration must contain exactly one JSON object")
	}
	if config.ProtocolVersion != mediaipc.ProtocolVersion {
		return helperConfig{}, fmt.Errorf("unsupported helper protocol version %d", config.ProtocolVersion)
	}
	if config.CallID == "" || config.ServerURL == "" || config.ParticipantToken == "" || config.SocketPath == "" {
		return helperConfig{}, errors.New("helper configuration is missing required fields")
	}
	if config.SampleRateHz == 0 {
		config.SampleRateHz = livekitrtc.DefaultSampleRateHz
	}
	if !livekitrtc.SupportsSampleRate(config.SampleRateHz) {
		return helperConfig{}, errors.New("helper sampleRateHz must be one of 16000, 24000, or 48000")
	}
	return config, nil
}
