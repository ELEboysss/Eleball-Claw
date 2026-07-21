# Eleball E2E Server Launcher (PowerShell)
# Recommended for Windows users - better Unicode support than .bat

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

$env:PORT = if ($env:PORT) { $env:PORT } else { "8080" }

# ============================================================================
# Auto-detect Go
# ============================================================================
$goExe = $null

$candidates = @(
    "J:\workspace\Prj\tools\go\bin\go.exe",
    "C:\Program Files\Go\bin\go.exe",
    "${env:LOCALAPPDATA}\go\bin\go.exe",
    "${env:USERPROFILE}\go\bin\go.exe"
)

foreach ($c in $candidates) {
    if (Test-Path $c) {
        $goExe = $c
        break
    }
}

if (-not $goExe) {
    $goInPath = Get-Command go -ErrorAction SilentlyContinue
    if ($goInPath) {
        $goExe = $goInPath.Source
    }
}

if (-not $goExe) {
    Write-Host "[ERROR] Go not found." -ForegroundColor Red
    Write-Host "Please install Go or set the path in this script."
    Read-Host "Press Enter to exit"
    exit 1
}

Write-Host "[INFO] Using Go: $goExe" -ForegroundColor Cyan

# ============================================================================
# Build
# ============================================================================
Write-Host ""
Write-Host "==========================================" -ForegroundColor Green
Write-Host "  Building Eleball E2E Server..." -ForegroundColor Green
Write-Host "==========================================" -ForegroundColor Green
Write-Host ""

try {
    & $goExe build -o e2e-server.exe . 2>$null
    if ($LASTEXITCODE -ne 0) { throw "Build failed with exit code $LASTEXITCODE" }
} catch {
    Write-Host "[ERROR] Build failed: $_" -ForegroundColor Red
    Read-Host "Press Enter to exit"
    exit 1
}

# ============================================================================
# Start
# ============================================================================
Write-Host ""
Write-Host "==========================================" -ForegroundColor Green
Write-Host "  Eleball E2E Server Started" -ForegroundColor Green
Write-Host "==========================================" -ForegroundColor Green
Write-Host "  API:      http://localhost:$($env:PORT)"
Write-Host "  Health:   http://localhost:$($env:PORT)/health"
Write-Host "  Admin:    http://localhost:$($env:PORT)/admin/"
Write-Host "=========================================="
Write-Host "  Android Debug:"
Write-Host "    Emulator:  http://10.0.2.2:$($env:PORT)"
Write-Host "    Real Device: http://$( (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | Select-Object -First 1).IPAddress ):$($env:PORT)"
Write-Host "=========================================="
Write-Host ""
Write-Host "Press Ctrl+C to stop" -ForegroundColor Yellow
Write-Host ""

& "$scriptDir\e2e-server.exe"
