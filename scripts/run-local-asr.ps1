$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "lib-local.ps1")
try { $Host.UI.RawUI.WindowTitle = "aura-asr" } catch {}

Add-LocalFfmpegPath
Import-LocalEnv

$venvPy = Join-Path $script:RepoRoot "asr\.venv\Scripts\python.exe"
if (-not (Test-Path $venvPy)) {
    throw "ASR venv missing. Run scripts\setup-local-gpu.ps1 first."
}

if (-not $env:HF_TOKEN -and -not $env:HUGGINGFACE_TOKEN) {
    Write-Warning "HF_TOKEN is empty. pyannote will fail until you accept the model licenses and set the token."
}

$env:GIGAAM_MODEL = if ($env:GIGAAM_MODEL) { $env:GIGAAM_MODEL } else { "v3_e2e_rnnt" }
$env:DIAR_MODEL = if ($env:DIAR_MODEL) { $env:DIAR_MODEL } else { "pyannote/speaker-diarization-3.1" }
$env:DIAR_SPEAKERS = if ($env:DIAR_SPEAKERS) { $env:DIAR_SPEAKERS } else { "2" }

Stop-ListenPort 8090
Write-Host "starting ASR on 127.0.0.1:8090"
Set-Location (Join-Path $script:RepoRoot "asr")
& $venvPy -m uvicorn app:app --host 127.0.0.1 --port 8090
