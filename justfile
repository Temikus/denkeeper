# Foxbox justfile

# Default: list available recipes
default:
    @just --list

# Build the foxbox binary
build:
    go build -o pkg/bin/foxbox ./cmd/foxbox

# Run all tests
test:
    go test -race ./...

# Run all tests (verbose)
test-v:
    go test -race -v ./...

# Run tests with coverage report
test-cover:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Open coverage in browser
test-cover-html: test-cover
    go tool cover -html=coverage.out

# Run tests for a specific package (e.g. just test-pkg internal/agent)
test-pkg pkg:
    go test -race -v ./{{pkg}}/...

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

# Run linter with auto-fix
lint-fix:
    golangci-lint run --fix

# Format all Go files
fmt:
    gofmt -w .

# Check formatting (CI-friendly, exits non-zero if unformatted)
fmt-check:
    @test -z "$(gofmt -l .)" || (echo "Unformatted files:" && gofmt -l . && exit 1)

# Vet the codebase
vet:
    go vet ./...

# Run all checks (fmt, vet, lint, test)
check: fmt-check vet lint test

# Tidy modules
tidy:
    go mod tidy

# Clean build artifacts
clean:
    rm -rf pkg/ coverage.out

# Show project structure
tree:
    @find . -type f -name '*.go' | grep -v vendor | sort

# Build Docker image
docker-build:
    docker build -t foxbox .

# Start with Docker Compose
docker-up:
    docker compose up -d

# Stop Docker Compose
docker-down:
    docker compose down

# Count lines of Go code (source and test separately)
loc:
    @echo "Source:"
    @find . -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1
    @echo "Tests:"
    @find . -name '*_test.go' | xargs wc -l | tail -1
