# Eleball-claw one-click installer (Windows PowerShell)
#
# Usage:
#   irm https://eleball.cn/install.ps1 | iex
#   irm https://eleball.cn/install.ps1 | iex -Port 8090
#
# Installs to %USERPROFILE%\.eleball-claw\, generates config, prints start command.

param(
    [int]$Port = 8090,
    [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"

$ConfigDir = Join-Path $env:USERPROFILE ".eleball-claw"
$BinDir = Join-Path $ConfigDir "bin"
$Binary = Join-Path $BinDir "eleball-claw.exe"

# Detect architecture
$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
$ArchTag = "windows-$Arch"
if ($env:CLAW_DOWNLOAD_URL) {
    $Url = $env:CLAW_DOWNLOAD_URL
} else {
    $Url = "https://api.eleball.cn/v1/releases/claw/download?arch=$ArchTag"
    if ($Version -ne "latest") {
        $Url += "&version=$Version"
    }
}

Write-Host "==> Downloading claw (windows/$Arch) from $Url" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
try {
    $resp = Invoke-WebRequest -Uri $Url -OutFile $Binary -UseBasicParsing -PassThru
} catch {
    Write-Error "Download failed: $Url`n$_"
    exit 1
}

# Optional integrity check: X-Content-SHA256 response header
$expectedSha = $resp.Headers["X-Content-SHA256"]
if ($expectedSha) {
    $actualSha = (Get-FileHash -Algorithm SHA256 -Path $Binary).Hash.ToLower()
    if ($expectedSha.ToLower() -ne $actualSha) {
        Write-Error "Checksum mismatch (expected $expectedSha, got $actualSha)"
        Remove-Item $Binary -Force
        exit 1
    }
    Write-Host "==> SHA256 verified" -ForegroundColor Cyan
}

# Init config
New-Item -ItemType Directory -Force -Path (Join-Path $ConfigDir "data") | Out-Null
$ConfigPath = Join-Path $ConfigDir "claw.yaml"
if (-not (Test-Path $ConfigPath)) {
    # Generate random JWT secret
    $Secret = -join ((1..64) | ForEach-Object { '{0:x}' -f (Get-Random -Max 16) })
    $Yaml = @"
server:
  port: $Port
  mode: release
  eleagent_base_url: "https://api.eleball.cn/v1"
database:
  driver: sqlite
  dsn: "$($ConfigDir -replace '\\','/')/data/claw.db"
jwt:
  secret: "$Secret"
  access_expire_hours: 2
  refresh_expire_hours: 720
agent:
  enabled: true
  base_path: "$($ConfigDir -replace '\\','/')/data/sessions"
  knowledge_base: "$($ConfigDir -replace '\\','/')/data/knowledge_base"
  model: "gpt-4o-mini"
  max_steps: 500
admin: { enabled: false }
admin_gate: { enabled: false }
payment: { order_expire_minutes: 30, alipay: { enabled: false } }
mail: { enabled: false, port: 465 }
"@
    Set-Content -Path $ConfigPath -Value $Yaml -Encoding UTF8
    Write-Host "==> Config generated: $ConfigPath" -ForegroundColor Cyan
}

# Relay 远程通道（P5.3）：CLAW_DEVICE_ID 首装生成并持久化到配置目录，重装复用
$DeviceIdFile = Join-Path $ConfigDir "device_id"
if (Test-Path $DeviceIdFile) {
    $ClawDeviceId = (Get-Content $DeviceIdFile -Raw).Trim()
} else {
    $ClawDeviceId = [guid]::NewGuid().ToString()
    Set-Content -Path $DeviceIdFile -Value $ClawDeviceId -Encoding ASCII
}

# relay 环境变量脚本：claw serve 前 dot-source 生效；重装时已存在则保留（含用户已填的 token）
$RelayEnvPath = Join-Path $ConfigDir "relay.env.ps1"
if (-not (Test-Path $RelayEnvPath)) {
    $RelayEnv = @"
# Eleball-claw relay 远程通道配置（启动前执行: . "$RelayEnvPath"）
# 云端中继地址（生产）
`$env:RELAY_URL = "wss://relay.eleball.cn"
# 设备 ID（首装生成，持久化于 $DeviceIdFile，请勿修改）
`$env:CLAW_DEVICE_ID = "$ClawDeviceId"
# 云端账户登录后的 JWT（与 gateway 统一账户验签）；请到 eleball.cn 登录获取后填入，勿泄露
`$env:CLAW_RELAY_TOKEN = ""
"@
    Set-Content -Path $RelayEnvPath -Value $RelayEnv -Encoding UTF8
    Write-Host "==> Relay env generated: $RelayEnvPath (fill in CLAW_RELAY_TOKEN to enable remote relay)" -ForegroundColor Cyan
}

Write-Host ""
Write-Host "==> Eleball-claw installed" -ForegroundColor Green
Write-Host "   Start: . '$RelayEnvPath'; `$env:CONFIG_PATH='$ConfigPath'; & '$Binary' serve --port=$Port" -ForegroundColor Yellow
Write-Host "   URL:   http://localhost:$Port"
Write-Host "   Conf:  $ConfigPath"
Write-Host "   Relay: $RelayEnvPath   # remote relay via wss://relay.eleball.cn (fill CLAW_RELAY_TOKEN first; skipped if empty)" -ForegroundColor Yellow
Write-Host "   Modules: & '$Binary' module ls   # module home: $ConfigDir\marketplace (official modules seeded on first use)" -ForegroundColor Yellow
Write-Host "            & '$Binary' module up   # start all modules via docker (Docker required)" -ForegroundColor Yellow
