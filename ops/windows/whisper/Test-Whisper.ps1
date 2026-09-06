#requires -Version 5.1
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'
$files = @(Get-ChildItem -LiteralPath $PSScriptRoot -Recurse -File | Where-Object Extension -in '.ps1','.psm1')
foreach ($file in $files) {
    $tokens=$null; $errors=$null
    $ast=[Management.Automation.Language.Parser]::ParseFile($file.FullName,[ref]$tokens,[ref]$errors)
    if ($errors.Count) { throw "PowerShell parser failed: $($file.Name)" }
    $text=Get-Content -LiteralPath $file.FullName -Raw
    $forbidden=@(('Invoke'+'-Expression'),('Stop'+'-Process'),('task'+'kill'))
    $commands=$ast.FindAll({param($node) $node -is [Management.Automation.Language.CommandAst]},$true)
    foreach($command in $commands) { if($command.GetCommandName() -in $forbidden) { throw "Forbidden command: $($file.Name)" } }
    if ($text -match 'C:\\Users\\User|192\.168\.') { throw "Hardcoded private path/address: $($file.Name)" }
}
Write-Output "Parser/static checks passed: $($files.Count) PowerShell files."
Import-Module Pester -MinimumVersion 3.4 -ErrorAction Stop
$result=Invoke-Pester -Script (Join-Path $PSScriptRoot 'tests\WhisperRuntime.Tests.ps1') -PassThru
if ($result.FailedCount) { throw "$($result.FailedCount) PowerShell tests failed." }
Write-Output "PowerShell tests: $($result.PassedCount) passed; $($result.FailedCount) failed; $($result.SkippedCount) skipped."
