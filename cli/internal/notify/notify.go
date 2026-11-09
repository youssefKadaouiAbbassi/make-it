// Package notify turns on the two phone channels in Claude Code's user settings.json: an ntfy
// topic for makeit's own hook, and Claude Code's own push through Remote Control.
package notify

import (
	"encoding/base32"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
)

// EnableNtfy writes env NTFY_TOPIC into settings.json and returns the topic in use: the one
// already there, or a new "makeit-" + 20 random base32 characters (100 bits, so it cannot be
// guessed). before gets the merge record of what is about to be written, before the file changes
// (it is not called when the topic is kept); an error from it stops the write.
func EnableNtfy(settingsPath string, rand io.Reader, before func(settings.State) error) (topic string, err error) {
	buf := make([]byte, 13)
	if _, err := io.ReadFull(rand, buf); err != nil {
		return "", err
	}
	topic = "makeit-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))[:20]
	env := jsonx.NewObject()
	env.Set("NTFY_TOPIC", topic)
	want := jsonx.NewObject()
	want.Set("env", env)
	// An empty or non-string topic is not one: it is replaced, any other is the user's.
	err = update(settingsPath, want, func(live *jsonx.Object) bool {
		if e, ok := live.Get("env"); ok {
			if eo, ok := e.(*jsonx.Object); ok {
				if v, ok := eo.Get("NTFY_TOPIC"); ok {
					if s, ok := v.(string); ok && s != "" {
						topic = s
						return false
					}
				}
			}
		}
		return true
	}, before)
	return topic, err
}

// EnablePush sets remoteControlAtStartup, inputNeededNotifEnabled and agentPushNotifEnabled to
// true in settings.json (Claude Code documents all three as settings.json keys; ~/.claude.json
// only holds older values). before is as for EnableNtfy.
func EnablePush(settingsPath string, before func(settings.State) error) error {
	want := jsonx.NewObject()
	for _, k := range []string{"remoteControlAtStartup", "inputNeededNotifEnabled", "agentPushNotifEnabled"} {
		want.Set(k, true)
	}
	return update(settingsPath, want, func(*jsonx.Object) bool { return true }, before)
}

// update merges want into settings.json, taking the value over an existing one. Every other key
// keeps its value and its place. A symlink is written through, so the link survives.
func update(path string, want *jsonx.Object, wanted func(*jsonx.Object) bool, before func(settings.State) error) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("%s is a symlink to a missing file; fix the link first: %w", path, err)
		}
		path = target
	}
	data, found, err := fsx.ReadOptional(path)
	if err != nil {
		return err
	}
	live := jsonx.NewObject()
	perm := fs.FileMode(0o600)
	if found {
		if live, err = jsonx.DecodeObject(data); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		perm = fi.Mode().Perm()
	}
	if !wanted(live) {
		return nil
	}
	opts := settings.DefaultOptions()
	opts.Force = true
	res := settings.Merge(live, want, nil, opts)
	if !res.Changed() {
		return nil
	}
	written := settings.State{}
	for _, c := range res.Changes {
		if c.Action == settings.Set || c.Action == settings.ReAsserted {
			written[c.Path] = res.State[c.Path]
		}
	}
	if before != nil {
		if err := before(written); err != nil {
			return err
		}
	}
	out, err := jsonx.Marshal(res.Settings)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(path, out, perm)
}
