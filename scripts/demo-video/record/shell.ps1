Set-PSReadLineOption -PredictionSource None -ErrorAction SilentlyContinue
Set-PSReadLineOption -HistorySaveStyle SaveNothing -ErrorAction SilentlyContinue
Set-PSReadLineOption -Colors @{ Command = "`e[38;2;126;231;135m"; Parameter = "`e[38;2;165;214;255m"; String = "`e[38;2;165;214;255m" } -ErrorAction SilentlyContinue
function prompt {
  $b = (git branch --show-current 2>$null)
  "`e[38;2;121;192;255m$((Get-Item .).Name)`e[0m `e[38;2;210;168;255m`u{e0a0} $b`e[0m `e[38;2;240;136;62m`u{276f}`e[0m "
}
Clear-Host
