#!/usr/bin/env bash
# ============================================================
# JTE 一键安装脚本（Linux）
#
# 用法（任选其一）：
#   curl -fsSL https://gitee.com/suoten/jt-engine/raw/master/install-jte.sh | sudo bash
#   wget -qO- https://gitee.com/suoten/jt-engine/raw/master/install-jte.sh | sudo bash
#   或下载后执行：sudo bash install-jte.sh
#
# 脚本会自动完成：
#   1. 检查/安装 Docker（未安装时用阿里云镜像源安装）
#   2. 拉取 JTE 镜像（多个镜像源依次尝试）
#   3. 拉取失败时自动配置 Docker 加速源并重启 Docker 后重试
#   4. 启动 JTE 容器（SQLite 存储，单容器即可用）
#   5. 等待服务就绪并打印访问地址
# ============================================================
set -u

CONTAINER_NAME="jte"
API_PORT="${JTE_API_PORT:-8080}"
GATEWAY_PORT="${JTE_GATEWAY_PORT:-7611}"

# 镜像候选：按顺序尝试，第一个成功的生效（可用环境变量 JTE_IMAGE 强制指定）
IMAGE_CANDIDATES=()
[ -n "${JTE_IMAGE:-}" ] && IMAGE_CANDIDATES+=("$JTE_IMAGE")
IMAGE_CANDIDATES+=("suoten/jt-engine:stable" "ghcr.io/suoten/jt-engine:stable")

# Docker Hub 加速源候选：拉取失败时逐个写入 daemon.json 重试。
# 公共加速源可用性会变化；建议注册阿里云使用专属加速器（优先读 JTE_MIRROR）：
#   https://cr.console.aliyun.com/cn-hangzhou/instances/mirrors
MIRROR_CANDIDATES=()
[ -n "${JTE_MIRROR:-}" ] && MIRROR_CANDIDATES+=("$JTE_MIRROR")
MIRROR_CANDIDATES+=("https://docker.1ms.run" "https://docker.m.daocloud.io" "https://docker.nju.edu.cn")

step() { printf '\033[36m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[32m[OK] %s\033[0m\n' "$*"; }
warn() { printf '\033[33m[!!] %s\033[0m\n' "$*"; }
fail() { printf '\033[31m[X]  %s\033[0m\n' "$*"; }

# ---------- root 权限 ----------
if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        warn "当前不是 root，尝试用 sudo 重新执行..."
        exec sudo bash "$0" "$@"
    else
        fail "请用 root 运行：sudo bash $0"
        exit 1
    fi
fi

# ---------- 1. 安装 Docker ----------
if ! command -v docker >/dev/null 2>&1; then
    warn "未检测到 Docker。"
    if command -v curl >/dev/null 2>&1; then
        read -r -p "是否现在自动安装 Docker（使用阿里云安装源）？[Y/n] " answer
        case "$answer" in
            n|N) fail "已取消。请手动安装 Docker 后重试：https://docs.docker.com/engine/install/"; exit 1 ;;
            *)
                step "正在安装 Docker（可能需要几分钟）..."
                if ! curl -fsSL https://get.docker.com | sh -s -- --mirror Aliyun; then
                    fail "自动安装失败，请手动安装：https://docs.docker.com/engine/install/"
                    exit 1
                fi
                ;;
        esac
    else
        fail "系统缺少 curl，请先安装 Docker：https://docs.docker.com/engine/install/"
        exit 1
    fi
fi

systemctl enable --now docker >/dev/null 2>&1 || service docker start >/dev/null 2>&1 || true

# 等待守护进程就绪（最多 60 秒）
step "等待 Docker 就绪..."
i=0
while ! docker info >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 12 ]; then
        fail "Docker 未就绪。请执行 systemctl status docker 排查后重试。"
        exit 1
    fi
    sleep 5
done
ok "Docker 运行中。"

# ---------- 2. 拉取镜像 ----------
try_pull() {
    for img in "${IMAGE_CANDIDATES[@]}"; do
        step "尝试拉取镜像：$img"
        if docker pull "$img"; then
            ok "镜像就绪：$img"
            PULLED_IMAGE="$img"
            return 0
        fi
        warn "拉取失败：$img，尝试下一个候选..."
    done
    return 1
}

if ! try_pull; then
    warn "直接拉取失败，开始自动配置加速源重试..."
    for mirror in "${MIRROR_CANDIDATES[@]}"; do
        step "切换加速源：$mirror"
        mkdir -p /etc/docker
        if [ -f /etc/docker/daemon.json ]; then
            cp /etc/docker/daemon.json "/etc/docker/daemon.json.bak-$(date +%Y%m%d-%H%M%S)"
        fi
        # 合并加速源配置：优先 python3，其次 jq；都不可用时备份后整体重建
        if command -v python3 >/dev/null 2>&1; then
            python3 - "$mirror" <<'PYEOF'
import json, sys, os
mirror = sys.argv[1]
path = "/etc/docker/daemon.json"
cfg = {}
if os.path.exists(path):
    try:
        with open(path) as f:
            cfg = json.load(f)
    except Exception:
        cfg = {}
cfg["registry-mirrors"] = [mirror]
with open(path, "w") as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
PYEOF
        elif command -v jq >/dev/null 2>&1; then
            tmp=$(mktemp)
            if [ -f /etc/docker/daemon.json ]; then
                jq --arg m "$mirror" '.["registry-mirrors"]=[$m]' /etc/docker/daemon.json > "$tmp" && mv "$tmp" /etc/docker/daemon.json
            else
                printf '{"registry-mirrors":["%s"]}' "$mirror" > /etc/docker/daemon.json
            fi
        else
            printf '{"registry-mirrors":["%s"]}' "$mirror" > /etc/docker/daemon.json
            warn "系统无 python3/jq，daemon.json 已备份并重建（如有自定义配置请检查还原）。"
        fi
        systemctl restart docker >/dev/null 2>&1 || service docker restart >/dev/null 2>&1
        sleep 3
        if ! docker info >/dev/null 2>&1; then
            fail "Docker 重启失败，已保留备份，请手动检查 /etc/docker/daemon.json"
            exit 1
        fi
        if try_pull; then
            break
        fi
    done
fi

if [ -z "${PULLED_IMAGE:-}" ]; then
    echo
    fail "所有镜像源均拉取失败。请按以下步骤手动处理："
    echo "  1. 编辑 /etc/docker/daemon.json，写入你的专属加速器地址，例如："
    echo '       {"registry-mirrors": ["https://xxxx.mirror.aliyuncs.com"]}'
    echo "     （阿里云免费专属加速器：https://cr.console.aliyun.com/cn-hangzhou/instances/mirrors ）"
    echo "  2. 执行 systemctl restart docker"
    echo "  3. 重新运行本脚本"
    echo '  4. 若仍报 "failed size validation"，多为本地缓存损坏，可执行 docker system prune -a 后重试'
    exit 1
fi

# ---------- 3. 启动容器（重装=升级：删除旧容器，数据卷保留，数据不丢）----------
step "启动 JTE 容器..."
docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1
docker volume create jte-data  >/dev/null
docker volume create jte-spool >/dev/null

if ! docker run -d --name "$CONTAINER_NAME" \
        --restart unless-stopped \
        -p "${GATEWAY_PORT}:7611" -p "${API_PORT}:8080" \
        -e JTE_STORAGE_TYPE=sqlite \
        -v jte-data:/app/data \
        -v jte-spool:/app/spool \
        "$PULLED_IMAGE" >/dev/null; then
    fail "容器启动失败，最常见原因是端口被占用。请检查："
    echo "    ss -lntp | grep -E ':${API_PORT}|:${GATEWAY_PORT}'"
    echo "  或查看容器日志：docker logs $CONTAINER_NAME"
    exit 1
fi
ok "容器已启动（SQLite 存储，数据保存在 jte-data 卷中）。"

# ---------- 4. 等待服务就绪 ----------
step "等待服务就绪（约 10-30 秒）..."
healthy=0
for _ in $(seq 1 30); do
    if curl -fsS -m 3 "http://localhost:${API_PORT}/healthz" >/dev/null 2>&1 \
       || wget -q -T 3 -O /dev/null "http://localhost:${API_PORT}/healthz" 2>/dev/null; then
        healthy=1
        break
    fi
    sleep 3
done
if [ "$healthy" -eq 1 ]; then
    ok "服务已就绪。"
else
    warn "服务在 90 秒内未就绪。可稍后手动检查："
    echo "    docker logs -f $CONTAINER_NAME"
    echo "    curl http://localhost:${API_PORT}/healthz"
fi

# ---------- 5. 完成 ----------
SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -z "$SERVER_IP" ] && SERVER_IP="服务器IP"

echo
printf '\033[32m======================================\033[0m\n'
ok "JTE 安装完成！"
printf '\033[32m======================================\033[0m\n'
echo
echo "  管理后台    :  http://${SERVER_IP}:${API_PORT}"
echo "  默认账号    :  admin / admin123 （登录后请立即修改密码）"
echo "  终端接入端口:  ${SERVER_IP}:${GATEWAY_PORT} （TCP，JT/T 808）"
echo
echo "  别忘了放行端口（二选一，云服务器还需在控制台安全组放行）："
echo "    firewall-cmd --permanent --add-port=${GATEWAY_PORT}/tcp --add-port=${API_PORT}/tcp && firewall-cmd --reload"
echo "    或 ufw allow ${GATEWAY_PORT}/tcp && ufw allow ${API_PORT}/tcp"
echo
echo "  常用命令："
echo "    停止：docker stop $CONTAINER_NAME"
echo "    启动：docker start $CONTAINER_NAME"
echo "    日志：docker logs -f $CONTAINER_NAME"
echo "    升级：重新运行本脚本（数据不会丢失）"
echo "    卸载：docker rm -f jte ；如需清空数据再执行 docker volume rm jte-data jte-spool"
echo
