# Releasing Nimbo

A release is a git tag. Everything else is done by `.github/workflows/release.yml`.

1. Decide the version (`0.3.0`, `1.0.0`...).
2. Change `VERSION` and `productVersion` in `wails.json` (a test checks they agree) and add a `## [x.y.z]` section to `CHANGELOG.md`.
   The workflow uses that section as the release notes and refuses a tag that does not match `VERSION`.
3. Make sure `main` is green, then:

   ```bash
   git tag v0.3.0
   git push origin v0.3.0
   ```

4. The workflow runs the Go and web tests, builds the Linux binary `nimbo-<version>-linux-amd64` (checking that it starts and serves the UI), builds the Docker image, and on a Windows runner builds the desktop app with Wails: the portable zip is smoke-tested with `packaging/smoke-test.ps1`, then the `.msi` is built with WiX 3 (`packaging/build-msi.ps1`). Another job saves the emulator images as `Nimbo-<version>-images-<pack>.tar.gz` (packs defined in `internal/nimbo/resources/image-packs.json`, each under the 2 GiB asset limit). Then:
   - publishes a GitHub release with the binary, `Nimbo-<version>-windows-x64.zip`, `Nimbo-<version>-windows-x64.msi`, the image packs and a `SHA256SUMS` that covers all of them (marked as a pre-release while the version is 0.x);
   - pushes `ghcr.io/<owner>/nimbo:<version>` and `:latest`.

The MSI keeps the upgrade code of the old Java installer, so installing it replaces a 0.2.x install in place. It is not code-signed; until there is a certificate the README tells users to expect a SmartScreen warning.

## Trying it without publishing

Actions > Release > Run workflow on `main` builds and tests everything, including the Windows zip and installer, and keeps the files as workflow artifacts. It does not create a release or push an image: those jobs only run for tags.

## Building locally

```bash
cd web && npm ci && npm run build && cd ..
go vet ./... && go test ./...
wails build                      # Windows: build/bin/Nimbo.exe (needs the Wails CLI)
CGO_ENABLED=0 go build -o nimbo . # headless binary: nimbo serve
pwsh packaging/build-msi.ps1 -Version 0.3.0 -Exe build/bin/Nimbo.exe -Out Nimbo.msi   # needs WiX 3
docker build -t nimbo .          # optional
```

The UI is embedded in the binary, so `web/dist` must exist before `go vet`, `go test` or `go build`.

For an offline kit (installer plus emulator images) run `packaging/make-offline-kit.ps1 -Version x.y.z -Out <folder>` on a connected machine.
