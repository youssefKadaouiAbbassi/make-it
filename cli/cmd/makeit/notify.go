package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"makeit/internal/cli"
	"makeit/internal/notify"
	"makeit/internal/paths"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// runNotify asks both phone questions again, whatever setup recorded.
func runNotify(p cli.Parsed, stderr io.Writer) int {
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

func askNotify(u *ui.UI) error {
	roots, err := paths.Current()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(roots.Claude, "settings.json")
	yes, err := u.Confirm("Send notifications to your phone through ntfy?", true)
	if err != nil {
		return err
	}
	if yes {
		topic, err := notify.EnableNtfy(settingsPath, rand.Reader)
		if err != nil {
			return err
		}
		u.Ok("subscribe to https://ntfy.sh/%s in the ntfy app or a browser; the topic is the password, keep it private", topic)
	}
	yes, err = u.Confirm("Also use Claude Code's own phone push (connects sessions to Remote Control)?", false)
	if err != nil {
		return err
	}
	if yes {
		if err := notify.EnablePush(settingsPath); err != nil {
			return err
		}
		u.Ok("Claude Code push is on: restart Claude Code to connect")
	}
	statePath := filepath.Join(roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil {
		return err
	}
	st.Notify = "asked"
	return state.SaveState(statePath, st)
}
