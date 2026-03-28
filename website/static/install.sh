#!/bin/sh
# Denkeeper installer — https://github.com/Temikus/denkeeper
#
# Usage:
#   curl -fsSL https://get.denkeeper.io | sh
#   curl -fsSL https://get.denkeeper.io | sh -s -- --version v1.2.3
#
# The script:
#   1. Detects OS and architecture
#   2. Fetches the latest (or pinned) release from GitHub
#   3. Downloads the tarball and checksums
#   4. Verifies the SHA256 checksum
#   5. Installs the binary to INSTALL_DIR (default: /usr/local/bin)
#
# Environment variables:
#   INSTALL_DIR   — override install location (default: /usr/local/bin)
#   GITHUB_TOKEN  — optional, avoids GitHub API rate limits

set -eu

REPO="Temikus/denkeeper"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION=""

# ── Helpers ──────────────────────────────────────────────────────────────────

log()   { printf '  %s\n' "$@"; }
info()  { printf '\033[1;34m==> %s\033[0m\n' "$@"; }
warn()  { printf '\033[1;33mWarning: %s\033[0m\n' "$@" >&2; }
error() { printf '\033[1;31mError: %s\033[0m\n' "$@" >&2; exit 1; }

need_cmd() {
  if ! command -v "$1" > /dev/null 2>&1; then
    error "need '$1' (command not found)"
  fi
}

# ── Argument parsing ─────────────────────────────────────────────────────────

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --help|-h)
      printf 'Usage: curl -fsSL https://get.denkeeper.io | sh -s -- [OPTIONS]\n\n'
      printf 'Options:\n'
      printf '  --version VERSION   Install a specific version (e.g. v1.2.3)\n'
      printf '  --help              Show this help\n\n'
      printf 'Environment variables:\n'
      printf '  INSTALL_DIR         Install location (default: /usr/local/bin)\n'
      printf '  GITHUB_TOKEN        GitHub token to avoid API rate limits\n'
      exit 0
      ;;
    *)
      error "unknown option: $1 (try --help)"
      ;;
  esac
done

# ── Detect platform ──────────────────────────────────────────────────────────

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    *)       error "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    aarch64|arm64)   echo "arm64" ;;
    *)               error "unsupported architecture: $(uname -m)" ;;
  esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

info "Detected platform: ${OS}/${ARCH}"

# ── Resolve version ──────────────────────────────────────────────────────────

need_cmd curl

github_curl() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: token ${GITHUB_TOKEN}" "$@"
  else
    curl -fsSL "$@"
  fi
}

if [ -z "$VERSION" ]; then
  info "Fetching latest release..."
  VERSION="$(github_curl "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"

  if [ -z "$VERSION" ]; then
    error "could not determine latest version (GitHub API rate limit? try setting GITHUB_TOKEN)"
  fi
fi

# Strip leading 'v' for asset filenames (assets use bare version numbers)
BARE_VERSION="${VERSION#v}"

info "Installing denkeeper ${VERSION}"

# ── Download ─────────────────────────────────────────────────────────────────

TARBALL="denkeeper_${BARE_VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
TMPDIR="$(mktemp -d)"

cleanup() { rm -rf "$TMPDIR"; }
trap cleanup EXIT

log "Downloading ${TARBALL}..."
github_curl -o "${TMPDIR}/${TARBALL}" "${BASE_URL}/${TARBALL}"

log "Downloading checksums..."
github_curl -o "${TMPDIR}/checksums.txt" "${BASE_URL}/checksums.txt"

# ── Verify checksum ──────────────────────────────────────────────────────────

info "Verifying SHA256 checksum..."

EXPECTED="$(grep "${TARBALL}" "${TMPDIR}/checksums.txt" | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
  error "checksum not found for ${TARBALL} in checksums.txt"
fi

if command -v sha256sum > /dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
elif command -v shasum > /dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
else
  error "need 'sha256sum' or 'shasum' to verify checksum"
fi

if [ "$EXPECTED" != "$ACTUAL" ]; then
  error "checksum mismatch!\n  expected: ${EXPECTED}\n  actual:   ${ACTUAL}"
fi

log "Checksum OK: ${EXPECTED}"

# ── Install ──────────────────────────────────────────────────────────────────

info "Extracting to ${INSTALL_DIR}..."

need_cmd tar

tar -xzf "${TMPDIR}/${TARBALL}" -C "${TMPDIR}"

if [ ! -f "${TMPDIR}/denkeeper" ]; then
  error "binary not found in archive"
fi

# Attempt direct copy; fall back to sudo if permission denied
if [ -w "${INSTALL_DIR}" ]; then
  cp "${TMPDIR}/denkeeper" "${INSTALL_DIR}/denkeeper"
  chmod +x "${INSTALL_DIR}/denkeeper"
else
  log "Elevated permissions required for ${INSTALL_DIR}"
  sudo cp "${TMPDIR}/denkeeper" "${INSTALL_DIR}/denkeeper"
  sudo chmod +x "${INSTALL_DIR}/denkeeper"
fi

# ── Done ─────────────────────────────────────────────────────────────────────

INSTALLED_VERSION="$("${INSTALL_DIR}/denkeeper" --version 2>/dev/null || echo "${VERSION}")"

printf '\n'
info "denkeeper ${INSTALLED_VERSION} installed to ${INSTALL_DIR}/denkeeper"
printf '\n'
log "Get started:"
log "  denkeeper setup       # first-run configuration wizard"
log "  denkeeper serve       # start the agent"
log ""
log "Documentation: https://github.com/${REPO}"
