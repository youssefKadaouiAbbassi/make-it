package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"makeit/internal/fsx"
	"makeit/internal/state"
)

// autoEvery is how often the daily run goes ahead.
const autoEvery = 24 * time.Hour

// autoDue returns the held auto lock when `makeit update --auto` should run now (the caller
// releases it), or nil and why not.
func autoDue(stack string, now time.Time) (*fsx.Lock, string) {
	if !autoUpdateOn(stack) {
		return nil, "auto-update is off (" + filepath.Join(stack, "config.json") + ")"
	}
	// The check runs under the lock the run keeps, so two sessions starting at once run once.
	lock, err := fsx.Acquire(filepath.Join(stack, "auto.lock"), time.Now())
	if err != nil {
		return nil, "another auto update is running"
	}
	if b, err := os.ReadFile(filepath.Join(stack, "last-auto")); err == nil {
		if last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && now.Sub(last) < autoEvery {
			lock.Release()
			return nil, fmt.Sprintf("ran %dh ago", int(now.Sub(last).Hours()))
		}
	}
	return lock, ""
}

// autoNote is the one line the next Claude Code session shows after a daily run moved something.
func autoNote(changes []state.Change, undoable bool) string {
	var parts []string
	for _, c := range changes {
		parts = append(parts, c.Item+" "+changeText(c))
	}
	if len(parts) == 0 {
		return ""
	}
	note := "makeit updated: " + strings.Join(parts, ", ")
	// Rollback undoes the run, or puts the old makeit back; npm and Claude Code moves alone it leaves.
	if undoable || slices.ContainsFunc(changes, func(c state.Change) bool { return c.Kind == "makeit" }) {
		note += " · undo: makeit rollback"
	}
	return note
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
func finishAuto(stack string, changes []state.Change, undoable bool) error {
	if note := autoNote(changes, undoable); note != "" {
		if err := fsx.WriteAtomic(filepath.Join(stack, "pending-note"), []byte(note+"\n"), 0o600); err != nil {
			return err
		}
	}
	return fsx.WriteAtomic(filepath.Join(stack, "last-auto"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}
