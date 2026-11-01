# Nimbo

**Your local cloud, no bill.** Nimbo is a small web app that starts, stops and inspects the open-source emulators you need to practise distributed systems on AWS, Azure, GCP and OCI, without paying for a single cloud resource.

It does not emulate any cloud by itself. It orchestrates existing emulators ([Floci](https://floci.io), Azurite, MinIO, Redpanda, PostgreSQL, Redis) and gives you one place to run them, see their state and read their logs and the environment variables your code needs to talk to them.

## Status

Early (M0-M2). Working today:

- Catalog of 9 providers, grouped by cloud, started and stopped with one click.
- Live state of every container, logs panel and copy-ready client environment variables.
- Docker-down detection, English and Spanish UI, light and dark theme.
- Stacks defined in `nimbo.yml`: start or stop a whole group (AWS + MinIO, multicloud, event-driven...) in one click.
- Export for a stack: `.env` variables, an OpenTofu/Terraform `nimbo_override.tf` that points the AWS provider at the emulator, and an AWS CLI profile.
- Optional API token and secrets read from [Doppler](https://www.doppler.com) (nothing sensitive is stored in the repo).

Planned: resource inspector, fault injection with Toxiproxy, a fidelity matrix and guided labs.

## Requirements

- Docker (Docker Desktop or Engine) running.
- JDK 21 and Node 22+.
- [Doppler CLI](https://docs.doppler.com/docs/install-cli) if you want the secrets-backed providers (MinIO, PostgreSQL) and API token.

## Run it

```bash
# 1. Build the web UI
cd web && npm ci && npm run build && cd ..

# 2. Start the backend (serves the UI and the API on http://127.0.0.1:8080)
./gradlew :backend:run
```

With secrets from Doppler (project `nimbo`, config `dev`; see `doppler.yaml`):

```bash
doppler setup
doppler run -- ./gradlew :backend:run
```

For UI development run the backend as above and `npm run dev` in `web/` (Vite proxies `/api`).

## Configuration

| Variable | Purpose | Default |
|---|---|---|
| `NIMBO_PORT` | HTTP port (always bound to `127.0.0.1`) | `8080` |
| `NIMBO_API_TOKEN` | If set, every `/api` call except `/api/health` needs `Authorization: Bearer <token>` | unset |
| `NIMBO_MINIO_USER`, `NIMBO_MINIO_PASSWORD` | MinIO root credentials | required to start MinIO |
| `NIMBO_POSTGRES_PASSWORD` | PostgreSQL password | required to start PostgreSQL |
| `NIMBO_WEB_DIR` | Directory with the built UI | `web/dist` |
| `NIMBO_DOCKER_BIN` | Docker CLI to use | `docker` |
| `NIMBO_STACKS_FILE` | Stacks file | `nimbo.yml`, then built-in stacks |

Providers whose secret is missing refuse to start and tell you which variable to provide.

## Security notes

Nimbo can start containers on your machine, so it only listens on loopback and only ever manages containers it labelled itself (`nimbo.managed=true`). Published ports are bound to `127.0.0.1`. Provider ids are validated against the built-in catalog before reaching Docker.

## Tests

```bash
./gradlew :backend:test
cd web && npm test
```

## License

MIT

## Example

[`examples/aws-lab`](examples/aws-lab) provisions an S3 bucket, an SQS queue and a DynamoDB table on Floci with OpenTofu, using the override Nimbo generates.
