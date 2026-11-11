# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/Sebas1705/Nimbo/security/advisories/new).
Do not open a public issue for a security problem. This is a one-person project, so expect a reply
within a few days, not hours.

## Supported versions

Only the latest release receives fixes. While Nimbo is 0.x, anything may still change.

## What Nimbo does, and what that means for security

Nimbo controls Docker on your machine: it starts, stops and inspects containers, and with the
Kubernetes provider it starts a privileged `rancher/k3s` container. Treat access to Nimbo as access
to your Docker daemon. What is in place today (see the *Security notes* in the README):

- It listens on `127.0.0.1` by default. The desktop window has no network port at all. In the Docker
  image it listens on all interfaces inside the container and relies on you publishing the port on
  `127.0.0.1`; do not publish it on a public interface.
- Requests whose `Host` header is not `localhost` or `127.0.0.1` are rejected (DNS rebinding),
  unless you add hostnames in `NIMBO_ALLOWED_HOSTS`.
- Requests that change something and come from a page of another site are refused (`Origin` check).
- If `NIMBO_API_TOKEN` is set, every `/api` call except `/api/health` needs
  `Authorization: Bearer <token>`. It is **optional and off by default**; set it if anyone else can
  reach the port.
- Nimbo only manages containers it labelled itself (`nimbo.managed=true`), binds the ports of the
  emulators to `127.0.0.1`, and validates provider ids against its built-in catalog before they reach
  Docker.
- The emulators are for practice, not for real data: several accept any credential. Do not put real
  secrets or production data in them.

## What to know about the releases

- **The Windows installer and the binaries are not code-signed.** Windows SmartScreen will warn that
  the publisher is unknown. Check the download against the `SHA256SUMS` of the release.
- The in-app updater verifies the installer against the `SHA256SUMS` **of the same release** and
  installs only after you confirm. That catches corrupted downloads, but it is not a signature:
  whoever can publish to the repository's releases can publish matching hashes.
