// Command applysettings runs the settings merge on files, for installing the stack by hand
// before the setup command exists: it writes the candidate settings.json and the merge state, and
// prints every change with secrets redacted. It never writes the live settings file itself.
//
//	go run ./tools/applysettings -theirs settings.json -baseline ../settings.baseline.json \
//	  -out candidate.json -state merge-state.json [-force]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
)

func show(path string, v any) string {
	if v == nil {
		return "-"
	}
	if settings.IsSecret(path) {
		return "<redacted>"
	}
	b, err := jsonx.Compact(v)
	if err != nil {
		return "?"
	}
	if len(b) > 160 {
		return string(b[:160]) + "..."
	}
	return string(b)
}

func run() error {
	theirsPath := flag.String("theirs", "", "current settings.json")
	baselinePath := flag.String("baseline", "", "settings.baseline.json")
	outPath := flag.String("out", "", "candidate settings.json to write")
	statePath := flag.String("state", "", "merge state to read (if present) and write")
	force := flag.Bool("force", false, "take the stack value on held and conflicting paths")
	flag.Parse()

	theirsRaw, err := os.ReadFile(*theirsPath)
	if err != nil {
		return err
	}
	theirs, err := jsonx.DecodeObject(theirsRaw)
	if err != nil {
		return fmt.Errorf("%s: %w", *theirsPath, err)
	}
	baseRaw, err := os.ReadFile(*baselinePath)
	if err != nil {
		return err
	}
	baseline, err := jsonx.DecodeObject(baseRaw)
	if err != nil {
		return fmt.Errorf("%s: %w", *baselinePath, err)
	}
	desired := settings.Desired(baseline, "makeit")
	var prev settings.State
	if b, ok, err := fsx.ReadOptional(*statePath); err != nil {
		return err
	} else if ok {
		if err := json.Unmarshal(b, &prev); err != nil {
			return fmt.Errorf("%s: %w", *statePath, err)
		}
	}
	opts := settings.DefaultOptions()
	opts.Force = *force
	r := settings.Merge(theirs, desired, prev, opts)
	for _, c := range r.Changes {
		if c.Action == settings.Noop {
			continue
		}
		fmt.Printf("%-12s %s: %s -> %s %s\n", c.Action, c.Path, show(c.Path, c.Before), show(c.Path, c.After), c.Note)
	}
	out, err := jsonx.Marshal(r.Settings)
	if err != nil {
		return err
	}
	if err := fsx.WriteAtomic(*outPath, out, 0o600); err != nil {
		return err
	}
	st, err := json.MarshalIndent(r.State, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("changed=%v canonical=%s\n", r.Changed(), jsonx.Hash(r.Settings))
	return fsx.WriteAtomic(*statePath, append(st, '\n'), 0o600)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "applysettings:", err)
		os.Exit(1)
	}
}
