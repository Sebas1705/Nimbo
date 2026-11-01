# syntax=docker/dockerfile:1

# 1. Build the web UI
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build the jar (UI, labs and stacks are packed inside it)
FROM eclipse-temurin:21-jdk AS build
ARG NIMBO_VERSION=0.1.0
WORKDIR /src
COPY gradlew settings.gradle.kts build.gradle.kts ./
COPY gradle gradle
# The wrapper may have been checked out with Windows line endings.
RUN sed -i 's/\r$//' gradlew && chmod +x gradlew
COPY backend backend
COPY labs labs
COPY nimbo.yml ./
COPY --from=web /src/web/dist web/dist
RUN ./gradlew --no-daemon --console=plain -PnimboVersion=${NIMBO_VERSION} :backend:buildFatJar -x test

# 3. Run it. Nimbo drives the host's Docker, so the image carries only the docker CLI.
FROM eclipse-temurin:21-jre
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /src/backend/build/libs/nimbo-*.jar /app/nimbo.jar

# Inside a container Nimbo must listen on all interfaces so the published port reaches it, and the
# emulators it starts are published on the host, not on this container. Publish the port on loopback only.
ENV NIMBO_HOST=0.0.0.0 \
    NIMBO_EMULATOR_HOST=host.docker.internal
EXPOSE 8080
LABEL org.opencontainers.image.title="Nimbo" \
      org.opencontainers.image.description="Your local cloud, no bill: a control center for local cloud emulators" \
      org.opencontainers.image.source="https://github.com/Sebas1705/Nimbo" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["java", "-jar", "/app/nimbo.jar"]
