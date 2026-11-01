# Nimbo

**Your local cloud, no bill.** Nimbo is a small web app that starts, stops and inspects the open-source emulators you need to practise distributed systems on AWS, Azure, GCP and OCI, without paying for a single cloud resource.

It does not emulate any cloud by itself. It orchestrates existing emulators ([Floci](https://floci.io), Azurite, MinIO, Redpanda, PostgreSQL, Redis) and gives you one place to run them, see their state and read their logs and the environment variables your code needs to talk to them.

## Status

Version 0.1.0 (see the [changelog](CHANGELOG.md)); while it is 0.x, anything may still change. Working today:

- Catalog of 12 providers, grouped by cloud, started and stopped with one click.
- Live state of every container, logs panel and copy-ready client environment variables.
- Docker-down detection, English and Spanish UI, light and dark theme.
- Stacks defined in `nimbo.yml`: start or stop a whole group (AWS + MinIO, multicloud, event-driven...) in one click.
- Export for a stack: `.env` variables, an OpenTofu/Terraform `nimbo_override.tf` that points the AWS provider at the emulator, and an AWS CLI profile.
- Read-only resource inspector: S3 buckets and object previews, SQS queues with message peek, DynamoDB tables and items (typed values flattened to plain JSON), and Pub/Sub topics and subscriptions. Auto-refreshes and tells you when an emulator is down.
- Fault injection with [Toxiproxy](https://github.com/Shopify/toxiproxy): latency, slow link, hang, connection reset, slow close, cut after N bytes and full outage, per emulator and live. Stacks can be exported to use the chaos ports.
- Fidelity matrix: 19 suites (12 AWS, 3 Azure, 3 GCP, plus Lambda) that run real operations against the emulators and report which services are fully, partly or not supported, step by step. Run it after upgrading an emulator.
- Kubernetes: a real single-node k3s cluster started like any other provider. Browse nodes, deployments, pods and services, apply manifests, scale, delete, read pod logs and copy the kubeconfig. `kubectl` runs inside the container, so nothing is installed on your machine.
- Traces: Jaeger as a provider and a trace view with a waterfall per trace (OTLP in, Jaeger query API out), plus a button that sends a sample trace.
- Guided labs: 8 exercises (S3, SQS, DynamoDB, Pub/Sub fan-out, OpenTofu, a latency drill, a Kubernetes deployment, a first trace) in English and Spanish. You build with your own tools; Nimbo verifies the result in the emulators.
- Optional API token and secrets read from [Doppler](https://www.doppler.com) (nothing sensitive is stored in the repo).

Planned: mount the Docker socket so Lambda and EC2 work, Azure and Cloud Storage inspectors.

## Install

You need Docker (Docker Desktop or Engine) running. Nimbo drives it to start the emulators.

**Windows app** (no Java needed, it brings its own). Download from the [releases](https://github.com/Sebas1705/Nimbo/releases) either `Nimbo-<version>-windows-x64.msi` (installs for your user only, no administrator rights, adds a Start menu entry and updates in place when you install a newer one) or `Nimbo-<version>-windows-x64.zip` (portable: unzip and run `Nimbo.exe`). Nimbo starts, opens its UI in your browser and leaves an icon in the system tray: double click or **Open Nimbo** to see it again, **Quit Nimbo** to stop it (the emulators it started keep running until you stop them). Starting it a second time only reopens the running one, and if port 8080 is taken by another program it picks a free port. Docker Desktop must be running.

The installer is **not code-signed**, so Windows SmartScreen will warn that the publisher is unknown ("More info" > "Run anyway"); check the file against `SHA256SUMS` first. Settings are read from your Windows environment variables (`NIMBO_API_TOKEN`, `NIMBO_PORT`, `NIMBO_MINIO_PASSWORD`...), and the log is in `%LOCALAPPDATA%\Nimbo\nimbo.log`.

**The jar** (needs Java 21). Download `nimbo-<version>.jar` from the [releases](https://github.com/Sebas1705/Nimbo/releases), check it against `SHA256SUMS`, and run it from any folder; the UI, the labs and the default stacks are inside it:

```bash
java -jar nimbo-0.1.0.jar
# then open http://127.0.0.1:8080
```

**Docker Compose** (no Java needed, the easiest way). From a checkout, or with just [`docker-compose.yml`](docker-compose.yml) saved in a folder:

```bash
docker compose up -d
# then open http://127.0.0.1:8080
```

It pulls the published image, restarts it with Docker, reports health and publishes the port on loopback only. Everything is optional; to set a port, an API token or the MinIO and PostgreSQL credentials, copy [`.env.example`](.env.example) to `.env` (or run `doppler run -- docker compose up -d`). Update with `docker compose pull && docker compose up -d`, stop with `docker compose down`, and use `docker compose up -d --build` to build the image from the checkout.

The container drives the Docker of your machine through its socket, which is root-equivalent access, so never publish its port beyond loopback. In this mode Nimbo reaches the emulators at `host.docker.internal` (`NIMBO_EMULATOR_HOST`), which the compose file maps for Linux and Docker Desktop resolves by itself.

**Plain `docker run`**, if you prefer:

```bash
docker run -d --name nimbo --restart unless-stopped -p 127.0.0.1:8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --add-host=host.docker.internal:host-gateway \
  ghcr.io/sebas1705/nimbo
```

**From source** (JDK 21 and Node 22+; the [Doppler CLI](https://docs.doppler.com/docs/install-cli) only if you want the secrets-backed providers, MinIO and PostgreSQL, and the API token):

```bash
cd web && npm ci && npm run build && cd ..
./gradlew :backend:buildFatJar        # backend/build/libs/nimbo-<version>.jar
```

See [RELEASING.md](RELEASING.md) for how a release is cut.

## Run it from source

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
| `NIMBO_PORT` | HTTP port | `8080` |
| `NIMBO_HOST` | Address to listen on. Keep loopback unless Nimbo runs in a container whose port you publish on loopback | `127.0.0.1` |
| `NIMBO_EMULATOR_HOST` | Where Nimbo reaches the emulators (`host.docker.internal` inside Docker) | `localhost` |
| `NIMBO_API_TOKEN` | If set, every `/api` call except `/api/health` needs `Authorization: Bearer <token>` | unset |
| `NIMBO_MINIO_USER`, `NIMBO_MINIO_PASSWORD` | MinIO root credentials | required to start MinIO |
| `NIMBO_POSTGRES_PASSWORD` | PostgreSQL password | required to start PostgreSQL |
| `NIMBO_WEB_DIR` | Directory with the built UI | `web/dist` |
| `NIMBO_DOCKER_BIN` | Docker CLI to use | `docker` |
| `NIMBO_STACKS_FILE` | Stacks file | `nimbo.yml`, then built-in stacks |
| `NIMBO_ALLOWED_HOSTS` | Extra `Host` values to accept, comma separated | loopback only |
| `NIMBO_LABS_DIR` | Directory with lab YAML files | `labs` |

Providers whose secret is missing refuse to start and tell you which variable to provide.

## Fidelity

The Fidelity tab runs small end-to-end scenarios (create, use, read back, delete) per service, so "the emulator has an SQS" becomes "SQS passes 6/6 steps, Lambda invoke fails because the Docker socket is not mounted". Each step is a real HTTP call; resources are named `nimbo-fit-<random>` and removed afterwards, even when a step fails. A step that depends on an earlier failure is reported as skipped instead of as a second failure. Status per service: **full**, **partial**, **unsupported** or **offline**.

Firestore is not covered: Floci GCP serves it over gRPC only.

## Labs

Labs live in [`labs/`](labs) as one YAML file each (`title`, `summary`, `steps`, `hints`, `solution` and `checks`, every text in `en` and `es`). A check is one of `s3.bucket`, `s3.object`, `sqs.queue`, `sqs.messages`, `dynamodb.table`, `dynamodb.items`, `pubsub.topic`, `pubsub.subscription` or `chaos.fault`. Invalid labs are skipped and listed in the UI. Progress is stored in your browser only. Set `NIMBO_LABS_DIR` to use your own.

## Kubernetes

Start **Kubernetes (k3s)** (or the `cluster` stack, which adds Jaeger). It is one `rancher/k3s` container named `nimbo-k3s` with the API on `127.0.0.1:6443` and NodePorts `30080`-`30084` published on localhost, so a Service with `nodePort: 30080` is reachable at http://localhost:30080. The cluster takes about 30 seconds to become ready.

**k3s needs `--privileged`.** Nimbo is the only thing that starts it and it is bound to loopback, but a privileged container is a big hammer: stop it when you are not using it. The kubeconfig gives full control of the cluster, so the endpoint that returns it is behind the API token like everything else. The UI offers Apply, Scale and Delete (pods, deployments and services outside the `kube-system`, `kube-public` and `kube-node-lease` namespaces); every `kubectl` call is an argument list, never a shell string, and names are validated first.

## Traces

Start **Jaeger**. Send traces with any OpenTelemetry SDK to `http://localhost:4318` (OTLP over HTTP, also exported as `OTEL_EXPORTER_OTLP_ENDPOINT` in the stack export) and open the Traces tab: pick a service, pick a trace, read the waterfall. Failed spans are red. Spans whose parent is missing are shown at the top level instead of being dropped.

## Chaos

Start the **Toxiproxy** provider, open the Chaos tab and apply a fault to an emulator. Each emulator gets a second port that goes through Toxiproxy (Floci AWS `4566` becomes `14566`, Redis `6379` becomes `16379`, ...); point your app at that port, or tick "Through chaos" when exporting a stack. Faults act on the emulator's responses and can affect only a percentage of connections. A proxy is created the first time you use a target, and `Reset all` restores every proxy.

Limits: Redpanda is not proxied because Kafka clients reconnect to the broker's advertised address, and Toxiproxy reaches the emulators through `host.docker.internal`.

## Inspector

The inspector talks to the emulators on `localhost:4566` (AWS) and `localhost:4588` (GCP). It is read-only and the emulators ignore signatures, so no credentials are involved. SQS peek uses visibility timeout 0: messages are not consumed, but their receive count goes up, exactly as on real SQS. Names are validated before they reach an emulator, queue URLs reported by an emulator are never followed, and XML with a DOCTYPE is rejected.

## Security notes

Nimbo can start containers on your machine, so it listens on loopback by default (inside the Docker image it listens on all interfaces and relies on you publishing the port on `127.0.0.1`) and rejects any request whose `Host` header is not `localhost` or `127.0.0.1` (a defence against DNS rebinding; add hostnames you control with `NIMBO_ALLOWED_HOSTS`), and only ever manages containers it labelled itself (`nimbo.managed=true`). Published ports are bound to `127.0.0.1`. Provider ids are validated against the built-in catalog before reaching Docker.

## Tests

```bash
./gradlew :backend:test
cd web && npm test
```

## License

MIT

## Example

[`examples/aws-lab`](examples/aws-lab) provisions an S3 bucket, an SQS queue and a DynamoDB table on Floci with OpenTofu, using the override Nimbo generates.
