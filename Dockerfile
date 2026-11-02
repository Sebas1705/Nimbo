# syntax=docker/dockerfile:1

# 1. Build the web UI
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build the Go binary (the UI, labs and stacks are embedded in it). No cgo: the headless mode needs no window.
FROM golang:1.25 AS build
ARG NIMBO_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${NIMBO_VERSION}" -o /out/nimbo .

# 3. Run it. Nimbo drives the host's Docker, so the image carries only the docker CLI.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/nimbo /usr/local/bin/nimbo

# Inside a container Nimbo must listen on all interfaces so the published port reaches it, and the
# emulators it starts are published on the host, not on this container. Publish the port on loopback only.
ENV NIMBO_HOST=0.0.0.0 \
    NIMBO_EMULATOR_HOST=host.docker.internal \
    NIMBO_DISTRIBUTION=docker \
    NIMBO_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
LABEL org.opencontainers.image.title="Nimbo" \
      org.opencontainers.image.description="Your local cloud, no bill: a control center for local cloud emulators" \
      org.opencontainers.image.source="https://github.com/Sebas1705/Nimbo" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["nimbo", "serve"]
