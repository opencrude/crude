#!/usr/bin/env bash
# Crude installer script
# Supports: Linux, macOS, Windows (Git Bash / MSYS2 / Cygwin)
# Usage: ./install.sh [stable|beta]

set -euo pipefail

CHANNEL="${1:-stable}"

if [[ "$CHANNEL" != "stable" && "$CHANNEL" != "beta" ]]; then
    echo "Error: unknown channel '$CHANNEL'. Use 'stable' or 'beta'." >&2
    exit 1
fi

# Determine binary name and build tags
if [[ "$CHANNEL" == "beta" ]]; then
    BINARY_NAME="crude-beta"
    BUILD_TAGS="-tags beta"
else
    BINARY_NAME="crude"
    BUILD_TAGS=""
fi

# Get the correct binary extension for the platform
GOEXE="$(go env GOEXE)"
BINARY_NAME="${BINARY_NAME}${GOEXE}"

echo "=== Crude Installer ==="
echo "Channel : $CHANNEL"
echo ""

# ---- Determine install directory ----

detect_install_dir() {
    local uname_s
    uname_s="$(uname -s 2>/dev/null || echo unknown)"

    # Windows (Git Bash / MSYS2 / Cygwin)
    if [[ "$uname_s" == MINGW* || "$uname_s" == MSYS* || "$uname_s" == CYGWIN* ]]; then
        local win_dir="${LOCALAPPDATA:-$USERPROFILE/AppData/Local}/crude/bin"
        mkdir -p "$win_dir" 2>/dev/null || true
        echo "$win_dir"
        return
    fi

    # Linux / macOS: try system-wide first
    if [[ -w /usr/local/bin ]] || [[ -w / ]] 2>/dev/null; then
        echo "/usr/local/bin"
        return
    fi

    # Fallback to user-local
    local fallback="$HOME/.local/bin"
    mkdir -p "$fallback" 2>/dev/null || true
    echo "$fallback"
}

INSTALL_DIR="$(detect_install_dir)"

echo "Install dir: $INSTALL_DIR"

# ---- Build ----

echo "Building $BINARY_NAME ..."
eval go build $BUILD_TAGS -o "$BINARY_NAME" .
echo "Build succeeded."

# ---- Install ----

DEST="$INSTALL_DIR/$BINARY_NAME"

echo "Installing to $DEST ..."
cp "$BINARY_NAME" "$DEST"
chmod +x "$DEST" 2>/dev/null || true

# ---- Clean up built binary ----

rm -f "$BINARY_NAME"

# ---- PATH check (Windows / Git Bash) ----

uname_s="$(uname -s 2>/dev/null || echo unknown)"
if [[ "$uname_s" == MINGW* || "$uname_s" == MSYS* || "$uname_s" == CYGWIN* ]]; then
    # Normalize INSTALL_DIR for comparison
    norm_dir="$(echo "$INSTALL_DIR" | tr '\\' '/' | tr '[:upper:]' '[:lower:]')"
    norm_path="$(echo "$PATH" | tr '[:upper:]' '[:lower:]')"
    if [[ ":$norm_path:" != *":$norm_dir:"* ]]; then
        echo ""
        echo "WARNING: $INSTALL_DIR is not in your PATH."
        echo "Add it by running:"
        echo "  setx PATH \"%PATH%;$(cygpath -w "$INSTALL_DIR" 2>/dev/null || echo "$INSTALL_DIR")\""
    fi
fi

echo ""
echo "=== Installed successfully ==="
echo "  $DEST"
echo ""
echo "Run 'crude' (or 'crude-beta') to get started."
