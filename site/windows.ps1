# Install or update Crude on Windows x64:
#
#   irm https://opencrude.github.io/windows.ps1 | iex
#
# This runs the installer of the latest published release,
# https://github.com/opencrude/crude/releases/latest/download/install.ps1
#
& {
    $url = 'https://github.com/opencrude/crude/releases/latest/download/install.ps1'
    $installer = ''
    $ProgressPreference = 'SilentlyContinue'
    $tls = [Net.ServicePointManager]::SecurityProtocol
    try {
        # Windows PowerShell 5.1 can start with TLS 1.0, which GitHub refuses.
        [Net.ServicePointManager]::SecurityProtocol = $tls -bor [Net.SecurityProtocolType]::Tls12
        for ($attempt = 1; $attempt -le 3 -and -not $installer; $attempt++) {
            if ($attempt -gt 1) { Start-Sleep -Seconds (2 * ($attempt - 1)) }
            try {
                $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 60 -ErrorAction Stop
                # Decode the bytes as UTF-8 on every PowerShell version.
                $installer = [Text.Encoding]::UTF8.GetString($response.RawContentStream.ToArray()).TrimStart([char] 0xFEFF)
            } catch {
                $installer = ''
            }
        }
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $tls
    }
    if (-not $installer) {
        throw "Crude installer: cannot download $url. Check your network connection, then try again. Nothing was changed."
    }
    & ([scriptblock]::Create($installer)) @args
} @args
