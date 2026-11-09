// Package notify turns on the two phone channels in Claude Code's user settings.json: an ntfy
// topic for makeit's own hook, and Claude Code's own push through Remote Control.
package notify

import (
	"encoding/base32"
	"fmt"
	"io"
	"os"
	"strings"

	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
)

// EnableNtfy writes env NTFY_TOPIC into settings.json and returns the topic: the one already
// there, or a new "makeit-" + 20 random base32 characters (100 bits, so it cannot be guessed).
func EnableNtfy(settingsPath string, rand io.Reader) (string, error) {
	buf := make([]byte, 13)
	if _, err := io.ReadFull(rand, buf); err != nil {
		return "", err
	}
	topic := "makeit-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))[:20]
	env := jsonx.NewObject()
	env.Set("NTFY_TOPIC", topic)
	want := jsonx.NewObject()
	want.Set("env", env)
	out, err := apply(settingsPath, want, false)
	if err != nil {
		return "", err
	}
	if env, ok := out["env"].(*jsonx.Object); ok {
		if v, ok := env.Get("NTFY_TOPIC"); ok {
			if s, ok := v.(string); ok && s != "" {
				return s, nil
			}
		}
	}
	return topic, nil
}

// EnablePush sets remoteControlAtStartup, inputNeededNotifEnabled and agentPushNotifEnabled to
// true in settings.json (Claude Code documents all three as settings.json keys; ~/.claude.json
// only holds older values). The file is copied to <path>.makeit-bak first; a symlink is refused.
func EnablePush(settingsPath string) error {
	want := jsonx.NewObject()
	for _, k := range []string{"remoteControlAtStartup", "inputNeededNotifEnabled", "agentPushNotifEnabled"} {
		want.Set(k, true)
	}
	_, err := apply(settingsPath, want, true)
	return err
}

// apply merges want into the file, taking the value over an existing one only with force.
// Every other key keeps its value and its place.
func apply(path string, want *jsonx.Object, force bool) (map[string]any, error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; makeit does not write through it", path)
	}
	data, found, err := fsx.ReadOptional(path)
	if err != nil {
		return nil, err
	}
	theirs := jsonx.NewObject()
	perm := os.FileMode(0o600)
	if found {
		if theirs, err = jsonx.DecodeObject(data); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		perm = fi.Mode().Perm()
		if err := fsx.WriteAtomic(path+".makeit-bak", data, perm); err != nil {
			return nil, err
		}
	}
	opts := settings.DefaultOptions()
	opts.Force = force
	res := settings.Merge(theirs, want, nil, opts)
	if res.Changed() {
		out, err := jsonx.Marshal(res.Settings)
		if err != nil {
			return nil, err
		}
		if err := fsx.WriteAtomic(path, out, perm); err != nil {
			return nil, err
		}
	}
	m := map[string]any{}
	for _, k := range res.Settings.Keys() {
		m[k], _ = res.Settings.Get(k)
	}
	return m, nil
}
