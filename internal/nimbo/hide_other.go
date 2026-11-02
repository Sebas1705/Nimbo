//go:build !windows

package nimbo

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
