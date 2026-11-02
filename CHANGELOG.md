# Changelog

All notable changes to Nimbo. The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project uses [Semantic Versioning](https://semver.org/); while the version is 0.x, anything may still change.

## [0.3.0]

### Changed
- **A real desktop app, rewritten in Go.** Nimbo is now a Wails app: it opens its own window (no browser, no Java), keeps an icon in the tray, runs a single instance and is about 14 MB instead of a 43 MB installer with its own Java. The React UI is the same. The backend was ported from Kotlin to Go keeping the API and its JSON, and the tests were ported with it. The MSI keeps the old upgrade code, so it replaces a 0.2.x install in place.
- The standalone build is a binary (`nimbo serve`) instead of a jar; the Docker image runs it. The distribution kinds are now `binary`, `desktop` and `docker`.

### Added
- **Configuration files for any stack.** The *Export* drawer writes a `docker-compose.yml` (+ its `.env`) to run a stack without Nimbo, and connection files for your own application: Spring Boot `application-nimbo.yml`, .NET `appsettings.Nimbo.json`, plus the existing `.env`, OpenTofu override and AWS CLI profile. Verified by bringing a generated compose up with real containers and using the generated credentials.
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
