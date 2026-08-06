package agentbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type APIClient struct {
	BaseURL     string
	APIKey      string
	UserAgent   string
	HTTPClient  *http.Client
	MaxAttempts int
	MediaStore  *MediaStore
}

type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Ambiguous  bool
}

func (err *APIError) Error() string { return err.Message }

func (err *APIError) Retryable() bool {
	return !err.Ambiguous && (err.Status == 0 || err.Status == http.StatusTooManyRequests || err.Status >= 500)
}

func (client *APIClient) RegisterAgent(ctx context.Context, agent AgentInfo) error {
	commands := make([]map[string]any, 0, len(agent.SlashCommands))
	for _, command := range agent.SlashCommands {
		commands = append(commands, map[string]any{
			"name": command.Name, "description": command.Description,
			"aliases": command.Aliases, "args_hint": command.ArgsHint,
			"subcommands": command.Subcommands,
		})
	}
	body := map[string]any{"agent_type": agent.Type, "slash_commands": commands}
	_, err := client.doJSONOnce(ctx, http.MethodPatch, "/agents/me/", body)
	return err
}

func (client *APIClient) SendMessage(ctx context.Context, params SendParams) (SendResult, error) {
	route, err := DecodeRoute(params.Route)
	if err != nil {
		return SendResult{}, err
	}
	params.Text = strings.TrimSpace(params.Text)
	params.Format = strings.ToLower(strings.TrimSpace(params.Format))
	if params.Format == "" {
		params.Format = "plain"
	}
	if params.Format != "plain" && params.Format != "markdown" {
		return SendResult{}, bridgeError("MESSAGE_INVALID", "message format must be plain or markdown", false)
	}
	if len(params.Media) > defaultMaxMediaCount {
		return SendResult{}, bridgeError("MESSAGE_INVALID", "a message may contain at most 9 images", false)
	}
	if params.Text == "" && len(params.Media) == 0 {
		return SendResult{}, bridgeError("MESSAGE_INVALID", "message requires text or media", false)
	}
	if params.ClientMessageID == "" {
		params.ClientMessageID, err = newClientMessageID()
		if err != nil {
			return SendResult{}, wrapBridgeError("INTERNAL_ERROR", "could not create an outbound message ID", false, err)
		}
	}
	if len(params.ClientMessageID) > 100 {
		return SendResult{}, bridgeError("MESSAGE_INVALID", "clientMessageId exceeds 100 characters", false)
	}

	var images []map[string]any
	if len(params.Media) > 0 {
		source := route.Source
		if source == "" && route.ConversationID != "" {
			source = "agenrena"
		}
		if source == "" {
			return SendResult{}, bridgeError("ROUTE_INVALID", "image sending requires a source-qualified route", false)
		}
		images, err = client.prepareAndUploadImages(ctx, source, params.Media)
		if err != nil {
			return SendResult{}, err
		}
	}

	messageIDs := []string{}
	if params.Text != "" {
		body := outboundMessageBody(route, params.ClientMessageID, params.ReplyTo)
		body["message_type"] = "text"
		body["text"] = params.Text
		body["text_format"] = params.Format
		messageID, err := client.sendOutboundMessage(ctx, body)
		if err != nil {
			return SendResult{}, err
		}
		messageIDs = append(messageIDs, messageID)
	}
	if len(images) > 0 {
		clientMessageID := params.ClientMessageID
		replyTo := params.ReplyTo
		if params.Text != "" {
			clientMessageID = derivedClientMessageID(params.ClientMessageID, "image")
			replyTo = ""
		}
		body := outboundMessageBody(route, clientMessageID, replyTo)
		body["message_type"] = "image"
		body["images"] = images
		messageID, err := client.sendOutboundMessage(ctx, body)
		if err != nil {
			if len(messageIDs) > 0 {
				err = partialDeliveryError(err, "image", messageIDs)
			}
			return SendResult{}, err
		}
		messageIDs = append(messageIDs, messageID)
	}

	messageID := messageIDs[len(messageIDs)-1]
	allMessageIDs := []string(nil)
	if len(messageIDs) > 1 {
		allMessageIDs = messageIDs
	}
	return SendResult{
		MessageID: messageID, MessageIDs: allMessageIDs, ClientMessageID: params.ClientMessageID,
	}, nil
}

func outboundMessageBody(route Route, clientMessageID, replyTo string) map[string]any {
	body := map[string]any{"message_id": clientMessageID}
	applyRoute(body, route)
	if replyTo != "" {
		body["reply_to_message_id"] = replyTo
	}
	return body
}

func (client *APIClient) sendOutboundMessage(ctx context.Context, body map[string]any) (string, error) {
	result, err := client.doJSONWithRetry(ctx, http.MethodPost, "/channels/messages/send/", body, true)
	if err != nil {
		return "", apiRPCError(err, true)
	}
	messageID := valueString(result["message_id"])
	if messageID == "" {
		messageID = valueString(result["id"])
	}
	return messageID, nil
}

func derivedClientMessageID(parent, part string) string {
	digest := sha256.Sum256([]byte(parent + "\x00" + part))
	return "bridge-" + hex.EncodeToString(digest[:])
}

func partialDeliveryError(err error, failedPart string, deliveredMessageIDs []string) error {
	rpcErr, ok := err.(*RPCError)
	if !ok {
		return err
	}
	result := *rpcErr
	result.Fields = map[string]any{
		"failedPart":          failedPart,
		"deliveredMessageIds": append([]string(nil), deliveredMessageIDs...),
	}
	return &result
}

// Handoff returns the conversation to its human owner. Agenrena only models a
// delegation for its own conversations, so a route that carries an external
// platform destination alone cannot be handed off.
//
// The endpoint is idempotent: handing off an already-human conversation reports
// the same state instead of failing, so an ambiguous POST is safe to retry.
//
// Agenrena answers 404 for every conversation without a delegation the agent
// owns, deliberately without distinguishing an assistant workspace from another
// agent's conversation from one that does not exist. That single reply means
// "this route has nothing to hand off", so it becomes HANDOFF_UNSUPPORTED like
// the route check above rather than a generic API error.
func (client *APIClient) Handoff(ctx context.Context, params HandoffParams) (HandoffResult, error) {
	route, err := DecodeRoute(params.Route)
	if err != nil {
		return HandoffResult{}, err
	}
	if route.ConversationID == "" {
		return HandoffResult{}, bridgeError("HANDOFF_UNSUPPORTED", "handoff requires a route with an Agenrena conversation", false)
	}
	endpoint := "/channels/conversations/" + url.PathEscape(route.ConversationID) + "/handoff/"
	result, err := client.doJSONWithRetry(ctx, http.MethodPost, endpoint, nil, true)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.Status == http.StatusNotFound {
			return HandoffResult{}, bridgeError("HANDOFF_UNSUPPORTED", "route has no Agenrena conversation the agent can hand off", false)
		}
		return HandoffResult{}, apiRPCError(err, true)
	}
	return HandoffResult{
		Responder:  valueString(result["responder"]),
		SwitchedAt: valueString(result["switched_at"]),
	}, nil
}

func applyRoute(body map[string]any, route Route) {
	if route.Source != "" && route.ChatID != "" {
		body["source"] = route.Source
		body["chat_id"] = route.ChatID
		return
	}
	if route.ConversationID != "" {
		body["conversation_id"] = route.ConversationID
		if route.Source != "" {
			body["source"] = route.Source
		} else {
			body["source"] = "agenrena"
		}
	}
}

func (client *APIClient) prepareAndUploadImages(ctx context.Context, source string, inputs []SendMedia) ([]map[string]any, error) {
	entries, err := client.presignImages(ctx, source, len(inputs))
	if err != nil {
		return nil, err
	}
	if len(entries) < len(inputs) {
		return nil, bridgeError("API_ERROR", "Agenrena returned fewer media upload targets than requested", true)
	}
	result := make([]map[string]any, 0, len(inputs))
	for index, input := range inputs {
		prepared, err := client.prepareOutboundImage(ctx, input)
		if err != nil {
			return nil, err
		}
		entry := entries[index]
		if err := client.uploadImage(ctx, valueString(entry["image_upload_url"]), prepared.ImageBytes); err != nil {
			return nil, err
		}
		if err := client.uploadImage(ctx, valueString(entry["thumbnail_upload_url"]), prepared.ThumbnailBytes); err != nil {
			return nil, err
		}
		id := valueString(entry["id"])
		if id == "" {
			return nil, bridgeError("API_ERROR", "Agenrena media upload target is missing an id", true)
		}
		result = append(result, map[string]any{
			"id": id, "width": prepared.Width, "height": prepared.Height,
			"thumbnail_width":  prepared.ThumbnailWidth,
			"thumbnail_height": prepared.ThumbnailHeight,
			"size_bytes":       prepared.SizeBytes, "mime_type": "image/jpeg",
		})
	}
	return result, nil
}

func (client *APIClient) presignImages(ctx context.Context, source string, count int) ([]map[string]any, error) {
	result, err := client.doJSONWithRetry(ctx, http.MethodPost, "/hub/media/presign/", map[string]any{
		"source": source, "count": count,
	}, true)
	if err != nil {
		return nil, apiRPCError(err, true)
	}
	raw, ok := result["media"].([]any)
	if !ok {
		return nil, bridgeError("API_ERROR", "Agenrena media presign response is invalid", true)
	}
	entries := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if entry, ok := item.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (client *APIClient) uploadImage(ctx context.Context, uploadURL string, data []byte) error {
	if uploadURL == "" {
		return bridgeError("API_ERROR", "Agenrena media upload target is incomplete", true)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
		if err != nil {
			return wrapBridgeError("MEDIA_INVALID", "could not create an image upload request", false, err)
		}
		request.Header.Set("Content-Type", "image/jpeg")
		request.Header.Set("User-Agent", client.userAgent())
		response, err := client.httpClient().Do(request)
		retryable := err != nil
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024*1024))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			retryable = response.StatusCode == 429 || response.StatusCode >= 500
			if !retryable {
				return bridgeError("API_ERROR", fmt.Sprintf("image upload returned HTTP %d", response.StatusCode), false)
			}
		}
		if attempt == 3 {
			if err != nil {
				return wrapBridgeError("NETWORK_ERROR", "image upload failed", true, err)
			}
			return bridgeError("API_ERROR", "image upload failed after retries", true)
		}
		if err := waitContext(ctx, time.Duration(1<<(attempt-1))*time.Second); err != nil {
			return wrapBridgeError("NETWORK_ERROR", "image upload was cancelled", true, err)
		}
	}
	return bridgeError("API_ERROR", "image upload failed", true)
}

func (client *APIClient) doJSONWithRetry(ctx context.Context, method, endpoint string, body any, retryAmbiguous bool) (map[string]any, error) {
	attempts := client.MaxAttempts
	if attempts <= 0 {
		attempts = 4
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := client.doJSONOnce(ctx, method, endpoint, body)
		if err == nil {
			return result, nil
		}
		lastErr = err
		apiErr, ok := err.(*APIError)
		retryable := ok && (apiErr.Retryable() || (retryAmbiguous && apiErr.Ambiguous))
		if !retryable || attempt == attempts {
			return nil, err
		}
		delay := apiErr.RetryAfter
		if delay <= 0 {
			delay = time.Duration(1<<(attempt-1)) * time.Second
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
		}
		if err := waitContext(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (client *APIClient) doJSONOnce(ctx context.Context, method, endpoint string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(client.BaseURL, "/")+endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+client.APIKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", client.userAgent())
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient().Do(request)
	if err != nil {
		return nil, &APIError{Message: "could not reach the Agenrena API: " + err.Error(), Ambiguous: method == http.MethodPost}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if readErr != nil {
		return nil, &APIError{Message: "could not read the Agenrena API response", Ambiguous: method == http.MethodPost}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeAgentAPIError(response, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var result any
	if json.Unmarshal(raw, &result) != nil {
		return nil, &APIError{Status: response.StatusCode, Code: "API_RESPONSE_INVALID", Message: "Agenrena returned invalid JSON"}
	}
	if object, ok := result.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{"result": result}, nil
}

func decodeAgentAPIError(response *http.Response, raw []byte) *APIError {
	result := &APIError{Status: response.StatusCode, Code: fmt.Sprintf("HTTP_%d", response.StatusCode)}
	result.Message = strings.TrimSpace(string(raw))
	if result.Message == "" {
		result.Message = response.Status
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) == nil {
		result.Code = firstNonEmpty(valueString(value["code"]), result.Code)
		result.Message = firstNonEmpty(valueString(value["detail"]), valueString(value["message"]), result.Message)
		if nested, ok := value["error"].(map[string]any); ok {
			result.Code = firstNonEmpty(valueString(nested["code"]), result.Code)
			result.Message = firstNonEmpty(valueString(nested["message"]), result.Message)
		}
	}
	if seconds, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); err == nil && seconds >= 0 {
		result.RetryAfter = time.Duration(seconds * float64(time.Second))
	}
	return result
}

func apiRPCError(err error, idempotent bool) error {
	if rpcErr, ok := err.(*RPCError); ok {
		return rpcErr
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		return wrapBridgeError("API_ERROR", "Agenrena request failed", false, err)
	}
	if apiErr.Ambiguous && !idempotent {
		return bridgeError("DELIVERY_UNKNOWN", "message delivery outcome is unknown", false)
	}
	code := "API_ERROR"
	recoverable := apiErr.Retryable()
	if apiErr.Status == 401 || apiErr.Status == 403 {
		code, recoverable = "AUTH_INVALID", false
	} else if apiErr.Status == 429 {
		code = "RATE_LIMITED"
	} else if apiErr.Status == 0 {
		code, recoverable = "NETWORK_ERROR", idempotent
	}
	return &RPCError{
		Code: code, Message: apiErr.Message, Recoverable: recoverable,
		RetryAfterMS: apiErr.RetryAfter.Milliseconds(), Cause: apiErr,
	}
}

func (client *APIClient) httpClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (client *APIClient) userAgent() string {
	if strings.TrimSpace(client.UserAgent) == "" {
		return "agenrena-agent-bridge"
	}
	return client.UserAgent
}

func newClientMessageID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "bridge-" + hex.EncodeToString(value[:]), nil
}

func valueString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type preparedImage struct {
	ImageBytes      []byte
	ThumbnailBytes  []byte
	Width           int
	Height          int
	ThumbnailWidth  int
	ThumbnailHeight int
	SizeBytes       int
}

func (client *APIClient) prepareOutboundImage(ctx context.Context, input SendMedia) (*preparedImage, error) {
	hasPath := strings.TrimSpace(input.Path) != ""
	hasURL := strings.TrimSpace(input.URL) != ""
	if hasPath == hasURL {
		return nil, bridgeError("MEDIA_INVALID", "each media item requires exactly one path or URL", false)
	}
	var data []byte
	var err error
	if hasPath {
		data, err = readLocalImage(input.Path)
	} else {
		if client.MediaStore == nil {
			return nil, bridgeError("MEDIA_INVALID", "URL media requires a media store", false)
		}
		data, _, _, err = client.MediaStore.Download(ctx, input.URL)
	}
	if err != nil {
		return nil, err
	}
	if mimeType, _, ok := detectedImageType(data); !ok {
		return nil, bridgeError("MEDIA_INVALID", "media is not a supported image", false)
	} else if mimeType == "image/webp" {
		return nil, bridgeError("MEDIA_INVALID", "WebP outbound images are not supported by this CLI build", false)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, wrapBridgeError("MEDIA_INVALID", "could not decode outbound image", false, err)
	}
	display := resizeImageToLongEdge(decoded, 1600)
	displayOpaque := flattenOnWhite(display)
	imageBytes, err := encodeImageJPEG(displayOpaque, 85)
	if err != nil {
		return nil, err
	}
	thumb := flattenOnWhite(resizeImageToLongEdge(displayOpaque, 300))
	thumbBytes, err := encodeImageJPEG(thumb, 80)
	if err != nil {
		return nil, err
	}
	displayBounds, thumbBounds := displayOpaque.Bounds(), thumb.Bounds()
	return &preparedImage{
		ImageBytes: imageBytes, ThumbnailBytes: thumbBytes,
		Width: displayBounds.Dx(), Height: displayBounds.Dy(),
		ThumbnailWidth: thumbBounds.Dx(), ThumbnailHeight: thumbBounds.Dy(),
		SizeBytes: len(imageBytes),
	}, nil
}

func readLocalImage(path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, bridgeError("MEDIA_INVALID", "local media path must be absolute", false)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, bridgeError("MEDIA_INVALID", "local media path must reference a regular file", false)
	}
	if info.Size() > defaultMaxMediaBytes {
		return nil, bridgeError("MEDIA_INVALID", fmt.Sprintf("local media exceeds the %d-byte size limit", defaultMaxMediaBytes), false)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapBridgeError("MEDIA_INVALID", "could not read local media", false, err)
	}
	return data, nil
}

func resizeImageToLongEdge(source image.Image, maxLongEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longEdge := width
	if height > longEdge {
		longEdge = height
	}
	if longEdge <= maxLongEdge || longEdge <= 0 {
		return source
	}
	destinationWidth := width * maxLongEdge / longEdge
	destinationHeight := height * maxLongEdge / longEdge
	if destinationWidth < 1 {
		destinationWidth = 1
	}
	if destinationHeight < 1 {
		destinationHeight = 1
	}
	destination := image.NewRGBA(image.Rect(0, 0, destinationWidth, destinationHeight))
	for y := 0; y < destinationHeight; y++ {
		sourceY := bounds.Min.Y + y*height/destinationHeight
		for x := 0; x < destinationWidth; x++ {
			sourceX := bounds.Min.X + x*width/destinationWidth
			destination.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return destination
}

func flattenOnWhite(source image.Image) *image.RGBA {
	bounds := source.Bounds()
	destination := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(destination, destination.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(destination, destination.Bounds(), source, bounds.Min, draw.Over)
	return destination
}

func encodeImageJPEG(source image.Image, quality int) ([]byte, error) {
	var output bytes.Buffer
	if err := jpeg.Encode(&output, source, &jpeg.Options{Quality: quality}); err != nil {
		return nil, wrapBridgeError("MEDIA_INVALID", "could not encode outbound image", false, err)
	}
	return output.Bytes(), nil
}
