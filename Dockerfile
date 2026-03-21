# Stage 1: Build
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /foxbox ./cmd/foxbox

# Stage 2: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /foxbox /usr/local/bin/foxbox
VOLUME ["/data"]
ENTRYPOINT ["foxbox", "serve", "--config", "/data/foxbox.toml"]
