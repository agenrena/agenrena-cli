package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	githubRepo     = "agenrena/agenrena-cli"
	installCommand = "curl -fsSL https://raw.githubusercontent.com/agenrena/agenrena-cli/main/install.sh | sh"
)

type updateInfo struct {
	Available      bool   `json:"available"`
	Required       bool   `json:"required"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	InstallCommand string `json:"install_command"`
	Error          string `json:"error,omitempty"`
}

func checkForUpdate(ctx context.Context) updateInfo {
	info := updateInfo{
		CurrentVersion: cliVersion,
		InstallCommand: installCommand,
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+githubRepo+"/releases/latest", nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agenrena-cli/"+cliVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		info.Error = fmt.Sprintf("GitHub latest release returned %s", resp.Status)
		return info
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		info.Error = err.Error()
		return info
	}
	latest := normalizeVersion(body.TagName)
	info.LatestVersion = latest
	info.ReleaseURL = body.HTMLURL
	if compareSemver(latest, cliVersion) > 0 {
		info.Available = true
	}
	return info
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func compareSemver(a, b string) int {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] > bp[i] {
			return 1
		}
		if ap[i] < bp[i] {
			return -1
		}
	}
	return 0
}

func parseSemver(version string) [3]int {
	var parsed [3]int
	parts := strings.Split(normalizeVersion(version), ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		value := 0
		for _, r := range parts[i] {
			if r < '0' || r > '9' {
				break
			}
			value = value*10 + int(r-'0')
		}
		parsed[i] = value
	}
	return parsed
}
