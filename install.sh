#!/usr/bin/env sh
set -eu

REPO="sauercrowd/gci"
VERSION="${GCI_VERSION:-latest}"
BIN_DIR="${GCI_INSTALL_DIR:-/usr/local/bin}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -v|--version)
      VERSION="$2"
      shift 2
      ;;
    -b|--bin-dir)
      BIN_DIR="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Required command not found: $1" >&2
    exit 1
  }
}

need_cmd uname
need_cmd curl
need_cmd tar

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  linux) OS="linux" ;;
  darwin) OS="darwin" ;;
  *)
    echo "Unsupported OS: $OS" >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/')"
fi

if [ -z "$VERSION" ]; then
  echo "Unable to resolve release version" >&2
  exit 1
fi

VERSION_NO_V="${VERSION#v}"
ARCHIVE="gci_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$ARCHIVE"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

echo "Downloading $URL"
curl -fsSL "$URL" -o "$TMP_DIR/$ARCHIVE"

tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"

mkdir -p "$BIN_DIR" 2>/dev/null || true

if [ -w "$BIN_DIR" ]; then
  install -m 755 "$TMP_DIR/gci" "$BIN_DIR/gci"
else
  if command -v sudo >/dev/null 2>&1; then
    sudo install -m 755 "$TMP_DIR/gci" "$BIN_DIR/gci"
  else
    echo "No write access to $BIN_DIR and sudo is unavailable" >&2
    exit 1
  fi
fi

echo "Installed gci to $BIN_DIR/gci"
"$BIN_DIR/gci" --help >/dev/null 2>&1 || true
