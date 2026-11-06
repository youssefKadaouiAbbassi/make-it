package state

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/fsx"
)

// Keep is how many generations survive a prune.
const Keep = 5

// Manifest is generations/<N>/manifest.json: what one run changed, with pointers to the
// pre-images that let rollback restore it.
type Manifest struct {
	Generation int       `json:"generation"`
	CLIVersion string    `json:"cliVersion"`
	Command    string    `json:"command"`
	Argv       []string  `json:"argv"` // redacted by the caller
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	Outcome    string    `json:"outcome,omitempty"`
	Ops        []Op      `json:"ops"`
	Plugins    PluginOps `json:"plugins"`
	// StateBackup is "state.pre-migration.json" when the run migrated state.json.
	StateBackup *string `json:"stateBackup"`
	// Pins are the plugin release moves the run made in the local plugin set (update's latest step).
	Pins []PinMove `json:"pins,omitempty"`
	// Changes are the versions the run moved, for the result, the daily note and rollback.
	Changes []Change `json:"changes,omitempty"`
	// RolledBack is set once makeit rollback has undone this run, so the next rollback goes further back.
	RolledBack bool `json:"rolledBack,omitempty"`
}

// Undoable reports whether rollback has anything to undo in this run: files, plugin releases, MCP
// server versions or tools. Claude Code's own moves, makeit's and the npm tools' are left as they
// are (never pin), so a run with only those is not one rollback picks.
func (m Manifest) Undoable() bool {
	return len(m.Ops) > 0 || len(m.Pins) > 0 || slices.ContainsFunc(m.Changes, func(c Change) bool { return c.Kind == "mcp" || c.Kind == "tool" })
}

// Change is one thing a run moved from one version to another: kind is makeit, claude-code,
// plugin, mcp or tool; From is "" when the earlier version is not known.
type Change struct {
	Kind string `json:"kind"`
	Item string `json:"item"`
	From string `json:"from"`
	To   string `json:"to"`
}

// PinMove is one plugin moved from one release to another.
type PinMove struct {
	ID      string `json:"id"`
	FromTag string `json:"fromTag"`
	FromSHA string `json:"fromSha"`
	ToTag   string `json:"toTag"`
	ToSHA   string `json:"toSha"`
}

// Op is one change to a file.
type Op struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Path string `json:"path"`
	// PreSha256 and PostSha256 are nil when the file was absent before or after.
	PreSha256  *string `json:"preSha256"`
	PostSha256 *string `json:"postSha256"`
	// PreImage is the copy of the file before the op, relative to the generation directory.
	PreImage string `json:"preImage,omitempty"`
}

// PluginOps lists the plugin ids a run changed.
type PluginOps struct {
	Installed   []string `json:"installed"`
	Uninstalled []string `json:"uninstalled"`
	Updated     []string `json:"updated"`
}

// Generation is one run's directory under stack:generations.
type Generation struct {
	Dir      string
	Manifest Manifest
	images   int
}

// GenerationsDir is where the generations live.
func GenerationsDir(stackDir string) string { return filepath.Join(stackDir, "generations") }

// NewGeneration creates the next generation's directory: one past both last (state.Generation)
// and every folder on disk, so leftovers from a reset state.json are never reused or outranked.
// The caller stores Manifest.Generation in state.
func NewGeneration(stackDir string, last int, command string, argv []string) (*Generation, error) {
	ns, err := generations(stackDir)
	if err != nil {
		return nil, err
	}
	if len(ns) > 0 {
		last = max(last, ns[len(ns)-1])
	}
	n := last + 1
	dir := filepath.Join(GenerationsDir(stackDir), strconv.Itoa(n))
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return nil, err
	}
	g := &Generation{Dir: dir, Manifest: Manifest{
		Generation: n, Command: command, Argv: argv, StartedAt: time.Now().UTC(),
		Ops: []Op{}, Plugins: PluginOps{Installed: []string{}, Uninstalled: []string{}, Updated: []string{}},
	}}
	return g, g.Save()
}

// KeepStateBackup stores the pre-migration bytes of state.json in the generation.
func (g *Generation) KeepStateBackup(data []byte) error {
	const name = "state.pre-migration.json"
	if err := fsx.WriteAtomic(filepath.Join(g.Dir, name), data, 0o600); err != nil {
		return err
	}
	n := name
	g.Manifest.StateBackup = &n
	return g.Save()
}

// PreImage copies data as the next pre-image and returns its path relative to the generation.
func (g *Generation) PreImage(data []byte) (string, error) {
	g.images++
	rel := fmt.Sprintf("files/%04d", g.images)
	return rel, fsx.WriteAtomic(filepath.Join(g.Dir, filepath.FromSlash(rel)), data, 0o600)
}

// Record appends an op and journals the manifest.
func (g *Generation) Record(op Op) error {
	g.Manifest.Ops = append(g.Manifest.Ops, op)
	return g.Save()
}

// Save writes the manifest, stamping the writer.
func (g *Generation) Save() error {
	g.Manifest.CLIVersion = buildinfo.Version
	return save(filepath.Join(g.Dir, "manifest.json"), &g.Manifest, 0o600)
}

// Prune deletes the generations rollback has nothing to undo in, except the newest, then all but
// the newest Keep of the rest. Call it only after a successful apply.
// shortcut: generations referenced by an unresolved conflict are not pinned yet; add a keep set
// when conflicts start pointing at generations.
func Prune(stackDir string) error {
	// Leftovers of an interrupted prune.
	leftovers, _ := filepath.Glob(filepath.Join(GenerationsDir(stackDir), "*.del"))
	for _, d := range leftovers {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	gens, err := LoadGenerations(stackDir)
	if err != nil {
		return err
	}
	// Runs with nothing to undo and no backups go first, except the newest (the home screen and the
	// daily note read it); then the newest Keep of the rest stay.
	for i, g := range gens {
		if i > 0 && !g.Manifest.Undoable() && !holdsBackups(g.Dir) {
			if err := removeGeneration(g.Dir); err != nil {
				return err
			}
		}
	}
	ns, err := generations(stackDir)
	if err != nil {
		return err
	}
	for len(ns) > Keep {
		if err := removeGeneration(filepath.Join(GenerationsDir(stackDir), strconv.Itoa(ns[0]))); err != nil {
			return err
		}
		ns = ns[1:]
	}
	return nil
}

// holdsBackups: uninstall's copy of state.json or plugin data saved before a removal, which no op
// records and no other place keeps.
func holdsBackups(dir string) bool {
	for _, name := range []string{"state.json", "data"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// removeGeneration renames the folder out of the numbered set first, so an interrupted removal
// never leaves a half record that rollback would refuse to pass.
func removeGeneration(dir string) error {
	if err := os.Rename(dir, dir+".del"); err != nil {
		return err
	}
	return os.RemoveAll(dir + ".del")
}

// LoadGenerations reads every generation's manifest, newest first. A folder without a readable
// manifest is left out; Unreadable lists those.
func LoadGenerations(stackDir string) ([]*Generation, error) {
	ns, err := generations(stackDir)
	if err != nil {
		return nil, err
	}
	var out []*Generation
	for i := len(ns) - 1; i >= 0; i-- {
		if g, ok := readGeneration(stackDir, ns[i]); ok {
			out = append(out, g)
		}
	}
	return out, nil
}

// Unreadable are the generation numbers on disk whose manifest cannot be read, ascending.
func Unreadable(stackDir string) ([]int, error) {
	ns, err := generations(stackDir)
	var bad []int
	for _, n := range ns {
		if _, ok := readGeneration(stackDir, n); !ok {
			bad = append(bad, n)
		}
	}
	return bad, err
}

func readGeneration(stackDir string, n int) (*Generation, bool) {
	dir := filepath.Join(GenerationsDir(stackDir), strconv.Itoa(n))
	var m Manifest
	data, found, err := fsx.ReadOptional(filepath.Join(dir, "manifest.json"))
	if err != nil || !found || json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	return &Generation{Dir: dir, Manifest: m}, true
}

// generations lists the generation numbers on disk, ascending.
func generations(stackDir string) ([]int, error) {
	entries, err := os.ReadDir(GenerationsDir(stackDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ns []int
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() {
			ns = append(ns, n)
		}
	}
	slices.Sort(ns)
	return ns, nil
}
