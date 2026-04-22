#!/bin/sh
set -eu

REPO="${BROWSE_REPO:-dotaikit/browse}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${BROWSE_VERSION:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version|-v) VERSION="$2"; shift 2 ;;
    *) shift ;;
  esac
done

if [ "${INSTALL_DIR#~/}" != "$INSTALL_DIR" ]; then
  INSTALL_DIR="$HOME/${INSTALL_DIR#~/}"
fi

if [ -n "$VERSION" ] && command -v browse >/dev/null 2>&1; then
  INSTALLED="$(browse --version 2>/dev/null | awk '{print $2}' || true)"
  if [ "$INSTALLED" = "$VERSION" ]; then
    printf 'browse %s already installed at %s\n' "$VERSION" "$(command -v browse)"
    exit 0
  fi
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: required command not found: $1" >&2
    exit 1
  fi
}

cleanup() {
  if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}

trap cleanup EXIT INT TERM

require_cmd curl
require_cmd tar

case "$(uname -s)" in
  Linux)
    OS="linux"
    ;;
  Darwin)
    OS="darwin"
    ;;
  *)
    echo "Error: unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64)
    ARCH="amd64"
    ;;
  arm64 | aarch64)
    ARCH="arm64"
    ;;
  *)
    echo "Error: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

ARCHIVE="browse-${OS}-${ARCH}.tar.gz"
if [ -n "$VERSION" ]; then
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
else
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
fi
ARCHIVE_URL="${BASE_URL}/${ARCHIVE}"
CHECKSUM_URL="${BASE_URL}/checksums.txt"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t browse-install)"
ARCHIVE_PATH="${TMP_DIR}/${ARCHIVE}"
CHECKSUM_PATH="${TMP_DIR}/checksums.txt"

printf 'Downloading %s\n' "$ARCHIVE_URL"
curl -fsSL "$ARCHIVE_URL" -o "$ARCHIVE_PATH"

if curl -fsSL "$CHECKSUM_URL" -o "$CHECKSUM_PATH"; then
  CHECKSUM_LINE="$(grep " ${ARCHIVE}\$" "$CHECKSUM_PATH" || true)"
  if [ -z "$CHECKSUM_LINE" ]; then
    echo "Error: checksum for ${ARCHIVE} not found in checksums.txt" >&2
    exit 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s\n' "$CHECKSUM_LINE" | (cd "$TMP_DIR" && sha256sum -c -)
  elif command -v shasum >/dev/null 2>&1; then
    EXPECTED_SUM="$(printf '%s' "$CHECKSUM_LINE" | awk '{print $1}')"
    ACTUAL_SUM="$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')"
    if [ "$EXPECTED_SUM" != "$ACTUAL_SUM" ]; then
      echo "Error: checksum verification failed for ${ARCHIVE}" >&2
      exit 1
    fi
    printf 'Verified checksum for %s\n' "$ARCHIVE"
  else
    echo "Warning: sha256sum/shasum not found; skipping checksum verification" >&2
  fi
else
  echo "Warning: checksums.txt not found; skipping checksum verification" >&2
fi

mkdir -p "$INSTALL_DIR"
tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"

if [ ! -f "${TMP_DIR}/browse" ]; then
  echo "Error: archive did not contain browse binary" >&2
  exit 1
fi

cp "${TMP_DIR}/browse" "${INSTALL_DIR}/browse"
chmod +x "${INSTALL_DIR}/browse"
xattr -d com.apple.quarantine "${INSTALL_DIR}/browse" 2>/dev/null || true

printf 'Installed browse to %s\n' "${INSTALL_DIR}/browse"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    printf 'Add %s to your PATH, for example:\n' "$INSTALL_DIR"
    printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    ;;
esac
