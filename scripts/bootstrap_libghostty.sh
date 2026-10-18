#!/usr/bin/env bash
# Download/check the pinned Ghostty revision and install libghostty-vt plus the
# pinned xterm-ghostty terminfo into deps/libghostty-vt. Requires Zig 0.16.x
# (see deps/GHOSTTY_PIN) and tic from ncurses-bin.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/deps/GHOSTTY_PIN"

PREFIX="${ROOT}/deps/libghostty-vt"
SRC="${GHOSTTY_SRC:-${HOME}/tools/src/ghostty}"
STAMP="${PREFIX}/.crude-pin"
# Keep in sync with types.GhosttyTermName.
TERM_NAME="xterm-ghostty"
TIDB="${PREFIX}/share/terminfo"
# Compiled entry + source are embedded into the binary via go:embed (see
# types/ghostty_terminfo.go), so a copy is materialized next to the packages
# that reference it. The files are pinned build artifacts and are committed.
EMBED="${ROOT}/types/terminfo"

need_zig() {
  if command -v zig >/dev/null 2>&1; then
    local ver
    ver="$(zig version)"
    case "$ver" in
      ${ZIG_VERSION}*) ;;
      *)
        echo "error: zig ${ver} found; need ${ZIG_VERSION}" >&2
        exit 1
        ;;
    esac
  elif [ -x "${HOME}/tools/zig-0.16.0/zig" ]; then
    export PATH="${HOME}/tools/zig-0.16.0:$PATH"
  else
    echo "error: zig ${ZIG_VERSION} not on PATH. Install to ~/tools/zig-0.16.0" >&2
    echo "  curl -fL https://ziglang.org/download/${ZIG_VERSION}/zig-x86_64-linux-${ZIG_VERSION}.tar.xz | tar -xJ -C ~/tools" >&2
    echo "  mv ~/tools/zig-x86_64-linux-${ZIG_VERSION} ~/tools/zig-0.16.0" >&2
    exit 1
  fi
  # Zig's clang does not add Debian multiarch include dirs the way gcc does.
  # linux-libc-dev installs asm/errno.h under /usr/include/$(gcc -print-multiarch).
  local multiarch
  multiarch="$(gcc -print-multiarch 2>/dev/null || echo x86_64-linux-gnu)"
  export CPATH="/usr/include/${multiarch}${CPATH:+:$CPATH}"
  export C_INCLUDE_PATH="${CPATH}"
  export CPLUS_INCLUDE_PATH="${CPATH}"
  export SSL_CERT_FILE="${SSL_CERT_FILE:-/etc/ssl/certs/ca-certificates.crt}"
}

lib_ok() {
  [ -f "${STAMP}" ] && [ "$(cat "${STAMP}")" = "${GHOSTTY_COMMIT}" ] &&
    [ -f "${PREFIX}/.crude-cpu" ] && [ "$(cat "${PREFIX}/.crude-cpu")" = baseline ] &&
    [ -f "${PREFIX}/lib/libghostty-vt.a" ] &&
    [ -f "${PREFIX}/include/ghostty/vt.h" ]
}

terminfo_ok() {
  [ -f "${TIDB}/x/${TERM_NAME}" ] &&
    [ -f "${EMBED}/x/${TERM_NAME}" ] &&
    [ -f "${EMBED}/${TERM_NAME}.terminfo" ]
}

ensure_src() {
  mkdir -p "$(dirname "${SRC}")"
  if [ ! -d "${SRC}/.git" ]; then
    git clone --filter=blob:none https://github.com/ghostty-org/ghostty.git "${SRC}"
  fi
  git -C "${SRC}" fetch --depth 1 origin "${GHOSTTY_COMMIT}" 2>/dev/null || git -C "${SRC}" fetch origin "${GHOSTTY_COMMIT}"
  git -C "${SRC}" checkout --detach "${GHOSTTY_COMMIT}"
  got="$(git -C "${SRC}" rev-parse HEAD)"
  if [ "${got}" != "${GHOSTTY_COMMIT}" ]; then
    echo "error: ghostty HEAD ${got} != pin ${GHOSTTY_COMMIT}" >&2
    exit 1
  fi
}

if lib_ok && terminfo_ok; then
  echo "libghostty-vt already installed at ${PREFIX} (${GHOSTTY_COMMIT})"
  exit 0
fi

need_zig
echo "zig $(zig version)"

if ! lib_ok; then
  ensure_src

  mkdir -p "${PREFIX}"
  echo "building libghostty-vt into ${PREFIX}"
  (
    cd "${SRC}"
    zig build -Demit-lib-vt -Doptimize=ReleaseFast -Dcpu=baseline --prefix "${PREFIX}"
  )

  if [ ! -f "${PREFIX}/lib/libghostty-vt.a" ]; then
    echo "error: expected ${PREFIX}/lib/libghostty-vt.a" >&2
    ls -la "${PREFIX}" "${PREFIX}/lib" >&2 || true
    exit 1
  fi
  printf '%s\n' "${GHOSTTY_COMMIT}" > "${STAMP}"
  printf '%s\n' baseline > "${PREFIX}/.crude-cpu"
  echo "installed libghostty-vt ${GHOSTTY_VERSION} @ ${GHOSTTY_COMMIT}"
fi

if ! terminfo_ok; then
  command -v tic >/dev/null 2>&1 || {
    echo "error: tic (ncurses-bin) is required to compile the pinned terminfo" >&2
    exit 1
  }
  ensure_src

  echo "generating pinned ${TERM_NAME} terminfo from ${GHOSTTY_COMMIT}"
  # The upstream build only installs terminfo when also emitting the full
  # Ghostty executable (GhosttyResources.install). Reproduce just the terminfo
  # step here: the terminfo module (src/terminfo/ghostty.zig + Source.zig) is
  # self-contained, so a tiny glued runner emits the exact same source text
  # that `ghostty-build-data +terminfo` would at this revision.
  tmp="$(mktemp -d)"
  cp "${SRC}/src/terminfo/ghostty.zig" "${SRC}/src/terminfo/Source.zig" "${tmp}/"
  cat > "${tmp}/emit.zig" <<'ZIG'
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    var buffer: [4096]u8 = undefined;
    var stdout_writer = std.Io.File.stdout().writerStreaming(init.io, &buffer);
    try @import("ghostty.zig").ghostty.encode(&stdout_writer.interface);
    try stdout_writer.end();
}
ZIG

  mkdir -p "${TIDB}" "${EMBED}/x"
  (cd "${tmp}" && zig run emit.zig > source.terminfo)
  install -m 0644 "${tmp}/source.terminfo" "${TIDB}/${TERM_NAME}.terminfo"
  install -m 0644 "${tmp}/source.terminfo" "${EMBED}/${TERM_NAME}.terminfo"
  rm -rf "${tmp}"

  # Compile the source into a real terminfo database (ncurses binary format).
  tic -x -o "${TIDB}" "${TIDB}/${TERM_NAME}.terminfo"
  install -m 0644 "${TIDB}/x/${TERM_NAME}" "${EMBED}/x/${TERM_NAME}"

  # Prove the compiled entry resolves in isolation before declaring success.
  TERMINFO="${TIDB}" infocmp -x "${TERM_NAME}" >/dev/null
  echo "installed pinned terminfo at ${TIDB}/x/${TERM_NAME} (embed copy in types/terminfo)"
fi
