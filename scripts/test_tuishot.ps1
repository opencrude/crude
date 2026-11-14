# Exercise the production capture waits without sending desktop input.
$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
# Resolve the same enums used by the live text-grid measurement.
[void][Windows.Automation.Text.TextPatternRangeEndpoint]::Start
[void][Windows.Automation.Text.TextUnit]::Character
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile(
    (Join-Path $PSScriptRoot "tuishot.ps1"), [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
foreach ($name in @("Test-CaptureSteps", "Assert-CaptureRunning", "Wait-CaptureBoundary", "Get-CellPoint")) {
    $definition = $ast.Find({ param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name
    }, $false)
    if ($null -eq $definition) { throw "missing production function: $name" }
    . ([scriptblock]::Create($definition.Extent.Text))
}
# For lifecycle tests, only the native window query is substituted. All file,
# marker, timeout and product-exit checks execute the production functions above.
Add-Type @'
namespace CrudeShot {
    public static class Native {
        public static bool Open = true;
        public static bool IsWindow(System.IntPtr window) { return Open; }
    }
}
'@
function Expect-Failure {
    param([scriptblock]$Action, [string]$Message)
    $caught = $null
    try { & $Action } catch { $caught = $_.Exception.Message }
    if ($null -eq $caught -or -not $caught.Contains($Message)) {
        throw "expected '$Message', got '$caught'"
    }
}
$root = Join-Path ([IO.Path]::GetTempPath()) ("crude-capture-tests-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory $root | Out-Null
$previousEvidence = $env:CRUDE_CAPTURE_EVIDENCE
try {
    $env:CRUDE_CAPTURE_EVIDENCE = Join-Path $root "evidence.txt"
    $Log = Join-Path $root "product.log"
    $CaptureHwnd = [IntPtr]1
    [IO.File]::WriteAllText($Log, "")
    Test-CaptureSteps @("key:F3", "await:READY phase=1", "quit:F9")
    Expect-Failure { Test-CaptureSteps @("quit:F9", "key:F4") } "final capture step"
    [IO.File]::WriteAllText($env:CRUDE_CAPTURE_EVIDENCE, "READY phase=1`n")
    Expect-Failure { Test-CaptureSteps @() } "empty or new"
    Wait-CaptureBoundary -Marker "READY phase=1" -TimeoutMilliseconds 0
    Expect-Failure { Wait-CaptureBoundary -Marker "READY" -TimeoutMilliseconds 0 } "did not publish"
    Expect-Failure { Wait-CaptureBoundary -Marker "ready phase=1" -TimeoutMilliseconds 0 } "did not publish"
    [CrudeShot.Native]::Open = $false
    Expect-Failure { Wait-CaptureBoundary -Marker "READY phase=1" -TimeoutMilliseconds 0 } "no longer exists"
    [CrudeShot.Native]::Open = $true
    [IO.File]::WriteAllText($Log, "CRUDE-EXITED rc=0`n")
    Expect-Failure { Wait-CaptureBoundary -Marker "READY phase=1" -TimeoutMilliseconds 0 } "exited before"
    Wait-CaptureBoundary -Exit -TimeoutMilliseconds 0
    [IO.File]::WriteAllText($Log, "CRUDE-EXITED rc=1`n")
    Expect-Failure { Wait-CaptureBoundary -Exit -TimeoutMilliseconds 0 } "did not exit cleanly"
    [IO.File]::WriteAllText($Log, "")
    Expect-Failure { Wait-CaptureBoundary -Exit -TimeoutMilliseconds 0 } "was not observed"
    $env:CRUDE_CAPTURE_EVIDENCE = ""
    Expect-Failure { Test-CaptureSteps @("await:READY") } "requires CRUDE_CAPTURE_EVIDENCE"
    Expect-Failure { Wait-CaptureBoundary -Marker "READY" -TimeoutMilliseconds 0 } "requires CRUDE_CAPTURE_EVIDENCE"
    # Execute the actual launcher's exit-recording line in cmd.exe. A digit
    # immediately before >> is a handle redirect, not part of the exit value.
    $exitLine = @(Get-Content (Join-Path $PSScriptRoot "tuishot.ps1") | Where-Object {
        $_ -cmatch 'CRUDE-EXITED rc=%ERRORLEVEL%'
    })
    if ($exitLine.Count -ne 1) { throw "launcher must have one exit record" }
    $batch = Join-Path $root "exit.cmd"
    $expanded = $ExecutionContext.InvokeCommand.ExpandString($exitLine[0])
    [IO.File]::WriteAllText($batch, "@echo off`r`ncmd /d /c exit 7`r`n$expanded`r`n")
    & $env:ComSpec /d /c $batch | Out-Null
    if (@(Get-Content -LiteralPath $Log) -cnotcontains "CRUDE-EXITED rc=7") {
        throw "launcher lost the real product exit code"
    }
    # A padded, scaled viewport on a monitor left of the main display must not
    # use the top-level window's origin or stretch cells to its client bounds.
    function Get-CaptureGrid {
        [pscustomobject]@{ Left = -1888; Top = 9; CellWidth = 12.5; CellHeight = 21 }
    }
    $Cols = 160; $Rows = 40
    $point = Get-CellPoint 11 6
    if ($point.X -ne -1745 -or $point.Y -ne 145) { throw "cell did not use measured grid geometry: $point" }
    Expect-Failure { Get-CellPoint 160 0 } "outside"
    Expect-Failure { Get-CellPoint 0 -1 } "outside"
    Write-Host "WINDOWS_CAPTURE_DRIVER_TESTS_PASSED"
} finally {
    $env:CRUDE_CAPTURE_EVIDENCE = $previousEvidence
    Remove-Item -LiteralPath $root -Recurse -Force
}
