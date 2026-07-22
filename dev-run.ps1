# claw 本地开发一键启动（从源码编译运行，无需 CDN 下载）
#
# 用法：
#   .\dev-run.ps1                # 默认端口 8090
#   .\dev-run.ps1 -Port 18090    # 指定端口
#
# 启动后自动验证 /health，Ctrl-C 停止 claw。
# 可选环境变量（启动前设置）：$env:JWT_SECRET / $env:RELAY_URL / $env:CLAW_RELAY_TOKEN / $env:CLAW_DEVICE_ID
param([int]$Port = 8090)
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir          # 主仓库根（.tools/go 所在）
$Gateway = Join-Path $ScriptDir "gateway"
$Go = Join-Path $RepoRoot ".tools\go\bin\go.exe"

# Go 环境（缓存指向项目盘，避免 C 盘空间不足）
$env:TMP = Join-Path $RepoRoot ".tools\tmp"
$env:TEMP = $env:TMP
$env:GOMODCACHE = Join-Path $RepoRoot ".tools\gomodcache"
$env:GOCACHE = Join-Path $RepoRoot ".tools\gocache"
$env:GOPROXY = "https://goproxy.cn,direct"

if (-not (Test-Path $Go)) {
    Write-Host "✗ 未找到 Go: $Go（请确认主仓库 .tools\go 已就位）" -ForegroundColor Red
    exit 1
}

New-Item -ItemType Directory -Force -Path (Join-Path $Gateway "data") | Out-Null
Set-Location $Gateway

$LogPath = Join-Path $env:TEMP "claw-dev.log"
Write-Host "▶ 编译并启动 claw-server (端口 $Port)..." -ForegroundColor Cyan
Write-Host "  /health: http://localhost:$Port/health"
Write-Host "  日志: $LogPath"

$proc = Start-Process -FilePath $Go `
    -ArgumentList "run", "./cmd/claw-server", "serve", "--port=$Port" `
    -PassThru -NoNewWindow `
    -RedirectStandardOutput $LogPath -RedirectStandardError "$LogPath.err"

# Ctrl-C 清理子进程
$null = Register-ObjectEvent -InputObject $proc -EventName Exited -Action { } -SourceIdentifier ClawExited -ErrorAction SilentlyContinue
try {
    Write-Host "⏳ 等待启动..." -ForegroundColor Yellow
    $ok = $false
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        if ($proc.HasExited) {
            Write-Host "✗ claw 进程已退出，日志：" -ForegroundColor Red
            Get-Content $LogPath -Tail 20 -ErrorAction SilentlyContinue
            Get-Content "$LogPath.err" -Tail 20 -ErrorAction SilentlyContinue
            exit 1
        }
        try {
            $r = Invoke-RestMethod "http://localhost:$Port/health" -TimeoutSec 2
            if ($r.code -eq 0) {
                Write-Host "✅ claw 已启动: http://localhost:$Port/health" -ForegroundColor Green
                $r | ConvertTo-Json -Compress
                $ok = $true
                break
            }
        } catch { }
    }
    if (-not $ok) {
        Write-Host "✗ 启动超时（20s），日志：" -ForegroundColor Red
        Get-Content $LogPath -Tail 20 -ErrorAction SilentlyContinue
        exit 1
    }
    Write-Host "💡 Ctrl-C 停止。可选 env: JWT_SECRET / RELAY_URL / CLAW_RELAY_TOKEN / CLAW_DEVICE_ID" -ForegroundColor Yellow
    $proc.WaitForExit()
} finally {
    if (-not $proc.HasExited) { $proc.Kill() | Out-Null }
}
