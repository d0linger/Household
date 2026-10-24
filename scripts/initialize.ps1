param([switch]$Development)
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskSecrets = Join-Path $taskRoot '.local/secrets'
if (Test-Path -LiteralPath $taskSecrets) {throw 'Secrets already exist. Preserve them; use the recovery runbook for rotation.'}
New-Item -ItemType Directory -Path $taskSecrets -Force | Out-Null
function New-HouseholdSecret {
    $taskBytes = New-Object byte[] 36
    $taskGenerator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {$taskGenerator.GetBytes($taskBytes)} finally {$taskGenerator.Dispose()}
    return [Convert]::ToBase64String($taskBytes).Replace('+','-').Replace('/','_').TrimEnd('=')
}
$taskValues = @{}
foreach ($taskName in @('postgres_password','owner_password','app_password','csrf_secret','encryption_key','rate_secret','bootstrap_password','restic_password')) {
    $taskValues[$taskName] = New-HouseholdSecret
    [IO.File]::WriteAllText((Join-Path $taskSecrets $taskName),$taskValues[$taskName])
}
[IO.File]::WriteAllText((Join-Path $taskSecrets 'app_database_url'),"postgres://household_app:$($taskValues['app_password'])@db:5432/household?sslmode=disable")
[IO.File]::WriteAllText((Join-Path $taskSecrets 'owner_database_url'),"postgres://household_owner:$($taskValues['owner_password'])@db:5432/household?sslmode=disable")
[IO.File]::WriteAllText((Join-Path $taskSecrets 'restore_database_url'),"postgres://household_owner:$($taskValues['owner_password'])@restore-db:5432/household_restore?sslmode=disable")
if ($env:OS -eq 'Windows_NT') {
    $taskIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $taskACL = Get-Acl -LiteralPath $taskSecrets
    $taskACL.SetAccessRuleProtection($true,$false)
    $taskRule = New-Object Security.AccessControl.FileSystemAccessRule($taskIdentity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
    $taskACL.AddAccessRule($taskRule)
    Set-Acl -LiteralPath $taskSecrets -AclObject $taskACL
}
$taskEnvPath = Join-Path $taskRoot '.env'
if (!(Test-Path -LiteralPath $taskEnvPath)) {
    if ($Development) {[IO.File]::WriteAllText($taskEnvPath,"HOUSEHOLD_ENV=development`nHOUSEHOLD_ORIGIN=http://localhost:8100`nHOUSEHOLD_PORT=8100`nHOUSEHOLD_NAME=Mein Haushalt`n")}
    else {Copy-Item -LiteralPath (Join-Path $taskRoot '.env.example') -Destination $taskEnvPath}
}
Write-Output 'Secrets created under .local/secrets. Read bootstrap_password locally for the first login; change it immediately. Do not commit or share these files.'
