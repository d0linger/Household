# Creates .local/secrets and .env through `household init` (Go, or Docker without Go),
# then restricts the secrets folder to the current Windows account.
# Usage: ./scripts/initialize.ps1 [-Development] [-EnvTemplate .env.all.example]
param([switch]$Development,[string]$EnvTemplate='.env.example')
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskArguments = @('init','--env-template',$EnvTemplate)
if ($Development) {$taskArguments += '--development'}
Push-Location $taskRoot
try {
    if (Get-Command go -ErrorAction SilentlyContinue) {
        & go run ./cmd/household @taskArguments
    } else {
        docker build --quiet --target runtime -t household:local . | Out-Null
        if ($LASTEXITCODE) {throw 'Image build failed'}
        & docker run --rm --read-only -v "${taskRoot}:/work" -w /work household:local @taskArguments
    }
    if ($LASTEXITCODE) {throw 'household init failed'}
} finally {Pop-Location}
if ($env:OS -eq 'Windows_NT') {
    $taskSecrets = Join-Path $taskRoot '.local/secrets'
    $taskIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $taskACL = Get-Acl -LiteralPath $taskSecrets
    $taskACL.SetAccessRuleProtection($true,$false)
    $taskRule = New-Object Security.AccessControl.FileSystemAccessRule($taskIdentity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
    $taskACL.AddAccessRule($taskRule)
    Set-Acl -LiteralPath $taskSecrets -AclObject $taskACL
}
