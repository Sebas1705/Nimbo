package main

import (
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sebas1705/Nimbo/internal/nimbo"
)

// What the launchers share. Everything here is pure or takes its probes as arguments, so it is tested without a
// window or a socket (see app_shell_test.go). It is the Go port of Desktop.kt.

// ---- Launch plan ----------------------------------------------------------------------------------------------------

type launchKind int

const (
	// openExisting: another Nimbo already answers on this port, so there is nothing to start.
	openExisting launchKind = iota
	startNew
)

type launchPlan struct {
	Kind launchKind
	Port int
}

// planLaunch decides between reusing a running Nimbo, taking the preferred port, or falling back to a free one.
// The probes are passed in so every branch is testable without sockets.
func planLaunch(preferred int, isNimbo, isFree func(int) bool, freePort func() int) launchPlan {
	switch {
	case isNimbo(preferred):
		return launchPlan{openExisting, preferred}
	case isFree(preferred):
		return launchPlan{startNew, preferred}
	default:
		return launchPlan{startNew, freePort()}
	}
}

// looksLikeNimbo is true when a /api/health body is Nimbo's: it is the only thing that answers with these two fields.
// It compacts the JSON first so spacing does not matter.
func looksLikeNimbo(body string) bool {
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(body)
	return strings.Contains(compact, `"status":"ok"`) && strings.Contains(compact, `"authRequired"`)
}

func probeIsNimbo(port int) bool {
	c := http.Client{Timeout: 700 * time.Millisecond}
	resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return err == nil && looksLikeNimbo(string(b))
}

func probeIsFree(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func probeFreePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// ---- Where things live ----------------------------------------------------------------------------------------------

// appDataDir is where the desktop app keeps its log: %LOCALAPPDATA%\Nimbo on Windows, ~/.nimbo elsewhere.
func appDataDir(env map[string]string, home string) string {
	if local := strings.TrimSpace(env["LOCALAPPDATA"]); local != "" {
		return filepath.Join(local, "Nimbo")
	}
	return filepath.Join(home, ".nimbo")
}

// shellDataDir is NIMBO_DATA_DIR when set (a volume in Docker, a throwaway folder in tests), else appDataDir.
func shellDataDir(env map[string]string, home string) string {
	if d := strings.TrimSpace(env["NIMBO_DATA_DIR"]); d != "" {
		return d
	}
	return appDataDir(env, home)
}

// rotateLog moves a log that grew past maxBytes aside, keeping a single previous generation.
func rotateLog(file string, maxBytes int64) {
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= maxBytes {
		return
	}
	old := file + ".1"
	_ = os.Remove(old)
	_ = os.Rename(file, old)
}

const logMaxBytes = 2 << 20

// openLog opens nimbo.log in dir for appending, rotating it first. A windowed app has no console, so the log is
// the only way to see why something failed.
func openLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "nimbo.log")
	rotateLog(p, logMaxBytes)
	return os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// ---- Serving ----------------------------------------------------------------------------------------------------

// Windows reads content types from the registry, where .js is often text/plain and module scripts then refuse
// to load. Fix the ones the UI needs.
func init() {
	for ext, typ := range map[string]string{
		".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
		".css": "text/css; charset=utf-8", ".html": "text/html; charset=utf-8", ".svg": "image/svg+xml",
		".json": "application/json", ".woff2": "font/woff2", ".woff": "font/woff", ".ico": "image/x-icon",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

var loopbackNames = map[string]bool{"localhost": true, "127.0.0.1": true}

// hostAllowed is the anti DNS-rebinding rule of the Kotlin server: a web page cannot make the browser talk to us
// under its own hostname. The port is ignored; allowed holds extra lower-case names (a reverse proxy you control).
func hostAllowed(hostHeader string, allowed []string) bool {
	h := strings.ToLower(strings.TrimSpace(hostHeader))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if loopbackNames[h] {
		return true
	}
	for _, a := range allowed {
		if h == a {
			return true
		}
	}
	return false
}

func parseAllowedHosts(env map[string]string) []string {
	var out []string
	for _, p := range strings.Split(env["NIMBO_ALLOWED_HOSTS"], ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// staticHandler serves the built UI. Anything missing is a plain 404 (the UI has no client-side routes).
func staticHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if p == "/index.html" { // FileServer would redirect it to "/"; the Kotlin server answered it directly
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			r, p = r2, "/"
		}
		if p != "/" {
			if st, err := fs.Stat(ui, strings.TrimPrefix(p, "/")); err != nil || st.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

// serveRouter is the headless server: /api/* goes to the in-process API (which has its own host guard and token),
// everything else is the UI, behind the same host guard.
func serveRouter(api http.Handler, ui fs.FS, allowed []string) http.Handler {
	static := staticHandler(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if !hostAllowed(r.Host, allowed) {
			nimbo.WriteJSON(w, http.StatusForbidden, nimbo.ErrorDto{Error: "Host not allowed"})
			return
		}
		static.ServeHTTP(w, r)
	})
}

// webviewHost is the name the Windows webview gives the app's own origin.
const webviewHost = "wails.localhost"

// desktopAPI is the handler behind the window's asset server. Requests from the webview carry Host
// wails.localhost, which the host guard rightly refuses. The asset server is not a network listener: only the
// window reaches it, so a request that really arrives under the webview's own name is presented to the API as
// loopback. Any other Host is left alone and the guard decides. The guard stays fully on for the TCP listener of
// `nimbo serve`, which never uses this wrapper.
func desktopAPI(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(stripPort(r.Host), webviewHost) || r.Host == "" {
			r2 := r.Clone(r.Context())
			r2.Host = "127.0.0.1"
			r = r2
		}
		api.ServeHTTP(w, r)
	})
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// ---- Wiring the API -----------------------------------------------------------------------------------------------

// buildAPI creates the in-process API. It is the only place of the shell that touches nimbo.Config and
// nimbo.Deps, so the integrator adapts one function if their shapes differ.
func buildAPI(env map[string]string, home, distribution, host string, port int) (http.Handler, io.Closer) {
	cfg := nimbo.ConfigFromEnv(env, home)
	cfg.Version = version
	cfg.Distribution = distribution
	cfg.Host = host
	cfg.Port = port
	h := nimbo.NewServer(cfg, nimbo.Deps{Defaults: bundledDefaults})
	c, _ := h.(io.Closer)
	return h, c
}
