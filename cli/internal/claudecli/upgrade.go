package claudecli

import (
	"path/filepath"
	"slices"
	"strings"

	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"makeit/internal/fetch"
	"makeit/internal/platform"
	"net/http"
)

// UpgradeCommand is the upgrade command for an install (setup.md 78-82, 92, 291, 409, 435, 457,
// 471), in the forms that need no answer: setup runs it under --yes with no terminal, and doctor
// shows the same command. A claude on PATH at no route's location is taken to come from the
// package manager.
func UpgradeCommand(source string, pm platform.PM, cask string) string {
	switch source {
	case "native":
		return "claude update"
	case "brew":
		if cask == "" {
			cask = "claude-code"
		}
		return "brew upgrade " + cask
	case "npm":
		return "npm install -g @anthropic-ai/claude-code@latest"
	}
	switch pm {
	case platform.APT:
		return "sudo apt-get update && sudo apt-get install -y --only-upgrade claude-code"
	case platform.DNF:
		return "sudo dnf upgrade -y claude-code"
	case platform.APK:
		return "sudo apk update && sudo apk upgrade claude-code"
	case platform.WinGet:
		return "winget upgrade --exact --id Anthropic.ClaudeCode --accept-source-agreements --accept-package-agreements --disable-interactivity"
	}
	return "claude update"
}

// BrewOrigin reads where a bin/claude link in a Homebrew prefix resolves to. Homebrew's node sets
// npm's prefix to the Homebrew prefix (homebrew-core Formula/n/node.rb), so the link may be an
// npm install; otherwise it points into a cask's staged directory, $(brew --caskroom)/<token>/
// <version> (docs.brew.sh Cask-Cookbook). cask is "" when no cask token is in the path.
func BrewOrigin(resolved string) (source, cask string) {
	slashed := filepath.ToSlash(resolved)
	if strings.Contains(slashed, "/node_modules/@anthropic-ai/claude-code/") {
		return "npm", ""
	}
	segs := strings.Split(slashed, "/")
	for _, token := range []string{"claude-code@latest", "claude-code"} {
		if slices.Contains(segs, token) {
			return "brew", token
		}
	}
	return "brew", ""
}

// NewestVersion is the newest Claude Code on a release channel ("latest" when ""; "stable"),
// read from the npm registry's dist-tags at base (https://registry.npmjs.org). The native
// installer, the npm package and Anthropic's apt, dnf and apk repositories publish the same
// versions on the same two channels.
func NewestVersion(ctx context.Context, c *http.Client, base, channel string) (string, error) {
	channel = cmp.Or(channel, "latest")
	var b bytes.Buffer
	if err := fetch.Get(ctx, c, "makeit", base+"/-/package/@anthropic-ai/claude-code/dist-tags", 1<<16, &b); err != nil {
		return "", err
	}
	var tags map[string]string
	if err := json.Unmarshal(b.Bytes(), &tags); err != nil {
		return "", fmt.Errorf("npm dist-tags: %w", err)
	}
	v, ok := tags[channel]
	if !ok || v == "" {
		return "", fmt.Errorf("no Claude Code release on the %q channel", channel)
	}
	return v, nil
}
