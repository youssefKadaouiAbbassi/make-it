// Package execx runs the external programs makeit drives (claude, git, gh, package
// managers) with a timeout, captured output, and argument checks for Windows batch files.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Cmd describes one program run.
type Cmd struct {
	Argv []string
	Dir  string
	// Env entries are added to the inherited environment, overriding keys of the same name.
	Env []string
	// Unset are inherited variables removed before Env is added.
	Unset []string
	Stdin io.Reader
	// Inherit connects the terminal, for package managers and sudo that prompt the user.
	Inherit bool
	Timeout time.Duration
}

// Result is what the program did.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Text is stdout and stderr together, trimmed, for messages.
func (r Result) Text() string { return strings.TrimSpace(r.Stdout + "\n" + r.Stderr) }

// Runner runs a command. Commands take one so tests can stand in for claude and friends.
type Runner func(ctx context.Context, c Cmd) (Result, error)

// LookPath finds a program on PATH. A found name is not proof it runs; callers that care run
// its version command.
func LookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// batchMeta are the characters cmd.exe interprets inside arguments to .cmd and .bat files.
// os/exec quotes for CommandLineToArgvW, not for cmd.exe (src/os/exec/exec.go), so such
// arguments are refused instead of quoted.
const batchMeta = "%!^&|<>\"()"

// CheckBatchArgs refuses arguments cmd.exe would reinterpret when the target is a batch file.
func CheckBatchArgs(path string, args []string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return nil
	}
	for _, a := range args {
		if strings.ContainsAny(a, batchMeta) {
			return fmt.Errorf("refusing to pass %q to the batch file %s: cmd.exe would reinterpret it", a, path)
		}
	}
	return nil
}

// Run is the real Runner.
func Run(ctx context.Context, c Cmd) (Result, error) {
	if len(c.Argv) == 0 {
		return Result{}, errors.New("execx: empty command")
	}
	path, err := exec.LookPath(c.Argv[0])
	if err != nil {
		return Result{Code: 127}, fmt.Errorf("%s is not on PATH", c.Argv[0])
	}
	if runtime.GOOS == "windows" {
		if err := CheckBatchArgs(path, c.Argv[1:]); err != nil {
			return Result{Code: 2}, err
		}
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, c.Argv[1:]...)
	cmd.Dir = c.Dir
	// A killed program's children can keep the output pipes open; stop waiting for them.
	cmd.WaitDelay = time.Second
	if len(c.Env) > 0 || len(c.Unset) > 0 {
		env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
			k, _, _ := strings.Cut(kv, "=")
			return slices.Contains(c.Unset, k)
		})
		cmd.Env = append(env, c.Env...)
	}
	var out, errb bytes.Buffer
	if c.Inherit {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	} else {
		cmd.Stdin = c.Stdin
		cmd.Stdout, cmd.Stderr = &out, &errb
	}
	err = cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.Code = ee.ExitCode()
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return res, fmt.Errorf("%s timed out after %s", c.Argv[0], timeout)
		case ctx.Err() != nil:
			// The caller cancelled, such as on Ctrl-C: not a timeout.
			return res, fmt.Errorf("%s: %w", c.Argv[0], ctx.Err())
		}
		err = nil
	default:
		res.Code = -1
	}
	return res, err
}

// Quote renders argv for display in a plan, quoting what a POSIX shell would split.
func Quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
