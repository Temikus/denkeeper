# Stage 1: Build web dashboard
# Runs on the build host's native arch (no QEMU).
FROM --platform=$BUILDPLATFORM node:25-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --prefer-offline || npm install
COPY web/ ./
# outDir in vite.config.js is ../internal/web/dist (relative to /src/web → /src/internal/web/dist)
RUN npm run build

# Stage 2: Build Go binary
# Go cross-compiles natively — no need for QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
# Overwrite the placeholder stub with the real Vite build.
COPY --from=frontend /src/internal/web/dist ./internal/web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /denkeeper ./cmd/denkeeper

# Stage 3: Runtime
FROM alpine:3.24
# `apk upgrade` before `apk add`: the published alpine:3.24 tag lags the v3.24
# branch repository, so a plain `apk add` leaves already-fixed CVEs in the
# image and the release pipeline's Grype gate rejects it (v0.45.0 through
# v0.47.1 failed this way on openssl 3.5.7-r0, fixed in 3.5.8-r0).
#
# VERSION is written to /etc/denkeeper-version so the upgrade layer's cache key
# changes on every tagged release. Without it, buildx would replay the cached
# layer from the registry buildcache whenever the alpine:3.24 tag itself has not
# been republished, silently shipping the stale packages again.
ARG VERSION=dev
RUN echo "${VERSION}" > /etc/denkeeper-version && \
    apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata
COPY --from=builder /denkeeper /usr/local/bin/denkeeper
VOLUME ["/data"]
ENV DENKEEPER_CONFIG=/data/denkeeper.toml
USER 65534
ENTRYPOINT ["denkeeper", "serve"]
