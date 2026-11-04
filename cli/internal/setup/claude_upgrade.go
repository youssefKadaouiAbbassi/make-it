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

// claudeSeenStep records a move Claude Code made on its own since the last run (native installs
// update themselves), so the run lists it even when nothing else changes.
func (e *env) claudeSeenStep() plan.Step {
	var have string
	return plan.Step{ID: "claude.seen", Title: "Record Claude Code's version",
		Already: func(ctx context.Context) (bool, error) {
			if e.claude == nil {
				if _, _, ok := e.findClaude(ctx); !ok {
					return true, nil
				}
			}
			v, err := e.claude.Version(ctx)
			if err != nil {
				return true, nil
			}
			have = v
			// The first version seen is only recorded (the apply pass saves it): nothing moved yet.
			if e.st.ClaudeCode.VersionSeen == "" && e.gen != nil {
				e.st.ClaudeCode.VersionSeen = have
			}
			return e.st.ClaudeCode.VersionSeen == "" || e.st.ClaudeCode.VersionSeen == have, nil
		},
		Run: func(context.Context) (string, error) {
			e.changes = append(e.changes, state.Change{Kind: "claude-code", Item: "Claude Code", From: e.st.ClaudeCode.VersionSeen, To: have})
			e.st.ClaudeCode.VersionSeen = have
			return e.st.ClaudeCode.VersionSeen, nil
		}}
}

// upgradeRoute is the install's own upgrade command and whether it needs root: a package
// manager behind sudo, or npm whose global prefix is outside the home folder.
func (e *env) upgradeRoute(ctx context.Context) (cmd string, root, ok bool) {
	if e.Locate == nil {
		return "", false, false
	}
	in := e.Locate(ctx)
	if len(in) == 0 {
		return "", false, false
	}
	cask := ""
	if in[0].Source == "brew" {
		if resolved, err := filepath.EvalSymlinks(in[0].Path); err == nil {
			in[0].Source, cask = claudecli.BrewOrigin(resolved)
		}
	}
	cmd = claudecli.UpgradeCommand(in[0].Source, e.Platform.PM, cask)
	root = strings.Contains(cmd, "sudo ") || (in[0].Source == "npm" && !strings.HasPrefix(in[0].Path, e.Roots.Home+string(filepath.Separator)))
	return cmd, root, true
}

// claudeUpgradeStep brings Claude Code to the newest release of its channel on every setup and
// update, through the install's own upgrade. With no one to type a password (--yes, a pipe, the
// daily run) a route that needs root is a note; an upgrade that fails is a note too. Neither
// stops the run.
func (e *env) claudeUpgradeStep() plan.Step {
	var newest string
	cmd, _, _ := e.upgradeRoute(context.Background())
	s := plan.Step{ID: "claude.upgrade", Title: "Update Claude Code"}
	if cmd != "" {
		s.Command = []string{"sh", "-c", cmd}
	}
	s.Already = func(ctx context.Context) (bool, error) {
		if e.claude == nil {
			if _, _, ok := e.findClaude(ctx); !ok {
				return true, nil // the install step's business
			}
		}
		have, err := e.claude.Version(ctx)
		if err != nil || e.Offline || e.RegistryBase == "" {
			return true, nil
		}
		newest, err = e.newestClaude(ctx)
		if err != nil {
			return true, nil
		}
		if c, err := claudecli.Compare(have, newest); err != nil || c >= 0 {
			return true, nil
		}
		c, root, ok := e.upgradeRoute(ctx)
		if !ok {
			return true, nil
		}
		cmd = c
		if root && !e.Root && (!e.prompting() || e.Background) {
			e.keep("claude-code", fmt.Sprintf("Claude Code %s is out (you have %s): makeit update at a terminal upgrades it, it needs your password", newest, have))
			return true, nil
		}
		return false, nil
	}
	s.Run = func(ctx context.Context) (string, error) {
		have, err := e.claude.Version(ctx)
		if err != nil {
			return "", err
		}
		if err := e.runShell(ctx, cmd); err != nil {
			e.keep("claude-code", fmt.Sprintf("Claude Code %s is out (you have %s); its upgrade failed: %v", newest, have, err))
			return "", plan.ErrSkip
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
	}
	return s
}

// newestClaude is Claude Code's newest version for the user's channel, read once per run.
func (e *env) newestClaude(ctx context.Context) (string, error) {
	e.claudeNewestOnce.Do(func() {
		e.claudeNewest, e.claudeNewestErr = claudecli.NewestVersion(ctx, e.client(), e.RegistryBase, e.updatesChannel())
	})
	return e.claudeNewest, e.claudeNewestErr
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
