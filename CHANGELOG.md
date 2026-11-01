# Changelog

All notable changes to Nimbo. The format follows [Keep a Changelog](https://keepachangelog.com/) and the
project uses [Semantic Versioning](https://semver.org/); while the version is 0.x, anything may still change.

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
