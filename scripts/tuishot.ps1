# Launch Crude in one dedicated Windows Terminal window, drive it with
# keyboard/mouse steps, and capture that exact window with NirCmd.
#
# Usage:
#   pwsh scripts/tuishot.ps1 <outdir> [step ...]
#
# Steps mirror scripts/tuishot.sh:
#   key:<key-spec>             key:ctrl+alt+p  key:Down  key:Escape
#   type:<literal text>        type:settings
#   click:<col>,<row>[,<btn>]  btn 1=left 2=middle 3=right
#   move:<col>,<row>
#   drag:<c1>,<r1>,<c2>,<r2>
#   dragburst:<c1>,<r1>,<c2>,<r2>,<steps>
#   wheel:<col>,<row>,<n>      positive=down, negative=up
#   hwheel:<col>,<row>,<n>     positive=right, negative=left
#   sleep:<seconds>
#   shot[:label]
#
# Environment:
#   COLS ROWS PAINT_WAIT STEP_WAIT CRUDE_ARGS CRUDE_CWD CRUDE_BIN
#   KEEP_OPEN NIRCMD

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$OutDir,

    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]]$Steps = @(),

    [int]$Cols = 160,
    [int]$Rows = 40,
    [double]$PaintWait = 5,
    [double]$StepWait = 0.7,
    [string]$CrudeArgs = "",
    [string]$CrudeCwd = "",
    [string]$CrudeBin = "",
    [switch]$KeepOpen
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$ScriptBoundParameters = @{} + $PSBoundParameters

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw "scripts/tuishot.ps1 requires PowerShell 7+ (pwsh)"
}

function Set-FromEnvironment {
    param(
        [string]$Name,
        [scriptblock]$Convert,
        [string]$ParameterName
    )
    if ($ScriptBoundParameters.ContainsKey($ParameterName)) {
        return
    }
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        return
    }
    & $Convert $value
}

Set-FromEnvironment "COLS" { param($v) $script:Cols = [int]$v } "Cols"
Set-FromEnvironment "ROWS" { param($v) $script:Rows = [int]$v } "Rows"
Set-FromEnvironment "PAINT_WAIT" { param($v) $script:PaintWait = [double]$v } "PaintWait"
Set-FromEnvironment "STEP_WAIT" { param($v) $script:StepWait = [double]$v } "StepWait"
Set-FromEnvironment "CRUDE_ARGS" { param($v) $script:CrudeArgs = $v } "CrudeArgs"
Set-FromEnvironment "CRUDE_CWD" { param($v) $script:CrudeCwd = $v } "CrudeCwd"
Set-FromEnvironment "CRUDE_BIN" { param($v) $script:CrudeBin = $v } "CrudeBin"
if (-not $ScriptBoundParameters.ContainsKey("KeepOpen") -and $env:KEEP_OPEN -eq "1") {
    $KeepOpen = $true
}

if ($Cols -lt 20 -or $Rows -lt 8) {
    throw "COLS and ROWS must describe a usable terminal grid"
}

$Repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ([string]::IsNullOrWhiteSpace($CrudeCwd)) {
    $CrudeCwd = $Repo
}
$CrudeCwd = (Resolve-Path $CrudeCwd).Path
$OutDir = [IO.Path]::GetFullPath($OutDir)
$Work = Join-Path $env:TEMP "crude-tuishot"
$Log = Join-Path $Work "crude.log"
$Token = [Guid]::NewGuid().ToString("N")
$WindowName = "CrudeShot-$Token"
$Launch = Join-Path $Work "launch-$Token.cmd"
$BuiltBin = Join-Path $Work "crude-shot.exe"
$LockPath = Join-Path $Work "capture.lock"

New-Item -ItemType Directory -Force -Path $Work, $OutDir | Out-Null
[IO.File]::WriteAllText($Log, "", [Text.UTF8Encoding]::new($false))

$LockStream = $null
$CaptureHwnd = [IntPtr]::Zero

if (-not ("CrudeShot.Native" -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;

namespace CrudeShot {
    public static class Native {
        private delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);

        [StructLayout(LayoutKind.Sequential)]
        public struct RECT {
            public int Left;
            public int Top;
            public int Right;
            public int Bottom;
        }

        [StructLayout(LayoutKind.Sequential)]
        public struct POINT {
            public int X;
            public int Y;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct INPUT {
            public uint type;
            public InputUnion U;
        }

        [StructLayout(LayoutKind.Explicit)]
        private struct InputUnion {
            [FieldOffset(0)]
            public KEYBDINPUT ki;
            [FieldOffset(0)]
            public MOUSEINPUT mi;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct KEYBDINPUT {
            public ushort wVk;
            public ushort wScan;
            public uint dwFlags;
            public uint time;
            public IntPtr dwExtraInfo;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct MOUSEINPUT {
            public int dx;
            public int dy;
            public uint mouseData;
            public uint dwFlags;
            public uint time;
            public IntPtr dwExtraInfo;
        }

        private const uint INPUT_KEYBOARD = 1;
        private const uint KEYEVENTF_KEYUP = 0x0002;
        private const uint KEYEVENTF_UNICODE = 0x0004;
        public const uint WM_CLOSE = 0x0010;

        [DllImport("user32.dll")]
        private static extern bool EnumWindows(EnumWindowsProc callback, IntPtr lParam);
        [DllImport("user32.dll")]
        private static extern bool IsWindowVisible(IntPtr hWnd);
        [DllImport("user32.dll")]
        public static extern bool IsWindow(IntPtr hWnd);
        [DllImport("user32.dll")]
        public static extern IntPtr GetForegroundWindow();
        [DllImport("user32.dll")]
        public static extern bool SetForegroundWindow(IntPtr hWnd);
        [DllImport("user32.dll")]
        public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
        [DllImport("user32.dll")]
        public static extern bool PostMessage(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);
        [DllImport("user32.dll")]
        public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint processId);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        private static extern int GetWindowText(IntPtr hWnd, System.Text.StringBuilder text, int maxCount);
        [DllImport("user32.dll")]
        private static extern int GetWindowTextLength(IntPtr hWnd);
        [DllImport("user32.dll")]
        private static extern bool GetClientRect(IntPtr hWnd, out RECT rect);
        [DllImport("user32.dll")]
        private static extern bool ClientToScreen(IntPtr hWnd, ref POINT point);
        [DllImport("user32.dll")]
        private static extern int GetSystemMetrics(int index);
        [DllImport("user32.dll")]
        public static extern void mouse_event(uint flags, uint dx, uint dy, int data, UIntPtr extraInfo);
        [DllImport("user32.dll")]
        private static extern uint SendInput(uint count, INPUT[] inputs, int size);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "VkKeyScanW")]
        public static extern short VkKeyScanW(char ch);

        public static IntPtr[] VisibleWindows() {
            var result = new List<IntPtr>();
            EnumWindows(delegate (IntPtr hWnd, IntPtr lParam) {
                if (IsWindowVisible(hWnd)) {
                    result.Add(hWnd);
                }
                return true;
            }, IntPtr.Zero);
            return result.ToArray();
        }

        public static uint WindowProcessId(IntPtr hWnd) {
            uint processId;
            GetWindowThreadProcessId(hWnd, out processId);
            return processId;
        }

        public static string WindowTitle(IntPtr hWnd) {
            int length = GetWindowTextLength(hWnd);
            var text = new System.Text.StringBuilder(length + 1);
            GetWindowText(hWnd, text, text.Capacity);
            return text.ToString();
        }

        public static RECT ClientScreenRect(IntPtr hWnd) {
            RECT client;
            if (!GetClientRect(hWnd, out client)) {
                throw new InvalidOperationException("GetClientRect failed");
            }
            var topLeft = new POINT { X = client.Left, Y = client.Top };
            var bottomRight = new POINT { X = client.Right, Y = client.Bottom };
            if (!ClientToScreen(hWnd, ref topLeft) || !ClientToScreen(hWnd, ref bottomRight)) {
                throw new InvalidOperationException("ClientToScreen failed");
            }
            return new RECT {
                Left = topLeft.X,
                Top = topLeft.Y,
                Right = bottomRight.X,
                Bottom = bottomRight.Y
            };
        }

        private static INPUT KeyInput(ushort vk, ushort scan, uint flags) {
            return new INPUT {
                type = INPUT_KEYBOARD,
                U = new InputUnion {
                    ki = new KEYBDINPUT {
                        wVk = vk,
                        wScan = scan,
                        dwFlags = flags,
                        time = 0,
                        dwExtraInfo = IntPtr.Zero
                    }
                }
            };
        }

        public static void MovePointer(int x, int y) {
            // SendInput produces actual terminal motion reports; SetCursorPos
            // alone moved the cursor but omitted hover/drag events in Windows
            // Terminal. Absolute virtual-desktop coordinates cover all monitors.
            int left = GetSystemMetrics(76), top = GetSystemMetrics(77);
            int width = GetSystemMetrics(78), height = GetSystemMetrics(79);
            var input = new INPUT {
                type = 0,
                U = new InputUnion {
                    mi = new MOUSEINPUT {
                        dx = (int)(((long)x - left) * 65536 / width),
                        dy = (int)(((long)y - top) * 65536 / height),
                        dwFlags = 0x0001 | 0x8000 | 0x4000 | 0x2000 // MOVE | ABSOLUTE | VIRTUALDESK | MOVE_NOCOALESCE
                    }
                }
            };
            if (SendInput(1, new INPUT[] { input }, Marshal.SizeOf(typeof(INPUT))) != 1) {
                throw new InvalidOperationException("SendInput mouse movement failed");
            }
        }

        public static void SendChord(ushort[] modifiers, ushort key) {
            var inputs = new List<INPUT>();
            foreach (ushort modifier in modifiers) {
                inputs.Add(KeyInput(modifier, 0, 0));
            }
            inputs.Add(KeyInput(key, 0, 0));
            inputs.Add(KeyInput(key, 0, KEYEVENTF_KEYUP));
            for (int i = modifiers.Length - 1; i >= 0; i--) {
                inputs.Add(KeyInput(modifiers[i], 0, KEYEVENTF_KEYUP));
            }
            INPUT[] array = inputs.ToArray();
            if (SendInput((uint)array.Length, array, Marshal.SizeOf(typeof(INPUT))) != array.Length) {
                throw new InvalidOperationException("SendInput key chord failed");
            }
        }

        public static void SendUnicode(string text) {
            foreach (char ch in text) {
                INPUT[] pair = new INPUT[] {
                    KeyInput(0, ch, KEYEVENTF_UNICODE),
                    KeyInput(0, ch, KEYEVENTF_UNICODE | KEYEVENTF_KEYUP)
                };
                if (SendInput(2, pair, Marshal.SizeOf(typeof(INPUT))) != 2) {
                    throw new InvalidOperationException("SendInput Unicode text failed");
                }
            }
        }
    }
}
'@
}

function Get-WindowsTerminalWindows {
    $windows = @()
    foreach ($handle in [CrudeShot.Native]::VisibleWindows()) {
        $processId = [int][CrudeShot.Native]::WindowProcessId($handle)
        if ($processId -le 0) {
            continue
        }
        $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
        if ($null -eq $process -or $process.ProcessName -ne "WindowsTerminal") {
            continue
        }
        $windows += [pscustomobject]@{
            Handle = $handle
            ProcessId = $processId
            Title = [CrudeShot.Native]::WindowTitle($handle)
        }
    }
    return $windows
}

function Find-Executable {
    param([string]$Explicit, [string]$Name)
    if (-not [string]::IsNullOrWhiteSpace($Explicit)) {
        $resolved = Resolve-Path $Explicit -ErrorAction Stop
        return $resolved.Path
    }
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        throw "$Name was not found on PATH"
    }
    return $command.Source
}

function Wait-Seconds {
    param([double]$Seconds)
    if ($Seconds -le 0) {
        return
    }
    Start-Sleep -Milliseconds ([Math]::Max(1, [int][Math]::Round($Seconds * 1000)))
}

function Focus-CaptureWindow {
    if ($CaptureHwnd -eq [IntPtr]::Zero -or -not [CrudeShot.Native]::IsWindow($CaptureHwnd)) {
        throw "the dedicated CrudeShot window no longer exists"
    }
    $handleText = "0x{0:X}" -f $CaptureHwnd.ToInt64()
    & $NirCmd win activate handle $handleText
    [CrudeShot.Native]::ShowWindow($CaptureHwnd, 9) | Out-Null
    [CrudeShot.Native]::SetForegroundWindow($CaptureHwnd) | Out-Null
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        if ([CrudeShot.Native]::GetForegroundWindow() -eq $CaptureHwnd) {
            return
        }
        Start-Sleep -Milliseconds 50
    }
    throw "Windows refused to activate the dedicated CrudeShot window"
}

function Convert-ToSlug {
    param([string]$Value)
    $slug = [regex]::Replace($Value, "[^a-zA-Z0-9]+", "_").Trim("_")
    if ([string]::IsNullOrWhiteSpace($slug)) {
        return "shot"
    }
    return $slug.Substring(0, [Math]::Min(22, $slug.Length))
}

$ShotNumber = 0
function Save-Shot {
    param([string]$Label = "shot")
    Focus-CaptureWindow
    $script:ShotNumber++
    $fileName = "{0:D2}-{1}.png" -f $script:ShotNumber, (Convert-ToSlug $Label)
    $path = Join-Path $OutDir $fileName
    $before = [CrudeShot.Native]::GetForegroundWindow()
    if ($before -ne $CaptureHwnd) {
        throw "capture aborted because the dedicated window lost focus"
    }
    & $NirCmd savescreenshotwin $path
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        if ((Test-Path $path) -and (Get-Item $path).Length -gt 0) {
            break
        }
        Start-Sleep -Milliseconds 50
    }
    $after = [CrudeShot.Native]::GetForegroundWindow()
    if ($after -ne $CaptureHwnd) {
        Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
        throw "capture discarded because another window became active"
    }
    if (-not (Test-Path $path) -or (Get-Item $path).Length -eq 0) {
        throw "NirCmd did not produce $path"
    }
    Write-Host "saved $path"
}

function Get-CellPoint {
    param([int]$Column, [int]$Row)
    if ($Column -lt 0 -or $Column -ge $Cols -or $Row -lt 0 -or $Row -ge $Rows) {
        throw "cell ($Column,$Row) is outside the ${Cols}x${Rows} grid"
    }
    $rect = [CrudeShot.Native]::ClientScreenRect($CaptureHwnd)
    $width = $rect.Right - $rect.Left
    $height = $rect.Bottom - $rect.Top
    if ($width -le 0 -or $height -le 0) {
        throw "dedicated window has invalid client geometry"
    }
    return [pscustomobject]@{
        X = $rect.Left + [int][Math]::Floor(($Column + 0.5) * $width / $Cols)
        Y = $rect.Top + [int][Math]::Floor(($Row + 0.5) * $height / $Rows)
    }
}

function Set-CursorToCell {
    param([int]$Column, [int]$Row)
    $point = Get-CellPoint $Column $Row
    [CrudeShot.Native]::MovePointer($point.X, $point.Y)
}

function Move-ToCell {
    param([int]$Column, [int]$Row)
    Focus-CaptureWindow
    Set-CursorToCell $Column $Row
}

function Send-MouseButton {
    param([int]$Button, [bool]$Down)
    $flag = switch ($Button) {
        1 { if ($Down) { 0x0002 } else { 0x0004 } }
        2 { if ($Down) { 0x0020 } else { 0x0040 } }
        3 { if ($Down) { 0x0008 } else { 0x0010 } }
        default { throw "unsupported mouse button $Button" }
    }
    [CrudeShot.Native]::mouse_event($flag, 0, 0, 0, [UIntPtr]::Zero)
}

function Send-MouseWheel {
    param([int]$Notches, [bool]$Horizontal)
    $flag = if ($Horizontal) { 0x1000 } else { 0x0800 }
    [CrudeShot.Native]::mouse_event($flag, 0, 0, $Notches * 120, [UIntPtr]::Zero)
}

function Get-VirtualKey {
    param([string]$Name)
    $named = @{
        "backspace" = 0x08; "tab" = 0x09; "return" = 0x0D; "enter" = 0x0D
        "escape" = 0x1B; "esc" = 0x1B; "capslock" = 0x14; "space" = 0x20; "pageup" = 0x21
        "pagedown" = 0x22; "end" = 0x23; "home" = 0x24; "left" = 0x25
        "up" = 0x26; "right" = 0x27; "down" = 0x28; "insert" = 0x2D
        "delete" = 0x2E; "f1" = 0x70; "f2" = 0x71; "f3" = 0x72
        "f4" = 0x73; "f5" = 0x74; "f6" = 0x75; "f7" = 0x76
        "f8" = 0x77; "f9" = 0x78; "f10" = 0x79; "f11" = 0x7A
        "f12" = 0x7B
    }
    $lower = $Name.ToLowerInvariant()
    if ($named.ContainsKey($lower)) {
        return [pscustomobject]@{ Key = [UInt16]$named[$lower]; Modifiers = @() }
    }
    if ($Name.Length -ne 1) {
        throw "unsupported key name $Name"
    }
    $character = $Name[0]
    $codePoint = [int]$character
    if ($codePoint -ge 0x61 -and $codePoint -le 0x7A) {
        return [pscustomobject]@{
            Key = [UInt16][char]::ToUpperInvariant($character)
            Modifiers = @()
        }
    }
    if ($codePoint -ge 0x41 -and $codePoint -le 0x5A) {
        return [pscustomobject]@{
            Key = [UInt16]$character
            Modifiers = @([UInt16]0x10)
        }
    }
    if ($codePoint -ge 0x30 -and $codePoint -le 0x39) {
        return [pscustomobject]@{ Key = [UInt16]$character; Modifiers = @() }
    }
    $scan = [CrudeShot.Native]::VkKeyScanW($character)
    if ($scan -eq -1) {
        throw "Windows has no virtual-key mapping for $Name"
    }
    $modifiers = @()
    $state = ($scan -shr 8) -band 0xFF
    if (($state -band 1) -ne 0) { $modifiers += [UInt16]0x10 }
    if (($state -band 2) -ne 0) { $modifiers += [UInt16]0x11 }
    if (($state -band 4) -ne 0) { $modifiers += [UInt16]0x12 }
    return [pscustomobject]@{ Key = [UInt16]($scan -band 0xFF); Modifiers = $modifiers }
}

function Send-KeySpec {
    param([string]$Spec)
    Focus-CaptureWindow
    $parts = $Spec.Split("+", [StringSplitOptions]::RemoveEmptyEntries)
    if ($parts.Count -eq 0) {
        throw "empty key specification"
    }
    $modifiers = @()
    $keyName = $parts[-1]
    foreach ($part in $parts[0..([Math]::Max(0, $parts.Count - 2))]) {
        if ($parts.Count -eq 1) {
            break
        }
        switch ($part.ToLowerInvariant()) {
            "ctrl" { $modifiers += [UInt16]0x11 }
            "control" { $modifiers += [UInt16]0x11 }
            "alt" { $modifiers += [UInt16]0x12 }
            "shift" { $modifiers += [UInt16]0x10 }
            "win" { $modifiers += [UInt16]0x5B }
            "super" { $modifiers += [UInt16]0x5B }
            default { throw "unsupported key modifier $part" }
        }
    }
    $resolved = Get-VirtualKey $keyName
    foreach ($implicit in $resolved.Modifiers) {
        if ($modifiers -notcontains $implicit) {
            $modifiers += $implicit
        }
    }
    [CrudeShot.Native]::SendChord([UInt16[]]$modifiers, [UInt16]$resolved.Key)
}

function Split-Numbers {
    param([string]$Value, [int]$Expected)
    $parts = $Value.Split(",")
    if ($parts.Count -ne $Expected) {
        throw "expected $Expected comma-separated values, got $Value"
    }
    return @($parts | ForEach-Object { [int]$_ })
}

try {
    try {
        $LockStream = [IO.File]::Open(
            $LockPath,
            [IO.FileMode]::OpenOrCreate,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
    } catch [IO.IOException] {
        throw "another scripts/tuishot.ps1 capture is already running"
    }

    $Wt = Find-Executable "" "wt.exe"
    $NirCmd = Find-Executable $env:NIRCMD "nircmd.exe"

    if ([string]::IsNullOrWhiteSpace($CrudeBin)) {
        Write-Host "== building crude =="
        . (Join-Path $PSScriptRoot "libghostty_env.ps1")
        Push-Location $Repo
        try {
            & go build -o $BuiltBin .
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed"
            }
        } finally {
            Pop-Location
        }
        $CrudeBin = $BuiltBin
    } else {
        $CrudeBin = (Resolve-Path $CrudeBin).Path
    }

    $launchText = @"
@echo off
set "NO_COLOR="
set "NO_COLOUR="
set "CLICOLOR="
set "COLORTERM=truecolor"
set "TERM=xterm-256color"
cd /d "$CrudeCwd" || exit /b 1
"$CrudeBin" $CrudeArgs 2>>"$Log"
echo CRUDE-EXITED rc=%ERRORLEVEL%>>"$Log"
ping -n 900 127.0.0.1 >nul
"@
    [IO.File]::WriteAllText($Launch, $launchText, [Text.ASCIIEncoding]::new())

    $before = @{}
    foreach ($window in Get-WindowsTerminalWindows) {
        $before[$window.Handle.ToInt64()] = $true
    }

    $wtArguments = @(
        "-w", $WindowName,
        "--pos", "0,0",
        "--size", "$Cols,$Rows",
        "--focus",
        "new-tab",
        "--title", $WindowName,
        "--suppressApplicationTitle",
        "cmd.exe", "/d", "/q", "/c", $Launch
    )
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Wt
    $startInfo.UseShellExecute = $false
    foreach ($argument in $wtArguments) {
        [void]$startInfo.ArgumentList.Add($argument)
    }
    [void][Diagnostics.Process]::Start($startInfo)

    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        $candidates = @(
            Get-WindowsTerminalWindows | Where-Object {
                -not $before.ContainsKey($_.Handle.ToInt64())
            }
        )
        $titled = @($candidates | Where-Object { $_.Title -eq $WindowName })
        if ($titled.Count -eq 1) {
            $CaptureHwnd = $titled[0].Handle
            break
        }
        if ($titled.Count -gt 1) {
            throw "multiple Windows Terminal windows claimed the unique capture title"
        }
        Start-Sleep -Milliseconds 125
    }
    if ($CaptureHwnd -eq [IntPtr]::Zero) {
        throw "Windows Terminal did not create a dedicated window"
    }

    Focus-CaptureWindow
    $client = [CrudeShot.Native]::ClientScreenRect($CaptureHwnd)
    $clientWidth = $client.Right - $client.Left
    $clientHeight = $client.Bottom - $client.Top
    Write-Host ("window=0x{0:X} client={1}x{2}+{3}+{4} grid={5}x{6}" -f
        $CaptureHwnd.ToInt64(), $clientWidth, $clientHeight,
        $client.Left, $client.Top, $Cols, $Rows)

    Wait-Seconds $PaintWait
    Save-Shot "boot"

    foreach ($step in $Steps) {
        if ($step -match "^key:(.*)$") {
            $spec = $Matches[1]
            Send-KeySpec $spec
            Wait-Seconds $StepWait
            Save-Shot "key-$spec"
        } elseif ($step -match "^type:(.*)$") {
            Focus-CaptureWindow
            [CrudeShot.Native]::SendUnicode($Matches[1])
            Wait-Seconds $StepWait
            Save-Shot "type-$($Matches[1])"
        } elseif ($step -match "^click:(.*)$") {
            $parts = $Matches[1].Split(",")
            if ($parts.Count -lt 2 -or $parts.Count -gt 3) {
                throw "click expects col,row[,button]"
            }
            $button = if ($parts.Count -eq 3) { [int]$parts[2] } else { 1 }
            Move-ToCell ([int]$parts[0]) ([int]$parts[1])
            Send-MouseButton $button $true
            Send-MouseButton $button $false
            Wait-Seconds $StepWait
            Save-Shot "click-$($Matches[1])"
        } elseif ($step -match "^move:(.*)$") {
            $values = Split-Numbers $Matches[1] 2
            Move-ToCell $values[0] $values[1]
            Wait-Seconds $StepWait
            Save-Shot "move-$($Matches[1])"
        } elseif ($step -match "^drag:(.*)$") {
            $values = Split-Numbers $Matches[1] 4
            Focus-CaptureWindow
            Set-CursorToCell $values[0] $values[1]
            Send-MouseButton 1 $true
            Wait-Seconds 0.25
            Set-CursorToCell $values[2] $values[3]
            Wait-Seconds 0.25
            Save-Shot "dragheld-$($Matches[1])"
            Send-MouseButton 1 $false
            Wait-Seconds $StepWait
            Save-Shot "drag-$($Matches[1])"
        } elseif ($step -match "^dragburst:(.*)$") {
            $values = Split-Numbers $Matches[1] 5
            $count = [Math]::Max(1, $values[4])
            Focus-CaptureWindow
            Set-CursorToCell $values[0] $values[1]
            Send-MouseButton 1 $true
            for ($index = 1; $index -le $count; $index++) {
                $column = $values[0] + [int](($values[2] - $values[0]) * $index / $count)
                $row = $values[1] + [int](($values[3] - $values[1]) * $index / $count)
                Set-CursorToCell $column $row
            }
            Send-MouseButton 1 $false
            Wait-Seconds $StepWait
            Save-Shot "dragburst-$($Matches[1])"
        } elseif ($step -match "^wheel:(.*)$") {
            $values = Split-Numbers $Matches[1] 3
            Move-ToCell $values[0] $values[1]
            Send-MouseWheel (-$values[2]) $false
            Wait-Seconds $StepWait
            Save-Shot "wheel-$($Matches[1])"
        } elseif ($step -match "^hwheel:(.*)$") {
            $values = Split-Numbers $Matches[1] 3
            Move-ToCell $values[0] $values[1]
            Send-MouseWheel $values[2] $true
            Wait-Seconds $StepWait
            Save-Shot "hwheel-$($Matches[1])"
        } elseif ($step -match "^sleep:(.*)$") {
            Wait-Seconds ([double]$Matches[1])
        } elseif ($step -eq "shot") {
            Save-Shot "extra"
        } elseif ($step -match "^shot:(.*)$") {
            Save-Shot $Matches[1]
        } else {
            throw "unknown step: $step"
        }
    }

    Write-Host "== crude stderr tail =="
    if (Test-Path $Log) {
        Get-Content $Log -Tail 30
    }
} finally {
    if ($CaptureHwnd -ne [IntPtr]::Zero -and -not $KeepOpen) {
        [CrudeShot.Native]::PostMessage(
            $CaptureHwnd,
            [CrudeShot.Native]::WM_CLOSE,
            [IntPtr]::Zero,
            [IntPtr]::Zero
        ) | Out-Null
        for ($attempt = 0; $attempt -lt 40 -and [CrudeShot.Native]::IsWindow($CaptureHwnd); $attempt++) {
            Start-Sleep -Milliseconds 125
        }
        if ([CrudeShot.Native]::IsWindow($CaptureHwnd)) {
            $handleText = "0x{0:X}" -f $CaptureHwnd.ToInt64()
            & $NirCmd win close handle $handleText
        }
    } elseif ($CaptureHwnd -ne [IntPtr]::Zero) {
        Write-Host ("KEEP_OPEN=1: close the dedicated window handle 0x{0:X} when review is complete" -f $CaptureHwnd.ToInt64())
    }
    Remove-Item -LiteralPath $Launch -Force -ErrorAction SilentlyContinue
    if ($null -ne $LockStream) {
        $LockStream.Dispose()
    }
}
