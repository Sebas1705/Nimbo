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

4. The workflow runs the tests, builds `nimbo-<version>.jar`, checks that the jar starts and serves the UI, builds the Docker image, then:
   - publishes a GitHub release with the jar and `SHA256SUMS` (marked as a pre-release while the version is 0.x);
   - pushes `ghcr.io/<owner>/nimbo:<version>` and `:latest`.

## Trying it without publishing

Actions > Release > Run workflow on `main` builds and tests everything and keeps the jar as a workflow artifact. It does not create a release or push an image: those jobs only run for tags.

## Building locally

```bash
cd web && npm ci && npm run build && cd ..
./gradlew :backend:buildFatJar        # backend/build/libs/nimbo-<version>.jar
docker build -t nimbo .               # optional
```

The jar needs `web/dist` to exist because it packs the UI; Gradle stops with a clear message when it does not.
