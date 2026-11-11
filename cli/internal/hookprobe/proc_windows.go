package hookprobe

import "os/exec"

const supported = false

func ownGroup(*exec.Cmd) {}

func killGroup(int) {}
