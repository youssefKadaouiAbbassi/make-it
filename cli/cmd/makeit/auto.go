package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"makeit/internal/fsx"
	"makeit/internal/state"
)

// autoEvery is how often the daily run goes ahead.
const autoEvery = 24 * time.Hour

// autoDue reports whether `makeit update --auto` should run now, and why not when it should not.
func autoDue(stack string, now time.Time) (bool, string) {
	if !autoUpdateOn(stack) {
		return false, "auto-update is off (" + filepath.Join(stack, "config.json") + ")"
	}
	lock, err := fsx.Acquire(filepath.Join(stack, "auto.lock"), time.Now())
	if err != nil {
		return false, "another auto update is running"
	}
	lock.Release()
	if b, err := os.ReadFile(filepath.Join(stack, "last-auto")); err == nil {
		if last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && now.Sub(last) < autoEvery {
			return false, fmt.Sprintf("ran %dh ago", int(now.Sub(last).Hours()))
		}
	}
	return true, ""
}

// autoNote is the one line the next Claude Code session shows after a daily run moved something.
func autoNote(changes []state.Change) string {
	var parts []string
	for _, c := range changes {
		parts = append(parts, c.Item+" "+changeText(c))
	}
	if len(parts) == 0 {
		return ""
	}
	return "makeit updated: " + strings.Join(parts, ", ") + " · undo: makeit rollback"
}

// autoLogMax keeps update.log small: past it, the next run starts the file over.
const autoLogMax = 1 << 20

// openAutoLog opens ~/.makeit/update.log for one run's output.
func openAutoLog(stack string) (*os.File, error) {
	path := filepath.Join(stack, "update.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > autoLogMax {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	if err := os.MkdirAll(stack, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, flags, 0o600)
}

// finishAuto records the daily run: when it ran, and the note for the next session.
func finishAuto(stack string, changes []state.Change) error {
	if note := autoNote(changes); note != "" {
		if err := fsx.WriteAtomic(filepath.Join(stack, "pending-note"), []byte(note+"\n"), 0o600); err != nil {
			return err
		}
	}
	return fsx.WriteAtomic(filepath.Join(stack, "last-auto"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}
