package codexbridge

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Hashes record notifications, not proof of model reading.
type guidanceUpdate struct {
	hashes map[string]string
	notice string
}

type guidanceSource struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Missing bool   `json:"missing,omitempty"`
	hash    string
}

func guidanceFile(name, path string) (guidanceSource, error) {
	source := guidanceSource{Name: name, Path: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		source.Missing, source.hash = true, "missing"
		return source, nil
	}
	if err != nil {
		return source, err
	}
	if !info.Mode().IsRegular() {
		return source, fmt.Errorf("guidance must be a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return source, err
	}
	source.hash = fmt.Sprintf("%x", sha256.Sum256(data))
	return source, nil
}

func skillGuidance(root string) (guidanceSource, error) {
	source := guidanceSource{Name: "skill", Path: filepath.Join(root, "SKILL.md")}
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			source.Missing = true
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		file, err := guidanceFile("", path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// WalkDir sorts paths; names and byte hashes detect edits, additions and removals.
		fmt.Fprintf(h, "%s\x00%s\x00", filepath.ToSlash(rel), file.hash)
		return nil
	})
	if err != nil {
		return source, err
	}
	if source.Missing {
		source.hash = "missing"
	} else {
		source.hash = fmt.Sprintf("%x", h.Sum(nil))
	}
	if _, err := os.Stat(source.Path); errors.Is(err, os.ErrNotExist) {
		source.Missing = true
	} else if err != nil {
		return source, err
	}
	return source, nil
}

func (runner codexRunner) guidanceUpdate(threadID string) (*guidanceUpdate, error) {
	if runner.guidanceStore == nil {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	skillRoot := os.Getenv("AGENRENA_CODEX_BRIDGE_SKILL_DIR")
	if skillRoot == "" {
		candidates := []string{
			filepath.Join(runner.settings.Workspace, ".agents/skills/agenrena-skill"),
			filepath.Join(home, ".agents/skills/agenrena-skill"),
			filepath.Join(codexHome, "skills/agenrena-skill"),
		}
		skillRoot = candidates[0]
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				skillRoot = candidate
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
	// Workspace skills may be symlinks to the app's bundled skill directory.
	if resolved, err := filepath.EvalSymlinks(skillRoot); err == nil {
		skillRoot = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	skill, err := skillGuidance(skillRoot)
	if err != nil {
		return nil, err
	}
	guide, err := Guidance()
	if err != nil {
		return nil, err
	}
	sources := []guidanceSource{skill, {Name: "bridge", Command: "agenrena codex bridge guidance", hash: fmt.Sprintf("%x", sha256.Sum256([]byte(guide)))}}
	for _, source := range []struct{ name, path string }{
		{"global AGENTS.md", filepath.Join(codexHome, "AGENTS.md")},
		{"workspace AGENTS.md", filepath.Join(runner.settings.Workspace, "AGENTS.md")},
	} {
		file, err := guidanceFile(source.name, source.path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, file)
	}
	return compareGuidance(sources, runner.guidanceStore.guidanceHashes(threadID)), nil
}

func compareGuidance(sources []guidanceSource, previous map[string]string) *guidanceUpdate {
	hashes := make(map[string]string, len(sources))
	changed := []guidanceSource{}
	for _, source := range sources {
		hashes[source.Name] = source.hash
		if previous[source.Name] != source.hash {
			changed = append(changed, source)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	data, _ := json.Marshal(changed)
	return &guidanceUpdate{hashes: hashes, notice: "Local guidance is new or has changed. Before handling this message, reread the listed files or run the listed guidance command. For skill, read SKILL.md and any relevant references again; reference content may have changed too. Entries marked missing were removed: do not read them or rely on their old content. Use the current guidance, subject to higher-priority instructions. Keep this notification internal; do not send it to the customer. This notification changes no tools or permissions and does not reset the conversation. Sources: " + string(data)}
}

func (store *StateStore) guidanceHashes(threadID string) map[string]string {
	store.mu.Lock()
	defer store.mu.Unlock()
	copy := map[string]string{}
	for key, value := range store.data.GuidanceByThread[threadID] {
		copy[key] = value
	}
	return copy
}

func (store *StateStore) rememberGuidance(threadID string, hashes map[string]string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.data
	next.GuidanceByThread = make(map[string]map[string]string, len(store.data.GuidanceByThread)+1)
	for key, value := range store.data.GuidanceByThread {
		next.GuidanceByThread[key] = value
	}
	copy := map[string]string{}
	for key, value := range hashes {
		copy[key] = value
	}
	next.GuidanceByThread[threadID] = copy
	if err := atomicWriteJSON(store.path, next); err != nil {
		return err
	}
	store.data = next
	return nil
}

// Guidance returns the same bridge instructions and tool descriptions used by the runner.
func Guidance() (string, error) {
	tools, err := json.MarshalIndent([]any{handoffTool(), attachImageTool(), inputRequestTool()}, "", "  ")
	if err != nil {
		return "", err
	}
	text := "# Agenrena bridge guidance\n\n" + transportPolicyInstructions() + "\n\n" + inputRequestInstructions + "\n\n## Tool reference\n\nThese describe bridge tools; use only tools actually available in the current session.\n\n```json\n" + string(tools) + "\n```\n"
	return text, nil
}
