# ============================================================
# JTE 一键安装脚本（Windows）
#
# 用法：双击 install-jte.bat（推荐），或在 PowerShell 中执行：
#   powershell -NoProfile -ExecutionPolicy Bypass -File install-jte.ps1
#
# 脚本会自动完成：
#   1. 检查 Docker 是否已安装 / 正在运行（未安装可自动用 winget 安装）
#   2. 拉取 JTE 镜像（多个镜像源依次尝试）
#   3. 拉取失败时自动配置 Docker 加速源并重启 Docker 后重试
#   4. 启动 JTE 容器（SQLite 存储，单容器即可用）
#   5. 等待服务就绪后自动打开管理后台
# ============================================================
[CmdletBinding()]
param(
    # JTE 镜像候选：留空则使用脚本内默认列表（按顺序尝试，第一个拉取成功的生效）。
    # 可用环境变量 JTE_IMAGE 强制指定。
    [string[]]$ImageCandidates,

    # Docker Hub 加速源候选：拉取失败时逐个写入 daemon.json 并重试。
    # 公共加速源可用性会随时间变化；如需最稳定的加速，
    # 请注册阿里云并填入专属加速器地址（脚本会优先使用 JTE_MIRROR 环境变量）：
    #   https://cr.console.aliyun.com/cn-hangzhou/instances/mirrors
    [string[]]$RegistryMirrors,

    # 安装完成时不自动打开浏览器
    [switch]$NoBrowser,

    # 容器对外端口（一般不用改；改前请确认端口未被占用）
    [int]$ApiPort = 8080,
    [int]$GatewayPort = 7611
)

$ErrorActionPreference = 'Stop'
$ContainerName = 'jte'

# param 默认值不支持管道表达式，候选列表在此构建（空值/空白项被过滤）
if (-not $ImageCandidates) {
    $ImageCandidates = @($env:JTE_IMAGE, 'ghcr.io/suoten/jt-engine:stable', 'suoten/jt-engine:stable') |
        Where-Object { $_ -and $_.Trim() }
}
if (-not $RegistryMirrors) {
    $RegistryMirrors = @($env:JTE_MIRROR, 'https://docker.1ms.run', 'https://docker.m.daocloud.io', 'https://docker.nju.edu.cn') |
        Where-Object { $_ -and $_.Trim() }
}

# ---------- 输出辅助 ----------
function Write-Step { param($Text) Write-Host ('==> ' + $Text) -ForegroundColor Cyan }
function Write-Ok   { param($Text) Write-Host ('[OK] ' + $Text) -ForegroundColor Green }
function Write-Warn2{ param($Text) Write-Host ('[!!] ' + $Text) -ForegroundColor Yellow }
function Write-Fail { param($Text) Write-Host ('[X]  ' + $Text) -ForegroundColor Red }

function Show-Banner {
    Write-Host ''
    Write-Host '======================================' -ForegroundColor Cyan
    Write-Host '        JTE 一键安装（Windows）        ' -ForegroundColor Cyan
    Write-Host '   JT/T 808/1078 车联网部标协议引擎    ' -ForegroundColor Cyan
    Write-Host '======================================' -ForegroundColor Cyan
    Write-Host ''
}

# ---------- Docker 环境 ----------
function Test-DockerInstalled {
    return [bool](Get-Command docker -ErrorAction SilentlyContinue)
}

function Test-DockerDaemonUp {
    docker info *> $null
    return ($LASTEXITCODE -eq 0)
}

function Start-DockerDesktop {
    $dd = Join-Path $env:ProgramFiles 'Docker\Docker\Docker Desktop.exe'
    if (-not (Test-Path $dd)) {
        return $false
    }
    Start-Process $dd
    return $true
}

function Wait-DockerDaemon {
    param([int]$TimeoutSec = 180)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Test-DockerDaemonUp) { return $true }
        Start-Sleep -Seconds 5
        Write-Host '.' -NoNewline
    }
    Write-Host ''
    return $false
}

function Restart-DockerDesktop {
    Write-Warn2 '正在重启 Docker Desktop（已运行的容器稍后会自动恢复，约需 30-90 秒）...'
    Get-Process -Name 'Docker Desktop', 'com.docker.backend' -ErrorAction SilentlyContinue |
        Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 3
    if (-not (Start-DockerDesktop)) {
        Write-Fail '未找到 Docker Desktop，请手动启动后重新运行本脚本。'
        return $false
    }
    return (Wait-DockerDaemon -TimeoutSec 180)
}

# ---------- 加速源修复 ----------
function Repair-RegistryMirrors {
    param([string]$Mirror)
    $daemonJson = Join-Path $env:USERPROFILE '.docker\daemon.json'
    $dir = Split-Path $daemonJson

    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }

    # 备份现有配置；解析失败（坏文件）时从空配置重建
    if (Test-Path $daemonJson) {
        Copy-Item $daemonJson "$daemonJson.bak-$(Get-Date -Format 'yyyyMMdd-HHmmss')" -Force
        try {
            $json = Get-Content $daemonJson -Raw -Encoding UTF8 | ConvertFrom-Json
        } catch {
            Write-Warn2 '现有 daemon.json 不是合法 JSON，已备份后将重建。'
            $json = New-Object PSObject
        }
    } else {
        $json = New-Object PSObject
    }

    # 替换 registry-mirrors：旧的失效加速源正是"failed size validation"错误的常见根源
    if ($json.PSObject.Properties['registry-mirrors']) {
        $json.PSObject.Properties.Remove('registry-mirrors')
    }
    $json | Add-Member -NotePropertyName 'registry-mirrors' -NotePropertyValue @($Mirror) -Force

    # Docker 要求 daemon.json 为无 BOM 的 UTF-8，PS5.1 的 Set-Content 会加 BOM，故用 .NET 直接写
    $content = $json | ConvertTo-Json -Depth 10
    [System.IO.File]::WriteAllText($daemonJson, $content, (New-Object System.Text.UTF8Encoding($false)))

    Write-Ok "已写入加速源 $Mirror （原配置已备份）"
    return $true
}

# ---------- 镜像拉取 ----------
function Try-PullImage {
    param([string[]]$Candidates)
    foreach ($img in $Candidates) {
        Write-Step "尝试拉取镜像：$img"
        docker pull $img
        if ($LASTEXITCODE -eq 0) {
            Write-Ok "镜像就绪：$img"
            return $img
        }
        Write-Warn2 "拉取失败：$img，尝试下一个候选..."
    }
    return $null
}

# ---------- 主流程 ----------
Show-Banner

# 1. 检查 Docker
if (-not (Test-DockerInstalled)) {
    Write-Warn2 '未检测到 Docker。'
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if ($winget) {
        $answer = Read-Host '是否现在自动安装 Docker Desktop？（需要管理员权限，装完可能要重启电脑）[Y/n]'
        if ($answer -eq '' -or $answer -match '^[Yy]') {
            Write-Step '正在通过 winget 安装 Docker Desktop（下载约 500MB，请耐心等待）...'
            winget install -e --id Docker.DockerDesktop --accept-package-agreements --accept-source-agreements
            if ($LASTEXITCODE -ne 0) {
                Write-Fail '自动安装失败，请手动下载安装：https://www.docker.com/products/docker-desktop/'
                exit 1
            }
            Write-Ok 'Docker Desktop 安装命令已执行完成。'
            Write-Warn2 '如安装程序要求重启电脑，请重启后再重新双击本脚本。'
        } else {
            Start-Process 'https://www.docker.com/products/docker-desktop/'
            Write-Host '已打开下载页面，安装完成后请重新双击本脚本。'
            exit 0
        }
    } else {
        Start-Process 'https://www.docker.com/products/docker-desktop/'
        Write-Warn2 '已打开 Docker Desktop 下载页面。安装完成后请重新双击本脚本。'
        exit 0
    }
}

# 2. 确保守护进程运行
if (-not (Test-DockerDaemonUp)) {
    Write-Step 'Docker 未运行，正在启动 Docker Desktop（首次启动约需 1 分钟）...'
    if (-not (Start-DockerDesktop)) {
        Write-Fail '未找到 Docker Desktop，请先安装：https://www.docker.com/products/docker-desktop/'
        exit 1
    }
    if (-not (Wait-DockerDaemon -TimeoutSec 180)) {
        Write-Fail '等待 Docker 启动超时。请手动打开 Docker Desktop，等它变为绿色 Running 后重新运行本脚本。'
        exit 1
    }
}
Write-Ok 'Docker 运行中。'

# 3. 拉取镜像（先直接拉；失败则逐个加速源修复后重试）
$image = Try-PullImage -Candidates $ImageCandidates

if (-not $image) {
    Write-Warn2 '直接拉取失败，开始自动配置加速源重试...'
    foreach ($mirror in $RegistryMirrors) {
        Write-Step "切换加速源：$mirror"
        Repair-RegistryMirrors -Mirror $mirror | Out-Null
        if (-not (Restart-DockerDesktop)) {
            Write-Fail 'Docker 重启失败，请手动重启 Docker Desktop 后重新运行本脚本。'
            exit 1
        }
        $image = Try-PullImage -Candidates $ImageCandidates
        if ($image) { break }
    }
}

if (-not $image) {
    Write-Host ''
    Write-Fail '所有镜像源均拉取失败。请按以下步骤手动处理：'
    Write-Host '  1. 打开 Docker Desktop -> Settings -> Docker Engine'
    Write-Host '     在 "registry-mirrors" 中填入你的专属加速器地址，例如：'
    Write-Host '       "registry-mirrors": ["https://xxxx.mirror.aliyuncs.com"]'
    Write-Host '     （阿里云免费专属加速器：https://cr.console.aliyun.com/cn-hangzhou/instances/mirrors ）'
    Write-Host '  2. 点击 Apply & Restart'
    Write-Host '  3. 若仍报 "failed size validation"，多为本地镜像缓存损坏：'
    Write-Host '     Docker Desktop -> Troubleshoot -> Clean / Purge data（注意：会删除已有镜像和容器）'
    Write-Host '  4. 之后重新双击本脚本'
    exit 1
}

# 4. 启动容器（重装=升级：删除旧容器，数据卷保留，数据不丢）
Write-Step '启动 JTE 容器...'
docker rm -f $ContainerName 2> $null
docker volume create jte-data  *> $null
docker volume create jte-spool *> $null

docker run -d --name $ContainerName `
    --restart unless-stopped `
    -p "${GatewayPort}:7611" -p "${ApiPort}:8080" `
    -e JTE_STORAGE_TYPE=sqlite `
    -v jte-data:/app/data `
    -v jte-spool:/app/spool `
    $image | Out-Null

if ($LASTEXITCODE -ne 0) {
    Write-Fail '容器启动失败，最常见原因是端口被占用。请检查：'
    Write-Host "    netstat -ano | findstr :$ApiPort"
    Write-Host "    netstat -ano | findstr :$GatewayPort"
    Write-Host '  或查看容器日志：docker logs jte'
    exit 1
}
Write-Ok '容器已启动（SQLite 存储，数据保存在 jte-data 卷中）。'

# 5. 等待服务就绪
Write-Step '等待服务就绪（约 10-30 秒）...'
$deadline = (Get-Date).AddSeconds(90)
$healthy = $false
while ((Get-Date) -lt $deadline) {
    try {
        $resp = Invoke-WebRequest -Uri "http://localhost:$ApiPort/healthz" -UseBasicParsing -TimeoutSec 3
        if ($resp.StatusCode -lt 500) { $healthy = $true; break }
    } catch {
        Start-Sleep -Seconds 3
        Write-Host '.' -NoNewline
    }
}
Write-Host ''
if (-not $healthy) {
    Write-Warn2 '服务在 90 秒内未就绪。可稍后手动检查：'
    Write-Host "    docker logs -f $ContainerName"
    Write-Host "    http://localhost:$ApiPort/healthz"
} else {
    Write-Ok '服务已就绪。'
}

# 6. 完成
Write-Host ''
Write-Host '======================================' -ForegroundColor Green
Write-Ok 'JTE 安装完成！'
Write-Host '======================================' -ForegroundColor Green
Write-Host ''
Write-Host ("  管理后台    :  http://localhost:" + $ApiPort) -ForegroundColor Yellow
Write-Host ( "  默认账号    :  admin / admin123 （登录后请立即修改密码）") -ForegroundColor Yellow
Write-Host ("  终端接入端口:  本机IP:" + $GatewayPort + " （TCP，JT/T 808）") -ForegroundColor Yellow
Write-Host ''
Write-Host '  常用命令：'
Write-Host "    停止：docker stop $ContainerName"
Write-Host "    启动：docker start $ContainerName"
Write-Host "    日志：docker logs -f $ContainerName"
Write-Host '    升级：重新双击本脚本（数据不会丢失）'
Write-Host '    卸载：docker rm -f jte ；如需清空数据再执行 docker volume rm jte-data jte-spool'
Write-Host ''

if (-not $NoBrowser) {
    Start-Process "http://localhost:$ApiPort"
}
