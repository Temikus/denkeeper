# Denkeeper justfile

# Default: list available recipes
default:
    @just --list

# Build the denkeeper binary
build:
    go build -o pkg/bin/denkeeper ./cmd/denkeeper

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

# Start the agent (optionally pass config path: just serve ./denkeeper.toml)
serve config="":
    #!/usr/bin/env sh
    if [ -n "{{config}}" ]; then
        go run ./cmd/denkeeper serve --config "{{config}}"
    else
        go run ./cmd/denkeeper serve
    fi

# Run linter
lint:
    mise x -- golangci-lint run

# Run linter with auto-fix
lint-fix:
    mise x -- golangci-lint run --fix

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
    docker build -t denkeeper .

# Start with Docker Compose
docker-up:
    docker compose up -d

# Stop Docker Compose
docker-down:
    docker compose down

# Build the web dashboard (requires Node.js and npm)
build-ui:
    #!/usr/bin/env sh
    cd web
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm run build

# Full build: web dashboard then Go binary
build-full: build-ui build

# Run the Vite dev server (proxies /api to localhost:8080)
dev-ui:
    #!/usr/bin/env sh
    cd web
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm run dev

# Remove frontend build artifacts and node_modules
clean-ui:
    rm -rf web/node_modules

# Build the documentation website
build-website:
    #!/usr/bin/env sh
    cd website
    rm -rf resources/_gen
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm run build

# Run the Hugo dev server for the documentation website
dev-website:
    #!/usr/bin/env sh
    cd website
    rm -rf resources/_gen
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm run dev

# Tag and push a release (usage: just release patch|minor|major)
release bump:
    #!/usr/bin/env bash
    set -euo pipefail
    git fetch --tags
    latest=$(git tag -l 'v*' --sort=-v:refname | head -n1)
    if [ -z "$latest" ]; then
        latest="v0.0.0"
    fi
    # Strip leading 'v' and split
    ver="${latest#v}"
    IFS='.' read -r major minor patch <<< "$ver"
    case "{{bump}}" in
        patch) patch=$((patch + 1)) ;;
        minor) minor=$((minor + 1)); patch=0 ;;
        major) major=$((major + 1)); minor=0; patch=0 ;;
        *) echo "Usage: just release [patch|minor|major]"; exit 1 ;;
    esac
    tag="v${major}.${minor}.${patch}"
    echo "Tagging ${tag} (previous: ${latest})"
    git tag -a "$tag" -m "$tag"
    git push origin "$tag"
    echo "Released ${tag}"

# Count lines of Go code (source and test separately)
loc:
    @echo "Source:"
    @find . -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1
    @echo "Tests:"
    @find . -name '*_test.go' | xargs wc -l | tail -1
