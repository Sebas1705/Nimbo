# Changelog

All notable changes to Nimbo. The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project uses [Semantic Versioning](https://semver.org/); while the version is 0.x, anything may still change.

## [Unreleased]

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
