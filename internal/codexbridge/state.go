package codexbridge

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Media struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Data string `json:"data,omitempty"`
}

type Reply struct {
	InboundMessageID string  `json:"inboundMessageID"`
	Route            string  `json:"route"`
	ThreadID         string  `json:"threadID"`
	TurnID           string  `json:"turnID"`
	Text             string  `json:"text"`
	Media            []Media `json:"media,omitempty"`
	ClientMessageID  string  `json:"clientMessageID"`
}

type stateData struct {
	Version             int               `json:"version"`
	ThreadToolsVersion  int               `json:"threadToolsVersion"`
	Sessions            map[string]string `json:"sessions"`
	PendingReplies      map[string]Reply  `json:"pendingReplies"`
	CompletedMessageIDs []string          `json:"completedMessageIDs"`
}

type StateStore struct {
	path string
	mu   sync.Mutex
	data stateData
}

func NewStateStore(path string) *StateStore {
	return &StateStore{path: path, data: freshState()}
}

func freshState() stateData {
	return stateData{
		Version: configVersion, ThreadToolsVersion: threadToolsVersion,
		Sessions: make(map[string]string), PendingReplies: make(map[string]Reply),
		CompletedMessageIDs: []string{},
	}
}

func (store *StateStore) Load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	var loaded stateData
	if err := readJSON(store.path, &loaded); err != nil {
		return err
	}
	if loaded.Version != configVersion {
		store.data = freshState()
		return nil
	}
	if loaded.Sessions == nil || loaded.ThreadToolsVersion != threadToolsVersion {
		loaded.Sessions = make(map[string]string)
	}
	if loaded.PendingReplies == nil {
		loaded.PendingReplies = make(map[string]Reply)
	}
	if loaded.CompletedMessageIDs == nil {
		loaded.CompletedMessageIDs = []string{}
	}
	loaded.Version = configVersion
	loaded.ThreadToolsVersion = threadToolsVersion
	store.data = loaded
	return nil
}

func (store *StateStore) ThreadID(route string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.data.Sessions[route]
}

func (store *StateStore) Completed(id string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return contains(store.data.CompletedMessageIDs, id)
}

func (store *StateStore) Pending(id string) (Reply, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	reply, ok := store.data.PendingReplies[id]
	return reply, ok
}

func (store *StateStore) PendingReplies() []Reply {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]Reply, 0, len(store.data.PendingReplies))
	for _, reply := range store.data.PendingReplies {
		result = append(result, reply)
	}
	return result
}

func (store *StateStore) Record(reply Reply) (Reply, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	staged, err := store.stageMedia(reply.Media, reply.ClientMessageID)
	if err != nil {
		return Reply{}, err
	}
	reply.Media = staged
	store.data.Sessions[reply.Route] = reply.ThreadID
	store.data.PendingReplies[reply.InboundMessageID] = reply
	if err := atomicWriteJSON(store.path, store.data); err != nil {
		return Reply{}, err
	}
	return reply, nil
}

func (store *StateStore) CompleteWithoutReply(inboundMessageID, route, threadID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if threadID != "" {
		store.data.Sessions[route] = threadID
	}
	delete(store.data.PendingReplies, inboundMessageID)
	store.addCompleted(inboundMessageID)
	return atomicWriteJSON(store.path, store.data)
}

func (store *StateStore) MarkSent(id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	reply := store.data.PendingReplies[id]
	delete(store.data.PendingReplies, id)
	store.addCompleted(id)
	if err := atomicWriteJSON(store.path, store.data); err != nil {
		return err
	}
	mediaRoot, _ := filepath.Abs(filepath.Join(filepath.Dir(store.path), "outbound"))
	for _, media := range reply.Media {
		if media.Path == "" {
			continue
		}
		resolved, _ := filepath.Abs(media.Path)
		if filepath.Dir(resolved) == mediaRoot {
			_ = os.Remove(resolved)
		}
	}
	return nil
}

func (store *StateStore) addCompleted(id string) {
	filtered := make([]string, 0, len(store.data.CompletedMessageIDs)+1)
	for _, value := range store.data.CompletedMessageIDs {
		if value != id {
			filtered = append(filtered, value)
		}
	}
	filtered = append(filtered, id)
	if len(filtered) > maxCompletedIDs {
		filtered = filtered[len(filtered)-maxCompletedIDs:]
	}
	store.data.CompletedMessageIDs = filtered
}

func (store *StateStore) stageMedia(media []Media, clientMessageID string) ([]Media, error) {
	if len(media) > maxOutboundMediaCount {
		return nil, fmt.Errorf("a reply may contain at most %d images", maxOutboundMediaCount)
	}
	if len(media) == 0 {
		return []Media{}, nil
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(store.path), "outbound"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	prefix := sanitizeMediaPrefix(clientMessageID)
	staged := make([]Media, 0, len(media))
	total := 0
	for index, value := range media {
		if value.URL != "" {
			parsed, err := url.Parse(value.URL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
				return nil, errors.New("Codex generated image URL must be an absolute HTTPS URL without credentials")
			}
			staged = append(staged, Media{URL: parsed.String()})
			continue
		}
		data, err := readGeneratedImage(value)
		if err != nil {
			return nil, err
		}
		if len(data) > maxOutboundMediaBytes {
			return nil, fmt.Errorf("generated image %d exceeds the %d-byte limit", index+1, maxOutboundMediaBytes)
		}
		total += len(data)
		if total > maxTotalOutboundMedia {
			return nil, fmt.Errorf("generated images exceed the %d-byte total limit", maxTotalOutboundMedia)
		}
		extension, err := imageExtension(data)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(root, fmt.Sprintf("%s-%d%s", prefix, index+1, extension))
		temporary := filepath.Join(root, fmt.Sprintf(".%s-%d.%d.%d.tmp", prefix, index+1, os.Getpid(), time.Now().UnixNano()))
		if err := os.WriteFile(temporary, data, 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Remove(temporary)
			return nil, err
		}
		staged = append(staged, Media{Path: target})
	}
	return staged, nil
}

var invalidMediaPrefix = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizeMediaPrefix(value string) string {
	if value == "" {
		value = "codex-image"
	}
	value = invalidMediaPrefix.ReplaceAllString(value, "_")
	if len(value) > 80 {
		value = value[:80]
	}
	if value == "" {
		return "codex-image"
	}
	return value
}

func readGeneratedImage(media Media) ([]byte, error) {
	if media.Path != "" {
		if !filepath.IsAbs(media.Path) {
			return nil, errors.New("Codex generated image path must be absolute")
		}
		info, statErr := os.Stat(media.Path)
		if statErr == nil && info.Mode().IsRegular() && info.Size() <= maxOutboundMediaBytes {
			data, readErr := os.ReadFile(media.Path)
			if readErr == nil {
				if _, typeErr := imageExtension(data); typeErr == nil {
					return data, nil
				}
			}
		}
		if media.Data == "" {
			return nil, fmt.Errorf("could not read Codex generated image at %s", media.Path)
		}
	}
	encoded := strings.TrimSpace(media.Data)
	if comma := strings.Index(encoded, ","); strings.HasPrefix(strings.ToLower(encoded), "data:image/") && comma >= 0 {
		encoded = encoded[comma+1:]
	}
	if encoded == "" || strings.Contains(encoded, "://") {
		return nil, errors.New("Codex image generation did not provide usable image bytes")
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return nil, errors.New("Codex image generation did not provide usable image bytes")
	}
	if _, err := imageExtension(data); err != nil {
		return nil, err
	}
	return data, nil
}

func imageExtension(data []byte) (string, error) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if len(data) >= len(png) && bytes.Equal(data[:len(png)], png) {
		return ".png", nil
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return ".jpg", nil
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return ".gif", nil
	}
	return "", errors.New("Codex generated media is not a supported PNG, JPEG, or GIF image")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
