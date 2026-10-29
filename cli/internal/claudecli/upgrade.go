package claudecli

import (
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/platform"
)

// UpgradeCommand is the upgrade command for an install (setup.md 78-82, 92, 291, 409, 435, 457, 471).
// A claude on PATH at no route's location is taken to come from the package manager.
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
		return "sudo apt update && sudo apt upgrade claude-code"
	case platform.DNF:
		return "sudo dnf upgrade claude-code"
	case platform.APK:
		return "apk update && apk upgrade claude-code"
	case platform.WinGet:
		return "winget upgrade Anthropic.ClaudeCode"
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
