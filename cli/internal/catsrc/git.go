package catsrc

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/catalog/allowlist"
	"makeit/internal/execx"
)

// Git runs git hardened for reading a catalog: no hooks, no submodules, no prompts, only the allowed
// protocols, and a timeout per command.
type Git struct {
	Run execx.Runner
	// Protocols is GIT_ALLOW_PROTOCOL: https:ssh in production, file in tests.
	Protocols string
}

const gitTimeout = 5 * time.Minute

// repoEnv are inherited variables that would point git at another repository (a makeit run from a
// git hook inherits GIT_DIR): the catalog's git never reads them.
var repoEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"}

func (g Git) git(ctx context.Context, dir string, args ...string) (execx.Result, error) {
	argv := append([]string{"git", "-c", "core.hooksPath=/dev/null", "-c", "submodule.recurse=false"}, args...)
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=" + g.Protocols}
	if dir == "" {
		dir = os.TempDir()
	}
	// No password prompt on ssh, unless the user chose their own ssh command.
	if (args[0] == "ls-remote" || args[0] == "fetch") && !g.ownSSH(ctx, dir) {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	res, err := g.Run(ctx, execx.Cmd{Argv: argv, Dir: dir, Env: env, Unset: repoEnv, Timeout: gitTimeout})
	if err == nil && res.Code != 0 {
		err = fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(res.Stderr))
	}
	return res, err
}

// ownSSH reports a user's GIT_SSH, GIT_SSH_COMMAND or core.sshCommand.
func (g Git) ownSSH(ctx context.Context, dir string) bool {
	if os.Getenv("GIT_SSH") != "" || os.Getenv("GIT_SSH_COMMAND") != "" {
		return true
	}
	res, err := g.Run(ctx, execx.Cmd{Argv: []string{"git", "config", "--get", "core.sshCommand"}, Dir: dir, Unset: repoEnv, Timeout: gitTimeout})
	return err == nil && res.Code == 0 && strings.TrimSpace(res.Stdout) != ""
}

// LsRemote lists the remote's tags, peeled to their commits, and its branch heads.
func LsRemote(ctx context.Context, g Git, url string) (Refs, error) {
	res, err := g.git(ctx, "", "ls-remote", "--tags", "--heads", "--", url)
	if err != nil {
		return Refs{}, err
	}
	refs := Refs{Tags: map[string]string{}, Heads: map[string]string{}}
	peeled := map[string]string{}
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			refs.Heads[strings.TrimPrefix(name, "refs/heads/")] = sha
		case strings.HasSuffix(name, "^{}"):
			peeled[strings.TrimSuffix(strings.TrimPrefix(name, "refs/tags/"), "^{}")] = sha
		case strings.HasPrefix(name, "refs/tags/"):
			refs.Tags[strings.TrimPrefix(name, "refs/tags/")] = sha
		}
	}
	for tag, sha := range peeled {
		refs.Tags[tag] = sha
	}
	return refs, nil
}

// Fetch fetches src (refs/tags/<t>, refs/heads/<b>, or a commit) from url into a temporary bare
// repository, checks that it is commit, and reads the commit's allow-listed files as data: no work
// tree, so no checkout, filter or hook runs. The temporary repository is removed.
func Fetch(ctx context.Context, g Git, url, src, commit string) (*catalog.Tree, error) {
	tmp, err := os.MkdirTemp("", "makeit-catalog-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := g.git(ctx, tmp, "init", "--bare", "-q"); err != nil {
		return nil, err
	}
	if _, err := g.git(ctx, tmp, "fetch", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--", url, src+":refs/makeit/fetched"); err != nil {
		if src == commit && refusedByID(err) {
			return nil, fmt.Errorf("%w: %v", errFetchByID, err)
		}
		return nil, err
	}
	res, err := g.git(ctx, tmp, "rev-parse", "--verify", "refs/makeit/fetched^{commit}")
	if err != nil {
		return nil, err
	}
	if got := strings.TrimSpace(res.Stdout); got != commit {
		return nil, fmt.Errorf("%s of %s is %s now, not the resolved %s: run it again", src, url, got, commit)
	}
	// The tree read is the one fetched, which the check above proved is commit.
	res, err = g.git(ctx, tmp, "ls-tree", "-r", "-l", "-z", "refs/makeit/fetched")
	if err != nil {
		return nil, err
	}
	files, exec := map[string][]byte{}, map[string]bool{}
	var total int64
	// Split, not bufio.Scanner: a path past the Scanner's 64 KiB limit would end the read early.
	for entry := range strings.SplitSeq(res.Stdout, "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 || !allowlist.Allowed(path) {
			continue
		}
		mode, typ, obj := f[0], f[1], f[2]
		switch {
		case mode == "120000":
			return nil, fmt.Errorf("catalog %s has a symlink: %s", url, path)
		case typ != "blob":
			return nil, fmt.Errorf("catalog %s has a submodule: %s", url, path)
		}
		size, _ := strconv.ParseInt(f[3], 10, 64)
		if total += size; total > catalog.MaxTree {
			return nil, fmt.Errorf("catalog is larger than %d bytes", catalog.MaxTree)
		}
		blob, err := g.git(ctx, tmp, "cat-file", "blob", obj)
		if err != nil {
			return nil, err
		}
		files[path], exec[path] = []byte(blob.Stdout), mode == "100755"
	}
	return catalog.TreeOf(files, exec)
}

// refusedByID reports a server that would not hand out an object it did not advertise.
func refusedByID(err error) bool {
	m := err.Error()
	return strings.Contains(m, "not our ref") || strings.Contains(m, "unadvertised object") ||
		strings.Contains(m, "couldn't find remote ref") || strings.Contains(m, "not found in upstream")
}
