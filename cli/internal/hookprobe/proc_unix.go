//go:build !windows

package hookprobe

import (
	"os/exec"
	"syscall"
)

const supported = true

// ownGroup starts the hook as the leader of a new process group.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills every process of the group the hook leads. SIGKILL cannot be ignored.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
