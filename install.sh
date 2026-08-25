#!/bin/bash
set -e

echo "AIX"
echo "========================="
echo ""

BIN_DIR="${BIN_DIR:-}"
CONFIG_ONLY="${CONFIG_ONLY:-0}"
NO_SETUP="${NO_SETUP:-0}"

# --- Go version check ---
if [ "$CONFIG_ONLY" != "1" ]; then
    if ! command -v go &>/dev/null; then
        echo "❌ Go is not installed. Install Go 1.23+ from https://go.dev/dl/"
        exit 1
    fi
    GO_VER=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go')
    GO_MAJOR=$(echo "$GO_VER" | cut -d. -f1)
    GO_MINOR=$(echo "$GO_VER" | cut -d. -f2)
    if [ "$GO_MAJOR" -lt 1 ] || { [ "$GO_MAJOR" -eq 1 ] && [ "$GO_MINOR" -lt 23 ]; }; then
        echo "❌ Go $GO_VER is too old. aix requires Go 1.23+."
        echo "   Install from https://go.dev/dl/"
        exit 1
    fi
    echo "  Go $GO_VER ✓"
fi

# --- Determine BIN_DIR ---
if [ -z "$BIN_DIR" ]; then
    if [ -d "/opt/homebrew/bin" ] && [ -w "/opt/homebrew/bin" ]; then
        BIN_DIR="/opt/homebrew/bin"
    elif [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
        BIN_DIR="/usr/local/bin"
    elif [ -d "$HOME/.local/bin" ]; then
        BIN_DIR="$HOME/.local/bin"
    else
        BIN_DIR="$HOME/.local/bin"
        mkdir -p "$BIN_DIR"
    fi
fi

# --- Writable Go build cache ---
if ! go env GOCACHE >/dev/null 2>&1 || [ ! -w "$(go env GOCACHE)" ]; then
    export GOCACHE="${TMPDIR:-/tmp}/go-cache-aix"
    mkdir -p "$GOCACHE"
fi

if [ "$CONFIG_ONLY" = "1" ]; then
    echo "Config-only mode (binary already installed)"
else
    # Build
    echo "[1/3] Building..."
    go build -o aix . || {
        echo "  ❌ go build failed. Check Go version and dependencies."
        exit 1
    }

    # Install binary
    echo "[2/3] Installing to $BIN_DIR..."
    mkdir -p "$BIN_DIR"

    rm -f "$BIN_DIR/aix.new"
    cp aix "$BIN_DIR/aix.new"
    chmod +x "$BIN_DIR/aix.new"
    mv "$BIN_DIR/aix.new" "$BIN_DIR/aix"

    if command -v codesign &>/dev/null; then
        codesign -s - -f "$BIN_DIR/aix" 2>/dev/null || true
    fi

    INSTALLED_VERSION=$("$BIN_DIR/aix" --version 2>/dev/null || true)
    INSTALLED_VERSION=${INSTALLED_VERSION#"aix - "}
    echo "  ✓ Installed ${INSTALLED_VERSION:-AIX}"

    if [ -f "$HOME/.aix/proxy.toml" ]; then
        AIX_INTERNAL_INSTALL_SERVICE=1 "$BIN_DIR/aix" || \
            echo "  ⚠ Claude gateway service refresh failed; run aix setup"
    fi

    # Check if BIN_DIR is on PATH
    if ! echo "$PATH" | tr ':' '\n' | grep -qxF "$BIN_DIR"; then
        echo "  ⚠ $BIN_DIR is not on your PATH."
        echo "     Add:  export PATH=\"$BIN_DIR:\$PATH\""
    fi
fi

# Setup
if [ "$NO_SETUP" = "1" ]; then
    echo "[3/3] Skipping setup (NO_SETUP=1)"
else
    echo "[3/3] Checking configured provider credentials..."
    echo ""
    exec "$BIN_DIR/aix" setup
fi
