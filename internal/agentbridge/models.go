package agentbridge

import "encoding/json"

const ProtocolVersion = 1

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SlashCommand struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	ArgsHint    string   `json:"argsHint,omitempty"`
	Subcommands []string `json:"subcommands,omitempty"`
}

type AgentInfo struct {
	Type          string         `json:"type"`
	SlashCommands []SlashCommand `json:"slashCommands,omitempty"`
}

type ClientCapabilities struct {
	InboundMedia  bool `json:"inboundMedia,omitempty"`
	OutboundMedia bool `json:"outboundMedia,omitempty"`
	Calls         bool `json:"calls,omitempty"`
}

type InitializeParams struct {
	ProtocolVersion int                `json:"protocolVersion"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
	Agent           AgentInfo          `json:"agent"`
	Capabilities    ClientCapabilities `json:"capabilities,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	InboundMedia  bool     `json:"inboundMedia"`
	OutboundMedia bool     `json:"outboundMedia"`
	MessageTypes  []string `json:"messageTypes"`
	Handoff       bool     `json:"handoff"`
	Calls         bool     `json:"calls"`
}

type InitializeResult struct {
	ProtocolVersion int                `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	State           string             `json:"state"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	Warnings        []string           `json:"warnings,omitempty"`
}

type Sender struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type MaterializedMedia struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

type ContextItem struct {
	Label       string              `json:"label,omitempty"`
	MessageType string              `json:"messageType,omitempty"`
	Text        string              `json:"text,omitempty"`
	Media       []MaterializedMedia `json:"media,omitempty"`
	Metadata    map[string]any      `json:"metadata,omitempty"`
}

type IncomingMessage struct {
	ID          string              `json:"id"`
	Route       string              `json:"route"`
	MessageType string              `json:"messageType"`
	Sender      Sender              `json:"sender"`
	Text        string              `json:"text"`
	Media       []MaterializedMedia `json:"media"`
	ReplyTo     any                 `json:"replyTo"`
	Context     []ContextItem       `json:"context"`
	CreatedAt   string              `json:"createdAt,omitempty"`
}

type CallRTC struct {
	ServerURL        string `json:"serverUrl"`
	ParticipantToken string `json:"participantToken,omitempty"`
}

type IncomingCall struct {
	CallID         string  `json:"callId"`
	ConversationID string  `json:"conversationId"`
	Route          string  `json:"route,omitempty"`
	Caller         *Sender `json:"caller,omitempty"`
	ExpiresAt      string  `json:"expiresAt"`
	RTC            CallRTC `json:"rtc"`
}

type CancelledCall struct {
	CallID string `json:"callId"`
}

type CallAudioPreferences struct {
	SampleRateHz int `json:"sampleRateHz,omitempty"`
}

type CallRealtimePreferences struct {
	Transport string `json:"transport"`
}

type AcceptCallParams struct {
	CallID   string                   `json:"callId"`
	Audio    *CallAudioPreferences    `json:"audio,omitempty"`
	Realtime *CallRealtimePreferences `json:"realtime,omitempty"`
}

type LeaveCallParams struct {
	CallID string `json:"callId"`
}

type CallMedia struct {
	Transport       string             `json:"transport"`
	SocketPath      string             `json:"socketPath"`
	ProtocolVersion int                `json:"protocolVersion"`
	Format          string             `json:"format"`
	SampleRateHz    int                `json:"sampleRateHz"`
	Channels        int                `json:"channels"`
	FrameDurationMS int                `json:"frameDurationMs"`
	Realtime        *CallRealtimeMedia `json:"realtime,omitempty"`
}

type CallRealtimeMedia struct {
	Transport string `json:"transport"`
	SDP       string `json:"sdp"`
}

type AcceptCallResult struct {
	CallID string    `json:"callId"`
	Media  CallMedia `json:"media"`
}

type LeaveCallResult struct {
	CallID string `json:"callId"`
	State  string `json:"state"`
}

type CallEnded struct {
	CallID string `json:"callId"`
	Reason string `json:"reason"`
}

type Status struct {
	State     string    `json:"state"`
	Attempt   int       `json:"attempt,omitempty"`
	RetryInMS int64     `json:"retryInMs,omitempty"`
	Error     *RPCError `json:"error,omitempty"`
}

type SendMedia struct {
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

type SendParams struct {
	Route           string      `json:"route"`
	ReplyTo         string      `json:"replyTo,omitempty"`
	ClientMessageID string      `json:"clientMessageId,omitempty"`
	Text            string      `json:"text,omitempty"`
	Format          string      `json:"format,omitempty"`
	Media           []SendMedia `json:"media,omitempty"`
}

type SendResult struct {
	MessageID       string   `json:"messageId"`
	MessageIDs      []string `json:"messageIds,omitempty"`
	ClientMessageID string   `json:"clientMessageId"`
}

type HandoffParams struct {
	Route string `json:"route"`
}

type HandoffResult struct {
	Responder  string `json:"responder"`
	SwitchedAt string `json:"switchedAt,omitempty"`
}

type Event struct {
	Method string
	Params any
}

type rawInboundMedia struct {
	Kind     string
	URL      string
	MIMEType string
	Width    int
	Height   int
}

func decodeObject(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, target)
}
