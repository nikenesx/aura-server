param(
    [switch]$Stop
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "lib-local.ps1")

if ($Stop) {
    Stop-LocalAura
    Write-Host "local API/ASR stopped"
    return
}

Add-LocalFfmpegPath
Import-LocalEnv

$venvPy = Join-Path $script:RepoRoot "asr\.venv\Scripts\python.exe"
if (-not (Test-Path $venvPy)) {
    throw "ASR venv missing. Run scripts\setup-local-gpu.ps1 first."
}

Stop-LocalAura

$asrWindow = Start-Process -FilePath "powershell.exe" -WorkingDirectory $script:RepoRoot -PassThru -ArgumentList @(
    "-NoExit",
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", (Join-Path $PSScriptRoot "run-local-asr.ps1")
)
$apiWindow = $null
try {
    Wait-UrlOk -Url "http://127.0.0.1:8090/health" -Name "ASR" -TimeoutSec 600

    $apiWindow = Start-Process -FilePath "powershell.exe" -WorkingDirectory $script:RepoRoot -PassThru -ArgumentList @(
        "-NoExit",
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-File", (Join-Path $PSScriptRoot "run-local-api.ps1")
    )
    Wait-UrlOk -Url "http://127.0.0.1:8080/health" -Name "API" -TimeoutSec 60 -Ready {
        param($Response)
        if ($Response.StatusCode -ne 200) { return $false }
        $body = $Response.Content | ConvertFrom-Json
        return $body.asr -eq "ok"
    }
} catch {
    if ($apiWindow -and -not $apiWindow.HasExited) {
        Stop-Process -Id $apiWindow.Id -Force -ErrorAction SilentlyContinue
    }
    if ($asrWindow -and -not $asrWindow.HasExited) {
        Stop-Process -Id $asrWindow.Id -Force -ErrorAction SilentlyContinue
    }
    Stop-LocalAura
    throw
}

Write-Host ""
Write-Host "local stack is fresh"
Write-Host "  API  http://127.0.0.1:8080"
Write-Host "  ASR  http://127.0.0.1:8090"
Write-Host "Logs are in the two new PowerShell windows. Close them or run:"
Write-Host "  .\scripts\run-local.ps1 -Stop"
Write-Host ""
Write-Host "curl.exe -D - -fsS -F file=@recording.wav http://127.0.0.1:8080/v1/transcribe"
Write-Host "curl.exe -fsS http://127.0.0.1:8080/v1/transcribe/JOB_ID"
