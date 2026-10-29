package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/doctor"
	"makeit/internal/execx"
	"makeit/internal/platform"
)

// fakeRunner answers each claude argv (joined by spaces, without the binary). The doctor
// package's test helpers are not visible here, so this is a small copy.
type fakeRunner map[string]execx.Result

func (f fakeRunner) run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{Code: -1}, err
	}
	if r, ok := f[strings.Join(c.Argv[1:], " ")]; ok {
		return r, nil
	}
	return execx.Result{Code: 1, Stderr: "unexpected " + strings.Join(c.Argv[1:], " ")}, nil
}

// embeddedPlugins is `claude plugin list --json` with the embedded bundle and every
// dependency installed and enabled.
func embeddedPlugins(t *testing.T) string {
	t.Helper()
	req, err := catalog.LoadRequires(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	name, _, _ := strings.Cut(req.Bundle, "@")
	raw, err := catalog.Embedded.ReadFile("plugins/" + name + "/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, id := range append([]string{req.Bundle}, manifest.Dependencies...) {
		if !strings.Contains(id, "@") {
			id += "@" + req.Marketplace
		}
		rows = append(rows, fmt.Sprintf(`{"id": %q, "version": "1.0.0", "scope": "user", "enabled": true}`, id))
	}
	return "[" + strings.Join(rows, ", ") + "]"
}

// machine is a doctor.Machine on the embedded catalog: without Claude Code (one error check),
// or with a healthy one (no error checks).
func machine(t *testing.T, withClaude bool) doctor.Machine {
	t.Helper()
	dir := t.TempDir()
	m := doctor.Machine{
		Version: "dev", Target: buildinfo.Target(), CatalogDigest: "sha256:test",
		Platform:     platform.Platform{OS: "linux", Arch: "amd64", PM: platform.APT},
		Routes:       []platform.Route{{Name: "native", Argv: []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}}},
		Catalog:      catalog.Embedded,
		SettingsPath: filepath.Join(dir, "settings.json"),
		StatePath:    filepath.Join(dir, "state.json"),
	}
	if withClaude {
		f := fakeRunner{
			"--version":                      {Stdout: "2.1.311 (Claude Code)\n"},
			"doctor":                         {},
			"plugin marketplace list --json": {Stdout: `[{"name": "makeit", "source": "directory", "path": "/repo"}]`},
			"plugin list --json":             {Stdout: embeddedPlugins(t)},
		}
		m.Installs = []claudecli.Install{{Path: "/home/u/.local/bin/claude", Source: "native", OnPath: true}}
		m.Claude = &claudecli.CLI{Path: m.Installs[0].Path, Run: f.run}
	}
	return m
}

// decodeOne decodes out into v and fails unless out holds exactly one JSON value.
func decodeOne(t *testing.T, out string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, out)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON value (%v): %q", err, out)
	}
}

func gatherOf(m doctor.Machine) func(context.Context) (doctor.Machine, error) {
	return func(context.Context) (doctor.Machine, error) { return m, nil }
}

func noGather(t *testing.T) func(context.Context) (doctor.Machine, error) {
	return func(context.Context) (doctor.Machine, error) {
		t.Error("gather called")
		return doctor.Machine{}, nil
	}
}

func call(t *testing.T, gather func(context.Context) (doctor.Machine, error), args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb, gather)
	return code, out.String(), errb.String()
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code, out, _ := call(t, noGather(t), args...); code != 0 || !strings.Contains(out, "Usage: makeit <command>") {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
	}
	if code, out, errOut := call(t, noGather(t)); code != 2 || out != "" || !strings.Contains(errOut, "Usage: makeit <command>") {
		t.Errorf("no args: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, args := range [][]string{{"help", "doctor"}, {"doctor", "-h"}} {
		if code, out, _ := call(t, noGather(t), args...); code != 0 || !strings.Contains(out, "Usage: makeit doctor") {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
	}
	if code, out, _ := call(t, noGather(t), "help", "exit-codes"); code != 0 || !strings.Contains(out, "143") {
		t.Errorf("help exit-codes: exit %d, stdout %q", code, out)
	}
	if code, out, _ := call(t, noGather(t), "help", "environment"); code != 0 || !strings.Contains(out, "MAKEIT_DIR") {
		t.Errorf("help environment: exit %d, stdout %q", code, out)
	}
	_, out, _ := call(t, noGather(t), "help")
	for _, unbuilt := range []string{"setup", "init", "update", "uninstall"} {
		if strings.Contains(out, "  "+unbuilt+" ") {
			t.Errorf("usage lists %s, which is not built: %q", unbuilt, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, out, errOut := call(t, noGather(t), "docter")
	if code != 2 || out != "" || !strings.Contains(errOut, "did you mean 'doctor'?") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, _, errOut := call(t, noGather(t), "help", "nope"); code != 2 || !strings.Contains(errOut, "nope") {
		t.Errorf("help nope: exit %d, stderr %q", code, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"version", "--bogus"}, {"doctor", "extra"}, {"doctor", "--color", "sometimes"}, {"help", "a", "b"}} {
		if code, out, errOut := call(t, noGather(t), args...); code != 2 || out != "" || errOut == "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestVersion(t *testing.T) {
	if code, out, _ := call(t, noGather(t), "version"); code != 0 || out != "makeit "+buildinfo.Version+"\n" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := call(t, noGather(t), "version", "--json")
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); code != 0 || err != nil {
		t.Fatalf("exit %d, %v: %q", code, err, out)
	}
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"catalogDigest", "commit", "go", "repo", "target", "version"}; !slices.Equal(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}
	m, err := catalog.EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	if v["target"] != buildinfo.Target() || v["catalogDigest"] != catalog.Digest(m) || v["go"] != runtime.Version() || v["version"] != buildinfo.Version {
		t.Errorf("version --json = %v", v)
	}
}

func TestDoctorJSON(t *testing.T) {
	code, out, errOut := call(t, gatherOf(machine(t, false)), "doctor", "--json")
	var report struct {
		Version  string         `json:"version"`
		Checks   []doctor.Check `json:"checks"`
		Errors   int            `json:"errors"`
		Warnings int            `json:"warnings"`
	}
	decodeOne(t, out, &report)
	if code != 1 || report.Errors == 0 || errOut != "" || len(report.Checks) == 0 {
		t.Errorf("without claude: exit %d, errors %d, stderr %q", code, report.Errors, errOut)
	}
	code, out, errOut = call(t, gatherOf(machine(t, false)), "doctor")
	if code != 1 || out != "" || !strings.Contains(errOut, "Claude Code is not installed") || !strings.Contains(errOut, "errors,") {
		t.Errorf("text mode: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, out, _ := call(t, gatherOf(machine(t, true)), "doctor", "--json"); code != 0 || !strings.Contains(out, `"errors": 0`) {
		t.Errorf("healthy: exit %d, stdout %q", code, out)
	}
}

func TestDoctorJSONHidesSecret(t *testing.T) {
	m := machine(t, true)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "makeit.json"), `{"schemaVersion": 1, "requires": {"claudeCode": {"hard": "2.1.287", "soft": "2.1.310"}},
		"marketplace": "makeit", "bundle": "makeit-bundle@makeit"}`)
	write(t, filepath.Join(dir, "settings.baseline.json"), `{"env": {"API_KEY": "stack-s3cret", "NTFY_TOPIC": "stack-topic"}}`)
	write(t, filepath.Join(dir, "plugins", "makeit-bundle", ".claude-plugin", "plugin.json"), `{"dependencies": []}`)
	m.Catalog = catalog.Dir(dir)
	write(t, m.SettingsPath, `{"env": {"NTFY_TOPIC": "my-topic"}}`)
	for _, args := range [][]string{{"doctor", "--json"}, {"doctor"}} {
		_, out, errOut := call(t, gatherOf(m), args...)
		for _, secret := range []string{"stack-s3cret", "stack-topic", "my-topic"} {
			if strings.Contains(out+errOut, secret) {
				t.Errorf("%v prints %s: %s%s", args, secret, out, errOut)
			}
		}
	}
}

func TestSignalCode(t *testing.T) {
	if got := signalCode(os.Interrupt); got != 130 {
		t.Errorf("SIGINT = %d, want 130", got)
	}
	if got := signalCode(syscall.SIGTERM); got != 143 {
		t.Errorf("SIGTERM = %d, want 143", got)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	if code := run(ctx, []string{"doctor"}, &out, &errb, gatherOf(machine(t, true))); code == 0 {
		t.Errorf("a cancelled doctor exits 0: %s", errb.String())
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// tree lists every path under dir with its size and modification time.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %s", path, info.Size(), info.ModTime()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for claude")
	}
	home, bin := t.TempDir(), t.TempDir()
	// Builtins only: PATH holds nothing but this directory.
	write(t, filepath.Join(bin, "claude"), `#!/bin/sh
case "$*" in
  "--version") echo "2.1.311 (Claude Code)" ;;
  "doctor") ;;
  "plugin marketplace list --json") echo '[{"name": "makeit", "source": "directory", "path": "/repo"}]' ;;
  "plugin list --json") printf '%s\n' '`+embeddedPlugins(t)+`' ;;
  *) echo "unexpected $*" >&2; exit 1 ;;
esac
`)
	write(t, filepath.Join(home, ".claude", "settings.json"), "{}")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("MAKEIT_DIR", filepath.Join(home, ".makeit"))
	t.Setenv("PATH", bin)
	before := tree(t, home)
	gather := func(ctx context.Context) (doctor.Machine, error) {
		return doctor.Gather(ctx, buildinfo.Version, buildinfo.Target(), "sha256:test")
	}
	code, out, errOut := call(t, gather, "doctor", "--json")
	var report struct {
		Checks []doctor.Check `json:"checks"`
		Errors int            `json:"errors"`
	}
	if errOut != "" {
		t.Errorf("stderr in --json mode: %q", errOut)
	}
	decodeOne(t, out, &report)
	levels := map[string]doctor.Level{}
	for _, c := range report.Checks {
		levels[c.ID] = c.Level
	}
	for _, id := range []string{"claude.installed", "claude.version", "settings.valid", "marketplace", "plugin.makeit-bundle@makeit"} {
		if levels[id] != doctor.OK {
			t.Errorf("%s = %s, want ok: %v", id, levels[id], report.Checks)
		}
	}
	if (code == 1) != (report.Errors > 0) || (code != 0 && code != 1) {
		t.Errorf("exit %d with %d errors", code, report.Errors)
	}
	if after := tree(t, home); !slices.Equal(before, after) {
		t.Errorf("doctor wrote under HOME:\nbefore %v\nafter  %v", before, after)
	}
}
