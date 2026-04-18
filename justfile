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

# Run integration tests (requires -tags=integration)
test-integration:
    go test -tags integration -race -v ./internal/integration/...

# Run tests for a specific package (e.g. just test-pkg internal/agent)
test-pkg pkg:
    go test -race -v ./{{pkg}}/...

# Start the agent with live reload (optionally pass config path: just serve ./denkeeper.toml)
serve config="":
    #!/usr/bin/env sh
    if [ -n "{{config}}" ]; then
        air -- serve --config "{{config}}"
    else
        air
    fi

# Run with debug logging and live reload
serve-debug config="":
    #!/usr/bin/env sh
    export DENKEEPER_LOG_LEVEL=debug
    if [ -n "{{config}}" ]; then
        air -- serve --config "{{config}}"
    else
        air
    fi

# Start the agent without live reload (optionally pass config path)
serve-once config="":
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

# Lint web UI (Svelte)
lint-ui:
    #!/usr/bin/env sh
    # Catch unescaped ${VAR} in Svelte templates — Svelte interprets {VAR} as an
    # expression, causing runtime ReferenceErrors. Use placeholder={"${VAR}"} instead.
    errors=$(grep -rn -E '="[^"]*\$\{[A-Za-z_][A-Za-z_0-9]*\}[^"]*"' web/src --include='*.svelte' | grep -v '={"') || true
    if [ -n "$errors" ]; then
        echo "ERROR: Unescaped \${} in Svelte attribute strings (Svelte interprets {…} as expressions):"
        echo "$errors"
        echo ""
        echo 'Fix: wrap the attribute value as a JS string, e.g. placeholder={"text ${VAR} text"}'
        exit 1
    fi

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
check: fmt-check vet lint lint-ui test test-ui

# Run all checks with minimal output (for agent hooks)
hook:
    #!/usr/bin/env bash
    set -euo pipefail
    steps=("just fmt-check" "just vet" "just lint" "just lint-ui" "just test" "just test-ui")
    labels=("fmt-check" "vet" "lint" "lint-ui" "test" "test-ui")
    tmpfile=$(mktemp)
    trap 'rm -f "$tmpfile"' EXIT
    for i in "${!steps[@]}"; do
        if ${steps[$i]} >"$tmpfile" 2>&1; then
            echo "✓ ${labels[$i]}"
        else
            echo "✗ ${labels[$i]}"
            cat "$tmpfile"
            exit 1
        fi
    done

# Run all checks including E2E (requires running server)
check-full: check test-e2e

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

# Run web UI unit tests
test-ui:
    #!/usr/bin/env sh
    cd web
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm test

# Run web UI tests in watch mode
test-ui-watch:
    #!/usr/bin/env sh
    cd web
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npm run test:watch

# Run Playwright E2E tests
test-e2e:
    #!/usr/bin/env sh
    cd web
    if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
        npm ci
    fi
    npx playwright test

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

# Run security scans (usage: just scan [gosec|govulncheck], default: all)
scan tool="all":
    #!/usr/bin/env sh
    set -e
    case "{{tool}}" in
        all)
            just scan gosec
            just scan govulncheck
            ;;
        gosec)
            go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude=G101,G104,G120 ./...
            ;;
        govulncheck)
            go run golang.org/x/vuln/cmd/govulncheck@latest ./...
            ;;
        *)
            echo "Unknown scan: {{tool}}. Available: gosec, govulncheck"
            exit 1
            ;;
    esac

# Count lines of Go code (source and test separately)
loc:
    @echo "Source:"
    @find . -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1
    @echo "Tests:"
    @find . -name '*_test.go' | xargs wc -l | tail -1
