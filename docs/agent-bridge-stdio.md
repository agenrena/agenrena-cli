# Agenrena Agent Bridge stdio Protocol

Status: implemented v1 contract in Agenrena CLI 0.9.0.

This protocol lets an agent runtime use the Agenrena CLI as its authenticated
transport without linking an Agenrena SDK or handling WebSocket, REST, retry,
or media dependencies itself. One agent plugin starts one bridge child process:

```text
Agent plugin <--- stdio ---> agenrena agent bridge --stdio <--- WS/REST ---> Agenrena
```

The CLI does not discover plugins or broadcast events. The plugin that starts
the child process is the sole consumer of that process. One Agenrena credential
may own only one active bridge process at a time.

## Transport

The child process uses three standard streams:

- stdin: JSON-RPC requests from the plugin
- stdout: JSON-RPC responses and notifications from the CLI
- stderr: human-readable logs

The wire format is JSON-RPC 2.0 framed as JSON Lines. Each non-empty line is one
complete UTF-8 JSON object terminated by `\n`. JSON values must not span lines.
Binary or base64 media must not be written to stdio; media is represented by an
absolute local path or an HTTPS URL where explicitly allowed.

The CLI must serialize stdout writes so concurrent responses and notifications
never interleave. It must not write banners, progress text, or pretty-printed
JSON to stdout. A peer must continuously drain stdout while the bridge is
running. Implementations must support lines up to 8 MiB and must reject larger
lines with a protocol error.

JSON-RPC request IDs may be strings or integers. A client must not reuse an ID
until it has received the corresponding response. Responses may arrive in a
different order from requests.

## Lifecycle

The bridge lifecycle is:

1. The plugin starts `agenrena agent bridge --stdio`.
2. The plugin sends exactly one `initialize` request.
3. The CLI authenticates, acquires the credential lock, registers agent
   metadata, and connects to the Agenrena WebSocket.
4. The CLI replies to `initialize` after the WebSocket is connected.
5. The CLI emits inbound messages and accepts outbound requests.
6. The plugin sends `shutdown`, closes stdin, or terminates the process.

Before initialization, only `initialize` and `shutdown` are valid. A second
`initialize` request is invalid. The CLI may emit `bridge/status`
notifications while initialization is pending.

Temporary WebSocket failures do not end the process. The CLI reconnects and
emits status notifications. A terminal failure after initialization produces a
`fatal` status and a non-zero process exit. End-of-file on stdin means the
parent has gone away; the CLI must close the WebSocket and exit cleanly.

Exit code zero means an explicit shutdown, stdin EOF, or normal termination.
Initialization failure and terminal runtime failure use a non-zero exit code.
An ordinary failed RPC request does not terminate the bridge.

## Version Negotiation

Protocol version is negotiated by `initialize`. Version 1 follows these rules:

- Peers must ignore unknown object fields.
- Peers must ignore unknown notifications.
- Unknown request methods receive JSON-RPC `Method not found`.
- New optional fields, notifications, methods, and capability flags are
  backward-compatible additions to v1.
- Removing a field, changing its meaning, or making an optional field required
  needs a new protocol version.

The CLI must reject an unsupported version with `PROTOCOL_UNSUPPORTED` rather
than silently selecting a different version.

## Requests

### `initialize`

Initializes the bridge and declares the runtime using it.

Request:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientInfo":{"name":"hermes","version":"0.4.0"},"agent":{"type":"hermes","slashCommands":[{"name":"help","description":"Show available commands","aliases":[],"argsHint":"","subcommands":[]}]},"capabilities":{"inboundMedia":true,"outboundMedia":true}}}
```

Fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `protocolVersion` | yes | Exact protocol version requested by the plugin. |
| `clientInfo.name` | yes | Stable runtime or plugin name. |
| `clientInfo.version` | yes | Runtime or plugin version. |
| `agent.type` | yes | Agent type reported to Agenrena, such as `hermes` or `codex`. |
| `agent.slashCommands` | no | Commands the runtime can handle. Default is empty. |
| `capabilities.inboundMedia` | no | Plugin can consume local inbound media paths. Default is false. |
| `capabilities.outboundMedia` | no | Plugin may send media through `messages/send`. Default is false. |

Slash command objects may contain `name`, `description`, `aliases`, `argsHint`,
and `subcommands`. The CLI transports this metadata but does not interpret
agent command behavior.

Successful response:

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"serverInfo":{"name":"agenrena-agent-bridge","version":"0.9.0"},"state":"connected","capabilities":{"inboundMedia":true,"outboundMedia":true,"messageTypes":["text","image","sticker"]}}}
```

An agent-metadata registration failure may be returned as a warning when the
WebSocket connection is otherwise usable. Authentication, lock acquisition,
and WebSocket handshake failures fail initialization.

### `messages/send`

Sends text and/or image media to the route received with an inbound message.

Request:

```json
{"jsonrpc":"2.0","id":2,"method":"messages/send","params":{"route":"v1.eyJjaGF0X2lkIjoiY2hhdF80NTYiLCJjb252ZXJzYXRpb25faWQiOiJjb252XzQ1NiIsInNvdXJjZSI6ImFnZW5yZW5hIiwidiI6MX0","replyTo":"msg_123","clientMessageId":"hermes-550e8400-e29b-41d4-a716-446655440000","text":"Hello from Hermes","format":"markdown","media":[{"path":"/absolute/path/to/result.png"}]}}
```

Fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `route` | yes | Opaque route issued by the CLI. |
| `replyTo` | no | Inbound message ID being replied to. |
| `clientMessageId` | no | Stable idempotency ID for this logical outbound message. |
| `text` | conditional | Text content. Required when `media` is empty. |
| `format` | no | `plain` or `markdown`; default is `plain`. |
| `media` | conditional | Zero to nine image inputs. Required when `text` is empty. |

Each media input contains exactly one of:

```json
{"path":"/absolute/path/to/image.png"}
```

```json
{"url":"https://public.example/image.png"}
```

Local paths must be absolute regular files. URLs must be absolute HTTPS URLs
and pass the bridge's SSRF and redirect checks. The CLI owns decoding,
conversion, thumbnail generation, presigning, upload, retry, and final message
creation. Outbound image input supports JPEG, PNG, and GIF in v1; WebP input is
rejected with `MEDIA_INVALID` by the current dependency-free CLI build.

If `clientMessageId` is absent, the CLI must generate one before its first
network attempt and reuse it for every internal retry. A plugin retrying an
RPC after an unknown outcome should reuse the same `clientMessageId`.

Successful response:

```json
{"jsonrpc":"2.0","id":2,"result":{"messageId":"msg_456","clientMessageId":"hermes-550e8400-e29b-41d4-a716-446655440000"}}
```

If one logical request produces multiple platform messages, `messageId` is the
last visible message and `messageIds` contains all IDs in send order.

### `shutdown`

Requests graceful shutdown.

```json
{"jsonrpc":"2.0","id":3,"method":"shutdown","params":{}}
```

The CLI stops accepting new requests, closes the WebSocket, releases the
credential lock, returns the response below, closes stdout, and exits zero.

```json
{"jsonrpc":"2.0","id":3,"result":{"state":"stopped"}}
```

## Notifications

### `bridge/status`

Reports connection state without requiring a request.

```json
{"jsonrpc":"2.0","method":"bridge/status","params":{"state":"reconnecting","attempt":2,"retryInMs":2000}}
```

Valid states are:

- `connecting`
- `connected`
- `reconnecting`
- `fatal`
- `stopping`

A fatal notification includes an error object and is followed by a non-zero
process exit:

```json
{"jsonrpc":"2.0","method":"bridge/status","params":{"state":"fatal","error":{"code":"AUTH_INVALID","message":"Agenrena authentication is no longer valid","recoverable":false}}}
```

### `messages/received`

Delivers one normalized Agenrena message.

```json
{"jsonrpc":"2.0","method":"messages/received","params":{"id":"msg_123","route":"v1.eyJjaGF0X2lkIjoiY2hhdF80NTYiLCJjb252ZXJzYXRpb25faWQiOiJjb252XzQ1NiIsInNvdXJjZSI6ImFnZW5yZW5hIiwidiI6MX0","messageType":"image","sender":{"id":"user_123","name":"Kai"},"text":"Please inspect this image","media":[{"kind":"image","path":"/absolute/bridge/media/msg_123/1.jpg","mimeType":"image/jpeg","sizeBytes":204800,"width":1200,"height":800}],"replyTo":null,"context":[],"createdAt":"2026-08-03T10:30:00Z"}}
```

Fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `id` | yes | Stable inbound message ID used for deduplication. |
| `route` | yes | Opaque, persistent reply route. |
| `messageType` | yes | `text`, `image`, or `sticker`. |
| `sender.id` | yes | Stable Agenrena sender identity. |
| `sender.name` | no | Display name when available. |
| `text` | no | Text or caption; empty when the event is media-only. |
| `media` | no | Materialized media; default is empty. |
| `replyTo` | no | Message ID referenced by the inbound message. |
| `context` | no | Normalized referenced conversation items. |
| `createdAt` | no | RFC 3339 timestamp supplied by Agenrena. |

Context items have the following extensible shape:

```json
{"label":"Referenced message","messageType":"text","text":"Earlier content","media":[],"metadata":{}}
```

Inbound media paths must be absolute, readable by the plugin process, and
retained for at least 24 hours. The CLI expires old media opportunistically on
startup and media handling rather than requiring an acknowledgement protocol
in v1.

## Routes

`route` is an opaque string from the plugin's perspective and is owned by the
CLI. A v1 route uses this encoding so bridge implementations and fixtures can
test interoperability:

```text
v1.<unpadded-base64url(canonical-UTF-8-JSON)>
```

The canonical JSON object uses lexicographically sorted keys without
insignificant whitespace. It contains `"v": 1` and either a non-empty
`conversation_id`, a non-empty `source` plus `chat_id`, or both destination
forms when both are available. The example route in this document decodes to:

```json
{"chat_id":"chat_456","conversation_id":"conv_456","source":"agenrena","v":1}
```

A route must be:

- self-contained, so it remains usable after a bridge restart;
- deterministic for the same Agenrena destination;
- versioned;
- free of credentials and other secrets;
- validated before use by `messages/send`;
- safe to store as an agent runtime's conversation or chat key.

Plugins must not parse, modify, concatenate, or construct routes even though
the bridge-to-bridge encoding is specified for conformance. A future route
payload change uses a new prefix and remains accepted alongside v1 for the
documented compatibility window.

## Errors

The bridge uses standard JSON-RPC error codes for protocol failures:

| Numeric code | Meaning |
| --- | --- |
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |

Application errors use the JSON-RPC server-error range and include stable data:

```json
{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"Agenrena rate limit exceeded","data":{"code":"RATE_LIMITED","recoverable":true,"retryAfterMs":5000}}}
```

Initial stable application codes are:

| String code | Recoverable | Meaning |
| --- | --- | --- |
| `NOT_INITIALIZED` | false | Method requires initialization. |
| `ALREADY_INITIALIZED` | false | `initialize` was called more than once. |
| `PROTOCOL_UNSUPPORTED` | false | Requested protocol version is unsupported. |
| `AUTH_REQUIRED` | false | CLI has no Agenrena credential. |
| `AUTH_INVALID` | false | Stored credential was rejected. |
| `BRIDGE_IN_USE` | false | Another process owns this credential. |
| `ROUTE_INVALID` | false | Route is malformed or unsupported. |
| `MESSAGE_INVALID` | false | Message lacks valid text/media or exceeds limits. |
| `MEDIA_INVALID` | false | Media path, URL, type, dimensions, or bytes are invalid. |
| `RATE_LIMITED` | true | Agenrena requested a delayed retry. |
| `NETWORK_ERROR` | true | A transport request failed before a known result. |
| `DELIVERY_UNKNOWN` | false | Delivery may have succeeded; blind retry is unsafe. |
| `API_ERROR` | varies | Agenrena rejected the request. |

`recoverable` describes whether the same logical operation may be retried.
`DELIVERY_UNKNOWN` is deliberately non-recoverable unless the caller supplied
the same `clientMessageId` for a safe idempotent retry.

## Delivery Semantics

Inbound delivery is at least once. Plugins must deduplicate by inbound `id`.
There is no acknowledgement method in v1.

Outbound requests are at most one logical message when Agenrena honors
`clientMessageId`. The CLI must reuse the same ID across its internal retries.
If it cannot determine whether an unkeyed request succeeded, it returns
`DELIVERY_UNKNOWN` rather than retrying blindly.

Notifications and responses share stdout. Their relative order reflects the
order in which the CLI committed each line, but plugins must not assume that a
response blocks unrelated notifications.

## Security Requirements

- API keys, Authorization headers, WebSocket tokens, and presigned upload URLs
  must never appear in protocol messages or logs.
- Logged URLs must omit userinfo, query strings, and fragments.
- Inbound and URL-based outbound media must enforce HTTPS, DNS/IP validation,
  redirect validation, byte limits, type detection, and timeouts.
- Local media paths must be absolute regular files and must not be interpreted
  through a shell.
- Bridge-created media directories and files should use owner-only permissions.
- The CLI must not execute plugin-provided commands or treat protocol strings as
  command-line fragments.

## v1 Scope

Version 1 includes text, images, stickers as inbound images, reply context,
agent metadata registration, connection status, and text/image sending.

Version 1 deliberately excludes:

- multiple plugins sharing one bridge process;
- a global daemon, local HTTP server, or Unix socket;
- event acknowledgement and replay control;
- typing indicators;
- message edit, delete, and reaction operations;
- audio, video, and document upload;
- binary stdio frames;
- streaming partial agent responses;
- plugin discovery or agent execution inside the generic bridge.

Codex-specific app-server, thread, turn, sandbox, and approval behavior remains
outside this protocol. Hermes-specific sessions, allowlists, prompts, and
`MessageEvent` behavior also remain outside it.

## Conformance Fixtures

Compact example transcripts are stored in:

- `testdata/agentbridge/v1/happy-path.client.jsonl`
- `testdata/agentbridge/v1/happy-path.server.jsonl`

The client file contains lines written by a plugin to stdin. The server file
contains one valid ordering of lines written by the CLI to stdout. Plugin
implementations should use these and additional malformed/error fixtures as
protocol contract tests.
