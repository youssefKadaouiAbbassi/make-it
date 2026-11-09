package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"makeit/internal/cli"
	"makeit/internal/fsx"
	"makeit/internal/notify"
	"makeit/internal/paths"
	"makeit/internal/settings"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// runNotify asks both phone questions again, whatever setup recorded.
func runNotify(p cli.Parsed, stderr io.Writer) int {
	if p.Bool["dry-run"] {
		fmt.Fprintf(stderr, "%s: notify has no dry run: it only asks and writes what you answer yes to\n", prog)
		return 2
	}
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !p.Bool["json"]
	if !interactive {
		fmt.Fprintf(stderr, "%s: notify asks questions and needs a terminal\n", prog)
		return 2
	}
	u := ui.New(os.Stdin, stderr, true, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Mode = ui.DecideMode(isTTY(os.Stdout), isTTY(stderr), os.Getenv, false, p.Bool["plain"] || p.Bool["verbose"])
	if err := askNotify(u); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	return 0
}

// askNotifyOnce is setup's closing question: only at a terminal, and only until it has been asked.
func askNotifyOnce(u *ui.UI) error {
	if !u.Interactive {
		return nil
	}
	roots, err := paths.Current()
	if err != nil {
		return err
	}
	st, _, err := state.LoadState(filepath.Join(roots.Stack, "state.json"))
	if err != nil || st.Notify != "" {
		return err
	}
	return askNotify(u)
}

// askNotify asks both questions, then writes under the stack lock, so it never races a modifying
// run. Reached by setup, by makeit notify and, through doctor's "Fix now?" when the fix is setup,
// by a first-time user there too; that is intended.
func askNotify(u *ui.UI) error {
	roots, err := paths.Current()
	if err != nil {
		return err
	}
	ntfy, err := u.Confirm("Send notifications to your phone through ntfy?", true)
	if err != nil {
		return err
	}
	push, err := u.Confirm("Also use Claude Code's own phone push (connects sessions to Remote Control)?", false)
	if err != nil {
		return err
	}
	lock, err := fsx.Acquire(filepath.Join(roots.Stack, "lock"), time.Now())
	if err != nil {
		return err
	}
	defer lock.Release()
	statePath := filepath.Join(roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(roots.Claude, "settings.json")
	var added []string
	if st.NotifyKeys == nil {
		st.NotifyKeys = settings.State{}
	}
	// The record is saved before settings.json is written, so a crash between the two leaves a
	// record of a key that is not there, which uninstall skips. A failed write takes its record
	// back out: a stale one would keep an Original that is no longer true.
	record := func(written settings.State) error {
		for k, r := range written {
			if st.NotifyKeys[k] == nil {
				st.NotifyKeys[k] = r
				added = append(added, k)
			}
		}
		return state.SaveState(statePath, st)
	}
	rollBack := func() {
		for _, k := range added {
			delete(st.NotifyKeys, k)
		}
		added = nil
		_ = state.SaveState(statePath, st)
	}
	if ntfy {
		topic, err := notify.EnableNtfy(settingsPath, rand.Reader, record)
		if err != nil {
			rollBack()
			return err
		}
		added = nil
		u.Ok("subscribe to https://ntfy.sh/%s in the ntfy app or a browser; the topic is the password, keep it private", topic)
	}
	if push {
		if err := notify.EnablePush(settingsPath, record); err != nil {
			rollBack()
			return err
		}
		u.Ok("Claude Code push is on: restart Claude Code to connect")
	}
	st.Notify = "asked"
	return state.SaveState(statePath, st)
}
