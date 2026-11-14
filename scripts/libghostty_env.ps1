# Source after bootstrap. Safe to dot-source more than once.
# Usage: . scripts/libghostty_env.ps1
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$env:CGO_ENABLED = if ($env:CGO_ENABLED) { $env:CGO_ENABLED } else { "1" }
$env:PKG_CONFIG_PATH = "$Root\third_party\libghostty-vt\share\pkgconfig;$Root\third_party\libghostty-vt\lib\pkgconfig;$env:PKG_CONFIG_PATH"
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
# go-tree-sitter declares its exported callbacks in its cgo preamble and cgo's
# generated header re-declares them with __declspec(dllexport). clang (zig cc)
# reports that as -Wdll-attribute-on-redeclaration on every build. It is
# harmless, so silence exactly that diagnostic and keep every other warning.
$quietDll = "-Wno-dll-attribute-on-redeclaration"
# Compile cgo C/C++ (the tree-sitter grammars) for the x86-64 baseline, not the
# build machine's native CPU. Unlike gcc, `zig cc` defaults to the host CPU, so
# a binary built on a newer CI runner executes an illegal instruction inside
# tree-sitter (ts_parser_new) on older user machines. This matches the pinned
# `-Dcpu=baseline` libghostty-vt library, keeping the whole binary portable.
$baselineCpu = "-mcpu=baseline"
$cflags = if ($env:CGO_CFLAGS) { $env:CGO_CFLAGS } else { "-O2 -g" }
if ($cflags -notlike "*$quietDll*") { $cflags = "$cflags $quietDll" }
if ($cflags -notlike "*$baselineCpu*") { $cflags = "$cflags $baselineCpu" }
$env:CGO_CFLAGS = $cflags
$cxxflags = if ($env:CGO_CXXFLAGS) { $env:CGO_CXXFLAGS } else { "-O2 -g" }
if ($cxxflags -notlike "*$baselineCpu*") { $cxxflags = "$cxxflags $baselineCpu" }
$env:CGO_CXXFLAGS = $cxxflags