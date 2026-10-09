$ErrorActionPreference = "Stop"

$script:RepoRoot = Split-Path -Parent $PSScriptRoot

function Import-LocalEnv {
    $envFile = Join-Path $script:RepoRoot ".env"
    if (-not (Test-Path $envFile)) { return }
    Get-Content $envFile | ForEach-Object {
        if ($_ -match '^\s*#' -or $_ -notmatch '=') { return }
        $name, $value = $_.Split('=', 2)
        if (-not [string]::IsNullOrWhiteSpace($name) -and -not (Test-Path "Env:$name")) {
            Set-Item -Path "Env:$name" -Value $value.Trim()
        }
    }
}

function Add-LocalFfmpegPath {
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
        [System.Environment]::GetEnvironmentVariable("Path", "User")
    $wingetFfmpeg = Get-ChildItem -Path "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" -Recurse -Filter ffmpeg.exe -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($wingetFfmpeg) {
        $env:Path = $wingetFfmpeg.DirectoryName + ";" + $env:Path
    }
}

function Stop-ListenPort {
    param([int]$Port)

    $pids = @()
    try {
        $pids += @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
            Select-Object -ExpandProperty OwningProcess)
    } catch {}

    if (-not $pids) {
        foreach ($line in (netstat -ano | Select-String ":$Port\s+.*LISTENING")) {
            if ($line.Line -match '\s(\d+)\s*$') {
                $pids += [int]$Matches[1]
            }
        }
    }

    # Killing these takes Docker Desktop down with them: when a container
    # publishes the port, it is Docker that listens on it.
    $keep = "com.docker.backend", "com.docker.service", "wslrelay", "wslhost", "vpnkit", "Docker Desktop", "dockerd"

    foreach ($procId in ($pids | Select-Object -Unique)) {
        if ($procId -le 4) { continue }
        $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if (-not $proc) { continue }
        if ($keep -contains $proc.ProcessName) {
            Write-Warning "port $Port is held by Docker ($($proc.ProcessName)). Stop the container instead: docker compose down"
            continue
        }
        Write-Host "stopping PID $procId ($($proc.ProcessName)) on port $Port"
        Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
    }
}

function Stop-LocalAuraProcesses {
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
        $_.CommandLine -and (
            $_.CommandLine -like "*aura-server*cmd\api*" -or
            $_.CommandLine -like "*aura-server*cmd/api*" -or
            ($_.CommandLine -like "*uvicorn*" -and $_.CommandLine -like "*app:app*")
        )
    } | ForEach-Object {
        Write-Host "stopping leftover PID $($_.ProcessId) ($($_.Name))"
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

function Stop-LocalAura {
    Write-Host "stopping old local API/ASR"
    Stop-LocalAuraProcesses
    Stop-ListenPort 8080
    Stop-ListenPort 8090
    Start-Sleep -Seconds 1
    Stop-ListenPort 8080
    Stop-ListenPort 8090
}

function Start-LocalLlm {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker is not installed, and vLLM has no native Windows build. Install Docker Desktop or run without -Llm."
    }
    docker info 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Docker is not running. Start Docker Desktop and wait until it is ready." }

    Write-Host "starting vLLM in Docker (first run downloads the image and the model)"
    Set-Location $script:RepoRoot
    # --no-deps: ASR runs on the host here, not in its container.
    docker compose up -d --no-deps llm
    if ($LASTEXITCODE -ne 0) { throw "docker compose up llm failed" }

    # ASR and vLLM share one GPU; on 16 GB cards they do not fit together.
    Wait-UrlOk -Url "http://127.0.0.1:8000/health" -Name "LLM" -TimeoutSec 1800 -Check {
        $state = (docker compose ps -a --format "{{.Service}} {{.State}}" | Select-String '^llm ')
        if ($state -and $state -match 'exited') {
            docker compose logs --tail 20 llm
            throw "vLLM container exited. Out of GPU memory? Lower LLM_GPU_MEMORY_UTILIZATION and LLM_MAX_MODEL_LEN in .env, or keep ASR off the GPU."
        }
    }
}

function Stop-LocalLlm {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { return }
    docker info 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { return }
    Set-Location $script:RepoRoot
    $running = docker compose ps --format "{{.Service}}" | Select-String '^llm$'
    if ($running) {
        Write-Host "stopping vLLM container"
        docker compose stop llm | Out-Null
    }
}

function Wait-UrlOk {
    param(
        [string]$Url,
        [string]$Name,
        [int]$TimeoutSec = 300,
        [scriptblock]$Ready = { param($Response) $Response.StatusCode -eq 200 },
        # Runs on every attempt; throws when waiting further is pointless.
        [scriptblock]$Check = $null
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    Write-Host "waiting for $Name ($Url)"
    while ((Get-Date) -lt $deadline) {
        if ($Check) { & $Check }
        try {
            $resp = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 3
            if (& $Ready $resp) {
                Write-Host "$Name ready"
                return
            }
        } catch {
            $msg = $_.Exception.Message
            if ($msg -match '503') {
                Write-Host "  $Name still loading"
            }
        }
        Start-Sleep -Seconds 2
    }
    throw "$Name did not become ready in ${TimeoutSec}s: $Url"
}
