# bootstrap_libghostty.ps1
# Download/check the pinned Ghostty revision and install libghostty-vt into
# third_party/libghostty-vt. Requires Zig 0.16.x (see third_party/libghostty-vt/GHOSTTY_PIN).
#
# Windows counterpart to scripts/bootstrap_libghostty.sh. The terminfo step
# is skipped: the compiled xterm-ghostty entry is already committed under
# types/terminfo/ (embedded via go:embed), so Windows users don't need tic.
#
# Usage:
#   pwsh scripts/bootstrap_libghostty.ps1
# Or from the project root:
#   .\scripts\bootstrap_libghostty.ps1

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
. (Join-Path $Root "third_party/libghostty-vt/GHOSTTY_PIN.ps1")

$Prefix = Join-Path $Root "third_party/libghostty-vt"
$Src = if ($env:GHOSTTY_SRC) { $env:GHOSTTY_SRC } else { Join-Path $HOME "tools/src/ghostty" }
$Stamp = Join-Path $Prefix ".crude-pin"

function Test-Zig {
    $zigExe = Get-Command zig -ErrorAction SilentlyContinue
    if (-not $zigExe) {
        $local = Join-Path $HOME "tools/zig-0.16.0/zig.exe"
        if (Test-Path $local) {
            $env:PATH = "$(Split-Path $local);$env:PATH"
            $zigExe = Get-Command zig -ErrorAction SilentlyContinue
        }
    }
    if (-not $zigExe) {
        Write-Error "zig $ZIG_VERSION not found. Install to ~/tools/zig-0.16.0`n  Download from https://ziglang.org/download/$ZIG_VERSION/zig-x86_64-windows-$ZIG_VERSION.zip"
        exit 1
    }
    $ver = & zig version
    if ($LASTEXITCODE -ne 0) { throw 'zig version failed' }
    if ($ver -notlike "$ZIG_VERSION*") {
        Write-Error "zig $ver found; need $ZIG_VERSION"
        exit 1
    }
    Write-Host "zig $ver"
}

function Test-LibOk {
    (Test-Path $Stamp) -and
    ((Get-Content $Stamp -ErrorAction SilentlyContinue) -eq $GHOSTTY_COMMIT) -and
    ((Get-Content (Join-Path $Prefix '.crude-cpu') -ErrorAction SilentlyContinue) -eq 'baseline') -and
    (Test-Path (Join-Path $Prefix "lib/ghostty-vt-static.lib")) -and
    (Test-Path (Join-Path $Prefix "include/ghostty/vt.h"))
}

function Ensure-Src {
    $srcParent = Split-Path $Src
    if (-not (Test-Path $srcParent)) {
        New-Item -ItemType Directory -Force -Path $srcParent | Out-Null
    }
    if (-not (Test-Path (Join-Path $Src ".git"))) {
        git clone --filter=blob:none https://github.com/ghostty-org/ghostty.git $Src
        if ($LASTEXITCODE -ne 0) { throw 'Ghostty clone failed' }
    }
    git -C $Src fetch --depth 1 origin $GHOSTTY_COMMIT 2>$null
    if ($LASTEXITCODE -ne 0) {
        git -C $Src fetch origin $GHOSTTY_COMMIT
        if ($LASTEXITCODE -ne 0) { throw 'Ghostty fetch failed' }
    }
    git -C $Src checkout --detach $GHOSTTY_COMMIT
    if ($LASTEXITCODE -ne 0) { throw 'Ghostty checkout failed' }
    $got = git -C $Src rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Ghostty revision check failed' }
    if ($got -ne $GHOSTTY_COMMIT) {
        Write-Error "ghostty HEAD $got != pin $GHOSTTY_COMMIT"
        exit 1
    }
}

if (Test-LibOk) {
    Write-Host "libghostty-vt already installed at $Prefix ($GHOSTTY_COMMIT)"
    exit 0
}

Test-Zig

if (-not (Test-LibOk)) {
    Ensure-Src

    if (-not (Test-Path $Prefix)) {
        New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
    }
    Write-Host "building libghostty-vt into $Prefix"
    Push-Location $Src
    try {
        zig build -Demit-lib-vt -Doptimize=ReleaseFast -Dcpu=baseline --prefix $Prefix
        if ($LASTEXITCODE -ne 0) { throw 'Ghostty native build failed' }
    } finally { Pop-Location }

    if (-not (Test-Path (Join-Path $Prefix "lib/ghostty-vt-static.lib"))) {
        Write-Error "expected $Prefix/lib/ghostty-vt-static.lib"
        Get-ChildItem $Prefix, (Join-Path $Prefix "lib") -ErrorAction SilentlyContinue | Format-Table
        exit 1
    }
    Set-Content -Path $Stamp -Value $GHOSTTY_COMMIT
    Set-Content -Path (Join-Path $Prefix '.crude-cpu') -Value 'baseline'
    Write-Host "installed libghostty-vt $GHOSTTY_VERSION @ $GHOSTTY_COMMIT"
}
