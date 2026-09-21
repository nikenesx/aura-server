param(
    [switch]$Stop,
    [switch]$Rebuild
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "lib-local.ps1")
Set-Location $script:RepoRoot

function Assert-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker is not installed. Install Docker Desktop, enable WSL2 and NVIDIA GPU support."
    }
    docker info 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker is not running. Start Docker Desktop and wait until it is ready."
    }
}

if ($Stop) {
    Assert-Docker
    docker compose down
    Write-Host "docker stack stopped"
    return
}

Assert-Docker

$envFile = Join-Path $script:RepoRoot ".env"
if (-not (Test-Path $envFile)) {
    Copy-Item (Join-Path $script:RepoRoot ".env.example") $envFile
    Write-Host "created .env from .env.example"
}

Import-LocalEnv
if (-not $env:HF_TOKEN -and -not $env:HUGGINGFACE_TOKEN) {
    throw "HF_TOKEN is empty. Put it into .env after accepting the pyannote licenses."
}

Stop-LocalAura

Write-Host "building and starting API + ASR in Docker"
Write-Host "first run downloads CUDA/PyTorch and model weights; this can take a while"
if ($Rebuild) {
    docker compose build --no-cache
    if ($LASTEXITCODE -ne 0) { throw "docker compose build failed" }
}
docker compose up --build -d --wait --wait-timeout 900
if ($LASTEXITCODE -ne 0) { throw "docker compose up failed" }

Wait-UrlOk -Url "http://127.0.0.1:8080/health" -Name "API" -TimeoutSec 60 -Ready {
    param($Response)
    if ($Response.StatusCode -ne 200) { return $false }
    $body = $Response.Content | ConvertFrom-Json
    return $body.asr -eq "ok"
}

Write-Host ""
Write-Host "docker stack is ready"
Write-Host "  API  http://127.0.0.1:8080"
Write-Host "Logs:  docker compose logs -f"
Write-Host "Stop:  .\scripts\run-docker.ps1 -Stop"
Write-Host ""
Write-Host "curl.exe -D - -fsS -F file=@recording.wav http://127.0.0.1:8080/v1/transcribe"
Write-Host "curl.exe -fsS http://127.0.0.1:8080/v1/transcribe/JOB_ID"
