# Foxbox justfile

# Default: list available recipes
default:
    @just --list

# Build the foxbox binary
build:
    go build -o foxbox ./cmd/foxbox

# Run all tests
test:
    go test -race ./...

# Run tests with coverage
test-cover:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Start the agent (optionally pass config path: just serve ./foxbox.toml)
serve config="":
    #!/usr/bin/env sh
    if [ -n "{{config}}" ]; then
        go run ./cmd/foxbox serve --config "{{config}}"
    else
        go run ./cmd/foxbox serve
    fi

# Run linter
lint:
    golangci-lint run

# Tidy modules
tidy:
    go mod tidy

# Clean build artifacts
clean:
    rm -f foxbox coverage.out
