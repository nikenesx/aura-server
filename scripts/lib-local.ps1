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

    foreach ($procId in ($pids | Select-Object -Unique)) {
        if ($procId -le 4) { continue }
        $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if (-not $proc) { continue }
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

function Wait-UrlOk {
    param(
        [string]$Url,
        [string]$Name,
        [int]$TimeoutSec = 300,
        [scriptblock]$Ready = { param($Response) $Response.StatusCode -eq 200 }
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    Write-Host "waiting for $Name ($Url)"
    while ((Get-Date) -lt $deadline) {
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
