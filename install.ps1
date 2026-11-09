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
-Gui adds a window that opens Crude, with its own terminal and font. The same
command updates Crude and the window together.
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

function Get-CrudeAccept([Uri] $Uri) {
    # The GitHub API picks the representation from Accept, and a JSON entry
    # outranks an octet-stream entry with a quality weight below 1, so an asset
    # URL must ask for bytes alone. A download URL ignores this header.
    if ($Uri.AbsolutePath -like '*/releases/assets/*') { return 'application/octet-stream' }
    return 'application/vnd.github+json, application/octet-stream;q=0.9, */*;q=0.8'
}

function Invoke-CrudeDownloadAttempt([Uri] $Uri, [string] $Destination, [long] $MaximumBytes, [int] $TimeoutSeconds = 120) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $limit = $TimeoutSeconds * 1000
    for ($redirect = 0; $redirect -le 5; $redirect++) {
        Assert-CrudeHttps $Uri
        $remaining = $limit - [int] $clock.ElapsedMilliseconds
        if ($remaining -le 0) { throw "Download exceeded the $TimeoutSeconds-second attempt limit." }
        $request = [Net.HttpWebRequest] [Net.WebRequest]::Create($Uri)
        $request.Method = 'GET'
        $request.UserAgent = 'Crude-Release-Installer'
        $request.Accept = Get-CrudeAccept $Uri
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
                $remaining = $limit - [int] $clock.ElapsedMilliseconds
                if ($remaining -le 0) { throw "Download exceeded the $TimeoutSeconds-second attempt limit." }
                if ($inputStream.CanTimeout) { $inputStream.ReadTimeout = [Math]::Min(30000, $remaining) }
                $read = $inputStream.Read($buffer, 0, $buffer.Length)
                if ($read -eq 0) { break }
                $total += $read
                if ($total -gt $MaximumBytes -or $clock.ElapsedMilliseconds -gt $limit) {
                    throw "Download exceeded the size or $TimeoutSeconds-second attempt limit."
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

function Get-CrudeDownload([Uri] $Uri, [string] $Destination, [long] $MaximumBytes = 536870912, [int] $TimeoutSeconds = 120) {
    Assert-CrudeHttps $Uri
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try {
            Invoke-CrudeDownloadAttempt $Uri $Destination $MaximumBytes $TimeoutSeconds
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
    $assetUrls = @{}
    foreach ($name in @($assetName, 'SHA256SUMS')) {
        $assets = @($release.assets | Where-Object { $_.name -ceq $name })
        if ($assets.Count -ne 1) { throw "Release '$tag' must contain exactly one '$name' asset; found $($assets.Count)." }
        if ($assets[0].browser_download_url -cne "$baseUrl/$name") { throw "Release asset '$name' has an unexpected download URL." }
        # The public download path can fail while the API asset path still
        # serves the same bytes. Keep it as a second source for this exact name.
        if (([string] $assets[0].url) -cmatch '^https://api\.github\.com/repos/[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*/releases/assets/[0-9]+$') {
            $assetUrls[$name] = [string] $assets[0].url
        }
    }
    return [pscustomobject] @{ Tag = $tag; AssetName = $assetName; BaseUrl = $baseUrl; AssetUrls = $assetUrls }
}

function Get-CrudeReleaseFile($Release, [string] $Name, [string] $Destination, [long] $MaximumBytes) {
    # Resolving the release already proved that api.github.com answers, and the
    # published download path can fail on a network that carries every other
    # GitHub request. So try the API asset first and keep the published path as
    # the second source. The pinned checksum still decides what is accepted.
    $sources = @()
    if ($Release.AssetUrls -and $Release.AssetUrls.ContainsKey($Name)) { $sources += $Release.AssetUrls[$Name] }
    $sources += "$($Release.BaseUrl)/$Name"
    $failure = $null
    foreach ($source in $sources) {
        try {
            Get-CrudeDownload $source $Destination $MaximumBytes
            return
        } catch {
            $failure = $_.Exception.Message
            if ([IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
        }
    }
    throw $failure
}

function Get-CrudeHash([string] $Path) {
    # Windows PowerShell 5.1 can be present without the module that carries
    # Get-FileHash, so hash the bytes with the runtime itself.
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $sha = [System.Security.Cryptography.SHA256]::Create()
        try { return ([BitConverter]::ToString($sha.ComputeHash($stream)) -replace '-', '').ToLowerInvariant() }
        finally { $sha.Dispose() }
    } finally { $stream.Dispose() }
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

function Get-CrudeInstalledProcesses([string[]] $Executables) {
    # Only processes that run this installation block the update. A window shows
    # the same program, so it is covered by the same check.
    $paths = @{}
    foreach ($executable in $Executables) {
        if ($executable) { $paths[[IO.Path]::GetFullPath($executable)] = $true }
    }
    $list = New-Object 'System.Collections.Generic.List[object]'
    $names = @($paths.Keys | ForEach-Object { [IO.Path]::GetFileName($_) } | Sort-Object -Unique)
    if (-not $names.Count) { return $list.ToArray() }
    $filter = ($names | ForEach-Object { "Name = '$_'" }) -join ' OR '
    try {
        foreach ($process in (Get-CimInstance -ClassName Win32_Process -Filter $filter -ErrorAction Stop)) {
            if ($process.ExecutablePath -and $paths.ContainsKey([IO.Path]::GetFullPath($process.ExecutablePath))) {
                $list.Add($process)
            }
        }
    } catch {
        # Windows can refuse process discovery. The file transaction then reports
        # a locked program instead of stopping anything silently.
    }
    return $list.ToArray()
}

function Confirm-CrudeStop([string[]] $Executables, [switch] $Stop, [switch] $NoStop) {
    # The answer comes before the download; the program is signalled only after
    # the new one is verified.
    $running = @(Get-CrudeInstalledProcesses $Executables)
    if (-not $running.Count) { return 'none' }
    if ($NoStop) { return 'keep' }
    if (-not $Stop) {
        $question = "Crude is running ($($running.Count) processes). Stop it now? Open sessions and windows end. Unsaved editor text stays in History"
        if (-not (Confirm-CrudeQuestion $question $false)) {
            throw 'Crude is still running. Close it and run this installer again, or add -Stop.'
        }
    }
    return 'go'
}

function Stop-CrudeProcess([string[]] $Executables, [switch] $Force) {
    $running = @(Get-CrudeInstalledProcesses $Executables)
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

function Get-CrudeGitPaths {
    # Git Bash is what makes an agent command fast and reliable on Windows.
    # A copy installed for this user only needs no elevation and no PATH entry.
    $crudeHome = Join-Path $env:LOCALAPPDATA 'Crude'
    $root = Join-Path $crudeHome 'git'
    return [pscustomobject] @{
        Home = $crudeHome
        Root = $root
        Version = '2.56.0.4'
        Asset = 'PortableGit-2.56.0.4-64-bit.7z.exe'
        # The API asset path is tried first because resolving a Crude release has
        # already proved that api.github.com answers. The pinned checksum still
        # decides what is accepted.
        AssetApi = 'https://api.github.com/repos/git-for-windows/git/releases/assets/677312658'
        Sha256 = 'b1f3e52216e2ed6882d1bebfd4d3fdf319bec40d2edbbf9fd26ec4fd3fce7ed1'
        Bash = Join-Path $root 'bin\bash.exe'
    }
}

function Get-CrudeGitShell {
    # The user's own Git Bash is used first, exactly as Crude finds it.
    # System32\bash.exe starts WSL, so a PATH bash is never used.
    $bases = @($env:ProgramFiles, $env:ProgramW6432, ${env:ProgramFiles(x86)}, (Join-Path $env:LOCALAPPDATA 'Programs'))
    foreach ($base in $bases) {
        if (-not $base) { continue }
        $candidate = Join-Path $base 'Git\bin\bash.exe'
        if ([IO.File]::Exists($candidate)) { return $candidate }
    }
    $git = Get-Command git.exe -ErrorAction SilentlyContinue
    if ($git -and $git.Source) {
        $candidate = Join-Path (Split-Path (Split-Path $git.Source -Parent) -Parent) 'bin\bash.exe'
        if ([IO.File]::Exists($candidate)) { return $candidate }
    }
    $paths = Get-CrudeGitPaths
    if ([IO.File]::Exists($paths.Bash)) { return $paths.Bash }
    return $null
}

function Expand-CrudeGitShell([string] $Setup, [string] $Destination) {
    # The pinned package is a 7-Zip self-extractor. -o names the folder and -y
    # answers every prompt, so it unpacks without a window and without a click.
    [void] [IO.Directory]::CreateDirectory($Destination)
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Setup
    $start.Arguments = '-o"' + $Destination + '" -y'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) { throw 'The Git Shell package did not start.' }
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(300000)) {
            $process.Kill()
            [void] $process.WaitForExit(5000)
            throw 'The Git Shell package did not finish unpacking in five minutes.'
        }
        if ($process.ExitCode -ne 0) { throw 'The Git Shell package could not unpack.' }
        [void] $stdout.Wait(5000)
        [void] $stderr.Wait(5000)
    } finally { $process.Dispose() }
    if (-not [IO.File]::Exists((Join-Path $Destination 'bin\bash.exe'))) {
        throw 'The Git Shell package is missing bin\bash.exe.'
    }
}

function Assert-CrudeGitShell([string] $Bash) {
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Bash
    $start.Arguments = '--version'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) { throw 'The new Git Shell did not start.' }
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(30000)) {
            $process.Kill()
            [void] $process.WaitForExit(5000)
            throw 'The new Git Shell did not answer in 30 seconds.'
        }
        if ($process.ExitCode -ne 0) { throw 'The new Git Shell failed its start check.' }
        if (-not $stdout.Wait(5000) -or -not $stderr.Wait(5000)) { throw 'The new Git Shell did not close its output.' }
        if ($stdout.GetAwaiter().GetResult() -notmatch 'GNU bash') { throw 'The new Git Shell is not GNU bash.' }
    } finally { $process.Dispose() }
}

function Install-CrudeGitShell([string] $WorkDir) {
    # A shell that is already present needs no work and no words.
    if (Get-CrudeGitShell) { return 'present' }
    $paths = Get-CrudeGitPaths
    $unpack = Join-Path $paths.Home ('.git-' + [Guid]::NewGuid().ToString('N'))
    $previous = Join-Path $paths.Home ('.git-old-' + [Guid]::NewGuid().ToString('N'))
    try {
        [void] [IO.Directory]::CreateDirectory($paths.Home)
        $setup = Join-Path $WorkDir $paths.Asset
        # This package is larger than a Crude release, so it gets a longer
        # bounded attempt. It is still three attempts per source, and the
        # public path can fail while the API asset path still works.
        $failure = $null
        $downloaded = $false
        foreach ($source in @($paths.AssetApi, "https://github.com/git-for-windows/git/releases/download/v$($paths.Version)/$($paths.Asset)")) {
            try {
                Get-CrudeDownload $source $setup 268435456 600
                $downloaded = $true
                break
            } catch {
                $failure = $_.Exception.Message
                if ([IO.File]::Exists($setup)) { [IO.File]::Delete($setup) }
            }
        }
        if (-not $downloaded) { throw $failure }
        if ((Get-CrudeHash $setup) -cne $paths.Sha256) { throw 'The downloaded Git Shell does not match its pinned checksum.' }
        Expand-CrudeGitShell $setup $unpack
        Assert-CrudeGitShell (Join-Path $unpack 'bin\bash.exe')
        # Put the new copy in place in one move, so a failure never leaves a
        # half-written shell that Crude would find on its next start.
        if ([IO.Directory]::Exists($paths.Root)) { [IO.Directory]::Move($paths.Root, $previous) }
        try {
            [IO.Directory]::Move($unpack, $paths.Root)
        } catch {
            if ([IO.Directory]::Exists($previous) -and -not [IO.Directory]::Exists($paths.Root)) { [IO.Directory]::Move($previous, $paths.Root) }
            throw
        }
        if ([IO.Directory]::Exists($previous)) { try { [IO.Directory]::Delete($previous, $true) } catch { } }
        return 'installed'
    } catch {
        return 'missing'
    } finally {
        if ([IO.Directory]::Exists($unpack)) { try { [IO.Directory]::Delete($unpack, $true) } catch { } }
    }
}

function Get-CrudeWindowPaths {
    $windowHome = Join-Path $env:LOCALAPPDATA 'Crude'
    return [pscustomobject] @{
        Home = $windowHome
        Root = Join-Path $windowHome 'window'
        # Windows Terminal reads one JSON fragment per application.
        Fragment = Join-Path $env:LOCALAPPDATA 'Microsoft\Windows Terminal\Fragments\Crude\crude.json'
        # A fixed identity keeps the menu entry and the profile pointing at the
        # same thing across updates.
        Guid = '{9f2b6c53-6a1e-4c31-9f0a-2b7d5c8e4a11}'
        FontVersion = 'v3.5.1'
        FontSha256 = '04d5e8f903693f9dd13e16f867e994834e681eb3c72c0d337a770dcda09010cf'
        FontApi = 'https://api.github.com/repos/ryanoasis/nerd-fonts/releases/assets/523475208'
        # The families the window asks for, in this order.
        FontFamilies = @('JetBrainsMono Nerd Font', 'JetBrainsMono NF')
        FontFaces = 'JetBrainsMonoNerdFont-*.ttf'
        Icon = Join-Path $windowHome 'crude.ico'
        Link = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Crude.lnk'
    }
}

function Get-CrudeWindowsTerminal {
    # The terminal Crude opens in. The execution alias keeps working across
    # terminal updates, so it is also what the menu entry points at.
    if ($env:LOCALAPPDATA) {
        $alias = Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps\wt.exe'
        if ([IO.File]::Exists($alias)) { return $alias }
    }
    $command = Get-Command 'wt.exe' -ErrorAction SilentlyContinue
    if ($command -and $command.Source -and [IO.File]::Exists($command.Source)) { return $command.Source }
    return $null
}

function Confirm-CrudeWindow([switch] $Gui, [switch] $NoGui) {
    # The answer comes before the program download, so the question never waits
    # behind it.
    if ($NoGui) { return 'skip' }
    if ($Gui) { return 'install' }
    $paths = Get-CrudeWindowPaths
    if ([IO.File]::Exists($paths.Link)) { return 'install' }
    if (-not (Test-CrudeInteractive)) { return 'skip' }
    if (Confirm-CrudeQuestion 'Do you want the GUI version as well?' $true) { return 'install' }
    return 'skip'
}

function Get-CrudeFontFamily([string[]] $Families) {
    # Which of these families Windows can draw, and whether Windows answered at
    # all. A font this machine already has is used as it is; the window never
    # replaces or re-registers one.
    try {
        Add-Type -AssemblyName System.Drawing -ErrorAction Stop
        $collection = New-Object System.Drawing.Text.InstalledFontCollection
        try { $available = @($collection.Families | ForEach-Object { $_.Name }) } finally { $collection.Dispose() }
    } catch {
        return [pscustomobject] @{ Known = $false; Name = $null }
    }
    foreach ($family in $Families) {
        if ($available -contains $family) { return [pscustomobject] @{ Known = $true; Name = $family } }
    }
    return [pscustomobject] @{ Known = $true; Name = $null }
}

function Test-CrudeFontAvailable($Paths) {
    $lookup = Get-CrudeFontFamily $Paths.FontFamilies
    if ($lookup.Name) { return $true }
    # Without a font list, the two standard font folders still answer the same
    # question, and a face that is there is never copied over.
    $folders = @()
    if ($env:WINDIR) { $folders += Join-Path $env:WINDIR 'Fonts' }
    if ($env:LOCALAPPDATA) { $folders += Join-Path $env:LOCALAPPDATA 'Microsoft\Windows\Fonts' }
    foreach ($folder in $folders) {
        if (@(Get-ChildItem -LiteralPath $folder -Filter $Paths.FontFaces -File -ErrorAction SilentlyContinue).Count) { return $true }
    }
    # No answer is no evidence that the font is missing, so nothing is written.
    if (-not $lookup.Known) { return $true }
    return $false
}

function Get-CrudeWindowFont($Paths) {
    # The face to name in the profile, and nothing when Windows cannot list it,
    # so the terminal keeps its own font instead of guessing.
    $lookup = Get-CrudeFontFamily $Paths.FontFamilies
    if ($lookup.Name) { return $lookup.Name }
    return $null
}

function Add-CrudeFontRegistration([string] $Folder) {
    # One per-user value per face, pointing at the file in the folder that
    # belongs to Crude. No file is copied into the user's font folder, no
    # existing value is overwritten, and the whole footprint is inside Crude's
    # own folder, so removing it removes only what this window installed.
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Fonts')
    if (-not $key) { throw 'the per-user font list could not be opened' }
    $registered = 0
    try {
        foreach ($font in @(Get-ChildItem -LiteralPath $Folder -Filter '*.ttf' -File)) {
            $name = $font.BaseName + ' (TrueType)'
            $current = $key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            if ($null -ne $current -and -not [string]::Equals([string] $current, $font.FullName, [StringComparison]::OrdinalIgnoreCase)) {
                # Another copy of this face is registered. It is not ours to change.
                continue
            }
            if ($null -eq $current) { $registered++ }
            $key.SetValue($name, $font.FullName, [Microsoft.Win32.RegistryValueKind]::String)
        }
    } finally { $key.Dispose() }
    return $registered
}

function Get-CrudeWindowDownload([string[]] $Sources, [string] $Destination, [long] $MaximumBytes, [int] $TimeoutSeconds) {
    # One file, two GitHub paths, the same bytes behind both. The published
    # download path can fail while the API asset path answers, so each path is
    # tried in turn; the pinned checksum still decides what is accepted.
    $failure = $null
    foreach ($source in $Sources) {
        try {
            Get-CrudeDownload $source $Destination $MaximumBytes $TimeoutSeconds
            return
        } catch {
            $failure = $_.Exception.Message
            if ([IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
        }
    }
    throw $failure
}

function Install-CrudeWindowFonts($Paths, [string] $WorkDir) {
    # The font is only added when the machine cannot draw the family already.
    # That is the case this window must never change: a font the user installed
    # stays exactly as it is, byte for byte, with its own registration.
    if (Test-CrudeFontAvailable $Paths) { return }
    $fonts = Join-Path $Paths.Root 'fonts'
    $stamp = Join-Path $fonts '.crude-font'
    $present = @(Get-ChildItem -LiteralPath $fonts -Filter '*.ttf' -File -ErrorAction SilentlyContinue)
    if ($present.Count -and [IO.File]::Exists($stamp) -and ([IO.File]::ReadAllText($stamp)).Trim() -ceq $Paths.FontVersion) {
        try { [void] (Add-CrudeFontRegistration $fonts) } catch {
            Write-CrudeStep (Mark 'warn') 'Font' "the font for the window could not be registered: $($_.Exception.Message)"
        }
        return
    }
    if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
        Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong'
        return
    }
    $archive = Join-Path $WorkDir 'fonts.tar.xz'
    try {
        Get-CrudeWindowDownload @($Paths.FontApi, "https://github.com/ryanoasis/nerd-fonts/releases/download/$($Paths.FontVersion)/JetBrainsMono.tar.xz") $archive 33554432 300
    } catch {
        Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong'
        return
    }
    if ((Get-CrudeHash $archive) -cne $Paths.FontSha256) {
        Write-CrudeStep (Mark 'warn') 'Font' 'the downloaded font does not match its pinned checksum'
        return
    }
    [void] [IO.Directory]::CreateDirectory($fonts)
    & tar.exe -xJf $archive -C $fonts
    try { [void] (Add-CrudeFontRegistration $fonts) } catch {
        Write-CrudeStep (Mark 'warn') 'Font' "the font for the window could not be registered: $($_.Exception.Message)"
    }
    if (@(Get-ChildItem -LiteralPath $fonts -Filter '*.ttf' -File).Count) {
        [IO.File]::WriteAllText($stamp, $Paths.FontVersion)
    } else {
        Write-CrudeStep (Mark 'warn') 'Font' 'the font for the window is missing. Icons may look wrong'
    }
}

function Remove-CrudeFontRegistration([string] $Folder) {
    # Delete the per-user font values that point into this folder. Every other
    # value is left alone.
    if (-not $Folder) { return }
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Fonts', $true)
    if (-not $key) { return }
    $root = [IO.Path]::GetFullPath($Folder)
    try {
        foreach ($name in @($key.GetValueNames())) {
            $value = $key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            if ($value -isnot [string] -or -not $value) { continue }
            try { $full = [IO.Path]::GetFullPath($value) } catch { continue }
            if ($full.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
                try { $key.DeleteValue($name, $false) } catch { }
            }
        }
    } finally { $key.Dispose() }
}

function Get-CrudeWindowLegacyPrograms($Paths) {
    # Terminal programs that a window in this folder runs. A window holding one
    # of these cannot be replaced, so an update closes it with the program. A
    # terminal the user installed elsewhere is never in this list.
    $found = New-Object 'System.Collections.Generic.List[string]'
    if (-not [IO.Directory]::Exists($Paths.Root)) { return $found.ToArray() }
    foreach ($program in @(Get-ChildItem -LiteralPath $Paths.Root -Recurse -File -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -in @('wezterm-gui.exe', 'wezterm.exe', 'crude-window.exe') })) {
        $found.Add($program.FullName)
    }
    return $found.ToArray()
}

function Remove-CrudeWindowLegacy($Paths) {
    # Remove a terminal, a configuration and font values that a window in this
    # folder does not use: its own terminal folder, wezterm.lua, the tools
    # folder, and a menu entry that opens them. Nothing outside Crude's own
    # folder is touched, and no font the user installed is removed.
    if (-not [IO.Directory]::Exists($Paths.Root)) { return }
    $legacy = @(Get-ChildItem -LiteralPath $Paths.Root -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like 'wezterm-*' -or $_.Name -like '.unpack-*' -or $_.Name -like '.previous-*' -or $_.Name -eq 'tools' })
    $lua = Join-Path $Paths.Root 'wezterm.lua'
    if (-not $legacy.Count -and -not [IO.File]::Exists($lua) -and -not [IO.File]::Exists("$lua.new")) { return }
    # A menu entry that opens a removed program would stay behind.
    if ([IO.File]::Exists($Paths.Link)) {
        $target = ''
        try {
            $shell = New-Object -ComObject WScript.Shell
            try { $target = $shell.CreateShortcut($Paths.Link).TargetPath } finally { [void] [Runtime.InteropServices.Marshal]::ReleaseComObject($shell) }
        } catch { $target = '' }
        if ($target) {
            try {
                if ([IO.Path]::GetFullPath($target).StartsWith([IO.Path]::GetFullPath($Paths.Root), [StringComparison]::OrdinalIgnoreCase)) {
                    [IO.File]::Delete($Paths.Link)
                }
            } catch { }
        }
    }
    Remove-CrudeFontRegistration (Join-Path $Paths.Root 'fonts')
    foreach ($entry in $legacy) {
        try { [IO.Directory]::Delete($entry.FullName, $true) } catch { }
    }
    foreach ($stale in @($lua, "$lua.new")) {
        if ([IO.File]::Exists($stale)) { try { [IO.File]::Delete($stale) } catch { } }
    }
    $fonts = Join-Path $Paths.Root 'fonts'
    if ([IO.Directory]::Exists($fonts) -and -not @(Get-ChildItem -LiteralPath $fonts -File -ErrorAction SilentlyContinue).Count) {
        try { [IO.Directory]::Delete($fonts, $true) } catch { }
    }
}

function Write-CrudeWindowProfile($Paths, [string] $InstallDir) {
    # One JSON fragment in the user's Fragments folder: Windows Terminal reads
    # it at start, so the profile appears without editing the user's settings.
    $profile = [ordered] @{
        guid = $Paths.Guid
        name = 'Crude'
        commandline = (Join-Path $InstallDir 'crude.exe')
        startingDirectory = '%USERPROFILE%'
        # Keep the pane when Crude fails, so the message stays readable.
        closeOnExit = 'graceful'
    }
    if ([IO.File]::Exists($Paths.Icon)) { $profile.icon = $Paths.Icon }
    $face = Get-CrudeWindowFont $Paths
    if ($face) { $profile.font = [ordered] @{ face = $face; size = 12 } }
    $fragment = [ordered] @{ profiles = @($profile) }

    [void] [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Paths.Fragment))
    $pending = $Paths.Fragment + '.new'
    $bytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes(($fragment | ConvertTo-Json -Depth 6))
    $stream = [IO.File]::Open($pending, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
    } finally { $stream.Dispose() }
    # Replace in one step, so Windows Terminal never reads half a fragment.
    if (-not [IO.File]::Exists($Paths.Fragment)) {
        [IO.File]::Move($pending, $Paths.Fragment)
    } else {
        try {
            [IO.File]::Replace($pending, $Paths.Fragment, [NullString]::Value)
        } catch [PlatformNotSupportedException] {
            [IO.File]::Delete($Paths.Fragment)
            [IO.File]::Move($pending, $Paths.Fragment)
        }
    }
}

function Write-CrudeWindowLink($Paths, [string] $Terminal, [string] $InstallDir) {
    # The menu entry opens one maximized Crude window and carries the Crude icon,
    # because the entry belongs to Crude and not to the terminal. The program is
    # named on the command line as well as in the profile: a terminal that has
    # not read the profile yet then still opens Crude.
    [void] [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Paths.Link))
    $program = Join-Path $InstallDir 'crude.exe'
    $shell = New-Object -ComObject WScript.Shell
    try {
        $link = $shell.CreateShortcut($Paths.Link)
        $link.TargetPath = $Terminal
        $link.Arguments = '-w -1 new-tab -p "Crude" --maximized -- "' + $program + '"'
        $link.WorkingDirectory = $HOME
        $link.Description = 'Editor, terminals and AI agents in one window'
        if ([IO.File]::Exists($Paths.Icon)) { $link.IconLocation = "$($Paths.Icon),0" }
        $link.Save()
    } finally {
        [void] [Runtime.InteropServices.Marshal]::ReleaseComObject($shell)
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
        $terminal = Get-CrudeWindowsTerminal
        if (-not $terminal) {
            # The window needs the terminal Windows already provides. Crude does
            # not install one of its own.
            Write-CrudeStep (Mark 'warn') 'Window' 'needs Windows Terminal, which is not installed'
            Write-Host '              Install it, then run this installer again:'
            Write-Host '              winget install --id Microsoft.WindowsTerminal'
            return
        }
        Remove-CrudeWindowLegacy $paths
        [void] [IO.Directory]::CreateDirectory($paths.Root)
        $iconSource = Join-Path $Payload 'crude-icon.ico'
        if ([IO.File]::Exists($iconSource)) { Copy-Item -LiteralPath $iconSource -Destination $paths.Icon -Force }
        Install-CrudeWindowFonts $paths $WorkDir
        Write-CrudeWindowProfile $paths $InstallDir
        Write-CrudeWindowLink $paths $terminal $InstallDir
        Write-CrudeStep (Mark 'check') 'Window' 'Crude is in the Start menu and in Windows Terminal'
        if (Get-Process -Name 'WindowsTerminal' -ErrorAction SilentlyContinue) {
            # A running terminal keeps the profiles it read when it started.
            Write-CrudeStep (Mark 'dot') 'Profile' 'Windows Terminal reads new profiles when it starts. Run Reload settings, then open Crude'
        }
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
        # Stop this program, and any terminal program a window in Crude's folder
        # runs, before the update replaces them.
        $programs = @((Join-Path $InstallDir 'crude.exe')) + @(Get-CrudeWindowLegacyPrograms (Get-CrudeWindowPaths))
        $stopPlan = Confirm-CrudeStop $programs -Stop:$Stop -NoStop:$NoStop
        $windowPlan = Confirm-CrudeWindow -Gui:$Gui -NoGui:$NoGui
        $archive = Join-Path $work $release.AssetName
        $checksums = Join-Path $work 'SHA256SUMS'
        Get-CrudeReleaseFile $release 'SHA256SUMS' $checksums 4194304
        Get-CrudeReleaseFile $release $release.AssetName $archive 536870912
        Assert-CrudeChecksum $archive $checksums $release.AssetName
        $payload = Join-Path $work 'payload'
        $relativeFiles = @(Expand-CrudeArchive $archive $payload)
        Assert-CrudeCandidate (Join-Path $payload 'crude.exe') $release.Tag
        Write-CrudeStep (Mark 'check') 'Download' 'SHA-256 matches'
        if ($stopPlan -eq 'keep') {
            Write-CrudeStep (Mark 'dot') 'Running' 'Crude stays open on the old version. It changes at the next start'
        } elseif ($stopPlan -eq 'go') {
            Stop-CrudeProcess $programs -Force:$Stop
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
        if ((Install-CrudeGitShell -WorkDir $work) -eq 'missing') {
            Write-CrudeStep (Mark 'warn') 'Git Shell' 'not installed. Crude works best with Git Bash.'
            Write-Host '              Get Git for Windows from https://git-scm.com/download/win.'
            Write-Host '              Crude uses it the next time it starts.'
        }
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
