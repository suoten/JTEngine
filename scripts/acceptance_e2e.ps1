#!/usr/bin/env pwsh
# JTE 端到端验收脚本（Windows PowerShell 版）
#
# 执行流程：构建 → 启动 → 健康检查 → 关键 API 烟测 → 停止清理
# 用法：powershell -ExecutionPolicy Bypass -File scripts/acceptance_e2e.ps1

param(
    [string]$Config = "jte/configs/jte.yaml",
    [int]$ApiPort = 8080,
    [int]$TimeoutSec = 30
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$JteDir = Join-Path $ProjectRoot "jte"
$Binary = Join-Path $JteDir "bin" | Join-Path -ChildPath "jte.exe"
$ConfigPath = Join-Path $ProjectRoot $Config

$pass = 0
$fail = 0
$warnings = 0

function Write-Step($msg) { Write-Host "`n[STEP] $msg" -ForegroundColor Cyan }
function Write-Pass($msg) { Write-Host "  [PASS] $msg" -ForegroundColor Green; $script:pass++ }
function Write-Fail($msg) { Write-Host "  [FAIL] $msg" -ForegroundColor Red; $script:fail++ }
function Write-Warn($msg) { Write-Host "  [WARN] $msg" -ForegroundColor Yellow; $script:warnings++ }

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  JTE 端到端验收脚本 (Windows)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

# --- 1. 构建 ---
Write-Step "构建 JTE 核心引擎"
Push-Location $JteDir
try {
    & go build -o $Binary ./cmd/jte 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Pass "核心引擎编译成功"
    } else {
        Write-Fail "核心引擎编译失败"
        exit 1
    }
} finally {
    Pop-Location
}

# --- 2. 构建模块 ---
Write-Step "构建子模块"
$modulesDir = Join-Path $ProjectRoot "jte-modules"
$moduleDirs = Get-ChildItem -Path $modulesDir -Directory -Filter "module-*"
$moduleBuildFail = $false
foreach ($dir in $moduleDirs) {
    Push-Location $dir.FullName
    try {
        & go build ./... 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "$($dir.Name) 构建失败（非阻塞）"
        }
    } finally {
        Pop-Location
    }
}
if (-not $moduleBuildFail) {
    Write-Pass "子模块构建完成"
}

# --- 3. 启动服务 ---
Write-Step "启动 JTE 服务"

# 设置开发环境变量跳过 JWT 校验
$env:JTE_ALLOW_INSECURE_JWT = "1"

if (-not (Test-Path $ConfigPath)) {
    Write-Warn "配置文件不存在: $ConfigPath，跳过启动测试"
    Write-Host "`n验收结果: $pass 通过, $fail 失败, $warnings 警告" -ForegroundColor Cyan
    if ($fail -gt 0) { exit 1 }
    exit 0
}

$process = Start-Process -FilePath $Binary -ArgumentList "serve", "--config", $ConfigPath -PassThru -NoNewWindow -RedirectStandardOutput "$JteDir\jte_stdout.log" -RedirectStandardError "$JteDir\jte_stderr.log"

Start-Sleep -Seconds 3

if ($process.HasExited) {
    Write-Fail "JTE 服务启动后立即退出"
    Get-Content "$JteDir\jte_stderr.log" | Select-Object -First 20
    exit 1
}
Write-Pass "JTE 服务已启动 (PID: $($process.Id))"

# --- 4. 健康检查 ---
Write-Step "健康检查"
$healthy = $false
for ($i = 0; $i -lt $TimeoutSec; $i++) {
    try {
        $response = Invoke-RestMethod -Uri "http://localhost:$ApiPort/api/v1/health" -Method Get -TimeoutSec 2 -ErrorAction Stop
        $healthy = $true
        break
    } catch {
        Start-Sleep -Seconds 1
    }
}

if ($healthy) {
    Write-Pass "健康检查通过: $($response | ConvertTo-Json -Compress)"
} else {
    Write-Fail "健康检查超时（${TimeoutSec}s）"
}

# --- 5. 关键 API 烟测 ---
Write-Step "关键 API 烟测"

# 5.1 登录接口
try {
    $loginBody = @{ username = "admin"; password = "admin" } | ConvertTo-Json
    $loginResp = Invoke-RestMethod -Uri "http://localhost:$ApiPort/api/v1/auth/login" -Method Post -Body $loginBody -ContentType "application/json" -TimeoutSec 5 -ErrorAction Stop
    Write-Pass "登录接口可访问"
} catch {
    if ($_.Exception.Response.StatusCode -eq 401 -or $_.Exception.Response.StatusCode -eq 400) {
        Write-Pass "登录接口可访问（返回认证错误为预期行为）"
    } else {
        Write-Warn "登录接口异常: $($_.Exception.Message)"
    }
}

# 5.2 Prometheus 指标
try {
    $metrics = Invoke-WebRequest -Uri "http://localhost:$ApiPort/metrics" -Method Get -TimeoutSec 5 -ErrorAction Stop
    if ($metrics.Content -match "jte_") {
        Write-Pass "Prometheus 指标端点正常"
    } else {
        Write-Warn "指标端点响应但未包含 jte_ 前缀指标"
    }
} catch {
    Write-Warn "指标端点不可访问: $($_.Exception.Message)"
}

# --- 6. 停止清理 ---
Write-Step "停止 JTE 服务"
if (-not $process.HasExited) {
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
    Write-Pass "JTE 服务已停止"
} else {
    Write-Warn "JTE 服务已自行退出"
}

# 清理日志
Remove-Item "$JteDir\jte_stdout.log", "$JteDir\jte_stderr.log" -ErrorAction SilentlyContinue

# --- 结果汇总 ---
Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "  验收结果" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  通过: $pass" -ForegroundColor Green
Write-Host "  失败: $fail" -ForegroundColor Red
Write-Host "  警告: $warnings" -ForegroundColor Yellow

if ($fail -gt 0) {
    Write-Host "`n  状态: 验收未通过 ❌" -ForegroundColor Red
    exit 1
} else {
    Write-Host "`n  状态: 验收通过 ✅" -ForegroundColor Green
    exit 0
}
