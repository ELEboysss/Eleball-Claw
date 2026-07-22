# claw local dev one-click launcher (build from source, no CDN needed)
#
# Usage:
#   .\dev-run.ps1                # default port 8090
#   .\dev-run.ps1 -Port 18090    # custom port
#
# Auto-verifies /health; Ctrl-C stops claw.
# Optional env (set before launch): $env:JWT_SECRET / $env:RELAY_URL / $env:CLAW_RELAY_TOKEN / $env:CLAW_DEVICE_ID
param([int]$Port = 8090)
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$Gateway = Join-Path $ScriptDir "gateway"
$Go = Join-Path $RepoRoot ".tools\go\bin\go.exe"

# Go env: caches on project disk to avoid C: drive exhaustion
$env:TMP = Join-Path $RepoRoot ".tools\tmp"
$env:TEMP = $env:TMP
$env:GOMODCACHE = Join-Path $RepoRoot ".tools\gomodcache"
$env:GOCACHE = Join-Path $RepoRoot ".tools\gocache"
$env:GOPROXY = "https://goproxy.cn,direct"

if (-not (Test-Path $Go)) {
    Write-Host "ERROR: Go not found at $Go (ensure main repo .tools\go is present)" -ForegroundColor Red
    exit 1
}

New-Item -ItemType Directory -Force -Path (Join-Path $Gateway "data") | Out-Null
Set-Location $Gateway

$LogPath = Join-Path $env:TEMP "claw-dev.log"
Write-Host ">> Building and starting claw-server (port $Port)..." -ForegroundColor Cyan
Write-Host "   /health: http://localhost:$Port/health"
Write-Host "   log: $LogPath"

$proc = Start-Process -FilePath $Go `
    -ArgumentList "run", "./cmd/claw-server", "serve", "--port=$Port" `
    -PassThru -NoNewWindow `
    -RedirectStandardOutput $LogPath -RedirectStandardError "$LogPath.err"

try {
    Write-Host ">> Waiting for startup..." -ForegroundColor Yellow
    $ok = $false
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        if ($proc.HasExited) {
            Write-Host "ERROR: claw process exited. Log:" -ForegroundColor Red
            Get-Content $LogPath -Tail 20 -ErrorAction SilentlyContinue
            Get-Content "$LogPath.err" -Tail 20 -ErrorAction SilentlyContinue
            exit 1
        }
        try {
            $r = Invoke-RestMethod "http://localhost:$Port/health" -TimeoutSec 2
            if ($r.code -eq 0) {
                Write-Host "OK: claw started at http://localhost:$Port/health" -ForegroundColor Green
                $r | ConvertTo-Json -Compress
                $ok = $true
                break
            }
        } catch { }
    }
    if (-not $ok) {
        Write-Host "ERROR: startup timeout (20s). Log:" -ForegroundColor Red
        Get-Content $LogPath -Tail 20 -ErrorAction SilentlyContinue
        exit 1
    }
    Write-Host ">> Ctrl-C to stop. Optional env: JWT_SECRET / RELAY_URL / CLAW_RELAY_TOKEN / CLAW_DEVICE_ID" -ForegroundColor Yellow
    $proc.WaitForExit()
}
finally {
    if ($null -ne $proc -and -not $proc.HasExited) { $proc.Kill() | Out-Null }
}
