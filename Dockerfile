# Stage 1: Build
FROM golang:1.25-alpine AS builder
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /denkeeper ./cmd/denkeeper

# Stage 2: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /denkeeper /usr/local/bin/denkeeper
VOLUME ["/data"]
ENTRYPOINT ["denkeeper", "serve", "--config", "/data/denkeeper.toml"]
