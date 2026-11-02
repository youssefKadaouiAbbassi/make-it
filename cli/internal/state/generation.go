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

// Prune deletes all but the newest Keep generations. Call it only after a successful apply.
// shortcut: generations referenced by an unresolved conflict are not pinned yet; add a keep set
// when conflicts start pointing at generations.
func Prune(stackDir string) error {
	ns, err := generations(stackDir)
	if err != nil {
		return err
	}
	for len(ns) > Keep {
		if err := os.RemoveAll(filepath.Join(GenerationsDir(stackDir), strconv.Itoa(ns[0]))); err != nil {
			return err
		}
		ns = ns[1:]
	}
	return nil
}

// LoadGenerations reads every generation's manifest, newest first. A folder without a readable
// manifest is left out.
func LoadGenerations(stackDir string) ([]*Generation, error) {
	ns, err := generations(stackDir)
	if err != nil {
		return nil, err
	}
	var out []*Generation
	for i := len(ns) - 1; i >= 0; i-- {
		dir := filepath.Join(GenerationsDir(stackDir), strconv.Itoa(ns[i]))
		var m Manifest
		data, found, err := fsx.ReadOptional(filepath.Join(dir, "manifest.json"))
		if err != nil || !found || json.Unmarshal(data, &m) != nil {
			continue
		}
		out = append(out, &Generation{Dir: dir, Manifest: m})
	}
	return out, nil
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
