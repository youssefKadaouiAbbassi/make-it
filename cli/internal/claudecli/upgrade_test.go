package claudecli

import (
	"testing"

	"makeit/internal/platform"
)

func TestUpgradeFix(t *testing.T) {
	for _, c := range []struct {
		source string
		pm     platform.PM
		cask   string
		want   string
	}{
		{"native", platform.APT, "", "claude update"},
		{"brew", platform.Brew, "claude-code", "brew upgrade claude-code"},
		{"brew", platform.Brew, "claude-code@latest", "brew upgrade claude-code@latest"},
		{"brew", platform.Brew, "", "brew upgrade claude-code"},
		{"npm", platform.APT, "", "npm install -g @anthropic-ai/claude-code@latest"},
		{"path", platform.APT, "", "sudo apt update && sudo apt upgrade claude-code"},
		{"path", platform.DNF, "", "sudo dnf upgrade claude-code"},
		{"path", platform.APK, "", "apk update && apk upgrade claude-code"},
		{"path", platform.WinGet, "", "winget upgrade Anthropic.ClaudeCode"},
		{"path", platform.Pacman, "", "claude update"},
		{"path", platform.None, "", "claude update"},
	} {
		if got := UpgradeCommand(c.source, c.pm, c.cask); got != c.want {
			t.Errorf("UpgradeCommand(%s, %s, %q) = %q, want %q", c.source, c.pm, c.cask, got, c.want)
		}
	}
}

func TestBrewOrigin(t *testing.T) {
	for in, want := range map[string][2]string{
		"/opt/homebrew/Caskroom/claude-code@latest/2.1.311/claude":       {"brew", "claude-code@latest"},
		"/home/linuxbrew/.linuxbrew/Caskroom/claude-code/2.1.300/claude": {"brew", "claude-code"},
		"/home/u/.local/share/claude/versions/2.1.311":                   {"brew", ""},
		// npm install -g with Homebrew's node: the link sits in the Homebrew prefix.
		"/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe": {"npm", ""},
		"": {"brew", ""},
	} {
		if source, cask := BrewOrigin(in); source != want[0] || cask != want[1] {
			t.Errorf("BrewOrigin(%q) = %q, %q, want %q, %q", in, source, cask, want[0], want[1])
		}
	}
}
