#requires -Version 5.1
<#
.SYNOPSIS
Offline release-installer fixtures. Run with Windows PowerShell 5.1 or pwsh.
.EXAMPLE
.\scripts\test_install.ps1 -ReleaseDir dist -ReleaseVersion v1.2.3
.NOTES
Uses real ZIP/checksum/file transactions and mocks only HTTP, user PATH storage,
and (in portable fixtures) native version execution/platform detection. No test
parameters or security bypasses exist in the production installer. Native Windows
checks additionally compile and execute a small --version fixture and exercise a
locked target; they do not need Go, Zig, administrator rights, or Pester.
#>
[CmdletBinding()]
param(
    [string] $ReleaseDir,
    [string] $ReleaseVersion
)

$ErrorActionPreference = 'Stop'
$installer = Join-Path (Split-Path $PSScriptRoot -Parent) 'install.ps1'
$nativeWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
if ([bool] $ReleaseDir -ne [bool] $ReleaseVersion) { throw 'Real-release smoke acceptance needs both -ReleaseDir and -ReleaseVersion.' }
if ($ReleaseDir -and -not $nativeWindows) { throw 'Real Windows release acceptance must run natively on Windows, not on Linux or WSL.' }
$root = Join-Path ([IO.Path]::GetTempPath()) ('crude-installer-tests-' + [Guid]::NewGuid().ToString('N'))
[void] [IO.Directory]::CreateDirectory($root)
$originalProcessPath = $env:Path
$originalLocalAppData = $env:LOCALAPPDATA
$script:passed = 0
$script:failed = 0

function Assert-Equal($Actual, $Expected, [string] $Message) {
    if ($Actual -cne $Expected) { throw "$Message (expected '$Expected', got '$Actual')" }
}

function Assert-True($Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-Fails([scriptblock] $Action, [string] $Pattern) {
    try { & $Action } catch {
        if ($_.Exception.Message -notmatch $Pattern) { throw "Wrong failure (expected '$Pattern'): $($_.Exception.Message)" }
        return
    }
    throw "Expected failure matching '$Pattern', but the operation succeeded."
}

function Test-Case([string] $Name, [scriptblock] $Action) {
    try {
        & $Action
        $script:passed++
        Write-Host "PASS $Name"
    } catch {
        $script:failed++
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
        Write-Host $_.ScriptStackTrace
    }
}

function New-TestZip([string] $Path, [object[]] $Entries) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::Open($Path, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($item in $Entries) {
            $entry = $zip.CreateEntry($item.Name)
            if ($item.Mode) { $entry.ExternalAttributes = [int] $item.Mode }
            if ($item.Name.EndsWith('/')) { continue }
            $stream = $entry.Open()
            try {
                $bytes = [Text.Encoding]::UTF8.GetBytes($item.Value)
                $stream.Write($bytes, 0, $bytes.Length)
            } finally { $stream.Dispose() }
        }
    } finally { $zip.Dispose() }
}

function New-Fixture([string] $Tag = 'v1.2.3', [string] $Repository = 'uoosef403/crude', [object[]] $ExtraEntries = @()) {
    $directory = Join-Path $root ([Guid]::NewGuid().ToString('N'))
    [void] [IO.Directory]::CreateDirectory($directory)
    $asset = "crude_${Tag}_windows_amd64.zip"
    $entries = @(
        @{ Name = 'crude.exe'; Value = "fixture executable $Tag" },
        @{ Name = 'LICENSE-MIT'; Value = "MIT license $Tag" },
        @{ Name = 'LICENSE-APACHE'; Value = "Apache license $Tag" },
        @{ Name = 'README.md'; Value = "Readme $Tag" },
        @{ Name = 'licenses/'; Value = '' },
        @{ Name = 'licenses/third-party/NOTICE.txt'; Value = "third-party notice $Tag" }
    ) + $ExtraEntries
    $archive = Join-Path $directory $asset
    New-TestZip $archive $entries
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $directory 'SHA256SUMS'), "$hash  $asset`n")
    $baseUrl = "https://github.com/$Repository/releases/download/$Tag"
    $metadata = @{
        tag_name = $Tag; draft = $false; prerelease = $Tag.Contains('-')
        assets = @(
            @{ name = $asset; browser_download_url = "$baseUrl/$asset" },
            @{ name = 'SHA256SUMS'; browser_download_url = "$baseUrl/SHA256SUMS" }
        )
    }
    [IO.File]::WriteAllText((Join-Path $directory 'release.json'), ($metadata | ConvertTo-Json -Depth 5))
    return [pscustomobject] @{
        Tag = $Tag; Repository = $Repository; Directory = $directory; Asset = $asset
        Archive = $archive; Metadata = $metadata
        VersionOutput = "crude $Tag (0123456789abcdef0123456789abcdef01234567; 2026-10-18T12:00:00Z; windows/amd64)`n"
    }
}

function Save-FixtureMetadata($Fixture) {
    [IO.File]::WriteAllText((Join-Path $Fixture.Directory 'release.json'), ($Fixture.Metadata | ConvertTo-Json -Depth 5))
}

# Each fixture gets a new scope so production functions are restored between
# cases. All mocked storage lives under $root or in this process's variables.
function With-Fixture($Fixture, [scriptblock] $Action) {
    . $installer
    $fixtureState = [pscustomobject] @{
        Fixture = $Fixture; Calls = New-Object 'System.Collections.Generic.List[string]'
        Attempts = 0; DownloadsFail = $false; TransientFailures = 0; ProbeCalls = 0
        UserPath = 'C:\Existing User Tools;%USERPROFILE%\custom'; UserWrites = 0
        PathWriteFailure = $false; FailAfterPathWrite = $false
    }
    $install = Join-Path $Fixture.Directory 'install with spaces'
    $savedPath = $env:Path
    $savedLocalAppData = $env:LOCALAPPDATA
    $env:Path = 'C:\Windows\system32;C:\Machine Tools;C:\Existing User Tools'
    $env:LOCALAPPDATA = Join-Path $Fixture.Directory 'profile'
    function Assert-CrudePlatform { }
    function Get-CrudeSetupStatus { return [pscustomobject] @{ WindowsTerminal = $false; FontPresent = $false; FontsKnown = $false } }
    function Invoke-CrudeDownloadAttempt([Uri] $Uri, [string] $Destination, [long] $MaximumBytes) {
        $fixtureState.Attempts++
        $url = $Uri.AbsoluteUri
        # Uri escapes spaces only in local fixture paths, never in public URLs.
        $fixtureState.Calls.Add($url)
        if ($fixtureState.DownloadsFail -or $fixtureState.TransientFailures -gt 0) {
            $fixtureState.TransientFailures--
            [IO.File]::WriteAllText($Destination, 'incomplete response')
            throw 'fixture network failure'
        }
        $fixture = $fixtureState.Fixture
        $api = "https://api.github.com/repos/$($fixture.Repository)/releases/"
        $download = "https://github.com/$($fixture.Repository)/releases/download/$($fixture.Tag)/"
        if ($url -ceq ($api + 'latest') -or $url -ceq ($api + 'tags/' + $fixture.Tag)) { $name = 'release.json' }
        elseif ($url -ceq ($download + 'SHA256SUMS')) { $name = 'SHA256SUMS' }
        elseif ($url -ceq ($download + $fixture.Asset)) { $name = $fixture.Asset }
        else { throw "Unexpected fixture URL: $url" }
        $source = Join-Path $fixture.Directory $name
        if ((Get-Item -LiteralPath $source).Length -gt $MaximumBytes) { throw 'fixture response exceeds byte limit' }
        [IO.File]::Copy($source, $Destination, $true)
    }
    function Start-Sleep { param($Seconds) }
    function Get-CrudeCandidateVersion([string] $Executable) {
        $fixtureState.ProbeCalls++
        Assert-Equal ([IO.File]::ReadAllText($Executable)) "fixture executable $($fixtureState.Fixture.Tag)" 'The verified candidate must be the extracted archive executable'
        return $fixtureState.Fixture.VersionOutput
    }
    function Get-CrudeUserPath { return $fixtureState.UserPath }
    function Get-CrudeUserPathKind { return [Microsoft.Win32.RegistryValueKind]::ExpandString }
    function Set-CrudeUserPath([AllowNull()] [string] $Value, [Microsoft.Win32.RegistryValueKind] $Kind) {
        Assert-Equal $Kind ([Microsoft.Win32.RegistryValueKind]::ExpandString) 'User PATH update must preserve its original expandable registry value kind'
        $fixtureState.UserWrites++
        if ($fixtureState.PathWriteFailure) { throw 'fixture user PATH access denied' }
        $fixtureState.UserPath = $Value
        if ($fixtureState.FailAfterPathWrite) { $fixtureState.FailAfterPathWrite = $false; throw 'fixture error after user PATH write' }
    }
    try { . $Action } finally {
        $env:Path = $savedPath
        $env:LOCALAPPDATA = $savedLocalAppData
    }
}

function Assert-NoStaging([string] $InstallDir) {
    Assert-Equal @([IO.Directory]::GetDirectories($InstallDir, '.crude-install-*')).Count 0 'Installer staging directories must be removed'
}

function Test-RealRelease([string] $Directory, [string] $Tag) {
    . $installer
    Assert-CrudeVersion $Tag
    $directory = (Resolve-Path -LiteralPath $Directory).Path
    $fixture = New-Fixture -Tag $Tag
    [IO.File]::Copy((Join-Path $directory $fixture.Asset), $fixture.Archive, $true)
    [IO.File]::Copy((Join-Path $directory 'SHA256SUMS'), (Join-Path $fixture.Directory 'SHA256SUMS'), $true)
    $install = Join-Path $fixture.Directory 'real release install'
    # The only substituted production boundary in this smoke check is transport:
    # actual platform detection, package checks, native EXE probe, and filesystem
    # installation run unchanged. No user or machine environment is persisted.
    function Invoke-CrudeDownloadAttempt([Uri] $Uri, [string] $Destination, [long] $MaximumBytes) {
        $url = $Uri.AbsoluteUri
        switch -CaseSensitive ($url) {
            "https://api.github.com/repos/uoosef403/crude/releases/tags/$Tag" { $name = 'release.json' }
            "https://github.com/uoosef403/crude/releases/download/$Tag/SHA256SUMS" { $name = 'SHA256SUMS' }
            "https://github.com/uoosef403/crude/releases/download/$Tag/$($fixture.Asset)" { $name = $fixture.Asset }
            default { throw "Unexpected real-release fixture request: $url" }
        }
        $source = Join-Path $fixture.Directory $name
        Assert-True ((Get-Item -LiteralPath $source).Length -le $MaximumBytes) 'Release fixture must respect download limits'
        [IO.File]::Copy($source, $Destination, $true)
    }
    $oldUser = Get-CrudeUserPath
    $oldMachine = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::Machine)
    $oldProcess = $env:Path
    $oldCrudeHome = $env:CRUDE_HOME
    $env:CRUDE_HOME = Join-Path $fixture.Directory 'isolated profile'
    [void] [IO.Directory]::CreateDirectory((Join-Path $env:CRUDE_HOME '.crude'))
    $marker = Join-Path $env:CRUDE_HOME '.crude/preserved.txt'
    [IO.File]::WriteAllText($marker, 'application data must survive install/update')
    try {
        Install-Crude -AcceptBetaRisk -Version $Tag -InstallDir $install -NoPath
        $binary = Join-Path $install 'crude.exe'
        $version = Get-CrudeCandidateVersion $binary
        Assert-True ($version -cmatch ('\Acrude ' + [Regex]::Escape($Tag) + ' \([a-f0-9]{40}; [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z; windows/amd64\)\r?\n?\z')) 'Installed real release must report the requested version and native platform'
        foreach ($required in @('LICENSE-APACHE', 'LICENSE-MIT', 'README.md')) {
            Assert-True ([IO.File]::Exists((Join-Path $install $required))) "Real release must install '$required'"
        }
        Assert-True ([IO.Directory]::GetFiles((Join-Path $install 'licenses'), '*', [IO.SearchOption]::AllDirectories).Length -gt 0) 'Real release must install third-party notices'
        $installedHash = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash
        $sentinelTime = [DateTime]::UtcNow.AddDays(-2)
        [IO.File]::SetLastWriteTimeUtc($binary, $sentinelTime)
        Install-Crude -AcceptBetaRisk -Version $Tag -InstallDir $install -NoPath
        Assert-Equal (Get-Item -LiteralPath $binary).LastWriteTimeUtc $sentinelTime 'Identical real release must not replace installed executable'
        Assert-Equal (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash $installedHash 'Repeat installation must preserve verified executable bytes'
        Assert-Equal (Get-CrudeUserPath) $oldUser 'Real-release NoPath must not persist user PATH'
        Assert-Equal ([Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::Machine)) $oldMachine 'Real-release installation must not change machine PATH'
        Assert-Equal $env:Path $oldProcess 'Real-release NoPath must not change process PATH'
        Assert-Equal ([IO.File]::ReadAllText($marker)) 'application data must survive install/update' 'Real-release installation must not touch Crude application data'
        Assert-NoStaging $install
    } finally { $env:CRUDE_HOME = $oldCrudeHome }
}

try {
    Test-Case 'public installer parameters and thin batch wrapper' {
        $parseErrors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile($installer, [ref] $null, [ref] $parseErrors)
        Assert-Equal @($parseErrors).Count 0 'PowerShell source must parse'
        $names = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
        Assert-Equal ($names -join ',') 'Version,Repository,InstallDir,NoPath,AcceptBetaRisk' 'No HTTP or checksum bypass parameters are exposed'
        $batch = [IO.File]::ReadAllText((Join-Path (Split-Path $installer -Parent) 'install.bat'))
        Assert-True ($batch -match 'powershell\.exe.+-File "%~dp0install\.ps1" %\*') 'Batch must forward arguments to the adjacent installer'
        Assert-True ($batch -notmatch 'go build|setx|crude-beta') 'Batch must not build source or rewrite PATH'
    }

    Test-Case 'noninteractive installation and update require explicit beta-risk consent' {
        With-Fixture (New-Fixture) {
            function Test-CrudeInteractive { return $false }
            function Read-Host { throw 'Noninteractive installation must never prompt' }
            Assert-Fails { Install-Crude -InstallDir $install -NoPath } 'consent is required.+-AcceptBetaRisk.+noninteractive'
            Assert-True (-not [IO.Directory]::Exists($install)) 'Missing consent must not create installation state'
            Assert-Equal $fixtureState.Attempts 0 'Missing consent must not download'
            Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
            $fixtureState.Fixture = New-Fixture -Tag v2.3.4
            $attempts = $fixtureState.Attempts
            Assert-Fails { Install-Crude -InstallDir $install -NoPath } '-AcceptBetaRisk'
            Assert-Equal $fixtureState.Attempts $attempts 'Update without consent must not download'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'Update without consent must preserve installed build'
        }
    }

    Test-Case 'interactive consent accepts exactly lowercase yes and declines everything else' {
        With-Fixture (New-Fixture) {
            function Test-CrudeInteractive { return $true }
            $answer = 'no'
            function Read-Host { return $answer }
            foreach ($answer in @('no', 'YES', 'y', 'yes ', '')) {
                Assert-Fails { Install-Crude -InstallDir $install -NoPath } 'cancelled.+not accepted'
            }
            Assert-Equal $fixtureState.Attempts 0 'Rejected consent must not download'
            $answer = 'yes'
            Install-Crude -InstallDir $install -NoPath
            Assert-True ([IO.File]::Exists((Join-Path $install 'crude.exe'))) 'Exact yes must permit interactive install'
        }
    }

    Test-Case 'preferred setup warnings are actionable, non-mutating, and license identity is Apache' {
        With-Fixture (New-Fixture) {
            $output = (Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath *>&1 | Out-String)
            foreach ($required in @('beta-testing', 'lose or corrupt work', 'Back up important data and commit', 'cannot guarantee', 'Windows Terminal', '24-bit truecolor', 'JetBrainsMono Nerd Font NF \(not NFM\)', 'DEVCOM.JetBrainsMonoNerdFont --exact', 'Without admin rights', 'Appearance > Font face', 'ask Crude itself', 'Apache-2.0 \(LICENSE-APACHE\)')) {
                Assert-True ($output -match $required) "Installer output must explain '$required'"
            }
            Assert-Equal $fixtureState.UserWrites 0 'Setup advice with NoPath must not mutate user settings'
        }
    }

    Test-Case 'redirected real script without consent fails before HTTP on Windows' {
        if ($nativeWindows) {
            $shell = (Get-Process -Id $PID).Path
            $destination = Join-Path $root 'no-consent-entry'
            # Windows PowerShell turns native stderr into ErrorRecords. Capture
            # it without letting the intentional nonzero child exit abort tests.
            $savedPreference = $ErrorActionPreference
            try {
                $ErrorActionPreference = 'Continue'
                $output = & $shell -NoLogo -NoProfile -NonInteractive -File $installer -InstallDir $destination 2>&1 | Out-String
            } finally { $ErrorActionPreference = $savedPreference }
            Assert-True ($LASTEXITCODE -ne 0) 'Unattended script must exit nonzero without consent'
            Assert-True ($output -match '-AcceptBetaRisk') 'Entry point must explain explicit consent option'
            Assert-True (-not [IO.Directory]::Exists($destination)) 'Unattended entry must not create state'
        } else {
            . $installer
            Assert-True (-not (Test-CrudeInteractive)) 'Redirected portable test process must not be considered interactive'
        }
    }

    Test-Case 'stable latest install, licenses, user/process PATH, and same-version idempotence' {
        With-Fixture (New-Fixture) {
            $oldUser = $fixtureState.UserPath
            $oldProcess = $env:Path
            Install-Crude -AcceptBetaRisk -InstallDir $install
            Assert-Equal $fixtureState.Calls[0] 'https://api.github.com/repos/uoosef403/crude/releases/latest' 'Stable must resolve GitHub latest'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'The release executable must be installed'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'licenses/third-party/NOTICE.txt'))) 'third-party notice v1.2.3' 'Third-party notices must be installed'
            Assert-Equal $fixtureState.UserPath ($oldUser + ';' + $install) 'User PATH must preserve its original value'
            Assert-Equal $env:Path ($oldProcess + ';' + $install) 'Process PATH must preserve machine and user entries'
            Assert-Equal $fixtureState.UserWrites 1 'User PATH must be persisted once'
            $binary = Get-Item -LiteralPath (Join-Path $install 'crude.exe')
            $sentinelTime = [DateTime]::UtcNow.AddDays(-3)
            $binary.LastWriteTimeUtc = $sentinelTime
            [IO.File]::WriteAllText((Join-Path $install 'unrelated.txt'), 'keep me')
            Install-Crude -AcceptBetaRisk -InstallDir $install
            Assert-Equal (Get-Item -LiteralPath $binary.FullName).LastWriteTimeUtc $sentinelTime 'Identical executable must not be replaced'
            Assert-Equal $fixtureState.UserWrites 1 'Repeated install must not duplicate or rewrite PATH'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'unrelated.txt'))) 'keep me' 'Unrelated files must survive'
            Assert-NoStaging $install
        }
    }

    Test-Case 'default per-user install directory and NoPath' {
        With-Fixture (New-Fixture) {
            $oldUser = $fixtureState.UserPath
            $oldProcess = $env:Path
            Install-Crude -AcceptBetaRisk -NoPath
            $default = Join-Path $env:LOCALAPPDATA 'Crude/bin'
            Assert-True ([IO.File]::Exists((Join-Path $default 'crude.exe'))) 'Default must be LOCALAPPDATA/Crude/bin'
            Assert-Equal $fixtureState.UserPath $oldUser 'NoPath must preserve user PATH'
            Assert-Equal $env:Path $oldProcess 'NoPath must preserve current process PATH'
            Assert-Equal $fixtureState.UserWrites 0 'NoPath must not call user environment storage'
        }
    }

    Test-Case 'repeat script updates to a newly published stable version' {
        With-Fixture (New-Fixture) {
            Install-Crude -AcceptBetaRisk -InstallDir $install
            $fixtureState.Fixture = New-Fixture -Tag v2.3.4
            Install-Crude -AcceptBetaRisk -InstallDir $install
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v2.3.4' 'Repeat install must update the executable'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'LICENSE-MIT'))) 'MIT license v2.3.4' 'Update must replace notices too'
            Assert-Equal $fixtureState.UserWrites 1 'Update must preserve idempotent user PATH'
            Assert-NoStaging $install
        }
    }

    Test-Case 'explicit prerelease version and repository override' {
        With-Fixture (New-Fixture -Tag v1.2.3-rc.1 -Repository 'example/fork') {
            Install-Crude -AcceptBetaRisk -Version v1.2.3-rc.1 -Repository example/fork -InstallDir $install -NoPath
            Assert-Equal $fixtureState.Calls[0] 'https://api.github.com/repos/example/fork/releases/tags/v1.2.3-rc.1' 'Pinned release must resolve its exact tag'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3-rc.1' 'Explicit prerelease must install'
        }
    }

    Test-Case 'strict SemVer and repository validation happens before downloads' {
        With-Fixture (New-Fixture) {
            foreach ($bad in @('1.2.3', 'v01.2.3', 'v1.2', 'v1.2.3+build', 'v1.2.3-01', 'v1.2.3-rc..1', '../tag')) {
                Assert-Fails { Install-Crude -AcceptBetaRisk -Version $bad -InstallDir $install -NoPath } 'Invalid version'
            }
            foreach ($bad in @('https://github.com/owner/repo', 'owner/repo/path', '../repo', 'owner/repo?x=1')) {
                Assert-Fails { Install-Crude -AcceptBetaRisk -Repository $bad -InstallDir $install -NoPath } 'Repository must'
            }
            Assert-Equal $fixtureState.Attempts 0 'Invalid arguments must not reach HTTP'
            Assert-True (-not [IO.Directory]::Exists($install)) 'Invalid arguments must not create installation state'
        }
    }

    Test-Case 'latest prerelease, draft, and tag mismatches fail closed' {
        With-Fixture (New-Fixture -Tag v1.2.3-rc.1) {
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'must be stable'
            $fixtureState.Fixture.Metadata.draft = $true
            Save-FixtureMetadata $fixtureState.Fixture
            Assert-Fails { Install-Crude -AcceptBetaRisk -Version v1.2.3-rc.1 -InstallDir $install -NoPath } 'stable and published'
            $fixtureState.Fixture.Metadata.draft = $false
            $fixtureState.Fixture.Metadata.tag_name = 'v9.9.9'
            Save-FixtureMetadata $fixtureState.Fixture
            Assert-Fails { Install-Crude -AcceptBetaRisk -Version v1.2.3-rc.1 -InstallDir $install -NoPath } 'not the requested'
            Assert-True (-not [IO.File]::Exists((Join-Path $install 'crude.exe'))) 'Rejected releases must not install'
        }
    }

    Test-Case 'missing, duplicated, and redirected release assets fail before candidate execution' {
        foreach ($mutation in @('missing-binary', 'missing-checksum', 'duplicate', 'wrong-url')) {
            With-Fixture (New-Fixture) {
                $fixture = $fixtureState.Fixture
                switch ($mutation) {
                    'missing-binary' { $fixture.Metadata.assets = @($fixture.Metadata.assets[1]) }
                    'missing-checksum' { $fixture.Metadata.assets = @($fixture.Metadata.assets[0]) }
                    'duplicate' { $fixture.Metadata.assets += $fixture.Metadata.assets[0] }
                    'wrong-url' { $fixture.Metadata.assets[0].browser_download_url = 'http://example.invalid/payload.zip' }
                }
                Save-FixtureMetadata $fixture
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'exactly one|unexpected download URL'
                Assert-Equal $fixtureState.ProbeCalls 0 'Invalid assets must not execute'
                Assert-Equal $fixtureState.Attempts 1 'Invalid assets must not download payloads'
            }
        }
    }

    Test-Case 'checksum mismatch, duplicate record, and missing record preserve old installation' {
        foreach ($mutation in @('mismatch', 'duplicate', 'missing', 'malformed')) {
            With-Fixture (New-Fixture) {
                Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
                $fixtureState.Fixture = New-Fixture -Tag v2.3.4
                $fixture = $fixtureState.Fixture
                $sums = Join-Path $fixture.Directory 'SHA256SUMS'
                switch ($mutation) {
                    'mismatch' { [IO.File]::AppendAllText($fixture.Archive, 'modified after checksum') }
                    'duplicate' { [IO.File]::AppendAllText($sums, [IO.File]::ReadAllText($sums)) }
                    'missing' { [IO.File]::WriteAllText($sums, (('0' * 64) + '  another.zip')) }
                    'malformed' { [IO.File]::WriteAllText($sums, 'not a SHA-256 checksum') }
                }
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'checksum mismatch|exactly one checksum|invalid checksum record'
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'Checksum failure must keep old executable'
                Assert-Equal $fixtureState.ProbeCalls 1 'Checksum failure must not execute new candidate'
                Assert-NoStaging $install
            }
        }
    }

    Test-Case 'archive traversal, absolute paths, ADS, devices, duplicate paths, and links are rejected' {
        $entries = @(
            @{ Name = '../escaped.txt'; Value = 'bad' },
            @{ Name = 'licenses/../../escaped.txt'; Value = 'bad' },
            @{ Name = '..\escaped.txt'; Value = 'bad' },
            @{ Name = '/escaped.txt'; Value = 'bad' },
            @{ Name = 'C:\escaped.txt'; Value = 'bad' },
            @{ Name = 'licenses/NOTICE:stream'; Value = 'bad' },
            @{ Name = 'licenses/NUL.txt'; Value = 'bad' },
            @{ Name = 'licenses/trailing. '; Value = 'bad' },
            @{ Name = 'CRUDE.EXE'; Value = 'bad' },
            @{ Name = 'licenses/link'; Value = '../crude.exe'; Mode = -1577123840 }
        )
        foreach ($entry in $entries) {
            With-Fixture (New-Fixture -ExtraEntries @($entry)) {
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'Unsafe archive path|Duplicate archive path|links or special files'
                Assert-Equal $fixtureState.ProbeCalls 0 'Unsafe archive must not execute'
                Assert-True (-not [IO.File]::Exists((Join-Path $install 'crude.exe'))) 'Unsafe archive must not install'
                Assert-True (-not [IO.File]::Exists((Join-Path $fixtureState.Fixture.Directory 'escaped.txt'))) 'Traversal must not escape extraction'
                Assert-NoStaging $install
            }
        }
    }

    Test-Case 'missing root executable and third-party notices are rejected' {
        foreach ($missing in @('crude.exe', 'licenses/third-party/NOTICE.txt')) {
            With-Fixture (New-Fixture) {
                $fixture = $fixtureState.Fixture
                $zip = [IO.Compression.ZipFile]::Open($fixture.Archive, [IO.Compression.ZipArchiveMode]::Update)
                try { $zip.GetEntry($missing).Delete() } finally { $zip.Dispose() }
                $hash = (Get-FileHash -LiteralPath $fixture.Archive -Algorithm SHA256).Hash
                [IO.File]::WriteAllText((Join-Path $fixture.Directory 'SHA256SUMS'), "$hash *$($fixture.Asset)`n")
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'missing root|missing third-party'
                Assert-Equal $fixtureState.ProbeCalls 0 'Incomplete archive must not execute'
            }
        }
    }

    Test-Case 'candidate version, build identity, date, and platform are verified before mutation' {
        $outputs = @(
            "crude v9.9.9 (0123456789abcdef0123456789abcdef01234567; 2026-10-18T12:00:00Z; windows/amd64)`n",
            "crude v1.2.3 (unknown; unknown; windows/amd64)`n",
            "crude v1.2.3 (0123456789abcdef0123456789abcdef01234567; 2026-10-18T12:00:00Z; windows/arm64)`n",
            "crude v1.2.3 (0123456789abcdef0123456789abcdef01234567; 2026-99-18T12:00:00Z; windows/amd64)`n",
            "crude v1.2.3 (0123456789abcdef0123456789abcdef01234567; 2026-10-18T12:00:00Z; windows/amd64)`nextra"
        )
        foreach ($output in $outputs) {
            With-Fixture (New-Fixture) {
                [void] [IO.Directory]::CreateDirectory($install)
                [IO.File]::WriteAllText((Join-Path $install 'crude.exe'), 'original executable')
                $fixtureState.Fixture.VersionOutput = $output
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install } 'does not identify|invalid release date'
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'original executable' 'Candidate failure must leave executable untouched'
                Assert-Equal $fixtureState.UserWrites 0 'Candidate failure must not change PATH'
                Assert-NoStaging $install
            }
        }
    }

    Test-Case 'bounded retries clean partial responses and explain private/unpublished releases' {
        With-Fixture (New-Fixture) {
            $fixtureState.DownloadsFail = $true
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'no public release.+private.+3 attempts'
            Assert-Equal $fixtureState.Attempts 3 'Network failures must stop after three attempts'
            Assert-Equal $fixtureState.ProbeCalls 0 'Failed download must not execute'
            Assert-NoStaging $install
            $fixtureState.DownloadsFail = $false
            $fixtureState.TransientFailures = 1
            Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
            Assert-Equal $fixtureState.Attempts 7 'A transient error must retry once then complete metadata, sums, and archive'
        }
    }

    Test-Case 'HTTP and credential-bearing URLs are rejected without attempting download' {
        With-Fixture (New-Fixture) {
            foreach ($uri in @('http://github.com/example/archive.zip', 'https://user:password@github.com/example/archive.zip')) {
                Assert-Fails { Get-CrudeDownload $uri (Join-Path $root 'never-written') } 'absolute HTTPS URLs without embedded credentials'
            }
            Assert-Equal $fixtureState.Attempts 0 'Unsafe URLs must be rejected before transport'
        }
    }

    Test-Case 'held installation lock rejects concurrent installer and releases for retry' {
        With-Fixture (New-Fixture) {
            [void] [IO.Directory]::CreateDirectory($install)
            $held = [IO.File]::Open((Join-Path $install '.crude-install.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
            try { Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'installation lock.+Another installer' } finally { $held.Dispose() }
            Assert-Equal $fixtureState.Attempts 0 'Concurrent installer must not reach network'
            Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
            Assert-True ([IO.File]::Exists((Join-Path $install 'crude.exe'))) 'Retry must succeed after lock release'
        }
    }

    Test-Case 'interrupted staging is preserved and requires inspection' {
        With-Fixture (New-Fixture) {
            $unfinished = Join-Path $install '.crude-install-interrupted'
            [void] [IO.Directory]::CreateDirectory($unfinished)
            [IO.File]::WriteAllText((Join-Path $unfinished 'old.backup'), 'original work')
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'interrupted installation needs inspection'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $unfinished 'old.backup'))) 'original work' 'Recovery evidence must not be deleted'
            Assert-Equal $fixtureState.Attempts 0 'Unfinished transaction must fail before downloads'
        }
    }

    Test-Case 'PATH failure rolls back replaced executable, notices, and partial PATH writes' {
        foreach ($afterWrite in @($false, $true)) {
            With-Fixture (New-Fixture) {
                Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
                $oldUser = $fixtureState.UserPath
                $oldProcess = $env:Path
                $fixtureState.Fixture = New-Fixture -Tag v2.3.4
                $fixtureState.PathWriteFailure = -not $afterWrite
                $fixtureState.FailAfterPathWrite = $afterWrite
                Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install } 'fixture .+PATH'
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'PATH failure must roll back executable'
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'LICENSE-APACHE'))) 'Apache license v1.2.3' 'PATH failure must roll back notices'
                Assert-Equal $fixtureState.UserPath $oldUser 'PATH failure must restore original user PATH'
                Assert-Equal $env:Path $oldProcess 'PATH failure must keep original process PATH'
                Assert-NoStaging $install
            }
        }
    }

    Test-Case 'PATH failure removes first-install files but preserves unrelated work' {
        With-Fixture (New-Fixture) {
            [void] [IO.Directory]::CreateDirectory($install)
            [IO.File]::WriteAllText((Join-Path $install 'keep.txt'), 'unrelated')
            $fixtureState.PathWriteFailure = $true
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install } 'fixture user PATH access denied'
            Assert-True (-not [IO.File]::Exists((Join-Path $install 'crude.exe'))) 'Failed first install must remove newly installed binary'
            Assert-True (-not [IO.Directory]::Exists((Join-Path $install 'licenses'))) 'Failed first install must remove newly created notice directories'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'keep.txt'))) 'unrelated' 'Rollback must not delete unrelated files'
            Assert-NoStaging $install
        }
    }

    Test-Case 'file replacement failure rolls back earlier notices and keeps binary' {
        With-Fixture (New-Fixture) {
            Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
            $fixtureState.Fixture = New-Fixture -Tag v2.3.4
            $realSetFiles = ${function:Set-CrudeFiles}
            function Set-CrudeFiles($Transaction) {
                $binaryChange = @($Transaction.Changes | Where-Object { $_.Target.EndsWith('crude.exe') })[0]
                [IO.File]::Delete($binaryChange.Source)
                & $realSetFiles $Transaction
            }
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'No changes to the previous installation'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'Failed final replacement must leave original binary'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'LICENSE-MIT'))) 'MIT license v1.2.3' 'Earlier replacement must be rolled back'
            Assert-NoStaging $install
        }
    }

    Test-Case 'rollback refuses later work and retains backups for manual recovery' {
        With-Fixture (New-Fixture) {
            Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
            $fixtureState.Fixture = New-Fixture -Tag v2.3.4
            function Set-CrudeUserPath([string] $Value) {
                [IO.File]::WriteAllText((Join-Path $install 'crude.exe'), 'later independent edit')
                throw 'fixture error after independent file edit'
            }
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install } 'Rollback needs attention.+Backups and transaction details are retained'
            Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'later independent edit' 'Rollback must preserve later changes'
            $retained = @([IO.Directory]::GetDirectories($install, '.crude-install-*'))
            Assert-Equal $retained.Count 1 'Failed rollback must retain transaction directory'
            Assert-True ([IO.File]::Exists((Join-Path $retained[0] 'transaction.json'))) 'Retained transaction must include audit details'
            $journal = [IO.File]::ReadAllText((Join-Path $retained[0] 'transaction.json')) | ConvertFrom-Json
            Assert-Equal $journal.Path.OldUser $fixtureState.UserPath 'Recovery journal must retain exact raw user PATH preimage'
            Assert-Equal $journal.Path.UserKind ([int] [Microsoft.Win32.RegistryValueKind]::ExpandString) 'Recovery journal must retain PATH registry value kind'
            $backups = @([IO.Directory]::GetFiles($retained[0], '*.backup'))
            Assert-Equal $backups.Count 1 'Original binary backup must survive failed rollback'
            Assert-Equal ([IO.File]::ReadAllText($backups[0])) 'fixture executable v1.2.3' 'Backup must retain exact previous executable'
        }
    }

    Test-Case 'PATH normalization is exact, case-insensitive, and preserves long raw values' {
        . $installer
        Assert-Equal (Add-CrudePathEntry 'C:\Other;"c:/apps/crude/bin/";C:\After' 'C:\Apps\Crude\bin') 'C:\Other;"c:/apps/crude/bin/";C:\After' 'Existing normalized entry must not change'
        Assert-Equal (Add-CrudePathEntry 'C:\Apps\Crude\bin-extra' 'C:\Apps\Crude\bin') 'C:\Apps\Crude\bin-extra;C:\Apps\Crude\bin' 'Substring must not count as membership'
        $long = ('C:\Keep;' * 600) + '%USERPROFILE%\custom'
        Assert-Equal (Add-CrudePathEntry $long 'C:\Apps\Crude\bin') ($long + ';C:\Apps\Crude\bin') 'PATH must not be truncated or expanded'
        Assert-Equal (Add-CrudePathEntry $null 'C:\Apps\Crude\bin') 'C:\Apps\Crude\bin' 'Empty user PATH must be supported'
    }

    if ($nativeWindows) {
        if ($ReleaseDir) {
            Test-Case 'real release ZIP/SHA native version, installation, and idempotent repeat' { Test-RealRelease $ReleaseDir $ReleaseVersion }
        }
        Test-Case 'native architecture detection rejects ARM64 and x86' {
            . $installer
            Assert-CrudePlatform
            function Get-CrudeArchitecture { return 'Arm64' }
            Assert-Fails { Assert-CrudePlatform } 'Unsupported Windows architecture.+only'
            function Get-CrudeArchitecture { return 'X86' }
            Assert-Fails { Assert-CrudePlatform } 'Unsupported Windows architecture.+only'
        }

        Test-Case 'native executable version probe and verified installation' {
            With-Fixture (New-Fixture) {
                # The built-in .NET Framework compiler emits a runnable EXE for
                # both Windows PowerShell and PowerShell 7 fixture execution.
                $compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
                Assert-True ([IO.File]::Exists($compiler)) 'Windows .NET Framework C# fixture compiler must be available'
                $fixture = $fixtureState.Fixture
                $source = Join-Path $fixture.Directory 'VersionFixture.cs'
                $nativeExe = Join-Path $fixture.Directory 'VersionFixture.exe'
                [IO.File]::WriteAllText($source, @'
using System;
class VersionFixture {
    static int Main(string[] args) {
        if (args.Length != 1 || args[0] != "--version") return 42;
        Console.WriteLine("crude v1.2.3 (0123456789abcdef0123456789abcdef01234567; 2026-10-18T12:00:00Z; windows/amd64)");
        return 0;
    }
}
'@)
                & $compiler /nologo /target:exe "/out:$nativeExe" $source
                Assert-Equal $LASTEXITCODE 0 'Native --version fixture must compile'
                $zip = [IO.Compression.ZipFile]::Open($fixture.Archive, [IO.Compression.ZipArchiveMode]::Update)
                try {
                    $zip.GetEntry('crude.exe').Delete()
                    [void] [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $nativeExe, 'crude.exe')
                } finally { $zip.Dispose() }
                $hash = (Get-FileHash -LiteralPath $fixture.Archive -Algorithm SHA256).Hash
                [IO.File]::WriteAllText((Join-Path $fixture.Directory 'SHA256SUMS'), "$hash  $($fixture.Asset)`n")
                # Reload only the actual native probe, retaining the fixture HTTP
                # and user PATH boundaries. The install function still uses all
                # real verification, extraction, and replacement steps.
                $probeAst = [Management.Automation.Language.Parser]::ParseFile($installer, [ref] $null, [ref] $null)
                $probe = $probeAst.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Get-CrudeCandidateVersion' }, $false)
                . ([scriptblock]::Create($probe.Extent.Text))
                Install-Crude -AcceptBetaRisk -Version v1.2.3 -InstallDir $install -NoPath
                Assert-Equal (Get-CrudeCandidateVersion (Join-Path $install 'crude.exe')) $fixture.VersionOutput.Replace("`n", "`r`n") 'Installed native binary must report the verified version'
            }
        }

        Test-Case 'native locked binary failure is actionable and rolls back notices' {
            With-Fixture (New-Fixture) {
                Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
                $fixtureState.Fixture = New-Fixture -Tag v2.3.4
                $held = [IO.File]::Open((Join-Path $install 'crude.exe'), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
                try {
                    Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath } 'close the Crude shells/cores.+retry'
                    Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v1.2.3' 'Locked binary must be preserved'
                    Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'LICENSE-MIT'))) 'MIT license v1.2.3' 'Notices must roll back when binary is locked'
                } finally { $held.Dispose() }
                Install-Crude -AcceptBetaRisk -InstallDir $install -NoPath
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $install 'crude.exe'))) 'fixture executable v2.3.4' 'Retry after closing lock must update'
            }
        }
    } else {
        Test-Case 'non-Windows entry point rejects unsupported platform without downloads' {
            . $installer
            Assert-Fails { Install-Crude -AcceptBetaRisk -InstallDir (Join-Path $root 'unsupported') } 'supports Windows x64.+only'
            Assert-True (-not [IO.Directory]::Exists((Join-Path $root 'unsupported'))) 'Unsupported platform must not create install state'
        }
        Write-Host 'SKIP native Windows architecture/EXE/locked-binary checks (run this script on Windows PowerShell 5.1 and PowerShell 7).'
    }
} finally {
    $env:Path = $originalProcessPath
    $env:LOCALAPPDATA = $originalLocalAppData
    [IO.Directory]::Delete($root, $true)
}

Write-Host "$script:passed passed; $script:failed failed."
if ($script:failed) { exit 1 }
