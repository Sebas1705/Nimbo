# Changelog

All notable changes to Nimbo. The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project uses [Semantic Versioning](https://semver.org/); while the version is 0.x, anything may still change.

## [0.5.0]

### Changed
- **One shared kit for the interface.** Every screen now builds on the same components (feedback, notices, cards, pills, drawers, copy buttons, form fields and the confirmation button), so loading, errors and empty states look and behave alike everywhere. The registry screen was split into sections.
- **Confirmations happen in place.** The nine browser `confirm()` pop-ups became an inline question next to the button (Escape cancels only the question). Deleting things in the Kubernetes screen now asks too.
- **Polling adapts to what you look at.** Screens that show live state refresh every 3 s, the others every 10 s, Docker being down every 8 s, and nothing while the window is hidden.
- **Much lighter polling.** The page asks for the state of Docker, the emulators and the stacks every 3 seconds; each answer used to start its own `docker` processes (92 per minute, 0.6 to 0.9 s per answer). The container runtime now shares one run of `docker` between those requests and drops what it remembers the moment you change something, so what you do still shows at once. Measured with Docker running: 20 processes per minute and a median of 1 to 47 ms per answer.

## [0.4.1]

### Changed
- **A tidier interface.** Every button, field and select now shares one height (34 px, 28 px for small ones, 40 px on phones) with centred content, consistent padding and a visible keyboard focus ring.
- **Better use of the screen.** The content area grows up to 1560 px, the side rail fits a 900 px screen without scrolling, and the "How it works" note became a button in the corner of the title that opens a card, so the content starts about 300 px higher on every screen. The note starts folded with a dot until you open it once, and remembers your choice.
- Phones: bigger touch targets and a top bar that takes a third less height. Drawers keep their header in view while you scroll.

## [0.4.0]

### Added
- **The registry** (*Build > Registry*): your own images, the repositories your configuration lives in, and compose profiles.
  - **Own images** become one more emulator: name, ports (on loopback), variables, secret keys (values live in the encrypted store as `NIMBO_C_<ID>_<KEY>`), command and an optional data volume. They start, stop, keep data, enter stacks, exports and the image status. No privileged mode, host mounts or extra run flags exist in this model, by design.
  - **Repositories** are a local folder (a clone you already have, read live and never copied) or a Git URL that Nimbo clones into its data folder and updates with `pull --ff-only` (no hooks, no prompts, URL allow-list, no credentials stored). Status shows branch, head, uncommitted changes and how far behind.
  - **Compose profiles** are saved sets of compose files (+ `--profile` names and an env file) that Nimbo validates, brings up and down with `docker compose`, with state, logs, a live view of each file, and a warning when a file changed since the last *up*. Nimbo only touches projects it created (`nimbo-reg-*`).
  - Included in backups: images, repositories and profiles (not the record of the last *up*).

## [0.3.1]

### Fixed
- **The published app showed no labs.** It looked for `labs/` and `nimbo.yml` in the folder it was started from and, finding none, read the bundled copy the wrong way (the labs sit under `labs/` inside it). It now uses what is inside the app, unless you point `NIMBO_LABS_DIR` or `NIMBO_STACKS_FILE` somewhere, and the folder it was launched from no longer decides what it shows. A test with the real embedded files, run from an empty folder, now guards this.

## [0.3.0]

### Changed
- **A real desktop app, rewritten in Go.** Nimbo is now a Wails app: it opens its own window (no browser, no Java), keeps an icon in the tray, runs a single instance and is about 14 MB instead of a 43 MB installer with its own Java. The React UI is the same. The backend was ported from Kotlin to Go keeping the API and its JSON, and the tests were ported with it. The MSI keeps the old upgrade code, so it replaces a 0.2.x install in place.
- The standalone build is a binary (`nimbo serve`) instead of a jar; the Docker image runs it. The distribution kinds are now `binary`, `desktop` and `docker`.

### Added
- **Configuration files for any stack.** The *Export* drawer writes a `docker-compose.yml` (+ its `.env`) to run a stack without Nimbo, and connection files for your own application: Spring Boot `application-nimbo.yml`, .NET `appsettings.Nimbo.json`, plus the existing `.env`, OpenTofu override and AWS CLI profile. Verified by bringing a generated compose up with real containers and using the generated credentials.
- **Stacks of your own**, created, edited and deleted in the app, saved in the data folder and included in backups.
- **Cross-site protection.** Requests that change something are refused when a web page from another site sent them.
- **Emulator variables.** Every emulator lists what it can be given and says when it needs nothing. New optional settings: a Redis password (`requirepass`, read from the environment so it never shows in the command line), a k3s cluster token, an Azurite account and the PostgreSQL user and database. *Initialize* generates what is missing, *Show* reveals a value on request (journaled by name only), *Apply* recreates a running emulator with the new values keeping its data, and the client variables of an emulator (`DATABASE_URL`, `REDIS_URL`, the Azurite connection string) follow the settings, with placeholders until you ask to see the passwords. A card that cannot start says what it needs and offers *Initialize and start*.
- **A data folder.** Settings, lab progress, usage statistics, a journal and the fidelity history are kept in `~/.nimbo` (`NIMBO_DATA_DIR`), with backup, restore and reset. Lab progress and the language/theme move from the browser to that folder (what the browser had is imported once).
- **Keep emulator data.** A per-emulator switch keeps its data in a Docker volume across stop and remove; deleting it is a separate confirmed action. Verified with real containers for Redis, PostgreSQL, MinIO and Floci AWS.
- **Passwords in the Windows store** (DPAPI, per user) for MinIO and PostgreSQL; the environment still wins. Never part of a backup.
- **Updater.** Checks GitHub (at most once a day, or never in manual mode), downloads the installer for your installation verified against `SHA256SUMS`, installs the Windows app after you confirm, and imports an installer from a file for offline use.
- **Offline images.** Releases publish the emulator images as `docker save` packs; `packaging/make-offline-kit.ps1` builds a carry-around kit and *System > Offline* loads the packs.
- A *System* section with Activity, Settings and data, Updates and Offline screens.

### Fixed
- **MinIO could not start**: its images are no longer published on Docker Hub or quay.io; Nimbo now uses the Chainguard build (`cgr.dev/chainguard/minio`).
- A malformed JSON body now answers 400 instead of 500.

## [0.2.1]

### Added
- **A built-in guide.** A start page with the five steps and five one-click recipes, a usage guide for every provider (where it listens, commands to paste, limits) and a "How it works" note on every screen, in English and Spanish. The commands were run against the real emulators, and a test keeps the guides in step with the catalog, the labs and the stacks.
- A light/dark theme switch (it follows the system by default) and a remembered language.

### Changed
- **New look.** Navigation is a side rail grouped by what you are doing (Start, Build, Observe, Break, Learn), providers show their port on a front plate with a status LED, section titles and empty states say what to do next, and the fonts are bundled so it works offline. The layout adapts to phone width.
- Provider and stack descriptions follow the language of the interface.
- The `curl` commands in the guides are silent (`-sS`) so their answers are readable.

### Fixed
- The tests that read files outside the backend module (guides, labs, stacks, workflows, compose, changelog) were declared as inputs, so Gradle can no longer report a stale passing result after one of them changes.

## [0.2.0]

### Added
- **Windows desktop app.** `Nimbo.exe` carries its own Java, starts Nimbo, opens the UI in your browser and sits in the system tray (open, quit). A second launch just reopens the running one, and a busy port makes it pick another. Available as a portable zip and as an installer.
- `docker-compose.yml` and `.env.example` for one-command deployment with the published image.

### Changed
- The "missing secrets" error now explains the `.env` file as well as Doppler.

## [0.1.0]

First release.

### Added
- Control center for local cloud emulators with 12 providers: Floci (AWS, Azure, GCP, OCI), Azurite, MinIO, Redpanda, PostgreSQL, Redis, Toxiproxy, Kubernetes (k3s) and Jaeger.
- Stacks defined in `nimbo.yml`, started and stopped as a group, with exports for `.env`, OpenTofu/Terraform (`nimbo_override.tf`) and the AWS CLI.
- Read-only resource inspector for S3, SQS, DynamoDB and Pub/Sub.
- Fault injection through Toxiproxy: latency, slow link, hang, connection reset, slow close, byte cut-off and outage.
- Fidelity matrix: 19 suites of real operations against the AWS, Azure and GCP emulators.
- Eight guided labs in English and Spanish, verified against the live emulators.
- Kubernetes tab (nodes, deployments, pods, services, apply, scale, delete, logs, kubeconfig) and Traces tab with a waterfall view.
- Optional API token, secrets from Doppler, and a `Host` header guard against DNS rebinding.
- A single jar that carries the UI, the labs and the default stacks, and a Docker image.

### Known limits
- Lambda invocation, EC2 and Azure Functions need the Docker socket mounted into the emulator, which Nimbo does not do yet.
- No inspector for Azure storage or Cloud Storage; Firestore is not covered because Floci serves it over gRPC only.
- Redpanda is not reachable through Toxiproxy.
