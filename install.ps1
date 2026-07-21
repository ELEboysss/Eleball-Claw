# Eleball-claw 一键安装（Windows PowerShell）
#
# 用法：
#   irm https://eleball.cn/install.ps1 | iex
#   irm https://eleball.cn/install.ps1 | iex -Port 8090
#
# 安装到用户目录 %USERPROFILE%\.eleball-claw\，初始化配置并提示启动。

param(
    [int]$Port = 8090,
    [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"

$ConfigDir = Join-Path $env:USERPROFILE ".eleball-claw"
$BinDir = Join-Path $ConfigDir "bin"
$Binary = Join-Path $BinDir "eleball-claw.exe"

# 探测架构
$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
$Url = if ($env:CLAW_DOWNLOAD_URL) { $env:CLAW_DOWNLOAD_URL } `
  else { "https://eleball.cn/downloads/claw/$Version/eleball-claw-windows-$Arch.exe" }

Write-Host "==> 下载 claw (windows/$Arch) from $Url" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
try {
    Invoke-WebRequest -Uri $Url -OutFile $Binary -UseBasicParsing
} catch {
    Write-Error "下载失败: $Url`n$_"
    exit 1
}

# 初始化配置
New-Item -ItemType Directory -Force -Path (Join-Path $ConfigDir "data") | Out-Null
$ConfigPath = Join-Path $ConfigDir "claw.yaml"
if (-not (Test-Path $ConfigPath)) {
    # 生成随机 JWT secret
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
    Write-Host "==> 已生成配置 $ConfigPath" -ForegroundColor Cyan
}

Write-Host ""
Write-Host "✅ Eleball-claw 安装完成" -ForegroundColor Green
Write-Host "   启动: `$env:CONFIG_PATH='$ConfigPath'; & '$Binary' serve --port=$Port" -ForegroundColor Yellow
Write-Host "   首页: http://localhost:$Port"
Write-Host "   配置: $ConfigPath"
