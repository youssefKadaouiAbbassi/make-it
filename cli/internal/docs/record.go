package docs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"makeit/internal/fsx"
)

// Record is the last sync of one project: the manifests it read, so a hook can tell whether
// anything changed. It lives under <stack>/docs/, not in the repository.
type Record struct {
	Dir       string            `json:"dir"`
	Manifests map[string]string `json:"manifests"`
	SyncedAt  time.Time         `json:"syncedAt"`
	// Failed marks a sync that left libraries out for a reason a retry can change (Report.Failed).
	Failed bool `json:"failed,omitempty"`
}

// LockPath is the lock one docs sync holds, apart from the stack's run lock: a sync takes
// minutes, and setup, update and init must not wait on it.
func LockPath(stack string) string { return filepath.Join(stack, "docs", "lock") }

func recordPath(stack, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return filepath.Join(stack, "docs", fsx.Sha256([]byte(abs))[len("sha256:"):]+".json")
}

// LoadRecord returns dir's record, or an empty one.
func LoadRecord(stack, dir string) Record {
	var r Record
	if b, err := os.ReadFile(recordPath(stack, dir)); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return r
}

// SaveRecord writes dir's record.
func SaveRecord(stack, dir string, r Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := recordPath(stack, dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return fsx.WriteAtomic(p, append(b, '\n'), 0o644)
}

// ServerArgv reads the docs server's command from a plugin .mcp.json, without --read-only: the
// sync writes the index the plugin's read-only server serves.
func ServerArgv(mcpJSON []byte) ([]string, error) {
	var m struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpJSON, &m); err != nil {
		return nil, fmt.Errorf(".mcp.json: %w", err)
	}
	s, ok := m.MCPServers["docs"]
	if !ok || s.Command == "" {
		return nil, errors.New(".mcp.json has no docs server")
	}
	argv := []string{s.Command}
	for _, a := range s.Args {
		if a != "--read-only" {
			argv = append(argv, a)
		}
	}
	return argv, nil
}
