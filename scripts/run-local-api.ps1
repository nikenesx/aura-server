$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "lib-local.ps1")
try { $Host.UI.RawUI.WindowTitle = "aura-api" } catch {}

Import-LocalEnv

$env:ASR_URL = if ($env:ASR_URL) { $env:ASR_URL } else { "http://127.0.0.1:8090" }
$env:HTTP_ADDR = if ($env:HTTP_ADDR) { $env:HTTP_ADDR } else { "127.0.0.1:8080" }

Stop-ListenPort 8080
Write-Host "starting API on $($env:HTTP_ADDR)"
Set-Location $script:RepoRoot
go run ./cmd/api
