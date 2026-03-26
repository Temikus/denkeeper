#!/bin/sh
# install.sh — Denkeeper installer
# Usage: curl -fsSL https://raw.githubusercontent.com/Temikus/denkeeper/main/install.sh | sh
#
# Options (pass after --):
#   --version v1.2.3   install a specific version (default: latest)
#   --prefix  /path    install binary to <prefix>/bin (default: /usr/local)
#
# Environment variables:
#   DENKEEPER_VERSION        override version (same as --version)
#   DENKEEPER_INSTALL_PREFIX override prefix  (same as --prefix)
set -e

REPO="Temikus/denkeeper"
BINARY="denkeeper"
PREFIX="${DENKEEPER_INSTALL_PREFIX:-/usr/local}"
VERSION="${DENKEEPER_VERSION:-latest}"

# Parse flags (curl | sh passes args after -s --)
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --prefix)  PREFIX="$2";  shift 2 ;;
    -h|--help)
      echo "Usage: install.sh [--version v1.2.3] [--prefix /usr/local]"
      exit 0
      ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

# Detect OS
OS="$(uname -s)"
case "${OS}" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)
    echo "Unsupported OS: ${OS}" >&2
    echo "Download manually from: https://github.com/${REPO}/releases" >&2
    exit 1
    ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH}" >&2
    echo "Download manually from: https://github.com/${REPO}/releases" >&2
    exit 1
    ;;
esac

# Select download tool
if command -v curl >/dev/null 2>&1; then
  _fetch() { curl -fsSL "$1"; }
  _download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  _fetch() { wget -qO- "$1"; }
  _download() { wget -q "$1" -O "$2"; }
else
  echo "Error: curl or wget is required" >&2
  exit 1
fi

# Resolve latest version if not specified
if [ "${VERSION}" = "latest" ]; then
  VERSION=$(_fetch "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  if [ -z "${VERSION}" ]; then
    echo "Error: could not determine latest version" >&2
    exit 1
  fi
fi

# Strip leading 'v' for the archive filename (goreleaser uses bare version numbers)
VERSION_BARE="${VERSION#v}"
ARCHIVE="${BINARY}_${VERSION_BARE}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH}) to ${PREFIX}/bin ..."

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

_download "${BASE_URL}/${ARCHIVE}"    "${TMP}/${ARCHIVE}"
_download "${BASE_URL}/checksums.txt" "${TMP}/checksums.txt"

# Verify checksum
if command -v sha256sum >/dev/null 2>&1; then
  ( cd "${TMP}" && grep "  ${ARCHIVE}$" checksums.txt | sha256sum -c - )
elif command -v shasum >/dev/null 2>&1; then
  # macOS ships shasum instead of sha256sum
  ( cd "${TMP}" && grep "  ${ARCHIVE}$" checksums.txt | shasum -a 256 -c - )
else
  echo "Warning: sha256sum/shasum not found — skipping checksum verification"
fi

tar -xzf "${TMP}/${ARCHIVE}" -C "${TMP}"
install -d "${PREFIX}/bin"
install -m 755 "${TMP}/${BINARY}" "${PREFIX}/bin/${BINARY}"

echo ""
echo "Installed: ${PREFIX}/bin/${BINARY}"
"${PREFIX}/bin/${BINARY}" version
