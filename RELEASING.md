# Releasing Nimbo

A release is a git tag. Everything else is done by `.github/workflows/release.yml`.

1. Decide the version (`0.2.0`, `1.0.0`...).
2. Change the default in `backend/build.gradle.kts` (`?: "0.1.0"`) and add a `## [x.y.z]` section to `CHANGELOG.md`.
   The workflow uses that section as the release notes and refuses a tag that does not match the version in the build file.
3. Make sure `main` is green, then:

   ```bash
   git tag v0.2.0
   git push origin v0.2.0
   ```

4. The workflow runs the tests, builds `nimbo-<version>.jar`, checks that the jar starts and serves the UI, builds the Docker image, and on a Windows runner builds the desktop app (the portable zip is smoke-tested with `packaging/smoke-test.ps1`, then the `.msi` is built with WiX). Then:
   - publishes a GitHub release with the jar, `Nimbo-<version>-windows-x64.zip`, `Nimbo-<version>-windows-x64.msi` and a `SHA256SUMS` that covers all of them (marked as a pre-release while the version is 0.x);
   - pushes `ghcr.io/<owner>/nimbo:<version>` and `:latest`.

The Windows installer is not code-signed. Signing needs a certificate; until there is one the README tells users to expect a SmartScreen warning.

## Trying it without publishing

Actions > Release > Run workflow on `main` builds and tests everything, including the Windows zip and installer, and keeps the files as workflow artifacts. It does not create a release or push an image: those jobs only run for tags.

## Building locally

```bash
cd web && npm ci && npm run build && cd ..
./gradlew :backend:buildFatJar        # backend/build/libs/nimbo-<version>.jar
./gradlew :backend:zipDesktop         # Windows only: backend/build/distributions/Nimbo-<version>-windows-x64.zip
./gradlew :backend:packageDesktop -PdesktopType=msi   # Windows with WiX 3 installed: the installer
docker build -t nimbo .               # optional
```

`./gradlew :backend:generateIcon` redraws `packaging/nimbo.ico` from the code that draws the tray icon; a test fails when the committed icon is out of date.

The jar needs `web/dist` to exist because it packs the UI; Gradle stops with a clear message when it does not.
