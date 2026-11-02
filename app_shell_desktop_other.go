//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The window and the tray are Windows-only for now; elsewhere `nimbo serve` is the way to run Nimbo.
func runDesktop(environ []string) int {
	fmt.Fprintln(os.Stderr, "The Nimbo desktop window is only available on Windows. Run `nimbo serve` and open http://127.0.0.1:8080.")
	return 2
}
