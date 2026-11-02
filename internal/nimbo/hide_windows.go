//go:build windows

package nimbo

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from flashing when the desktop app runs docker or PowerShell.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
