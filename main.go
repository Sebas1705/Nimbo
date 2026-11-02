// Nimbo: your local cloud, no bill. One binary with two faces:
//
//	nimbo            the desktop app (Windows): a real window, a tray icon, no browser and no open port
//	nimbo serve      headless HTTP server for Docker, CI and smoke tests
//	nimbo --version  prints the version and exits
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// version is injected by the release build: go build -ldflags "-X main.version=1.2.3".
// A plain `go run` has none, and says so instead of inventing one (the health endpoint then reports null).
var version = ""

// declaredVersion is the single place the release version is declared. A release tag must match it
// (the release workflow checks) and the changelog needs a section for it.
//
//go:embed VERSION
var declaredVersion string

// The React UI, built by `npm run build` in web/. Build it before compiling (CI and the Dockerfile do).
//
//go:embed all:web/dist
var webDist embed.FS

// The labs and the default stacks travel inside the binary, so it runs from any folder.
//
//go:embed labs/*.yml nimbo.yml
var bundledDefaults embed.FS

// The icon of the executable, reused for the tray.
//
//go:embed packaging/nimbo.ico
var trayIcon []byte

func uiFS() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return webDist
	}
	return sub
}

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "--version" || a == "-version" || a == "version" {
			fmt.Println("Nimbo", shownVersion())
			return
		}
	}
	if len(args) > 0 && args[0] == "serve" {
		os.Exit(runServe(args[1:], os.Environ(), os.Stdout))
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Println("Usage: nimbo [serve [--host H] [--port N]] | --version")
		return
	}
	os.Exit(runDesktop(os.Environ()))
}

func shownVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

// envMap turns os.Environ() into a map. Windows variable names are case-insensitive, so they are upper-cased
// there; NIMBO_* names are already upper case everywhere.
func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			k := kv[:i]
			if runtime.GOOS == "windows" {
				k = strings.ToUpper(k)
			}
			m[k] = kv[i+1:]
		}
	}
	return m
}
