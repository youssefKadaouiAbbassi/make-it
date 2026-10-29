// Package state reads and writes the files where makeit records what it did: state.json
// (machine state), receipt.json (how the binary was installed), <repo>/.makeit.json (the
// committed per-repository record) and the generations that hold pre-images.
//
// Every file carries schemaVersion. An older file goes through ordered migrations, a newer one
// is refused, and members this binary does not know survive a round trip.
package state

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/fsx"
	"makeit/internal/settings"
)

// SchemaVersion of state.json, receipt.json and .makeit.json this binary writes. It is
// bumped only for a breaking change of shape, in a minor release.
const SchemaVersion = 1

// Revision counts additive changes that older binaries read on. It is not a write counter.
const Revision = 1

// State is state.json. Paths in it use the roots claude:, stack: and home: (package paths).
type State struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Revision      int                     `json:"revision"`
	CLIVersion    string                  `json:"cliVersion"`
	WrittenAt     time.Time               `json:"writtenAt,omitzero"`
	Generation    int                     `json:"generation"`
	Machine       Machine                 `json:"machine"`
	Catalog       Catalog                 `json:"catalog"`
	ClaudeCode    ClaudeCode              `json:"claudeCode"`
	Settings      Settings                `json:"settings"`
	Files         map[string]*File        `json:"files,omitempty"`
	Dirs          map[string]*Dir         `json:"dirs,omitempty"`
	Plugins       map[string]*Plugin      `json:"plugins,omitempty"`
	Marketplaces  map[string]*Marketplace `json:"marketplaces,omitempty"`
	Prereqs       map[string]*Prereq      `json:"prereqs,omitempty"`
	Steps         map[string]*StepRecord  `json:"steps,omitempty"`
	Migrations    []MigrationRun          `json:"migrations,omitempty"`
	// Extra, here and in every record, holds members written by a newer revision, so they
	// survive a round trip.
	Extra jsontext.Value `json:",embed"`
}

// Machine is where state was written.
type Machine struct {
	OS    string         `json:"os"`
	Arch  string         `json:"arch"`
	WSL   bool           `json:"wsl"`
	Extra jsontext.Value `json:",embed"`
}

// Catalog is the catalog source in use.
type Catalog struct {
	Kind     string `json:"kind"` // embedded, directory or git
	Location string `json:"location,omitempty"`
	// Requested is the ref asked for; nil when none was.
	Requested *string `json:"requested"`
	// Resolved is "sha256:<tree digest>" or a 40-hex commit.
	Resolved        string         `json:"resolved,omitempty"`
	MarketplaceName string         `json:"marketplaceName,omitempty"`
	Extra           jsontext.Value `json:",embed"`
}

// ClaudeCode is what makeit last saw of Claude Code.
type ClaudeCode struct {
	VersionSeen string         `json:"versionSeen,omitempty"`
	Extra       jsontext.Value `json:",embed"`
}

// Settings is the merge record for Claude Code's settings.json.
type Settings struct {
	File                       string         `json:"file,omitempty"`
	LastWrittenCanonicalSha256 string         `json:"lastWrittenCanonicalSha256,omitempty"`
	Paths                      settings.State `json:"paths,omitempty"`
	// CreatedByUs: makeit created settings.json, so uninstall may remove it when nothing else is left.
	CreatedByUs bool           `json:"createdByUs,omitempty"`
	Extra       jsontext.Value `json:",embed"`
}

// File is a file makeit wrote.
type File struct {
	Class      string         `json:"class"` // owned or seeded
	Source     string         `json:"source,omitempty"`
	Sha256     string         `json:"sha256"`
	PreExisted bool           `json:"preExisted"`
	Generation int            `json:"generation"`
	Extra      jsontext.Value `json:",embed"`
}

// Dir is a directory makeit may have created.
type Dir struct {
	CreatedByUs bool           `json:"createdByUs"`
	Extra       jsontext.Value `json:",embed"`
}

// Plugin is a plugin makeit installed, owned by observation of `claude plugin list`.
type Plugin struct {
	InstalledBy string         `json:"installedBy"`
	Scope       string         `json:"scope"`
	Generation  int            `json:"generation"`
	Observed    PluginObserved `json:"observed"`
	Extra       jsontext.Value `json:",embed"`
}

// PluginObserved is what `claude plugin list --json` reported after the install.
type PluginObserved struct {
	Version      string         `json:"version,omitempty"`
	GitCommitSha string         `json:"gitCommitSha,omitempty"`
	Extra        jsontext.Value `json:",embed"`
}

// Marketplace records who added a marketplace.
type Marketplace struct {
	AddedBy string         `json:"addedBy"` // makeit or user
	Kind    string         `json:"kind,omitempty"`
	Extra   jsontext.Value `json:",embed"`
}

// Prereq is a prerequisite makeit installed.
type Prereq struct {
	InstalledBy string         `json:"installedBy"`
	Route       string         `json:"route"`
	Argv        []string       `json:"argv,omitempty"`
	Extra       jsontext.Value `json:",embed"`
}

// StepRecord is the outcome of one plan step, journaled after each step.
type StepRecord struct {
	Status string         `json:"status"`
	At     time.Time      `json:"at,omitzero"`
	Error  string         `json:"error,omitempty"`
	Extra  jsontext.Value `json:",embed"`
}

// MigrationRun records one migration applied to the file.
type MigrationRun struct {
	ID   string    `json:"id"`
	From int       `json:"from"`
	To   int       `json:"to"`
	At   time.Time `json:"at,omitzero"`
}

// Receipt is receipt.json, written only by `self install`, so a binary placed by a package
// manager has none and never updates itself.
type Receipt struct {
	SchemaVersion int            `json:"schemaVersion"`
	Method        string         `json:"method"` // script
	BinPath       string         `json:"binPath"`
	Version       string         `json:"version"`
	Target        string         `json:"target"`
	InstalledAt   time.Time      `json:"installedAt,omitzero"`
	Previous      *string        `json:"previous"`
	ModifyPath    bool           `json:"modifyPath"`
	Migrations    []MigrationRun `json:"migrations,omitempty"`
	Extra         jsontext.Value `json:",embed"`
}

// Repo is <repo>/.makeit.json, the committed record of what `init` seeded.
type Repo struct {
	Comment       string                  `json:"$comment,omitempty"`
	SchemaVersion int                     `json:"schemaVersion"`
	CLI           string                  `json:"cli"`
	Catalog       RepoCatalog             `json:"catalog"`
	Answers       RepoAnswers             `json:"answers"`
	Files         map[string]*RepoFile    `json:"files,omitempty"`
	Rulesets      map[string]*RepoRuleset `json:"rulesets,omitempty"`
	Migrations    []MigrationRun          `json:"migrations,omitempty"`
	Extra         jsontext.Value          `json:",embed"`
}

// RepoCatalog is the catalog `init` used.
type RepoCatalog struct {
	Kind     string `json:"kind"`
	Resolved string `json:"resolved,omitempty"`
}

// RepoAnswers are the answers `init` was given.
type RepoAnswers struct {
	Types           []string `json:"types,omitempty"`
	ProductPriority string   `json:"productPriority,omitempty"`
}

// RepoFile is a file `init` seeded.
type RepoFile struct {
	Class         string `json:"class"`
	Template      string `json:"template"`
	WrittenSha256 string `json:"writtenSha256"`
	PreExisted    bool   `json:"preExisted"`
}

// RepoRuleset is a GitHub ruleset `init` sent.
type RepoRuleset struct {
	ID         int64  `json:"id"`
	SentSha256 string `json:"sentSha256"`
}

// RepoComment is the $comment of every .makeit.json.
const RepoComment = "written by makeit init; change it through makeit init --update"

// Migration upgrades a decoded file by one schema version. Up must be idempotent: it acts only
// when the old shape exists and the new one does not.
// shortcut: Up edits the JSON document only; the first migration that moves a file must also
// return a plan step, so the move shows in --dry-run and gets a pre-image.
type Migration struct {
	ID   string
	From int
	To   int
	// RemoveBy is the release from which the migration is deleted; a test enforces it.
	RemoveBy string
	Up       func(doc map[string]any) error
}

// v1 ships no migrations. The TypeScript draft's state file (no schemaVersion) was never applied
// to a machine, so it is refused with a move-aside instruction instead of migrated.
var (
	stateMigrations   []Migration
	receiptMigrations []Migration
	repoMigrations    []Migration
)

// FormatError reports a file whose schema this binary cannot write: a newer one, or one older
// than the oldest migration kept. Read-only commands may warn and go on; modifying ones refuse.
type FormatError struct {
	File      string
	Have      int
	Supported int
	WrittenBy string
}

func (e *FormatError) Error() string {
	switch {
	case e.Have > e.Supported:
		by := "a newer makeit"
		if e.WrittenBy != "" {
			by = "makeit " + e.WrittenBy
		}
		return fmt.Sprintf("%s is format %d written by %s; this is %s: run makeit self update",
			e.File, e.Have, by, buildinfo.Version)
	case e.Have == 0:
		return fmt.Sprintf("%s has no schemaVersion: it comes from a pre-release draft of makeit; "+
			"move it aside (mv %s %s.draft) and run the command again", e.File, e.File, e.File)
	default:
		return fmt.Sprintf("%s is format %d, older than this makeit can migrate: "+
			"install an older makeit once, run update, then upgrade", e.File, e.Have)
	}
}

// Info says how a file was loaded.
type Info struct {
	// Found is false when the file did not exist and a fresh document was returned.
	Found bool
	// PreMigration holds the file's bytes before migration; nil when none ran. The caller
	// keeps it in the generation as state.pre-migration.json before saving.
	PreMigration []byte
}

// LoadState reads state.json. A missing file gives a fresh state.
func LoadState(path string) (*State, Info, error) {
	s := &State{SchemaVersion: SchemaVersion, Revision: Revision}
	info, err := load(path, s, stateMigrations)
	// Callers add entries without nil checks; omitempty drops empty maps from the file.
	initMap(&s.Files)
	initMap(&s.Dirs)
	initMap(&s.Plugins)
	initMap(&s.Marketplaces)
	initMap(&s.Prereqs)
	initMap(&s.Steps)
	initMap(&s.Settings.Paths)
	return s, info, err
}

func initMap[M ~map[K]V, K comparable, V any](m *M) {
	if *m == nil {
		*m = M{}
	}
}

// SaveState writes state.json with mode 0600, stamping the writer and the time.
func SaveState(path string, s *State) error {
	if err := writable(path, s.SchemaVersion); err != nil {
		return err
	}
	s.Revision = max(s.Revision, Revision)
	s.CLIVersion = buildinfo.Version
	s.WrittenAt = time.Now().UTC()
	return save(path, s, 0o600)
}

// LoadReceipt reads receipt.json. A missing file gives found=false and a nil receipt.
func LoadReceipt(path string) (*Receipt, Info, error) {
	r := &Receipt{}
	info, err := load(path, r, receiptMigrations)
	if !info.Found {
		return nil, info, err
	}
	return r, info, err
}

// SaveReceipt writes receipt.json.
func SaveReceipt(path string, r *Receipt) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = SchemaVersion
	}
	if err := writable(path, r.SchemaVersion); err != nil {
		return err
	}
	return save(path, r, 0o644)
}

// LoadRepo reads <repo>/.makeit.json. A missing file gives a fresh record.
func LoadRepo(path string) (*Repo, Info, error) {
	r := &Repo{Comment: RepoComment, SchemaVersion: SchemaVersion}
	info, err := load(path, r, repoMigrations)
	initMap(&r.Files)
	initMap(&r.Rulesets)
	return r, info, err
}

// SaveRepo writes <repo>/.makeit.json, which is committed, so mode 0644.
func SaveRepo(path string, r *Repo) error {
	if err := writable(path, r.SchemaVersion); err != nil {
		return err
	}
	r.Comment = RepoComment
	r.CLI = buildinfo.Version
	return save(path, r, 0o644)
}

// writable refuses to write a document whose schema is not this binary's: nothing that cannot
// be parsed is ever rewritten.
func writable(path string, have int) error {
	if have != SchemaVersion {
		return &FormatError{File: path, Have: have, Supported: SchemaVersion}
	}
	return nil
}

// header is read first, so the error for a newer file works even when the rest would not decode.
// The writer fields are raw, so a non-string member of the same name in a newer file cannot fail it.
type header struct {
	SchemaVersion int            `json:"schemaVersion"`
	CLIVersion    jsontext.Value `json:"cliVersion"`
	CLI           jsontext.Value `json:"cli"`
	Version       jsontext.Value `json:"version"`
}

// str is the string a raw member holds, or "" when it is not a string.
func str(v jsontext.Value) string {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

func load(path string, out any, migrations []Migration) (Info, error) {
	data, found, err := fsx.ReadOptional(path)
	if err != nil || !found {
		return Info{}, err
	}
	info := Info{Found: true}
	var h header
	if err := json.Unmarshal(data, &h); err != nil {
		return info, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if h.SchemaVersion > SchemaVersion {
		return info, &FormatError{File: path, Have: h.SchemaVersion, Supported: SchemaVersion,
			WrittenBy: cmp.Or(str(h.CLIVersion), str(h.CLI), str(h.Version))}
	}
	if h.SchemaVersion < SchemaVersion {
		migrated, err := migrate(path, data, h.SchemaVersion, SchemaVersion, migrations, time.Now().UTC())
		if err != nil {
			return info, err
		}
		info.PreMigration, data = data, migrated
	}
	if err := json.Unmarshal(data, out); err != nil {
		return info, fmt.Errorf("read %s: %w", path, err)
	}
	return info, nil
}

// migrate applies migrations from have up to want, in order, recording each in "migrations".
func migrate(path string, data []byte, have, want int, migrations []Migration, now time.Time) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	runs, _ := doc["migrations"].([]any)
	for v := have; v < want; {
		var m *Migration
		for i := range migrations {
			if migrations[i].From == v {
				m = &migrations[i]
				break
			}
		}
		if m == nil {
			return nil, &FormatError{File: path, Have: have, Supported: want}
		}
		if m.To <= m.From {
			return nil, fmt.Errorf("migration %s does not advance (%d -> %d)", m.ID, m.From, m.To)
		}
		if err := m.Up(doc); err != nil {
			return nil, fmt.Errorf("migrate %s with %s: %w", path, m.ID, err)
		}
		v = m.To
		doc["schemaVersion"] = v
		runs = append(runs, map[string]any{"id": m.ID, "from": m.From, "to": m.To, "at": now.Format(time.RFC3339Nano)})
	}
	doc["migrations"] = runs
	return json.Marshal(doc)
}

func save(path string, v any, perm fs.FileMode) error {
	b, err := Encode(v)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(path, b, perm)
}

// Encode is the on-disk form: sorted map keys, two-space indent, trailing newline, so the same
// document always gives the same bytes.
func Encode(v any) ([]byte, error) {
	b, err := json.Marshal(v, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
