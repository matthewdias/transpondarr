# syntax=docker/dockerfile:1

# --- frontend build ---
# The frontend output is platform-independent, so always build it on the native
# build platform (never under QEMU emulation) regardless of the target arch.
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
# vite.config.ts builds into ../web/dist
RUN npm run build

# --- backend build ---
# Build on the native platform and cross-compile to the target arch. CGO is off,
# so Go cross-compiles cleanly and we avoid emulating the whole build under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.26 AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
# Only the Go source dirs — pulling in the whole context (frontend/, scripts/…)
# would invalidate this stage's cache on changes the Go build can't even see.
COPY cmd/ ./cmd
COPY internal/ ./internal
COPY web/ ./web
COPY --from=frontend /app/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X github.com/matthewdias/transpondarr/internal/version.Version=${VERSION}" \
    -o /transpondarrd ./cmd/transpondarrd
# An empty data dir to hand to the non-root runtime user (distroless has no shell
# to mkdir/chown in the final stage).
RUN mkdir -p /config-empty

# --- runtime ---
# The container starts as root only so the binary can chown /config (Docker
# creates a missing bind-mount dir root-owned), then drops itself to PUID/PGID
# (default 1000:1000) before serving — see internal/privdrop, gated on
# TRANSPONDARR_PRIVDROP. Pass --user to skip the root phase entirely; the
# mounted config dir must then already be writable by that uid.
FROM gcr.io/distroless/static-debian12
COPY --from=backend /transpondarrd /transpondarrd
# Seed /config owned by the default PUID so a *named* volume inherits usable
# ownership even when the root phase is skipped via --user.
COPY --from=backend --chown=1000:1000 /config-empty /config
ENV TRANSPONDARR_ADDR=:9797 \
    TRANSPONDARR_DATA_DIR=/config \
    TRANSPONDARR_PRIVDROP=1
EXPOSE 9797
VOLUME ["/config"]
# The binary probes its own /api/v1/health — distroless has no shell or curl.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/transpondarrd", "healthcheck"]
ENTRYPOINT ["/transpondarrd"]
