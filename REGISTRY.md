# The registry (temporary design note, deleted when the feature is merged)

Three things the owner asked for, with these decisions already taken:

1. **Own images, runnable.** A person registers an image (name, ports, variables, optional volume) and it behaves as one more
   emulator: start, stop, logs, keep data, variables and secrets, stacks, exports, offline-pack status.
2. **Compose profiles, runnable.** A saved set of compose files (+ `--profile` names, + env file) that Nimbo brings up and
   down with `docker compose`, and whose state it shows.
3. **Linked sources.** Compose files and env files live in repositories: a *source* is a local folder (a repo already
   cloned, read live, never copied) or a Git URL that Nimbo clones and updates in its data folder. Nimbo reads the files
   again every time, and says when one is missing or changed since the last `up`.

Areas (one agent each, separate files; the integrator wires them in `wiring.go` and the backup list):

| Area | Files |
|---|---|
| **images** (backend) | `registry_images.go`, `registry_images_routes.go`, tests; plus the change of `CatalogFind`/`CatalogProviders` consumers so custom images are found everywhere |
| **compose + sources** (backend) | `registry_sources.go`, `registry_compose.go`, `registry_routes.go`, tests |
| **ui** | `web/src/Registry.tsx`, `registryLogic.ts` (+ tests), additions to `api.ts`, `nav.ts`, `i18n.ts`, `guides.ts`, `App.tsx`, `styles.css` |

## Common rules
- Data lives in the data folder (`DataStore`): `registry-images.json`, `registry-compose.json`, `registry-sources.json`
  (each `{ "items": [...] }`), written atomically with the existing helpers (`coreReadOrDefault`, `coreWriteJSON`).
  The integrator adds them to the backup list.
- Ids: `^[a-z0-9][a-z0-9-]{0,30}$`. An image id must not equal a built-in provider id or another custom image id.
- Errors use `contracts.go` (`InvalidParam` 400, `UnknownTarget` 404, ...). A 409 is written in the route.
- Everything is fakeable: git and docker run through a small interface of your own (arguments are a list, never a shell
  string). **No unit test runs git, docker or compose.** Manual checks with real Docker are allowed if Docker is up; use names
  starting with `nimbo-test-`, clean up, never touch containers you did not create.
- No secret value ever appears in a list, a log line, the journal, or an error. Record in the journal with
  `Data.Stats.Record(kind, subject, detail)` (kinds: `registry.image.save|delete`, `registry.compose.save|delete|up|down`,
  `registry.source.save|delete|sync`).
- Send every field, null for absent; lists never null (see `contracts.go` `WriteJSON`).
- Comments say why, in plain English; match the surrounding code.

## 1. Own images (`/api/registry/images`)
```
ImageDef  { "id":"my-api", "name":"My API", "image":"ghcr.io/me/api:1.2", "ports":[8081],
            "env":[{"key":"MODE","value":"test"}], "secretKeys":["API_KEY"], "command":["serve","--dev"],
            "volume":"/data" | null, "notes":"" }
Response adds: "secrets":[{"key":"API_KEY","name":"NIMBO_C_MY_API_API_KEY","set":false}], "custom":true
GET    /api/registry/images            -> ImageDef[]
PUT    /api/registry/images/{id}       body: ImageDef without id/secrets/custom -> ImageDef (create or replace)
DELETE /api/registry/images/{id}       -> 204; 409 if the container exists ("remove it first"); the volume and secrets are NOT deleted
```
Validation (all `InvalidParam`, readable messages): image `^[a-z0-9][a-z0-9._/:@-]{0,200}$`; ports 1024-65535, at most 10, not
used by any built-in or other custom provider; env keys/secretKeys `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, unique across both,
at most 30 env entries (values ≤ 500 chars, no control characters); command at most 20 items of ≤ 200 chars; volume an
absolute path inside the container (`^/[A-Za-z0-9._/-]{0,200}$`); name ≤ 60 chars; notes ≤ 500. **No** privileged, host
mounts, extra run flags or network options exist in this model, by design.

How it plugs into the rest (the images agent does this):
- A custom image becomes a `ProviderDef` (Cloud `other`, container name `nimbo-<id>`, ports on `127.0.0.1`, `ContainerEnv` from env,
  `Persist` from volume with the standard `nimbo-<id>-data` named volume, `Vars` from secretKeys: `Secret:true`,
  `Container:<key>`, `Name:NIMBO_C_<ID upper, - to _>_<KEY upper>`, optional, `Generate:"password"`).
- Add `func AllProviders() []ProviderDef` (built-in + custom, built-in first) and `func FindProvider(id string) *ProviderDef`;
  keep `CatalogProviders`/`CatalogFind` meaning "built-in only" (existing tests rely on it) and move every runtime consumer
  (provider routes, stacks, variables, secrets name validation, exports, images status, wiring) to the new functions. A
  `sync.RWMutex`-guarded snapshot set by the registry service whenever it changes is fine.
- `ProviderDto` gets `"custom": bool` (true for these). `ValidateSecret` / `isSecretName` / `VarSources` must accept the dynamic
  names (length and control-character rules only). Stacks may contain custom image ids; export formats other than compose
  just treat them as having no endpoints, compose writes them like any provider.
- The image status screen (`/api/images`) lists custom images too (pack `null`); `pull` works for them.

## 2. Sources (`/api/registry/sources`)
```
SourceDef { "id":"my-repo", "kind":"local"|"git", "path":"C:\\code\\my-repo",        // local: absolute folder
            "url":"https://github.com/me/repo.git", "branch":"main"|null }              // git
Response adds: "dir":"<absolute folder files resolve against>", "status":"ok"|"missing"|"not-cloned"|"error",
               "head":"abc1234"|null, "currentBranch":"main"|null, "dirty":bool, "behind":int|null,
               "syncedAt":ms|null, "error":string|null
GET    /api/registry/sources
PUT    /api/registry/sources/{id}      create/replace (git: does not clone yet)
DELETE /api/registry/sources/{id}      -> 204; 409 if a compose profile uses it; a managed clone is deleted from the data folder
POST   /api/registry/sources/{id}/sync -> SourceDef. git: clone (first time) or `pull --ff-only`; local: refresh status only
```
- Git: managed clones live in `<data>/repos/<id>`. URL allow-list: `https://host/path[.git]`, `ssh://git@host/path`, or `git@host:path`
  (no `file://`, no `ext::`, nothing starting with `-`, no spaces/control characters). Run `git` with `GIT_TERMINAL_PROMPT=0`,
  `-c protocol.ext.allow=never -c protocol.file.allow=never -c core.hooksPath=` (no hooks), `--no-recurse-submodules`, depth 50
  for the clone, 120 s timeout, output trimmed to 2000 chars in errors. Credentials come from the user's own git setup; Nimbo
  stores none. A failed sync keeps the old working tree.
- Local: `path` must be an existing absolute folder. Status fields come from `git -C <dir> rev-parse/status` when it is a repo
  (not a repo is fine: `head:null`).
- `behind` for git sources: `rev-list --count HEAD..@{u}` after a `fetch` during sync only (never on plain GET).

## 3. Compose profiles (`/api/registry/compose`)
```
ComposeDef { "id":"api-tests", "name":"API tests", "purpose":"integration tests of the API",
             "files":[{"source":"my-repo"|"", "path":"deploy/compose.test.yml"}],   // source "" means path is absolute
             "profiles":["tests"], "envFile":{"source":"my-repo","path":".env.test"}|null }
Response adds: "state":{"running":2,"total":3,"services":[{"name":"api","state":"running","health":"healthy"|null}]},
               "fileStatus":[{"path":"<absolute>","status":"ok"|"missing"|"changed","sha256":"…"|null}],
               "envFileStatus": same shape or null, "lastUpSha":{"<absolute path>":"sha"}|null, "lastUpAt":ms|null
GET    /api/registry/compose
PUT    /api/registry/compose/{id}
DELETE /api/registry/compose/{id}      -> 204; 409 while it is running
POST   /api/registry/compose/{id}/validate -> {"ok":bool,"message":string}   (`docker compose ... config -q`)
POST   /api/registry/compose/{id}/up   -> ComposeDef   (202)
POST   /api/registry/compose/{id}/down?volumes=false -> ComposeDef
GET    /api/registry/compose/{id}/logs?tail=200&service= -> {"lines":"..."}
GET    /api/registry/compose/{id}/file?index=0 -> {"path":"<absolute>","content":"...","truncated":bool}   (live read, max 256 KB)
```
- Command (list, never a string): `docker compose --project-name nimbo-reg-<id> --project-directory <dir of the first file>`
  `-f <abs1> -f <abs2> [--env-file <abs>] [--profile p ...] up -d --remove-orphans`. State from
  `docker compose --project-name nimbo-reg-<id> -f ... ps -a --format json` (one JSON object per line or an array: handle both).
- Nimbo only ever touches compose projects whose name starts with `nimbo-reg-`.
- Files: source `""` -> absolute path; otherwise resolve against the source `dir`. The result must stay inside the source folder after
  `filepath.Clean` and symlink resolution (`InvalidParam` otherwise). Extensions: `.yml`, `.yaml` for files, `.env`/`.env.*`/no extension
  `*.env` for the env file. Files and sources are read again on every list/up, nothing is cached or copied.
- `changed` = the file's sha256 differs from the one recorded at the last successful `up`. Recorded with the profile in a small
  `registry-compose-state.json` (also per id). Nothing about secrets: Nimbo never reads values out of the env file.
- Before `up`, a profile whose files are missing answers 409 with the list of missing paths; a git source that was never synced says so.
- `up`, `down`, `validate`: timeout 300 s / 120 s / 60 s. Errors: the last 1500 chars of the output.

## 4. UI contract (the ui agent)
New screen id `registry` in the **Build** group after Infrastructure: label "Registro" / "Registry". Sections, each with an empty
state that explains what it is for and a primary action:
1. **Imágenes propias** (own images): list/cards with name, image, ports, state (from `/api/providers`, `custom:true`), start/stop (reuse
   `api.start/stop`), edit, delete; a form: id, name, image, ports (chips), environment (key/value rows), secret keys (chips; values are set in
   Settings > Emulator variables, with a link), command, volume, notes. The custom image also shows in Infrastructure with an "own" badge.
2. **Fuentes** (sources): local folder or Git URL, status pill (ok/missing/not cloned/error), branch, short head, behind/dirty, "Sincronizar".
3. **Perfiles de compose**: list with state (running x/y, services), fileStatus pills, buttons Validar / Subir / Bajar / Ver archivo / Logs / Editar /
   Borrar; form: id, name, purpose, files (source select + relative path rows, or absolute path), profiles (chips), env file.
Make it pleasant and obvious: plain sentences, no jargon, errors show the server's message, destructive actions confirm, long operations show
progress text, bilingual es/en through the existing helpers, keyboard accessible, works at 960px and at phone width. Pure logic goes in
`registryLogic.ts` with vitest tests (validation of ids/ports/keys mirrors the server rules, summaries, status wording).
