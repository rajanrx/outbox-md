#!/bin/sh
# install.sh — one-command installer for the outbox-md CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/rajanrx/outbox-md/main/install.sh | sh
#
# Downloads the prebuilt `outbox` binary for your OS/arch from the latest GitHub
# Release, verifies its checksum when available, and installs it to a bin dir on
# your PATH. No dependencies beyond curl + a shell. Safe to read first:
#
#   curl -fsSL https://raw.githubusercontent.com/rajanrx/outbox-md/main/install.sh | less
#
# Environment overrides:
#   VERSION             release tag to install (e.g. outbox-md-v0.6.0); default: latest
#   OUTBOX_INSTALL_DIR  install directory; default: /usr/local/bin, else ~/.local/bin
set -eu

REPO="rajanrx/outbox-md"
BIN="outbox"

info() { printf '%s\n' "$*"; }
err()  { printf 'install: %s\n' "$*" >&2; exit 1; }

# --- detect OS -------------------------------------------------------------
os=$(uname -s)
case "$os" in
	Darwin) os="darwin" ;;
	Linux)  os="linux" ;;
	*) err "unsupported OS: $os (only darwin and linux have prebuilt binaries — build from source instead)" ;;
esac

# --- detect ARCH -----------------------------------------------------------
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64)   arch="amd64" ;;
	arm64 | aarch64)  arch="arm64" ;;
	*) err "unsupported architecture: $arch (only amd64 and arm64 are built — build from source instead)" ;;
esac

asset="${BIN}_${os}_${arch}"

# --- resolve download URLs -------------------------------------------------
# Default uses the /releases/latest/download/ redirect, which resolves to the
# newest release regardless of tag naming. Pin an exact release with VERSION.
if [ -n "${VERSION:-}" ]; then
	base="https://github.com/${REPO}/releases/download/${VERSION}"
else
	base="https://github.com/${REPO}/releases/latest/download"
fi
asset_url="${base}/${asset}"
sums_url="${base}/checksums.txt"

command -v curl >/dev/null 2>&1 || err "curl is required but not found on PATH"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "Downloading ${asset}..."
curl -fsSL "$asset_url" -o "$tmp/$asset" ||
	err "download failed: $asset_url
No prebuilt binary yet? Releases publish binaries from the release job; until one ships, run via Docker (see the README)."

# --- best-effort checksum verification -------------------------------------
# checksums.txt lists "  <sha256>  <asset>" for every release binary. Verify
# with whichever tool this OS ships (Linux: sha256sum, macOS: shasum -a 256).
if curl -fsSL "$sums_url" -o "$tmp/checksums.txt" 2>/dev/null; then
	expected=$(grep " ${asset}\$" "$tmp/checksums.txt" | awk '{print $1}')
	if [ -n "$expected" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
		elif command -v shasum >/dev/null 2>&1; then
			actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
		else
			actual=""
			info "No sha256 tool found — skipping checksum verification."
		fi
		if [ -n "$actual" ]; then
			[ "$actual" = "$expected" ] || err "checksum mismatch for ${asset} (expected ${expected}, got ${actual})"
			info "Checksum verified."
		fi
	fi
else
	info "No checksums.txt published — skipping checksum verification."
fi

# --- choose an install dir -------------------------------------------------
if [ -n "${OUTBOX_INSTALL_DIR:-}" ]; then
	dir="$OUTBOX_INSTALL_DIR"
	mkdir -p "$dir"
elif [ -w /usr/local/bin ] 2>/dev/null; then
	dir="/usr/local/bin"
else
	dir="$HOME/.local/bin"
	mkdir -p "$dir"
fi

chmod +x "$tmp/$asset"
mv "$tmp/$asset" "$dir/$BIN"
info "Installed ${BIN} to ${dir}/${BIN}"

# --- PATH hint -------------------------------------------------------------
case ":${PATH}:" in
	*":${dir}:"*) : ;;
	*)
		info ""
		info "${dir} is not on your PATH. Add it, e.g.:"
		info "  echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.profile && . ~/.profile"
		;;
esac

# --- next steps ------------------------------------------------------------
info ""
info "Done. Next steps (from a folder of .md specs):"
info "  ${BIN} init      # scaffold outbox.yaml + register the MCP with Claude"
info "  ${BIN} up        # serve the review UI and open it in your browser"
