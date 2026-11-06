#requires -Version 5.1
<#
.SYNOPSIS
Installs or updates the Windows x64 release of Crude. No elevation is needed.
.EXAMPLE
.\install.ps1
.EXAMPLE
.\install.ps1 -Yes -Gui
.NOTES
Only public GitHub releases are supported. No Go, Zig, or source checkout is needed.
Dot-sourcing defines the installer functions without starting an installation.
#>
[CmdletBinding()]
param(
    [string] $Version,
    [string] $Repository = 'opencrude/crude',
    [string] $InstallDir,
    [switch] $NoPath,
    [switch] $Yes,
    [switch] $Gui,
    [switch] $NoGui,
    [switch] $Stop,
    [switch] $NoStop
)

function Mark([string] $Name) {
    switch ($Name) {
        'check' { return [string] [char] 0x2713 }
        'dot' { return [string] [char] 0x00B7 }
        default { return '!' }
    }
}

function Write-CrudeStep([string] $Mark, [string] $Name, [string] $Detail) {
    Write-Host ('  {0} {1,-9} {2}' -f $Mark, $Name, $Detail)
}

function Test-CrudeInteractive {
    try {
        if (-not [Environment]::UserInteractive -or $Host.Name -ne 'ConsoleHost' -or [Console]::IsInputRedirected -or [Console]::IsOutputRedirected) { return $false }
        foreach ($argument in [Environment]::GetCommandLineArgs()) {
            if ($argument -match '^[-/]noni') { return $false }
        }
        return $true
    } catch { return $false }
}

function Confirm-CrudeWarning([switch] $Accepted) {
    Write-Host 'Crude is in active development. It changes files and runs commands.'
    Write-Host 'Bugs and agent mistakes can lose work. Commit and back up important work.'
    Write-Host ''
    if ($Accepted) { return }
    if (-not (Test-CrudeInteractive)) {
        throw 'This installer needs an answer. Run it in a terminal window, or add -Yes to accept the warning.'
    }
    $answer = Read-Host 'Install anyway? [y/N]'
    if ($answer -notmatch '^(?i)y(es)?$') {
        throw 'You did not accept the warning. Nothing was installed.'
    }
}

function Confirm-CrudeQuestion([string] $Question, [bool] $DefaultYes = $false) {
    if (-not (Test-CrudeInteractive)) { return $false }
    $hint = if ($DefaultYes) { '[Y/n]' } else { '[y/N]' }
    $answer = Read-Host "$Question $hint"
    if (-not $answer) { return $DefaultYes }
    return [bool] ($answer -match '^(?i)y(es)?$')
}

function Get-CrudeArchitecture {
    $runtimeInfo = 'System.Runtime.InteropServices.RuntimeInformation' -as [type]
    if ($runtimeInfo) { return $runtimeInfo::OSArchitecture.ToString() }
    $architecture = [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE', [EnvironmentVariableTarget]::Machine)
    if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
    if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
    return $architecture
}

function Assert-CrudePlatform {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw 'Unsupported platform. This installer supports Windows x64 (AMD64) only; use install.sh on Linux.'
    }
    $architecture = Get-CrudeArchitecture
    if (-not [Environment]::Is64BitOperatingSystem -or $architecture -inotmatch '^(AMD64|X64)$') {
        throw "Unsupported Windows architecture '$architecture'. Crude releases support Windows x64 (AMD64) only, not ARM64 or 32-bit Windows."
    }
}

function Assert-CrudeVersion([string] $Tag) {
    if ($Tag -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$') {
        throw "Invalid version '$Tag'. Expected vMAJOR.MINOR.PATCH with an optional SemVer prerelease (for example v1.2.3-rc.1)."
    }
    if ($Matches[4]) {
        foreach ($part in $Matches[4].Split('.')) {
            if ($part -cmatch '^0[0-9]+$') { throw "Invalid version '$Tag': numeric prerelease identifiers cannot have leading zeroes." }
        }
    }
}

function Assert-CrudeHttps([Uri] $Uri) {
    if (-not $Uri.IsAbsoluteUri -or $Uri.Scheme -cne 'https' -or $Uri.UserInfo) {
        throw 'Downloads and redirects must use absolute HTTPS URLs without embedded credentials.'
    }
}

function Invoke-CrudeDownloadAttempt([Uri] $Uri, [string] $Destination, [long] $MaximumBytes) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    for ($redirect = 0; $redirect -le 5; $redirect++) {
        Assert-CrudeHttps $Uri
        $remaining = 120000 - [int] $clock.ElapsedMilliseconds
        if ($remaining -le 0) { throw 'Download exceeded the 120-second attempt limit.' }
        $request = [Net.HttpWebRequest] [Net.WebRequest]::Create($Uri)
        $request.Method = 'GET'
        $request.UserAgent = 'Crude-Release-Installer'
        $request.Accept = 'application/vnd.github+json, application/octet-stream;q=0.9, */*;q=0.8'
        $request.AllowAutoRedirect = $false
        $request.Timeout = [Math]::Min(30000, $remaining)
        $request.ReadWriteTimeout = [Math]::Min(30000, $remaining)
        $response = $null
        $inputStream = $null
        $outputStream = $null
        try {
            $response = [Net.HttpWebResponse] $request.GetResponse()
            $status = [int] $response.StatusCode
            if ($status -in @(301, 302, 303, 307, 308)) {
                if ($redirect -eq 5 -or -not $response.Headers['Location']) { throw 'Too many redirects or missing redirect location.' }
                $Uri = [Uri]::new($Uri, $response.Headers['Location'])
                Assert-CrudeHttps $Uri
                continue
            }
            if ($status -ne 200) { throw "Unexpected HTTP status $status." }
            if ($response.ContentLength -gt $MaximumBytes) { throw 'Download is larger than the permitted release asset size.' }
            $inputStream = $response.GetResponseStream()
            $outputStream = [IO.File]::Open($Destination, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
            $buffer = New-Object byte[] 65536
            [long] $total = 0
            while ($true) {
                $remaining = 120000 - [int] $clock.ElapsedMilliseconds
                if ($remaining -le 0) { throw 'Download exceeded the 120-second attempt limit.' }
                if ($inputStream.CanTimeout) { $inputStream.ReadTimeout = [Math]::Min(30000, $remaining) }
                $read = $inputStream.Read($buffer, 0, $buffer.Length)
                if ($read -eq 0) { break }
                $total += $read
                if ($total -gt $MaximumBytes -or $clock.ElapsedMilliseconds -gt 120000) {
                    throw 'Download exceeded the size or 120-second attempt limit.'
                }
                $outputStream.Write($buffer, 0, $read)
            }
            if ($response.ContentLength -ge 0 -and $total -ne $response.ContentLength) { throw 'The download ended before the complete response was received.' }
            $outputStream.Flush($true)
            return
        } finally {
            if ($outputStream) { $outputStream.Dispose() }
            if ($inputStream) { $inputStream.Dispose() }
            if ($response) { $response.Dispose() }
            $request.Abort()
        }
    }
}

function Get-CrudeDownload([Uri] $Uri, [string] $Destination, [long] $MaximumBytes = 536870912) {
    Assert-CrudeHttps $Uri
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try {
            Invoke-CrudeDownloadAttempt $Uri $Destination $MaximumBytes
            return
        } catch {
            if ([IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
            if ($attempt -eq 3) { throw "Could not download $Uri after 3 attempts: $($_.Exception.Message)" }
            Start-Sleep -Seconds $attempt
        }
    }
}

function Get-CrudeRelease([string] $Repository, [string] $Version, [string] $WorkDir) {
    $endpoint = 'latest'
    if ($Version) { $endpoint = "tags/$Version" }
    $metadata = Join-Path $WorkDir 'release.json'
    try {
        Get-CrudeDownload "https://api.github.com/repos/$Repository/releases/$endpoint" $metadata 4194304
    } catch {
        throw "Cannot resolve the public GitHub release for '$Repository' ($endpoint). There may be no public release, the tag may be missing, the repository may be private, or GitHub/network access may be unavailable. $($_.Exception.Message)"
    }
    try { $release = [IO.File]::ReadAllText($metadata) | ConvertFrom-Json -ErrorAction Stop } catch { throw 'GitHub returned invalid release metadata.' }
    if (-not $release -or -not $release.tag_name) { throw 'GitHub release metadata is missing tag_name.' }
    $tag = [string] $release.tag_name
    Assert-CrudeVersion $tag
    if ($Version -and $tag -cne $Version) { throw "GitHub returned tag '$tag', not the requested '$Version'." }
    if ($release.draft -or (-not $Version -and ($release.prerelease -or $tag.Contains('-')))) {
        throw 'The latest public release must be stable and published. Use -Version to select a published prerelease explicitly.'
    }
    $assetName = "crude_${tag}_windows_amd64.zip"
    $baseUrl = "https://github.com/$Repository/releases/download/$tag"
    foreach ($name in @($assetName, 'SHA256SUMS')) {
        $assets = @($release.assets | Where-Object { $_.name -ceq $name })
        if ($assets.Count -ne 1) { throw "Release '$tag' must contain exactly one '$name' asset; found $($assets.Count)." }
        if ($assets[0].browser_download_url -cne "$baseUrl/$name") { throw "Release asset '$name' has an unexpected download URL." }
    }
    return [pscustomobject] @{ Tag = $tag; AssetName = $assetName; BaseUrl = $baseUrl }
}

function Get-CrudeHash([string] $Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
}

function Assert-CrudeChecksum([string] $Archive, [string] $Checksums, [string] $AssetName) {
    $entries = New-Object 'System.Collections.Generic.List[string]'
    foreach ($line in [IO.File]::ReadAllLines($Checksums)) {
        if (-not $line.Trim()) { continue }
        if ($line -cnotmatch '^([0-9A-Fa-f]{64}) [ *](.+)$') { throw 'SHA256SUMS contains an invalid checksum record.' }
        if ($Matches[2] -ceq $AssetName) { $entries.Add($Matches[1].ToLowerInvariant()) }
    }
    if ($entries.Count -ne 1) { throw "SHA256SUMS must contain exactly one checksum for '$AssetName'; found $($entries.Count)." }
    if ((Get-CrudeHash $Archive) -cne $entries[0]) { throw "SHA-256 checksum mismatch for '$AssetName'. Nothing has been installed." }
}

function Expand-CrudeArchive([string] $Archive, [string] $Destination) {
    Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
    Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $files = New-Object 'System.Collections.Generic.List[string]'
        [long] $totalSize = 0
        if ($zip.Entries.Count -gt 4096) { throw 'Release archive has too many entries.' }
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            $isDirectory = $name.EndsWith('/')
            if ($isDirectory) { $name = $name.Substring(0, $name.Length - 1) }
            if (-not $name -or $name.StartsWith('/') -or $name -match '[\x00-\x1f\x7f<>:"|?*]') { throw "Unsafe archive path '$($entry.FullName)'." }
            foreach ($part in $name.Split('/')) {
                if (-not $part -or $part -in @('.', '..') -or $part.EndsWith('.') -or $part.EndsWith(' ') -or $part -match '^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') {
                    throw "Unsafe archive path '$($entry.FullName)'."
                }
            }
            if (-not $seen.Add($name)) { throw "Duplicate archive path '$name'." }
            $mode = ($entry.ExternalAttributes -shr 16) -band 61440
            if ($mode -notin @(0, 16384, 32768) -or ($entry.ExternalAttributes -band 1024)) { throw "Archive links or special files are not allowed: '$name'." }
            if ($name -cnotin @('crude.exe', 'LICENSE-MIT', 'LICENSE-APACHE', 'README.md', 'crude-icon.png', 'crude-icon.svg', 'crude-icon.ico', 'licenses') -and -not $name.StartsWith('licenses/', [StringComparison]::Ordinal)) {
                throw "Unexpected release archive path '$name'."
            }
            if (($isDirectory -and ($entry.Length -ne 0 -or ($name -cne 'licenses' -and -not $name.StartsWith('licenses/')))) -or (-not $isDirectory -and $name -ceq 'licenses')) {
                throw "Invalid release archive directory '$name'."
            }
            $totalSize += $entry.Length
            if ($totalSize -gt 1073741824) { throw 'Expanded release archive exceeds 1 GiB.' }
            if (-not $isDirectory) { $files.Add($name) }
        }
        foreach ($required in @('crude.exe', 'LICENSE-MIT', 'LICENSE-APACHE', 'README.md')) {
            if (-not $files.Contains($required)) { throw "Release archive is missing root '$required'." }
        }
        if (@($files | Where-Object { $_.StartsWith('licenses/') }).Count -eq 0) { throw 'Release archive is missing third-party licenses/ notices.' }
        [void] [IO.Directory]::CreateDirectory($Destination)
        $root = [IO.Path]::GetFullPath($Destination) + [IO.Path]::DirectorySeparatorChar
        foreach ($entry in $zip.Entries) {
            $relative = $entry.FullName.Replace('\', '/').Replace('/', [IO.Path]::DirectorySeparatorChar)
            $target = [IO.Path]::GetFullPath((Join-Path $Destination $relative))
            if (-not $target.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) { throw 'Archive path escapes the extraction directory.' }
            if ($relative.EndsWith([string] [IO.Path]::DirectorySeparatorChar)) { [void] [IO.Directory]::CreateDirectory($target); continue }
            [void] [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target))
            $source = $entry.Open()
            $output = $null
            try {
                $output = [IO.File]::Open($target, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
                $buffer = New-Object byte[] 65536
                [long] $written = 0
                while (($read = $source.Read($buffer, 0, $buffer.Length)) -gt 0) {
                    $written += $read
                    if ($written -gt $entry.Length) { throw "Archive entry expands beyond its declared size: '$relative'." }
                    $output.Write($buffer, 0, $read)
                }
                if ($written -ne $entry.Length) { throw "Truncated archive entry: '$relative'." }
            } finally {
                if ($output) { $output.Dispose() }
                $source.Dispose()
            }
        }
        return $files.ToArray()
    } finally { $zip.Dispose() }
}

function Get-CrudeCandidateVersion([string] $Executable) {
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Executable
    $start.Arguments = '--version'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) { throw 'Could not start the release candidate for --version verification.' }
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(10000)) {
            # Only the newly downloaded --version probe is ours to terminate.
            $process.Kill()
            [void] $process.WaitForExit(5000)
            throw 'The release candidate did not answer --version within 10 seconds.'
        }
        if (-not $stdout.Wait(5000) -or -not $stderr.Wait(5000)) { throw 'The release candidate did not close its --version output streams.' }
        if ($process.ExitCode -ne 0 -or $stderr.GetAwaiter().GetResult()) { throw 'The release candidate failed --version verification.' }
        return $stdout.GetAwaiter().GetResult()
    } finally { $process.Dispose() }
}

function Assert-CrudeCandidate([string] $Executable, [string] $Tag) {
    $output = Get-CrudeCandidateVersion $Executable
    $pattern = '\Acrude ' + [Regex]::Escape($Tag) + ' \([0-9A-Fa-f]{40}; (?<date>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z); windows/amd64\)\r?\n?\z'
    if ($output -cnotmatch $pattern) { throw "The release candidate's --version does not identify crude $Tag (commit; date; windows/amd64)." }
    try { [void] [DateTime]::ParseExact($Matches['date'], "yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture) } catch { throw 'The release candidate has an invalid release date.' }
}

function Assert-CrudeNotReparsePoint([string] $Path) {
    while ($Path) {
        $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
        if ($item -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Refusing to replace a symbolic link or junction: '$Path'." }
        $Path = [IO.Path]::GetDirectoryName($Path)
    }
}

function New-CrudeFileTransaction([string] $Payload, [string] $InstallDir, [string[]] $Files, [string] $WorkDir) {
    $transaction = [pscustomobject] @{
        Changes = New-Object 'System.Collections.Generic.List[object]'
        Directories = New-Object 'System.Collections.Generic.List[string]'
    }
    $ordered = @($Files | Where-Object { $_ -cne 'crude.exe' } | Sort-Object) + @('crude.exe')
    foreach ($relative in $ordered) {
        $relative = $relative.Replace('/', [IO.Path]::DirectorySeparatorChar)
        $target = Join-Path $InstallDir $relative
        Assert-CrudeNotReparsePoint $target
        if ([IO.Directory]::Exists($target)) { throw "An installation file conflicts with an existing directory: '$target'." }
        $parent = [IO.Path]::GetDirectoryName($target)
        while ($parent -and $parent.Length -ge $InstallDir.Length) {
            Assert-CrudeNotReparsePoint $parent
            if ([IO.File]::Exists($parent)) { throw "An installation directory conflicts with an existing file: '$parent'." }
            $parent = [IO.Path]::GetDirectoryName($parent)
        }
        $source = Join-Path $Payload $relative
        $newHash = Get-CrudeHash $source
        $oldHash = $null
        if ([IO.File]::Exists($target)) { $oldHash = Get-CrudeHash $target }
        if ($oldHash -ceq $newHash) { continue }
        $transaction.Changes.Add([pscustomobject] @{
            Source = $source; Target = $target; OldHash = $oldHash; NewHash = $newHash
            Backup = Join-Path $WorkDir ([Guid]::NewGuid().ToString('N') + '.backup'); Applied = $false
        })
    }
    return $transaction
}

function Set-CrudeFiles($Transaction) {
    foreach ($change in $Transaction.Changes) {
        $missing = New-Object 'System.Collections.Generic.List[string]'
        $parent = [IO.Path]::GetDirectoryName($change.Target)
        while (-not [IO.Directory]::Exists($parent)) { $missing.Add($parent); $parent = [IO.Path]::GetDirectoryName($parent) }
        for ($i = $missing.Count - 1; $i -ge 0; $i--) {
            [void] [IO.Directory]::CreateDirectory($missing[$i])
            $Transaction.Directories.Add($missing[$i])
        }
        Assert-CrudeNotReparsePoint $change.Target
        if ($change.OldHash) {
            if ((Get-CrudeHash $change.Target) -cne $change.OldHash) { throw "Installation file changed during the update: '$($change.Target)'." }
            [IO.File]::Replace($change.Source, $change.Target, $change.Backup)
        } else {
            [IO.File]::Move($change.Source, $change.Target)
        }
        $change.Applied = $true
    }
}

function Undo-CrudeFiles($Transaction) {
    $failures = New-Object 'System.Collections.Generic.List[string]'
    for ($i = $Transaction.Changes.Count - 1; $i -ge 0; $i--) {
        $change = $Transaction.Changes[$i]
        if (-not $change.Applied) { continue }
        try {
            Assert-CrudeNotReparsePoint $change.Target
            if ((Get-CrudeHash $change.Target) -cne $change.NewHash) { throw 'File changed after installation; refusing to overwrite later work.' }
            if ($change.OldHash) {
                # A real path avoids PowerShell binding $null to an empty string
                # for File.Replace's backup operand on some runtimes.
                [IO.File]::Replace($change.Backup, $change.Target, ($change.Backup + '.replaced'))
            } else { [IO.File]::Delete($change.Target) }
            $change.Applied = $false
        } catch { $failures.Add("$($change.Target): $($_.Exception.Message)") }
    }
    for ($i = $Transaction.Directories.Count - 1; $i -ge 0; $i--) {
        $directory = $Transaction.Directories[$i]
        if ([IO.Directory]::Exists($directory) -and [IO.Directory]::GetFileSystemEntries($directory).Length -eq 0) { [IO.Directory]::Delete($directory) }
    }
    if ($failures.Count) { throw ($failures -join '; ') }
}

function Get-CrudeUserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
    if (-not $key) { return $null }
    try {
        # Environment.GetEnvironmentVariable expands REG_EXPAND_SZ on PS5.1;
        # read raw so %USERPROFILE% and other user-owned tokens survive intact.
        $value = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if ($null -ne $value -and $value -isnot [string]) { throw 'User PATH must be a string registry value.' }
        return $value
    } finally { $key.Dispose() }
}

function Get-CrudeUserPathKind {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
    if (-not $key) { return [Microsoft.Win32.RegistryValueKind]::String }
    try {
        if ($key.GetValueNames() -icontains 'Path') {
            $kind = $key.GetValueKind('Path')
            if ($kind -notin @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) { throw 'User PATH must be REG_SZ or REG_EXPAND_SZ.' }
            return $kind
        }
        return [Microsoft.Win32.RegistryValueKind]::String
    } finally { $key.Dispose() }
}

function Set-CrudeUserPath([AllowNull()] [string] $Value, [Microsoft.Win32.RegistryValueKind] $Kind) {
    # The Environment API handles persistence and the environment-change notice.
    [Environment]::SetEnvironmentVariable('Path', $Value, [EnvironmentVariableTarget]::User)
    if ($Value -and $Kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
        try {
            if ($key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -cne $Value) { throw 'User PATH changed before restoring its expandable value kind.' }
            # SetEnvironmentVariable writes REG_SZ. Restore the existing kind so
            # Windows still expands the raw tokens in future terminal processes.
            $key.SetValue('Path', $Value, $Kind)
        } finally { if ($key) { $key.Dispose() } }
    }
}

function Add-CrudePathEntry([AllowNull()] [string] $Path, [string] $Directory) {
    $wanted = $Directory.Replace('/', '\').TrimEnd('\')
    foreach ($entry in ($Path -split ';')) {
        $normalized = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).Replace('/', '\').TrimEnd('\')
        if ([string]::Equals($normalized, $wanted, [StringComparison]::OrdinalIgnoreCase)) { return $Path }
    }
    if (-not $Path) { return $Directory }
    if ($Path.EndsWith(';')) { return $Path + $Directory }
    return $Path + ';' + $Directory
}

function New-CrudePathTransaction([string] $InstallDir) {
    $user = Get-CrudeUserPath
    return [pscustomobject] @{
        OldUser = $user; NewUser = Add-CrudePathEntry $user $InstallDir; UserKind = Get-CrudeUserPathKind; UserAttempted = $false
        OldProcess = $env:Path; NewProcess = Add-CrudePathEntry $env:Path $InstallDir; ProcessAttempted = $false
    }
}

function Set-CrudePath($Transaction) {
    if ($Transaction.NewUser -cne $Transaction.OldUser) {
        if ((Get-CrudeUserPath) -cne $Transaction.OldUser -or (Get-CrudeUserPathKind) -ne $Transaction.UserKind) { throw 'User PATH changed during installation; retry without overwriting that change.' }
        $Transaction.UserAttempted = $true
        Set-CrudeUserPath $Transaction.NewUser $Transaction.UserKind
    }
    if ($Transaction.NewProcess -cne $Transaction.OldProcess) {
        if ($env:Path -cne $Transaction.OldProcess) { throw 'Process PATH changed during installation.' }
        $Transaction.ProcessAttempted = $true
        $env:Path = $Transaction.NewProcess
    }
}

function Undo-CrudePath($Transaction) {
    if ($Transaction.ProcessAttempted) {
        if ($env:Path -ceq $Transaction.NewProcess) { $env:Path = $Transaction.OldProcess } elseif ($env:Path -cne $Transaction.OldProcess) { throw 'Process PATH changed; refusing to overwrite later work.' }
    }
    if ($Transaction.UserAttempted) {
        $current = Get-CrudeUserPath
        if ($current -ceq $Transaction.NewUser) { Set-CrudeUserPath $Transaction.OldUser $Transaction.UserKind } elseif ($current -cne $Transaction.OldUser) { throw 'User PATH changed; refusing to overwrite later work.' }
    }
}

function Get-CrudeInstalledProcesses([string] $Executable) {
    # Only processes that run this installation block the update.
    $full = [IO.Path]::GetFullPath($Executable)
    $list = New-Object 'System.Collections.Generic.List[object]'
    try {
        foreach ($process in (Get-CimInstance -ClassName Win32_Process -Filter "Name = 'crude.exe'" -ErrorAction Stop)) {
            if ($process.ExecutablePath -and [string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $full, [StringComparison]::OrdinalIgnoreCase)) {
                $list.Add($process)
            }
        }
    } catch {
        # Windows can refuse process discovery. The file transaction then reports
        # a locked program instead of stopping anything silently.
    }
    return $list.ToArray()
}

function Confirm-CrudeStop([string] $Executable, [switch] $Stop, [switch] $NoStop) {
    # The answer comes before the download; the program is signalled only after
    # the new one is verified.
    $running = @(Get-CrudeInstalledProcesses $Executable)
    if (-not $running.Count) { return 'none' }
    if ($NoStop) { return 'keep' }
    if (-not $Stop) {
        $question = "Crude is running ($($running.Count) processes). Stop it now? Open sessions end. Unsaved editor text stays in History"
        if (-not (Confirm-CrudeQuestion $question $false)) {
            throw 'Crude is still running. Close it and run this installer again, or add -Stop.'
        }
    }
    return 'go'
}

function Stop-CrudeProcess([string] $Executable, [switch] $Force) {
    $running = @(Get-CrudeInstalledProcesses $Executable)
    if (-not $running.Count) { return }
    $ids = @($running | ForEach-Object { [int] $_.ProcessId })
    foreach ($id in $ids) { Stop-Process -Id $id -ErrorAction SilentlyContinue }
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ([DateTime]::UtcNow -lt $deadline -and @($ids | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue }).Count) {
        Start-Sleep -Milliseconds 250
    }
    $left = @($ids | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue })
    if ($left.Count) {
        if (-not $Force -and -not (Confirm-CrudeQuestion 'Crude did not stop. Force it to stop? Unsaved work in open files can be lost' $false)) {
            throw 'Crude is still running. Nothing was installed.'
        }
        foreach ($id in $left) { Stop-Process -Id $id -Force -ErrorAction SilentlyContinue }
        Start-Sleep -Seconds 1
        $left = @($ids | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue })
        if ($left.Count) { throw 'Crude is still running. Nothing was installed.' }
    }
    Write-CrudeStep (Mark 'check') 'Stop' 'Crude is closed'
}

function Get-CrudeWindowPaths {
    $windowHome = Join-Path $env:LOCALAPPDATA 'Crude'
    return [pscustomobject] @{
        Home = $windowHome
        Root = Join-Path $windowHome 'window'
        Version = '20240203-110809-5046fc22'
        Asset = 'WezTerm-windows-20240203-110809-5046fc22.zip'
        Sha256 = '57e5d03b585303d81e8b8e96d1230362852eb39aca92b3b29c7a42cfb82f9ac4'
        FontVersion = 'v3.5.1'
        FontSha256 = '04d5e8f903693f9dd13e16f867e994834e681eb3c72c0d337a770dcda09010cf'
        Icon = Join-Path $windowHome 'crude.ico'
        Link = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Crude.lnk'
    }
}

function Confirm-CrudeWindow([switch] $Gui, [switch] $NoGui) {
    # The answer comes before the download, so the question never waits behind it.
    if ($NoGui) { return 'skip' }
    if ($Gui) { return 'install' }
    $paths = Get-CrudeWindowPaths
    if ([IO.File]::Exists($paths.Link)) { return 'install' }
    if (-not (Test-CrudeInteractive)) { return 'skip' }
    if (Confirm-CrudeQuestion 'Do you want the GUI version as well?' $true) { return 'install' }
    return 'skip'
}

function Expand-CrudeWindowArchive([string] $Archive, [string] $Destination) {
    Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
    Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $root = [IO.Path]::GetFullPath($Destination) + [IO.Path]::DirectorySeparatorChar
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            if (-not $name -or $name.StartsWith('/') -or $name -match '(^|/)\.\.?(/|$)') { throw "Unsafe window archive path '$($entry.FullName)'." }
            $target = [IO.Path]::GetFullPath((Join-Path $Destination $name.Replace('/', [IO.Path]::DirectorySeparatorChar)))
            if (-not $target.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) { throw 'Window archive path escapes its folder.' }
            if ($name.EndsWith('/')) { [void] [IO.Directory]::CreateDirectory($target); continue }
            [void] [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target))
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $target, $false)
        }
    } finally { $zip.Dispose() }
}

function Register-CrudeFonts([string] $Folder) {
    # A per-user font is a file copy plus one value under HKCU. No elevation.
    $target = Join-Path $env:LOCALAPPDATA 'Microsoft\Windows\Fonts'
    [void] [IO.Directory]::CreateDirectory($target)
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Fonts')
    try {
        foreach ($font in @(Get-ChildItem -LiteralPath $Folder -Filter '*.ttf' -File)) {
            $copy = Join-Path $target $font.Name
            Copy-Item -LiteralPath $font.FullName -Destination $copy -Force
            $key.SetValue($font.BaseName + ' (TrueType)', $copy, [Microsoft.Win32.RegistryValueKind]::String)
        }
    } finally { $key.Dispose() }
}

function Install-CrudeWindowTerminal($Paths, [string] $WorkDir) {
    $prefix = Join-Path $Paths.Root "wezterm-$($Paths.Version)"
    $exe = Get-ChildItem -LiteralPath $prefix -Filter 'wezterm-gui.exe' -File -Recurse -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($exe) { return $exe }
    [void] [IO.Directory]::CreateDirectory($Paths.Root)
    $archive = Join-Path $WorkDir 'wezterm.zip'
    Get-CrudeDownload "https://github.com/wezterm/wezterm/releases/download/$($Paths.Version)/$($Paths.Asset)" $archive
    if ((Get-CrudeHash $archive) -cne $Paths.Sha256) {
        throw 'the downloaded window does not match its pinned checksum'
    }
    $unpack = Join-Path $Paths.Root ('.unpack-' + [Guid]::NewGuid().ToString('N'))
    try {
        Expand-CrudeWindowArchive $archive $unpack
        if ([IO.Directory]::Exists($prefix)) { [IO.Directory]::Delete($prefix, $true) }
        [IO.Directory]::Move($unpack, $prefix)
    } finally {
        if ([IO.Directory]::Exists($unpack)) { try { [IO.Directory]::Delete($unpack, $true) } catch { } }
    }
    $exe = Get-ChildItem -LiteralPath $prefix -Filter 'wezterm-gui.exe' -File -Recurse | Select-Object -First 1
    if (-not $exe) { throw 'the downloaded window does not hold the program it needs' }
    return $exe
}

function Install-CrudeWindowFonts($Paths, [string] $WorkDir) {
    $fonts = Join-Path $Paths.Root 'fonts'
    $stamp = Join-Path $fonts '.crude-font'
    $present = @(Get-ChildItem -LiteralPath $fonts -Filter '*.ttf' -File -ErrorAction SilentlyContinue)
    if ($present.Count -and [IO.File]::Exists($stamp) -and ([IO.File]::ReadAllText($stamp)).Trim() -ceq $Paths.FontVersion) {
        return
    }
    if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
        if (-not $present.Count) { Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong' }
        return
    }
    $archive = Join-Path $WorkDir 'fonts.tar.xz'
    try {
        Get-CrudeDownload "https://github.com/ryanoasis/nerd-fonts/releases/download/$($Paths.FontVersion)/JetBrainsMono.tar.xz" $archive
    } catch {
        if (-not $present.Count) { Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong' }
        return
    }
    if ((Get-CrudeHash $archive) -cne $Paths.FontSha256) {
        Write-CrudeStep (Mark 'warn') 'Font' 'the downloaded font does not match its pinned checksum'
        return
    }
    [void] [IO.Directory]::CreateDirectory($fonts)
    & tar.exe -xJf $archive -C $fonts
    Register-CrudeFonts $fonts
    if (@(Get-ChildItem -LiteralPath $fonts -Filter '*.ttf' -File).Count) {
        [IO.File]::WriteAllText($stamp, $Paths.FontVersion)
    } else {
        Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong'
    }
}

function Write-CrudeWindowFiles($Paths, [string] $InstallDir, [string] $Payload, [string] $Gui) {
    $lua = Join-Path $Paths.Root 'wezterm.lua'
    $text = @"
-- Written by the Crude installer. This window opens Crude only.
local wezterm = require 'wezterm'

-- Open as an application window: use the desktop area, not a small terminal.
wezterm.on('gui-startup', function(cmd)
    local _, _, window = wezterm.mux.spawn_window(cmd or {})
    local gui = window:gui_window()
    if gui then gui:maximize() end
end)

return {
    default_prog = { '@@PROGRAM@@' },
    font = wezterm.font_with_fallback { 'JetBrainsMono Nerd Font', 'JetBrainsMono NF', 'monospace' },
    font_size = 13.0,
    window_decorations = 'TITLE | RESIZE',
    initial_cols = 190,
    initial_rows = 50,
    hide_tab_bar_if_only_one_tab = true,
    enable_kitty_keyboard = true,
    window_close_confirmation = 'NeverPrompt',
    check_for_updates = false,
    audible_bell = 'Disabled',
    -- Crude owns these keys, so the terminal must not act on them here.
    keys = {
        { key = 'n', mods = 'CTRL|SHIFT', action = wezterm.action.DisableDefaultAssignment },
        { key = 't', mods = 'CTRL|SHIFT', action = wezterm.action.DisableDefaultAssignment },
        { key = 'w', mods = 'CTRL|SHIFT', action = wezterm.action.DisableDefaultAssignment },
    },
}
"@
    [IO.File]::WriteAllText($lua, $text.Replace('@@PROGRAM@@', (Join-Path $InstallDir 'crude.exe').Replace('\', '\\')))

    $iconSource = Join-Path $Payload 'crude-icon.ico'
    if ([IO.File]::Exists($iconSource)) { Copy-Item -LiteralPath $iconSource -Destination $Paths.Icon -Force }

    [void] [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Paths.Link))
    $shell = New-Object -ComObject WScript.Shell
    $link = $shell.CreateShortcut($Paths.Link)
    $link.TargetPath = $Gui
    $link.Arguments = "--config-file ""$lua"" start --always-new-process --class crude"
    $link.WorkingDirectory = $HOME
    $link.Description = 'Editor, terminals and AI agents in one window'
    if ([IO.File]::Exists($Paths.Icon)) { $link.IconLocation = $Paths.Icon }
    $link.Save()
}

function Remove-CrudeWindowOldVersions($Paths) {
    $current = "wezterm-$($Paths.Version)"
    foreach ($old in @(Get-ChildItem -LiteralPath $Paths.Root -Directory -ErrorAction SilentlyContinue)) {
        if ($old.Name -like 'wezterm-*' -and $old.Name -cne $current) {
            try { [IO.Directory]::Delete($old.FullName, $true) } catch { }
        }
    }
}

function Install-CrudeWindow([string] $InstallDir, [string] $Payload, [string] $WorkDir, [string] $Plan) {
    $paths = Get-CrudeWindowPaths
    if ($Plan -ne 'install') {
        if ([IO.File]::Exists($paths.Link)) {
            Write-CrudeStep (Mark 'dot') 'Window' 'not updated. Run this installer with -Gui to update it'
        } else {
            Write-CrudeStep (Mark 'dot') 'Window' 'not added. Run this installer with -Gui to add it'
        }
        return
    }
    # A window problem never undoes the program install; report the cause and stop.
    try {
        $gui = Install-CrudeWindowTerminal $paths $WorkDir
        # The new copy is in place, so the older pins can go.
        Remove-CrudeWindowOldVersions $paths
        Install-CrudeWindowFonts $paths $WorkDir
        Write-CrudeWindowFiles $paths $InstallDir $Payload $gui.FullName
        Write-CrudeStep (Mark 'check') 'Window' 'Crude is in the Start menu'
    } catch {
        Write-CrudeStep (Mark 'warn') 'Window' "the window was not installed: $($_.Exception.Message)"
    }
}

function Install-Crude([string] $Version, [string] $Repository = 'opencrude/crude', [string] $InstallDir,
    [switch] $NoPath, [switch] $Yes, [switch] $Gui, [switch] $NoGui, [switch] $Stop, [switch] $NoStop) {
    $ErrorActionPreference = 'Stop'
    Assert-CrudePlatform
    if ($Repository -cnotmatch '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$') { throw 'Repository must be a public GitHub owner/repository name.' }
    if ($Version) { Assert-CrudeVersion $Version }
    Confirm-CrudeWarning -Accepted:$Yes
    if (-not $InstallDir) {
        if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is unavailable; specify -InstallDir explicitly.' }
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Crude\bin'
    }
    $InstallDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDir)
    $InstallDir = [IO.Path]::GetFullPath($InstallDir).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    if ($InstallDir -match '[;\r\n]' -or -not $InstallDir -or $InstallDir -eq [IO.Path]::GetPathRoot($InstallDir).TrimEnd('\', '/')) { throw 'Choose a non-root installation directory without semicolons or line breaks.' }
    Assert-CrudeNotReparsePoint $InstallDir
    [void] [IO.Directory]::CreateDirectory($InstallDir)
    $lockPath = Join-Path $InstallDir '.crude-install.lock'
    Assert-CrudeNotReparsePoint $lockPath
    try { $lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) } catch {
        throw "Cannot acquire the installation lock in '$InstallDir'. Another installer may be running, or the directory is not writable. Wait for it to finish, or choose a writable -InstallDir, then retry."
    }
    $work = Join-Path $InstallDir ('.crude-install-' + [Guid]::NewGuid().ToString('N'))
    $files = $null
    $path = $null
    $retainWork = $false
    $cleanupWork = $false
    $oldTls = [Net.ServicePointManager]::SecurityProtocol
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $unfinished = @([IO.Directory]::GetDirectories($InstallDir, '.crude-install-*'))
        if ($unfinished.Count) { throw "An interrupted installation needs inspection before retrying. Preserve and inspect its transaction.json and backups in '$($unfinished -join "', '")'; restore affected files or remove verified unused staging directories manually. Nothing was changed." }
        [void] [IO.Directory]::CreateDirectory($work)
        # State the version first, then ask the two questions, so a question
        # never waits behind a large download.
        $release = Get-CrudeRelease $Repository $Version $work
        Write-Host "Version $($release.Tag) · windows/amd64"
        $stopPlan = Confirm-CrudeStop (Join-Path $InstallDir 'crude.exe') -Stop:$Stop -NoStop:$NoStop
        $windowPlan = Confirm-CrudeWindow -Gui:$Gui -NoGui:$NoGui
        $archive = Join-Path $work $release.AssetName
        $checksums = Join-Path $work 'SHA256SUMS'
        Get-CrudeDownload "$($release.BaseUrl)/SHA256SUMS" $checksums 4194304
        Get-CrudeDownload "$($release.BaseUrl)/$($release.AssetName)" $archive
        Assert-CrudeChecksum $archive $checksums $release.AssetName
        $payload = Join-Path $work 'payload'
        $relativeFiles = @(Expand-CrudeArchive $archive $payload)
        Assert-CrudeCandidate (Join-Path $payload 'crude.exe') $release.Tag
        Write-CrudeStep (Mark 'check') 'Download' 'SHA-256 matches'
        if ($stopPlan -eq 'keep') {
            Write-CrudeStep (Mark 'dot') 'Running' 'Crude stays open on the old version. It changes at the next start'
        } elseif ($stopPlan -eq 'go') {
            Stop-CrudeProcess (Join-Path $InstallDir 'crude.exe') -Force:$Stop
        }
        $files = New-CrudeFileTransaction $payload $InstallDir $relativeFiles $work
        if (-not $NoPath) { $path = New-CrudePathTransaction $InstallDir }
        # Persist immutable preimages before replacing anything. If interrupted,
        # leave these details and the original-file backups for manual recovery.
        $journal = @{ Repository = $Repository; Version = $release.Tag; Files = $files.Changes.ToArray(); Path = $path } | ConvertTo-Json -Depth 6
        $journalFile = [IO.File]::Open((Join-Path $work 'transaction.json'), [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $bytes = [Text.Encoding]::UTF8.GetBytes($journal)
            $journalFile.Write($bytes, 0, $bytes.Length)
            $journalFile.Flush($true)
        } finally { $journalFile.Dispose() }
        Set-CrudeFiles $files
        if ($path) { Set-CrudePath $path }
        Write-CrudeStep (Mark 'check') 'Program' (Join-Path $InstallDir 'crude.exe')
        Install-CrudeWindow -InstallDir $InstallDir -Payload $payload -WorkDir $work -Plan $windowPlan
        Write-Host ''
        Write-Host "Crude $($release.Tag) is ready."
        if ($NoPath) {
            Write-Host ('Next: run: ' + (Join-Path $InstallDir 'crude.exe'))
            Write-Host 'PATH was not changed. Add the install folder to PATH to run crude by name.'
        } else {
            Write-Host 'Next: open a new terminal window, then run: crude'
        }
        Write-Host 'Run this installer again to update. Your sessions and settings were not changed.'
        $cleanupWork = $true
    } catch {
        $failure = $_.Exception.Message
        $rollbackFailures = New-Object 'System.Collections.Generic.List[string]'
        if ($path) { try { Undo-CrudePath $path } catch { $rollbackFailures.Add($_.Exception.Message) } }
        if ($files) { try { Undo-CrudeFiles $files } catch { $rollbackFailures.Add($_.Exception.Message) } }
        if ($rollbackFailures.Count) {
            $retainWork = $true
            throw "$failure Rollback needs attention: $($rollbackFailures -join '; '). Backups and transaction details are retained in '$work'; do not delete them."
        }
        $cleanupWork = $true
        throw "$failure No changes to the previous installation or PATH were retained. If the program is in use or access is denied, close Crude and run this installer again, or add -Stop."
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $oldTls
        if ($cleanupWork -and -not $retainWork -and [IO.Directory]::Exists($work)) {
            try { [IO.Directory]::Delete($work, $true) } catch { Write-Warning "Could not remove installer staging directory '$work': $($_.Exception.Message)" }
        }
        # Keep the empty lock file: deleting it after closing would race a new owner.
        $lock.Dispose()
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        Install-Crude -Version $Version -Repository $Repository -InstallDir $InstallDir -NoPath:$NoPath -Yes:$Yes -Gui:$Gui -NoGui:$NoGui -Stop:$Stop -NoStop:$NoStop
    } catch {
        Write-Error "Crude installation failed: $($_.Exception.Message)" -ErrorAction Continue
        exit 1
    }
}
