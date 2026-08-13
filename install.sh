#!/bin/sh
set -eu

REPO="agenrena/agenrena-cli"
BIN_NAME="agenrena"
HELPER_NAME="agenrena-rtc-helper"
INSTALL_DIR="${AGENRENA_INSTALL_DIR:-$HOME/.local/bin}"

detect_os() {
  os="$(uname -s)"
  case "$os" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *) echo "Unsupported OS: $os" >&2; exit 1 ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    arm64|aarch64) echo "arm64" ;;
    x86_64|amd64) echo "amd64" ;;
    *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

need_cmd curl

os="$(detect_os)"
arch="$(detect_arch)"
asset="${BIN_NAME}-${os}-${arch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"
helper_asset="${HELPER_NAME}-${os}-${arch}"
if [ "$os" = "darwin" ]; then
  helper_asset="${helper_asset}.tar.gz"
  need_cmd tar
fi
helper_url="https://github.com/${REPO}/releases/latest/download/${helper_asset}"

tmp_dir="$(mktemp -d)"
tmp_bin="${tmp_dir}/${BIN_NAME}"
tmp_helper="${tmp_dir}/${HELPER_NAME}"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

echo "Downloading ${asset} from ${REPO}..."
curl -fsSL "$url" -o "$tmp_bin"
chmod +x "$tmp_bin"

echo "Downloading ${helper_asset} from ${REPO}..."
if [ "$os" = "darwin" ]; then
  helper_archive="${tmp_dir}/${helper_asset}"
  curl -fsSL "$helper_url" -o "$helper_archive"
  tar -xzf "$helper_archive" -C "$tmp_dir"
  test -f "${tmp_dir}/libopus.0.dylib"
  test -f "${tmp_dir}/libsoxr.0.dylib"
else
  curl -fsSL "$helper_url" -o "$tmp_helper"
fi
chmod +x "$tmp_helper"

mkdir -p "$INSTALL_DIR"
mv "$tmp_bin" "${INSTALL_DIR}/${BIN_NAME}"
if [ "$os" = "darwin" ]; then
  mv "${tmp_dir}/libopus.0.dylib" "${INSTALL_DIR}/libopus.0.dylib"
  mv "${tmp_dir}/libsoxr.0.dylib" "${INSTALL_DIR}/libsoxr.0.dylib"
fi
mv "$tmp_helper" "${INSTALL_DIR}/${HELPER_NAME}"

echo "Installed ${BIN_NAME} to ${INSTALL_DIR}/${BIN_NAME}"
echo "Installed ${HELPER_NAME} to ${INSTALL_DIR}/${HELPER_NAME}"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "Add this directory to PATH if needed:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

"${INSTALL_DIR}/${BIN_NAME}" version
