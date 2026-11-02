# Dot-source in a separate PowerShell session after libghostty_env.ps1.
# This changes linker options for every Go command in that session.
if ($env:OS -ne "Windows_NT" -or (go env GOARCH) -ne "amd64") {
    throw "The Zig race environment requires Windows amd64."
}
if ($env:CC -ne "zig cc") {
    throw "Source scripts/libghostty_env.ps1 first."
}
if (-not $env:CRUDE_ZIG_RACE_ENV -and $env:GOFLAGS -match '(^|\s)-ldflags(?:=|\s)') {
    throw "Remove GOFLAGS -ldflags before enabling the Zig race environment."
}
$zigEnv = zig env | Out-String
if ($LASTEXITCODE -ne 0 -or $zigEnv -notmatch 'lib_dir = (.+?),') {
    throw "Cannot find the Zig library directory from zig env."
}
$zigLib = ConvertFrom-Json $Matches[1]
$definition = Join-Path $zigLib "libc\mingw\lib-common\api-ms-win-core-synch-l1-2-0.def"
if (-not (Test-Path -LiteralPath $definition -PathType Leaf)) {
    throw "Zig synchronization definition not found: $definition"
}
# Key by the input bytes and Zig version; publish only a completed archive.
$hasher = [Security.Cryptography.SHA256]::Create()
$stream = [IO.File]::OpenRead($definition)
try {
    $key = ([BitConverter]::ToString($hasher.ComputeHash($stream))).Replace("-", "")
} finally {
    $stream.Dispose()
    $hasher.Dispose()
}
$version = (zig version).Trim()
if ($LASTEXITCODE -ne 0 -or $version -notmatch '^[0-9A-Za-z.+-]+$') {
    throw "Cannot determine Zig version."
}
$cache = Join-Path $env:LOCALAPPDATA "Crude\race-zig\$version-$key"
New-Item -ItemType Directory -Force -Path $cache | Out-Null
$archive = Join-Path $cache "libsynchronization.a"
if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
    $candidate = Join-Path $cache ("libsynchronization-" + [guid]::NewGuid().ToString("N") + ".a")
    zig dlltool -d $definition -l $candidate -m i386:x86-64
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw "Zig could not create the synchronization import library."
    }
    try {
        [IO.File]::Move($candidate, $archive)
    } catch {
        if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) { throw }
    }
}
if (-not $env:CRUDE_ZIG_RACE_ENV) {
    $existing = go env CGO_LDFLAGS
    # Go's quoted-argument parser requires the quote around the whole token.
    $env:CGO_LDFLAGS = "$existing `"-L$cache`""
    # Zig supports --no-dynamicbase, but not Go's --disable-dynamicbase.
    # A low preferred image base is also needed for TSan's shadow mapping.
    # HIGH_ENTROPY_VA has no effect when DYNAMIC_BASE is disabled.
    $env:GOFLAGS = ($env:GOFLAGS + " -ldflags=-extldflags=-Wl,--no-dynamicbase,--image-base,0x400000").Trim()
    $env:CRUDE_ZIG_RACE_ENV = "1"
}
