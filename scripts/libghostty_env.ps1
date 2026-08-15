# Source after bootstrap. Safe to dot-source more than once.
# Usage: . scripts/libghostty_env.ps1
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$env:CGO_ENABLED = if ($env:CGO_ENABLED) { $env:CGO_ENABLED } else { "1" }
$env:PKG_CONFIG_PATH = "$Root\deps\libghostty-vt\share\pkgconfig;$Root\deps\libghostty-vt\lib\pkgconfig;$env:PKG_CONFIG_PATH"
$zigDir = Join-Path $HOME "tools/zig-0.16.0"
if (Test-Path (Join-Path $zigDir "zig.exe")) {
    if ($env:PATH -notlike "*$zigDir*") {
        $env:PATH = "$zigDir;$env:PATH"
    }
}
# Use zig cc as the C compiler on Windows: it produces MSVC COFF objects
# that link cleanly against ghostty-vt-static.lib while accepting gcc-style
# cgo flags. gcc/MinGW produces ELF-incompatible objects (chkstk mismatch);
# plain clang targets the MSVC ABI but rejects cgo's gcc flags (-mthreads).
$env:CC = "zig cc"
$env:CXX = "zig c++"