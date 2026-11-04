package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"makeit/internal/claudecli"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// claudeUpgradeStep brings Claude Code to the newest release of its channel on every setup and
// update, through the install's own upgrade. The daily run never asks for a password: a route
// that needs sudo is left with a note. A move Claude Code made on its own since the last run
// (native installs update themselves) is shown too.
func (e *env) claudeUpgradeStep() plan.Step {
	var newest, cmd string
	return plan.Step{ID: "claude.upgrade", Title: "Update Claude Code",
		Already: func(ctx context.Context) (bool, error) {
			if e.claude == nil {
				if _, _, ok := e.findClaude(ctx); !ok {
					return true, nil // the install step's business
				}
			}
			have, err := e.claude.Version(ctx)
			if err != nil {
				return true, nil
			}
			if e.gen != nil {
				if seen := e.st.ClaudeCode.VersionSeen; seen != "" && seen != have {
					e.changes = append(e.changes, state.Change{Kind: "claude-code", Item: "Claude Code", From: seen, To: have})
				}
				e.st.ClaudeCode.VersionSeen = have
			}
			if e.Offline || e.RegistryBase == "" {
				return true, nil
			}
			newest, err = claudecli.NewestVersion(ctx, e.client(), e.RegistryBase, e.updatesChannel())
			if err != nil {
				return true, nil
			}
			if c, err := claudecli.Compare(have, newest); err != nil || c >= 0 {
				return true, nil
			}
			in := e.Locate(ctx)
			if len(in) == 0 {
				return true, nil
			}
			cask := ""
			if in[0].Source == "brew" {
				if resolved, err := filepath.EvalSymlinks(in[0].Path); err == nil {
					in[0].Source, cask = claudecli.BrewOrigin(resolved)
				}
			}
			cmd = claudecli.UpgradeCommand(in[0].Source, e.Platform.PM, cask)
			if e.Background && !e.Root && strings.Contains(cmd, "sudo ") {
				e.keep("claude-code", fmt.Sprintf("Claude Code %s is out (you have %s): makeit update upgrades it, it needs your password", newest, have))
				return true, nil
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			have, err := e.claude.Version(ctx)
			if err != nil {
				return "", err
			}
			if err := e.runShell(ctx, cmd); err != nil {
				return "", err
			}
			now, err := e.claude.Version(ctx)
			if err != nil {
				return "", err
			}
			if now == have {
				e.keep("claude-code", fmt.Sprintf("Claude Code %s is not in your package manager yet; you have %s", newest, have))
				return "", plan.ErrSkip
			}
			e.st.ClaudeCode.VersionSeen = now
			e.changes = append(e.changes, state.Change{Kind: "claude-code", Item: "Claude Code", From: have, To: now})
			return have + " → " + now, nil
		}}
}

// updatesChannel is the user's autoUpdatesChannel ("latest" when unset), which Claude Code's own
// updates follow too.
func (e *env) updatesChannel() string {
	b, _, _ := fsx.ReadOptional(filepath.Join(e.Roots.Claude, "settings.json"))
	var s struct {
		Channel string `json:"autoUpdatesChannel"`
	}
	_ = json.Unmarshal(b, &s)
	return s.Channel
}
