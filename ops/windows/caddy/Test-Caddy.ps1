#requires -Version 5.1
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'

# Static-only checks; see ops/windows/api/Test-Api.ps1 for the same pattern
# and its rationale relative to ops/windows/whisper/Test-Whisper.ps1.
$files = @(Get-ChildItem -LiteralPath $PSScriptRoot -File | Where-Object Extension -in '.ps1', '.psm1')
foreach ($file in $files) {
    $tokens = $null; $errors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($file.FullName, [ref]$tokens, [ref]$errors)
    if ($errors.Count) { throw "PowerShell parser failed: $($file.Name)" }
    $text = Get-Content -LiteralPath $file.FullName -Raw
    $forbidden = @(('Invoke' + '-Expression'), ('Stop' + '-Process'), ('task' + 'kill'))
    $ast = [Management.Automation.Language.Parser]::ParseFile($file.FullName, [ref]$tokens, [ref]$errors)
    $commands = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.CommandAst] }, $true)
    foreach ($command in $commands) { if ($command.GetCommandName() -in $forbidden) { throw "Forbidden command: $($file.Name)" } }
    if ($text -match 'C:\\Users\\User|192\.168\.') { throw "Hardcoded private path/address: $($file.Name)" }
}
Write-Output "Parser/static checks passed: $($files.Count) PowerShell files."
