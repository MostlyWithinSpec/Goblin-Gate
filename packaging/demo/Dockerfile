# The demo (goblind --demo) as a container. Releases publish it as
# ghcr.io/mostlywithinspec/goblin-gate-demo (amd64 and arm64), so anyone can try Goblin Gate with
#   docker run --rm -p 127.0.0.1:8090:8090 ghcr.io/mostlywithinspec/goblin-gate-demo
# Build it yourself from the repository root:
#   docker build -f packaging/demo/Dockerfile -t goblin-gate-demo .
# See docs/dev/DEMO.md.

# The build stages run on the build machine's own architecture and cross-compile, so a
# multi-architecture build needs no emulation.

FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=demo
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/networkgoblinlabs/goblin-gate/internal/version.Version=${VERSION}" \
      -o /goblind ./cmd/goblind

# No shell, no package manager, runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/MostlyWithinSpec/Goblin-Gate" \
      org.opencontainers.image.title="Goblin Gate demo" \
      org.opencontainers.image.description="Goblin Gate's web UI on a pretend home network: nothing is real, nothing leaves the container." \
      org.opencontainers.image.licenses="GPL-3.0-only"
COPY --from=go /goblind /usr/sbin/goblind
COPY --from=web /src/web/dist /usr/share/goblin-gate/web
EXPOSE 8090
ENTRYPOINT ["/usr/sbin/goblind", "--demo", "--listen", ":8090", \
            "--web-dir", "/usr/share/goblin-gate/web", "--log-sources="]
# On your own computer it runs until you stop it. A shared public demo passes
# "--demo-reset 1h" instead (compose.yaml), and its restart policy starts it again from the seed.
CMD ["--demo-reset", "0"]
