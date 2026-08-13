# Agenrena RTC helper

`agenrena-rtc-helper` is the optional realtime media sidecar managed by the
Agenrena Agent Bridge. It joins the call-scoped LiveKit room and exposes PCM16
mono 24 kHz audio to the local OpenClaw plugin over a permission-restricted Unix
domain socket.

The standard Agenrena installer installs the helper beside the core `agenrena`
binary on macOS and Linux. The bridge starts it only when the local Agent runtime
accepts an incoming call; it does not run for normal CLI commands or messaging.

The helper reads exactly one JSON configuration object from stdin. LiveKit
credentials are never accepted as command-line arguments or returned over the
bridge protocol. Stdout is reserved for the one-line ready event; diagnostics
go to stderr.

For calls, the Agent plugin may request 16, 24, or 48 kHz PCM in
`calls/accept`; 24 kHz remains the default. PCM16 little-endian, mono channels,
and 20 ms frames are fixed. The helper performs the LiveKit audio conversion in
both directions.

The helper is a separate Go module so LiveKit, WebRTC, CGo, and libopus do not
become dependencies of the core `agenrena` binary.

## Development

Install a C compiler, pkg-config, libopus development headers, and libsoxr
development headers, then run:

```sh
go test -tags nolibopusfile ./...
go build -tags nolibopusfile ./cmd/agenrena-rtc-helper
```

`nolibopusfile` is intentional: realtime PCM needs the Opus encoder/decoder but
does not use the optional Ogg Opus file API. Production Linux artifacts are
statically linked by the included Dockerfile:

```sh
make linux-amd64
make linux-arm64
```

Place `agenrena-rtc-helper` next to the `agenrena` executable, put it on `PATH`,
or set `AGENRENA_RTC_HELPER_PATH` to its absolute path.
