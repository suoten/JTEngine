#!/usr/bin/env bash
# JTE 生产环境预检查脚本
# [工业级加固] 在部署前执行全面预检查，任一关键检查失败则拒绝部署
#
# 检查项目：
#   1. 所有必需的环境变量已设置
#   2. 密码强度（≥16 字符）
#   3. TLS 证书有效性（过期时间 > 30 天）
#   4. 磁盘空间（数据目录可用空间 > 10GB）
#   5. 端口未被占用
#   6. 依赖服务（MySQL/Redis/TDengine/MinIO）可达性
#
# 用法：
#   ./preflight.sh                    # 执行全部检查
#   ./preflight.sh --env-file .env    # 从 .env 文件加载环境变量
#   ./preflight.sh --skip-tls         # 跳过 TLS 证书检查（无 TLS 时）
#   ./preflight.sh --skip-deps        # 跳过依赖服务连通性检查
#   ./preflight.sh --json             # 输出 JSON 格式报告
set -euo pipefail

# ===== 默认配置 =====
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE=""
SKIP_TLS=false
SKIP_DEPS=false
JSON_OUTPUT=false
FAIL=0
WARN=0
MIN_PASSWORD_LEN=16
MIN_DISK_GB=10
MIN_CERT_DAYS=30

# JTE 需要的端口
JTE_PORTS=("7611" "8080")
# 依赖服务端口
DEP_PORTS=("3306" "6379" "6030" "6041" "9000")

# 必需环境变量列表
REQUIRED_VARS=(
    "MYSQL_ROOT_PASSWORD"
    "MYSQL_PASSWORD"
    "REDIS_PASSWORD"
    "TDENGINE_PASSWORD"
    "MINIO_ROOT_USER"
    "MINIO_ROOT_PASSWORD"
)

# 密码类变量（需检查强度）
PASSWORD_VARS=(
    "MYSQL_ROOT_PASSWORD"
    "MYSQL_PASSWORD"
    "REDIS_PASSWORD"
    "TDENGINE_PASSWORD"
    "MINIO_ROOT_PASSWORD"
)

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()    { echo "[$(date '+%F %T')] [PREFLIGHT] $*"; }
ok()     { echo -e "${GREEN}[✓]${NC} $*"; }
fail()   { echo -e "${RED}[✗]${NC} $*" >&2; FAIL=$((FAIL+1)); }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; WARN=$((WARN+1)); }
info()   { echo -e "${BLUE}[i]${NC} $*"; }

# ===== 参数解析 =====
while [[ $# -gt 0 ]]; do
    case "$1" in
        --env-file)  ENV_FILE="$2"; shift 2 ;;
        --skip-tls)  SKIP_TLS=true; shift ;;
        --skip-deps) SKIP_DEPS=true; shift ;;
        --json)      JSON_OUTPUT=true; shift ;;
        --help|-h)
            echo "JTE 生产环境预检查脚本"
            echo ""
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --env-file <FILE>   从指定文件加载环境变量"
            echo "  --skip-tls          跳过 TLS 证书检查"
            echo "  --skip-deps         跳过依赖服务连通性检查"
            echo "  --json              输出 JSON 格式报告"
            echo "  --help, -h          显示帮助"
            exit 0
            ;;
        *) echo "未知选项: $1" >&2; exit 1 ;;
    esac
done

# ===== 加载环境变量文件 =====
if [[ -n "$ENV_FILE" ]]; then
    if [[ -f "$ENV_FILE" ]]; then
        info "加载环境变量文件: $ENV_FILE"
        set -a
        # shellcheck disable=SC1090
        source "$ENV_FILE"
        set +a
    else
        fail "环境变量文件不存在: $ENV_FILE"
        exit 1
    fi
fi

log "════════════════════════════════════════════════════"
log "JTE 生产环境预检查"
log "  跳过 TLS 检查: $SKIP_TLS"
log "  跳过依赖检查: $SKIP_DEPS"
log "  最小密码长度: $MIN_PASSWORD_LEN"
log "  最小磁盘空间: ${MIN_DISK_GB}GB"
log "  最小证书有效期: ${MIN_CERT_DAYS} 天"
log "════════════════════════════════════════════════════"

# ===== 检查 1：必需环境变量 =====
log ""
log "检查 1: 必需环境变量"

for var in "${REQUIRED_VARS[@]}"; do
    val="${!var:-}"
    if [[ -z "$val" ]]; then
        fail "  环境变量 $var 未设置"
    else
        ok "  $var 已设置"
    fi
done

# ===== 检查 2：密码强度 =====
log ""
log "检查 2: 密码强度（≥${MIN_PASSWORD_LEN} 字符）"

check_password_strength() {
    local var="$1"
    local val="${!var:-}"

    if [[ -z "$val" ]]; then
        fail "  $var 未设置（无法检查强度）"
        return 1
    fi

    local len=${#val}

    # 长度检查
    if (( len < MIN_PASSWORD_LEN )); then
        fail "  $var 密码长度不足（${len} 字符，要求 ≥${MIN_PASSWORD_LEN}）"
        return 1
    fi

    # 弱密码模式检查
    local weak_patterns=("123456" "password" "jte123" "admin" "root" "change-me" "taosdata" "minioadmin")
    local lower_val
    lower_val=$(echo "$val" | tr '[:upper:]' '[:lower:]')
    for pattern in "${weak_patterns[@]}"; do
        if [[ "$lower_val" == *"$pattern"* ]]; then
            fail "  $var 包含弱密码模式 '$pattern'"
            return 1
        fi
    done

    # 复杂度检查：至少包含字母和数字
    if [[ "$val" =~ ^[a-zA-Z]+$ ]] || [[ "$val" =~ ^[0-9]+$ ]]; then
        warn "  $var 仅包含字母或数字，建议混合大小写字母、数字和特殊字符"
    fi

    ok "  $var 密码强度合格（${len} 字符）"
    return 0
}

for var in "${PASSWORD_VARS[@]}"; do
    check_password_strength "$var" || true
done

# ===== 检查 3：TLS 证书有效性 =====
log ""
log "检查 3: TLS 证书有效性"

if [[ "$SKIP_TLS" == "true" ]]; then
    warn "  已跳过 TLS 证书检查（--skip-tls）"
else
    TLS_ENABLED="${JTE_API_TLS_ENABLED:-false}"
    CERT_FILE="${JTE_TLS_CERT_FILE:-}"
    KEY_FILE="${JTE_TLS_KEY_FILE:-}"

    if [[ "$TLS_ENABLED" == "true" ]]; then
        info "  TLS 已启用，检查证书..."

        if [[ -z "$CERT_FILE" || -z "$KEY_FILE" ]]; then
            fail "  TLS 已启用但 JTE_TLS_CERT_FILE 或 JTE_TLS_KEY_FILE 未设置"
        elif [[ ! -f "$CERT_FILE" ]]; then
            fail "  证书文件不存在: $CERT_FILE"
        elif [[ ! -f "$KEY_FILE" ]]; then
            fail "  私钥文件不存在: $KEY_FILE"
        else
            # 检查证书过期时间
            if command -v openssl &>/dev/null; then
                cert_end_date=$(openssl x509 -in "$CERT_FILE" -noout -enddate 2>/dev/null | cut -d= -f2)
                if [[ -n "$cert_end_date" ]]; then
                    cert_end_epoch=$(date -d "$cert_end_date" +%s 2>/dev/null || date -j -f "%b %d %H:%M:%S %Y %Z" "$cert_end_date" +%s 2>/dev/null || echo 0)
                    now_epoch=$(date +%s)
                    days_remaining=$(( (cert_end_epoch - now_epoch) / 86400 ))

                    if (( days_remaining < 0 )); then
                        fail "  证书已过期（过期时间: $cert_end_date）"
                    elif (( days_remaining < MIN_CERT_DAYS )); then
                        fail "  证书将在 ${days_remaining} 天内过期（过期时间: $cert_end_date），要求 >${MIN_CERT_DAYS} 天"
                    else
                        ok "  证书有效，剩余 ${days_remaining} 天（过期时间: $cert_end_date）"
                    fi

                    # 检查证书和私钥是否匹配
                    cert_mod=$(openssl x509 -in "$CERT_FILE" -noout -modulus 2>/dev/null | openssl md5 2>/dev/null)
                    key_mod=$(openssl rsa -in "$KEY_FILE" -noout -modulus 2>/dev/null | openssl md5 2>/dev/null)
                    if [[ "$cert_mod" == "$key_mod" ]]; then
                        ok "  证书与私钥匹配"
                    else
                        fail "  证书与私钥不匹配"
                    fi
                else
                    warn "  无法解析证书过期时间（openssl 不可用或证书格式错误）"
                fi
            else
                warn "  openssl 不可用，跳过证书过期时间检查"
            fi
        fi
    else
        info "  TLS 未启用（JTE_API_TLS_ENABLED=false），跳过证书检查"
        warn "  生产环境建议启用 TLS（等保2.0 要求传输安全）"
    fi
fi

# ===== 检查 4：磁盘空间 =====
log ""
log "检查 4: 磁盘空间（要求 >${MIN_DISK_GB}GB）"

check_disk_space() {
    local dir="$1"
    local label="$2"

    if [[ ! -d "$dir" ]]; then
        warn "  $label 目录不存在: $dir"
        return 1
    fi

    local avail_kb avail_gb
    avail_kb=$(df -P "$dir" 2>/dev/null | tail -1 | awk '{print $4}')
    avail_gb=$(( avail_kb / 1024 / 1024 ))

    if (( avail_gb < MIN_DISK_GB )); then
        fail "  $label 可用空间不足: ${avail_gb}GB（要求 >${MIN_DISK_GB}GB）"
        return 1
    else
        ok "  $label 可用空间: ${avail_gb}GB"
        return 0
    fi
}

check_disk_space "${JTE_DATA_DIR:-/app/data}" "数据目录"
check_disk_space "${JTE_LOG_DIR:-/app/logs}" "日志目录"
check_disk_space "${JTE_SPOOL_DIR:-/app/spool}" "缓冲目录"
check_disk_space "${JTE_MODULE_DIR:-/app/modules}" "模块目录"
check_disk_space "/var/lib/taos" "TDengine 数据"
check_disk_space "/var/lib/mysql" "MySQL 数据"

# ===== 检查 5：端口未被占用 =====
log ""
log "检查 5: 端口未被占用"

check_port_available() {
    local port="$1"
    local label="$2"

    # 检查端口是否被占用
    if ss -tlnp 2>/dev/null | grep -q ":${port} " || netstat -tlnp 2>/dev/null | grep -q ":${port} "; then
        # 如果是 JTE 自己监听的端口，允许被占用（重启场景）
        local pid
        pid=$(ss -tlnp 2>/dev/null | grep ":${port} " | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2 || echo "")
        if [[ -n "$pid" ]] && [[ -f "/proc/${pid}/cmdline" ]] && grep -q "jte" "/proc/${pid}/cmdline" 2>/dev/null; then
            ok "  $label 端口 $port 已被 JTE 进程占用（重启场景，正常）"
        else
            fail "  $label 端口 $port 已被其他进程占用"
            ss -tlnp 2>/dev/null | grep ":${port} " || netstat -tlnp 2>/dev/null | grep ":${port} "
        fi
    else
        ok "  $label 端口 $port 未被占用"
    fi
}

for port in "${JTE_PORTS[@]}"; do
    check_port_available "$port" "JTE"
done

# 依赖服务端口（仅在非跳过依赖检查时）
if [[ "$SKIP_DEPS" == "false" ]]; then
    for port in "${DEP_PORTS[@]}"; do
        check_port_available "$port" "依赖服务"
    done
fi

# ===== 检查 6：依赖服务可达性 =====
log ""
log "检查 6: 依赖服务可达性"

if [[ "$SKIP_DEPS" == "true" ]]; then
    warn "  已跳过依赖服务连通性检查（--skip-deps）"
else
    # MySQL 连通性
    if command -v mysql &>/dev/null; then
        info "  检查 MySQL..."
        mysql_host="${JTE_MYSQL_HOST:-${MYSQL_HOST:-mysql}}"
        mysql_port="${JTE_MYSQL_PORT:-${MYSQL_PORT:-3306}}"
        if mysql -h"$mysql_host" -P"$mysql_port" -u"${MYSQL_USER:-root}" -p"${MYSQL_ROOT_PASSWORD:-}" \
            -e "SELECT 1" 2>/dev/null | grep -q "1"; then
            ok "  MySQL 连接成功 (${mysql_host}:${mysql_port})"
        else
            fail "  MySQL 连接失败 (${mysql_host}:${mysql_port})"
        fi
    else
        warn "  mysql 客户端不可用，跳过 MySQL 连通性检查"
    fi

    # Redis 连通性
    if command -v redis-cli &>/dev/null; then
        info "  检查 Redis..."
        redis_host="${JTE_REDIS_HOST:-${REDIS_HOST:-redis}}"
        redis_port="${JTE_REDIS_PORT:-${REDIS_PORT:-6379}}"
        redis_pass="${JTE_REDIS_PASSWORD:-${REDIS_PASSWORD:-}}"
        if [[ -n "$redis_pass" ]]; then
            if redis-cli -h "$redis_host" -p "$redis_port" -a "$redis_pass" ping 2>/dev/null | grep -q PONG; then
                ok "  Redis 连接成功 (${redis_host}:${redis_port})"
            else
                fail "  Redis 连接失败 (${redis_host}:${redis_port})"
            fi
        else
            if redis-cli -h "$redis_host" -p "$redis_port" ping 2>/dev/null | grep -q PONG; then
                ok "  Redis 连接成功 (${redis_host}:${redis_port}, 无密码)"
                warn "  Redis 未设置密码，生产环境不安全"
            else
                fail "  Redis 连接失败 (${redis_host}:${redis_port})"
            fi
        fi
    else
        warn "  redis-cli 不可用，跳过 Redis 连通性检查"
    fi

    # TDengine 连通性
    if command -v taos &>/dev/null; then
        info "  检查 TDengine..."
        tdengine_host="${JTE_TDENGINE_HOST:-${TDENGINE_HOST:-tdengine}}"
        tdengine_port="${JTE_TDENGINE_PORT:-${TDENGINE_PORT:-6030}}"
        tdengine_pass="${JTE_TDENGINE_PASSWORD:-${TDENGINE_PASSWORD:-}}"
        if taos -h "$tdengine_host" -p "$tdengine_pass" -s "SELECT 1" 2>/dev/null | grep -q "1"; then
            ok "  TDengine 连接成功 (${tdengine_host}:${tdengine_port})"
        else
            fail "  TDengine 连接失败 (${tdengine_host}:${tdengine_port})"
        fi
    else
        warn "  taos 客户端不可用，跳过 TDengine 连通性检查"
    fi

    # MinIO 连通性
    if command -v mc &>/dev/null || command -v curl &>/dev/null; then
        info "  检查 MinIO..."
        minio_endpoint="${JTE_MINIO_ENDPOINT:-minio:9000}"
        minio_user="${MINIO_ROOT_USER:-${JTE_MINIO_ACCESS_KEY:-minioadmin}}"
        minio_pass="${MINIO_ROOT_PASSWORD:-${JTE_MINIO_SECRET_KEY:-}}"
        if command -v curl &>/dev/null; then
            # 使用 curl 检查 MinIO 健康
            minio_url="http://${minio_endpoint}/minio/health/live"
            http_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "$minio_url" 2>/dev/null || echo "000")
            if [[ "$http_code" == "200" ]]; then
                ok "  MinIO 健康检查通过 (${minio_endpoint})"
            else
                fail "  MinIO 健康检查失败 (HTTP ${http_code}, ${minio_endpoint})"
            fi
        fi
    else
        warn "  curl/mc 不可用，跳过 MinIO 连通性检查"
    fi
fi

# ===== 额外安全检查 =====
log ""
log "额外安全检查"

# 检查是否在生产模式
if [[ "${JTE_ENV:-}" != "production" ]]; then
    warn "  JTE_ENV 未设置为 'production'，生产环境必须设置"
fi

# 检查 pprof 是否关闭
if [[ "${JTE_PPROF_ENABLED:-false}" == "true" ]]; then
    fail "  JTE_PPROF_ENABLED=true，生产环境必须关闭 pprof 调试端点"
else
    ok "  pprof 调试端点已关闭"
fi

# 检查是否允许不安全 JWT
if [[ -n "${JTE_ALLOW_INSECURE_JWT:-}" ]]; then
    fail "  JTE_ALLOW_INSECURE_JWT 已设置，生产环境必须留空以强制 JWT 校验"
else
    ok "  JWT 安全校验已启用"
fi

# 检查 metrics 端点鉴权
if [[ -z "${JTE_METRICS_TOKEN:-}" ]]; then
    warn "  JTE_METRICS_TOKEN 未设置，/metrics 端点无鉴权（生产环境建议设置）"
else
    ok "  /metrics 端点鉴权已配置"
fi

# ===== 最终结果 =====
log ""
log "════════════════════════════════════════════════════"
log "预检查结果"
log "════════════════════════════════════════════════════"

if [[ "$JSON_OUTPUT" == "true" ]]; then
    echo "{"
    echo "  \"timestamp\": \"$(date -Iseconds)\","
    echo "  \"failures\": $FAIL,"
    echo "  \"warnings\": $WARN,"
    echo "  \"status\": \"$(if (( FAIL == 0 )); then echo "PASS"; else echo "FAIL"; fi)\""
    echo "}"
fi

if (( FAIL == 0 && WARN == 0 )); then
    ok "✅ 所有预检查通过，可以安全部署"
    exit 0
elif (( FAIL == 0 )); then
    warn "⚠️ 预检查通过，但存在 ${WARN} 个警告"
    warn "建议解决警告项后再部署，或确认警告可接受后继续"
    exit 0
else
    fail "❌ 存在 ${FAIL} 个预检查失败项，${WARN} 个警告"
    fail "请修复所有失败项后再尝试部署"
    exit 1
fi
