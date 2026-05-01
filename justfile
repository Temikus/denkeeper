# Denkeeper justfile

# Default: list available recipes
default:
    @just --list

# Build the web dashboard if internal/web/dist/ is missing (required by //go:embed).
# Delegates to `task ui:build` which is fingerprinted: cheap when up to date,
# rebuilds when web sources change.
ensure-web-dist:
    @mise x -- task ui:build

# Build the denkeeper binary (cached via Task: skips when Go + dist are unchanged)
build:
    @mise x -- task build

# Run all tests (cached via Task; --force to bust). -count=1 disables Go's
# in-process test cache so a stale checksum never masks fresh failures.
test:
    @mise x -- task test

# Run all tests (verbose, uncached — agent-friendly diagnostic mode)
test-v: ensure-web-dist
    go test -race -count=1 -v ./...

# Run tests with coverage report (uncached)
test-cover: ensure-web-dist
    go test -race -count=1 -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Open coverage in browser
test-cover-html: test-cover
    go tool cover -html=coverage.out

# Run integration tests (cached via Task; requires -tags=integration)
test-integration:
    @mise x -- task test:integration

# Run tests for a specific package (e.g. just test-pkg internal/agent)
test-pkg pkg: ensure-web-dist
    go test -race -count=1 -v ./{{pkg}}/...

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

# Run linter (cached via Task)
lint:
    @mise x -- task lint

# Run linter with auto-fix (uncached — modifies files)
lint-fix: ensure-web-dist
    mise x -- golangci-lint run --fix

# Lint web UI (cached via Task)
lint-ui:
    @mise x -- task ui:lint

# Format all Go files (uncached — modifies files)
fmt:
    gofmt -w .

# Check formatting (cached via Task)
fmt-check:
    @mise x -- task fmt-check

# Vet the codebase (cached via Task)
vet:
    @mise x -- task vet

# Run all checks (fmt, vet, lint, test)
check: fmt-check vet lint lint-ui test test-ui

# Run all checks with minimal output (for agent hooks).
# Per-step caching via Task: each step skips when its declared sources are
# unchanged. Set JUST_HOOK_FORCE=1 to bypass all per-step caches.
hook:
    #!/usr/bin/env bash
    set -euo pipefail
    force_flag=""
    if [ "${JUST_HOOK_FORCE:-}" = "1" ]; then
        force_flag="--force"
    fi
    steps=("fmt-check" "vet" "lint" "ui:lint" "test" "ui:test")
    labels=("fmt-check" "vet" "lint" "lint-ui" "test" "test-ui")
    tmpfile=$(mktemp "${TMPDIR:-/tmp}/just-hook.XXXXXX")
    trap 'rm -f "$tmpfile"' EXIT
    for i in "${!steps[@]}"; do
        if mise x -- task --silent ${force_flag} "${steps[$i]}" >"$tmpfile" 2>&1; then
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

# Build the web dashboard (cached via Task; runs npm ci only when lockfile changed)
build-ui:
    @mise x -- task ui:build

# Full build: web dashboard then Go binary
build-full: build-ui build

# Generate OpenAPI spec from Go annotations (cached via Task)
openapi:
    @mise x -- task openapi

# Run the Vite dev server (proxies /api to localhost:8080)
dev-ui:
    @mise x -- task ui:install
    cd web && npm run dev

# Run web UI unit tests (cached via Task)
test-ui:
    @mise x -- task ui:test

# Run web UI tests in watch mode
test-ui-watch:
    @mise x -- task ui:install
    cd web && npm run test:watch

# Run Playwright E2E tests
test-e2e:
    @mise x -- task ui:install
    cd web && npx playwright test

# Remove frontend build artifacts and node_modules
clean-ui:
    rm -rf web/node_modules

# Build the documentation website
build-website:
    @mise x -- task website:install
    cd website && rm -rf resources/_gen && npm run build

# Run the Hugo dev server for the documentation website
dev-website:
    @mise x -- task website:install
    cd website && rm -rf resources/_gen && npm run dev

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

# Run security scans (cached via Task; usage: just scan [gosec|govulncheck], default: all).
# Pass --force to a specific scan (e.g. `mise x -- task scan:govulncheck --force`)
# to re-check against a fresh vulnerability database when sources are unchanged.
scan tool="all":
    #!/usr/bin/env sh
    set -e
    case "{{tool}}" in
        all)         mise x -- task scan ;;
        gosec)       mise x -- task scan:gosec ;;
        govulncheck) mise x -- task scan:govulncheck ;;
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
