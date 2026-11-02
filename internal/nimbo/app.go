package nimbo

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Deps is everything the server needs besides the configuration. Every field has a sensible default, so tests only
// set what they care about.
type Deps struct {
	// Runtime drives Docker. Default: the docker CLI named by Config.DockerBin.
	Runtime ContainerRuntime
	// Stacks default to the built-in ones.
	Stacks *StackCatalog
	// Data default is an in-memory store.
	Data *AppData
	// Secrets default to an unavailable store that explains why.
	Secrets SecretStore
	// UI is the built React app (index.html at its root). Nil serves no UI: the root is a 404 and the API still works.
	UI fs.FS
	// Routes mount the routes of the other areas (updates, inspection, chaos, fidelity, labs, kube, traces, images).
	// They run on the same mux, so the host guard and the token apply to them as well.
	Routes []func(*http.ServeMux)
	// Lifetime, when set, starts the sampler that measures how long providers stay up; it stops when Lifetime ends.
	Lifetime context.Context
	// SampleEvery is how often the sampler looks (default 15 seconds).
	SampleEvery time.Duration
}

var coreLoopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true}

// RunStatsSampler measures the time spent up: every period it looks at what runs and adds it to the statistics. It
// blocks until ctx ends.
func RunStatsSampler(ctx context.Context, rt ContainerRuntime, stats *StatsService, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var running []string
			for id, info := range rt.Containers(ctx) {
				if info.Running {
					running = append(running, id)
				}
			}
			stats.Sample(running, int64(every/time.Second))
		}
	}
}

// NewServer builds the whole HTTP surface: the host guard and the API token wrap every route, then the provider,
// stack, data and health routes, the routes of the other areas, and the UI.
func NewServer(cfg Config, deps Deps) http.Handler {
	if deps.Data == nil {
		deps.Data = NewAppData(nil, nil)
	}
	if deps.Runtime == nil {
		deps.Runtime = NewCliContainerRuntime(ProcessExec{Bin: orDefault(cfg.DockerBin, "docker")})
	}
	if deps.Stacks == nil {
		deps.Stacks = BuiltInStacks()
	}
	if deps.Secrets == nil {
		deps.Secrets = UnavailableSecretStore{Why: UnavailableSecretMessage}
	}
	if deps.Lifetime != nil {
		go RunStatsSampler(deps.Lifetime, deps.Runtime, deps.Data.Stats, deps.SampleEvery)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", Handle(func(w http.ResponseWriter, r *http.Request) error {
		var version *string
		if cfg.Version != "" {
			version = coreStr(cfg.Version)
		}
		WriteJSON(w, http.StatusOK, HealthDto{Status: "ok", AuthRequired: cfg.APIToken != "", Version: version})
		return nil
	}))
	(&ProviderRoutes{Config: cfg, Runtime: deps.Runtime, Data: deps.Data, Secrets: deps.Secrets}).Register(mux)
	(&StackRoutes{Config: cfg, Runtime: deps.Runtime, Stacks: deps.Stacks, Data: deps.Data, Secrets: deps.Secrets}).Register(mux)
	(&DataRoutes{Data: deps.Data, Secrets: deps.Secrets, Config: cfg}).Register(mux)
	(&VariableRoutes{Config: cfg, Runtime: deps.Runtime, Data: deps.Data, Secrets: deps.Secrets}).Register(mux)
	for _, register := range deps.Routes {
		register(mux)
	}
	mux.Handle("/", coreUIHandler(deps.UI))

	return coreRecover(coreHostGuard(cfg, coreOriginGuard(cfg, coreAPIToken(cfg, mux))))
}

// coreRecover answers 500 without leaking details, like the catch-all of the Kotlin StatusPages block.
func coreRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.Printf("Unhandled error on %s: %v", r.URL.Path, rec)
				WriteJSON(w, http.StatusInternalServerError, ErrorDto{"Internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// coreRequestHost is the Host header without its port, lowercased. A client that omits it is judged by the local
// address it reached, like Ktor does.
func coreRequestHost(r *http.Request) string {
	host := r.Host
	if host == "" {
		if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			host = addr.String()
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

// coreHostGuard is the defence against DNS rebinding: a web page cannot make the browser talk to us under its own
// hostname.
func coreHostGuard(cfg Config, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range cfg.AllowedHosts {
		allowed[strings.ToLower(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := coreRequestHost(r)
		if !coreLoopbackHosts[host] && !allowed[host] {
			WriteJSON(w, http.StatusForbidden, ErrorDto{"Host not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// webviewOrigin is how the desktop window names itself. It is not a network name: only the window reaches the API
// through it.
const coreWebviewOrigin = "wails.localhost"

// coreOriginGuard refuses a request that changes something when a web page from another site sent it. A browser
// always tells where a page came from (Origin), so a page that is not Nimbo's own, or the desktop window, cannot make
// your browser start containers, read a secret or delete data. Requests with no Origin (curl, scripts, the tests of
// your own tools) are not browsers and pass.
func coreOriginGuard(cfg Config, next http.Handler) http.Handler {
	allowed := map[string]bool{coreWebviewOrigin: true}
	for _, h := range cfg.AllowedHosts {
		allowed[strings.ToLower(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			u, err := url.Parse(origin)
			same := err == nil && (strings.EqualFold(u.Host, r.Host) || allowed[strings.ToLower(u.Hostname())])
			if !same {
				WriteJSON(w, http.StatusForbidden, ErrorDto{"Cross-origin request refused"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// apiToken asks for "Authorization: Bearer <token>" on every /api route except the health check, when a token is
// configured. The UI itself is not behind it, or it could never load.
func coreAPIToken(cfg Config, next http.Handler) http.Handler {
	if cfg.APIToken == "" {
		return next
	}
	want := []byte(cfg.APIToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if (p == "/api" || strings.HasPrefix(p, "/api/")) && p != "/api/health" {
			sent := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if subtle.ConstantTimeCompare([]byte(sent), want) != 1 {
				WriteJSON(w, http.StatusUnauthorized, ErrorDto{"Invalid or missing API token"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// coreUIHandler serves the built UI. A path that is not a file and has no extension is a client-side route and gets
// index.html; a missing asset is a real 404. Anything under /api that no route matched is a 404 too, never the UI.
func coreUIHandler(ui fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if ui == nil || p == "/api" || strings.HasPrefix(p, "/api/") || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			WriteJSON(w, http.StatusNotFound, ErrorDto{"Not found"})
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(ui, name)
		if err != nil {
			// A directory (or a folder-like route) falls back to its index, as `default("index.html")` did.
			if idx, ierr := fs.ReadFile(ui, path.Join(name, "index.html")); ierr == nil {
				data, name = idx, path.Join(name, "index.html")
			} else if path.Ext(name) == "" {
				data, err = fs.ReadFile(ui, "index.html")
				name = "index.html"
				if err != nil {
					WriteJSON(w, http.StatusNotFound, ErrorDto{"Not found"})
					return
				}
			} else {
				WriteJSON(w, http.StatusNotFound, ErrorDto{"Not found"})
				return
			}
		}
		// Windows maps .js to whatever the registry says (often text/plain), which browsers refuse as a module.
		if ct, ok := coreUIContentTypes[path.Ext(name)]; ok {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

var coreUIContentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".json": "application/json", ".svg": "image/svg+xml", ".wasm": "application/wasm",
	".map": "application/json", ".txt": "text/plain; charset=utf-8",
}
