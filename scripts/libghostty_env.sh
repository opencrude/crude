# Source from Makefile/CI after bootstrap. Safe to source more than once.
# Usage: . scripts/libghostty_env.sh
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export CGO_ENABLED="${CGO_ENABLED:-1}"
export PKG_CONFIG_PATH="${ROOT}/deps/libghostty-vt/share/pkgconfig:${ROOT}/deps/libghostty-vt/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
if [ -x "${HOME}/tools/zig-0.16.0/zig" ]; then
  case ":$PATH:" in
    *":${HOME}/tools/zig-0.16.0:"*) ;;
    *) export PATH="${HOME}/tools/zig-0.16.0:$PATH" ;;
  esac
fi
