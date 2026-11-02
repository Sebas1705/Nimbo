# Porting Nimbo from Kotlin to Go (Wails + React)

Goal: a real desktop app (a Wails window, no browser) written in Go, with the React UI in `web/` unchanged.
The Kotlin backend in `backend/` is the **specification**: same HTTP routes, same JSON, same behaviour, same tests
(ported). It is deleted only once the Go version passes everything. This file is temporary and is deleted with it.

## Ground rules for every area

- **One Go package**, `internal/nimbo`, plus `main.go` and the Wails shell at the repo root. `contracts.go` is shared
  and owned by the foundation: do not edit it. If you need something added to it, say so in your final report
  and work around it inside your own files.
- **Own only your files.** Each area below lists the Kotlin files it ports and the Go files it may create. Other
  areas are being written at the same time in other worktrees and are merged afterwards, so a name you declare must not
  collide with another area: declare only the identifiers of your area, and prefix helpers with your area name
  (for example `labParse`, `kubeRun`). Exported identifiers that appear in the Kotlin DTOs keep the Kotlin name
  (`ProviderDto`, `UpdateStatus`...).
- **Narrow dependencies.** When your area needs something from another area (the data store, the catalog, the stats
  journal), define the **smallest interface you need in your own file** (name it after what you need, for example
  `updateSettings`, `activityLog`) and take it in your constructor. The integrator wires the real implementations
  and writes any adapter. Never import another area's concrete types.
- **Routes.** Expose `func (x *Thing) Register(mux *http.ServeMux)` using Go 1.22 patterns with the full path, for
  example `mux.HandleFunc("GET /api/updates", Handle(x.status))`. Errors are returned and mapped by `Handle` and
  `StatusFor` (see `contracts.go`); when a route needs a different status for an error (the Kotlin routes catch
  `UpdateError` and answer 422/502), do it in the route.
- **JSON must match the Kotlin DTOs field for field** (names, nesting). Send every field; nullable ones as `null`
  (use pointers or `any`). The React types in `web/src/api.ts` show what the UI reads.
- **Tests.** Port every Kotlin test of your area (`backend/src/test/kotlin/es/sebas1705/nimbo/*Test.kt`) to Go
  (`*_test.go`, table tests where natural), using fakes for `Exec`, `Shell` and `HTTPGateway`. Add tests for edge
  cases you find. `go vet ./...`, `gofmt -l .` (must print nothing) and `go test ./...` must pass in your worktree.
- **Dependencies.** Standard library first. `gopkg.in/yaml.v3` is allowed (stacks and labs are YAML). Anything else:
  justify it in the final report. Do not run `go get` for packages you do not use.
- **Windows first.** The target is Windows 11 (the dev machine) but the code must also build on Linux (CI and the
  Docker image run a headless `serve` mode). Use `//go:build` tags for Windows-only parts.
- **Never** run an MSI, start/stop the user's own containers, or touch anything named `nimbo-*` in Docker that you did
  not create in this session; tests use fakes. The user's installed Nimbo owns port 8080: use other ports.
- **Secrets**: never print, store or commit secret values.
- Comments say *why*, in plain English, like the Kotlin ones. Keep the Kotlin comments that explain decisions.
- Do not commit generated build output. Commit your work on your own branch with small, descriptive messages, as the
  git user already configured, **without any co-author trailer**. Do not push, do not open PRs, do not merge.

## Areas

| Area | Ports (Kotlin) | May create (Go) |
|---|---|---|
| **core** | `App.kt` (config, host guard, token, wiring), `Models.kt`, `Catalog.kt`, `Docker.kt`, `Stacks.kt`, `Exports.kt`, `Data.kt`, `DataRoutes.kt`, `ProviderRoutes.kt`, `Secrets.kt` | `config.go`, `app.go`, `models.go`, `catalog.go`, `docker.go`, `stacks.go`, `exports.go`, `data.go`, `data_routes.go`, `provider_routes.go`, `secrets.go`, `secrets_windows.go`, `secrets_other.go` and their tests |
| **update** | `Updater.kt`, `UpdateRoutes.kt`, `Images.kt`, `ImageRoutes.kt`, `image-packs.json` | `updater.go`, `update_routes.go`, `images.go`, `image_routes.go`, `semver.go`, `resources/image-packs.json` (embedded) and tests |
| **observe** | `Inspector.kt`, `InspectRoutes.kt`, `Chaos.kt`, `ChaosRoutes.kt`, `Traces.kt`, `TraceRoutes.kt`, `Kube.kt`, `KubeRoutes.kt` | `inspector*.go`, `chaos*.go`, `traces*.go`, `kube*.go` and tests |
| **fidelity** | `Conformance.kt`, `FidelityRoutes.kt`, `Labs.kt`, `LabRoutes.kt` (and the lab YAML loading; the lab files stay in `labs/`) | `conformance*.go`, `fidelity_routes.go`, `labs*.go`, `lab_routes.go` and tests |
| **shell** | `Main.kt`, `Desktop.kt`, `DesktopMain.kt`, `packaging/`, `Dockerfile`, CI workflows | `main.go`, `wails.json`, `app_shell*.go`, `build/`, `packaging/`, `.github/workflows/*`, `Dockerfile` |

`web/` is shared: the **shell** area may adjust how the UI is served, the others do not touch it (the UI already
speaks the API above). If the UI needs a change to work with the Go backend, report it instead of making it.

## The surface the integrator expects

```go
// core
type Config struct { /* port, token, host, allowed hosts, emulator host, version, distribution, data dir... */ }
func ConfigFromEnv(env map[string]string, home string) Config      // same variables as the Kotlin NimboConfig
func NewServer(cfg Config, deps Deps) http.Handler                 // host guard + token + all routes + static UI
```

The integrator (not the areas) writes the final wiring in `app.go`, so each area's constructor can take its
dependencies in whatever shape is most natural, as long as they are narrow interfaces.
