$ErrorActionPreference = "Stop"

$env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
    [System.Environment]::GetEnvironmentVariable("Path", "User")

$Root = Split-Path -Parent $PSScriptRoot
$Asr = Join-Path $Root "asr"
$Venv = Join-Path $Asr ".venv"

function Find-FFmpeg {
    $cmd = Get-Command ffmpeg -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $found = Get-ChildItem -Path "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" -Recurse -Filter ffmpeg.exe -ErrorAction SilentlyContinue |
        Select-Object -First 1 -ExpandProperty FullName
    if ($found) {
        $env:Path = (Split-Path $found) + ";" + $env:Path
        return $found
    }
    return $null
}

function Find-Python312 {
    $cmd = Get-Command py -ErrorAction SilentlyContinue
    if ($cmd) {
        $exe = & py -3.12 -c "import sys; print(sys.executable)" 2>$null
        if ($LASTEXITCODE -eq 0 -and $exe) { return $exe.Trim() }
    }
    $fallback = "$env:LocalAppData\Programs\Python\Python312\python.exe"
    if (Test-Path $fallback) { return $fallback }
    return $null
}

Write-Host "checking NVIDIA driver"
nvidia-smi | Out-Host

$python = Find-Python312
if (-not $python) {
    throw "Python 3.12 not found. Install: winget install Python.Python.3.12"
}
$ffmpeg = Find-FFmpeg
if (-not $ffmpeg) {
    throw "ffmpeg not found. Install: winget install Gyan.FFmpeg then close and reopen the terminal."
}
Write-Host "ffmpeg $ffmpeg"

Write-Host "using $python"
Get-CimInstance Win32_Process -Filter "Name='python.exe'" | Where-Object {
    $_.CommandLine -and $_.CommandLine -like "*aura-server*asr*" -or ($_.CommandLine -like "*uvicorn app:app*")
} | ForEach-Object {
    Write-Host "stopping leftover ASR PID $($_.ProcessId)"
    Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Seconds 1

if (-not (Test-Path (Join-Path $Venv "Scripts\python.exe"))) {
    & $python -m venv $Venv
}

$venvPy = Join-Path $Venv "Scripts\python.exe"
& $venvPy -m pip install --upgrade pip
if ($LASTEXITCODE -ne 0) { throw "pip upgrade failed" }
& $venvPy -m pip install torch torchaudio --index-url https://download.pytorch.org/whl/cu128
if ($LASTEXITCODE -ne 0) { throw "torch install failed" }
& $venvPy -m pip install -r (Join-Path $Asr "requirements.txt")
if ($LASTEXITCODE -ne 0) { throw "ASR requirements install failed" }
& $venvPy -c "import torch, gigaam, pyannote.audio; print('cuda', torch.cuda.is_available(), torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'cpu')"
if ($LASTEXITCODE -ne 0) { throw "import check failed" }

Write-Host ""
Write-Host "Accept Hugging Face licenses, then put HF_TOKEN into .env:"
Write-Host "  https://huggingface.co/pyannote/speaker-diarization-3.1"
Write-Host "  https://huggingface.co/pyannote/segmentation-3.0"
Write-Host ""
Write-Host "Start both on the host: .\scripts\run-local.ps1"
Write-Host "Or use Docker instead:  .\scripts\run-docker.ps1"
