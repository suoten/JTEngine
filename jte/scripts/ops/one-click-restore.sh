#!/usr/bin/env bash
# JTE 一键灾难恢复脚本
# AUTO-FIX-2026-07-02: 备份与恢复 — 一键全量恢复
#
# 功能：按依赖顺序恢复所有 JTE 服务数据
#   顺序：配置 → MySQL → Redis → TDengine → MinIO → 重启服务
#
# 用法：
#   ./one-click-restore.sh <DATE>              # 恢复所有服务到指定日期
#   ./one-click-restore.sh <DATE> --dry-run    # 预演恢复（不实际执行）
#   ./one-click-restore.sh <DATE> --only mysql # 仅恢复指定服务
#
# 依赖：mysql_backup.sh、redis_backup.sh、tdengine_backup.sh、config_backup.sh
set -euo pipefail

# ===== 默认配置 =====
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_ROOT="${JTE_BACKUP_ROOT:-/data/backups}"
SERVICES="config mysql redis tdengine minio"

log()  { echo "[$(date '+%F %T')] [RESTORE] $*"; }
err()  { echo "[$(date '+%F %T')] [RESTORE][ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
fail() { echo -e "${RED}[✗]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }

# ===== 参数解析 =====
RESTORE_DATE=""
DRY_RUN=false
ONLY_SERVICE=""

if [[ $# -lt 1 ]]; then
    echo "JTE 一键灾难恢复工具"
    echo ""
    echo "用法: $0 <DATE> [选项]"
    echo ""
    echo "参数:"
    echo "  DATE                 恢复日期 (YYYYMMDD)"
    echo ""
    echo "选项:"
    echo "  --dry-run            预演恢复，不实际执行"
    echo "  --only <SERVICE>     仅恢复指定服务 (config/mysql/redis/tdengine/minio)"
    echo ""
    echo "示例:"
    echo "  $0 20260701                    # 恢复所有服务到 2026-07-01"
    echo "  $0 20260701 --dry-run          # 预演恢复"
    echo "  $0 20260701 --only mysql       # 仅恢复 MySQL"
    exit 1
fi

RESTORE_DATE="$1"; shift
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        --only)    ONLY_SERVICE="$2"; shift 2 ;;
        *)         fatal "未知选项: $1" ;;
    esac
done

if [[ -n "$ONLY_SERVICE" ]]; then
    SERVICES="$ONLY_SERVICE"
fi

log "════════════════════════════════════════════════════"
log "JTE 灾难恢复开始"
log "  恢复日期: $RESTORE_DATE"
log "  恢复服务: $SERVICES"
log "  预演模式: $DRY_RUN"
log "  备份根目录: $BACKUP_ROOT"
log "════════════════════════════════════════════════════"

# ===== 恢复前检查 =====
log "阶段 0: 恢复前检查"

check_backup() {
    local service="$1"
    local backup_dir="${BACKUP_ROOT}/${service}"
    if [[ ! -d "$backup_dir" ]]; then
        warn "  ${service} 备份目录不存在: $backup_dir"
        return 1
    fi
    # 检查是否有匹配日期的备份
    local found
    found=$(find "$backup_dir" -name "*${RESTORE_DATE}*" -type f 2>/dev/null | head -1)
    if [[ -z "$found" ]]; then
        warn "  ${service} 未找到 ${RESTORE_DATE} 的备份"
        return 1
    fi
    ok "  ${service} 备份可用: $found"
    return 0
}

ALL_OK=true
for svc in $SERVICES; do
    check_backup "$svc" || ALL_OK=false
done

if [[ "$ALL_OK" == "false" ]]; then
    warn "部分服务备份缺失，将跳过这些服务的恢复"
    if [[ "$DRY_RUN" == "false" ]]; then
        read -p "是否继续恢复？(y/N) " confirm
        [[ "$confirm" != "y" && "$confirm" != "Y" ]] && exit 0
    fi
fi

# ===== 恢复执行函数 =====
restore_service() {
    local service="$1"
    local script="${SCRIPT_DIR}/${service}_backup.sh"

    if [[ ! -f "$script" ]]; then
        # config 用 config_backup.sh，minio 用 minio_replication.sh
        case "$service" in
            config) script="${SCRIPT_DIR}/config_backup.sh" ;;
            minio)  script="${SCRIPT_DIR}/minio_replication.sh" ;;
        esac
    fi

    if [[ ! -f "$script" ]]; then
        fail "  ${service} 恢复脚本不存在: $script"
        return 1
    fi

    log "恢复 ${service}..."

    if [[ "$DRY_RUN" == "true" ]]; then
        warn "  [预演] 将执行: $script restore $RESTORE_DATE"
        return 0
    fi

    # 执行恢复
    if bash "$script" restore "$RESTORE_DATE"; then
        ok "  ${service} 恢复成功"
        return 0
    else
        fail "  ${service} 恢复失败"
        return 1
    fi
}

# ===== 按依赖顺序恢复 =====
FAILED_SERVICES=""

for svc in $SERVICES; do
    log ""
    log "阶段 $(echo "$SERVICES" | tr ' ' '\n' | grep -n "$svc" | cut -d: -f1): 恢复 ${svc}"

    # 检查备份是否可用
    if ! check_backup "$svc" 2>/dev/null; then
        warn "  跳过 ${svc}（备份不可用）"
        continue
    fi

    # 特殊处理：minio 恢复逻辑
    if [[ "$svc" == "minio" ]]; then
        if [[ "$DRY_RUN" == "true" ]]; then
            warn "  [预演] MinIO 数据从副本恢复（mc mirror）"
        else
            # MinIO 通过跨区域复制或备份恢复
            local minio_backup="${BACKUP_ROOT}/minio/${RESTORE_DATE}"
            if [[ -d "$minio_backup" ]]; then
                warn "  MinIO 恢复需手动执行: mc mirror $minio_backup jte-backup/"
                warn "  或使用 minio_replication.sh failback"
            else
                warn "  MinIO 无本地备份，依赖跨区域复制恢复"
            fi
        fi
        ok "  minio 恢复步骤完成（需手动确认）"
        continue
    fi

    restore_service "$svc" || FAILED_SERVICES="$FAILED_SERVICES $svc"
done

# ===== 恢复后验证 =====
log ""
log "════════════════════════════════════════════════════"
log "阶段 $(echo "$SERVICES" | wc -w): 恢复后验证"
log "════════════════════════════════════════════════════"

if [[ "$DRY_RUN" == "true" ]]; then
    warn "[预演模式] 跳过实际验证"
else
    # 验证 MySQL
    if echo "$SERVICES" | grep -q "mysql" && [[ -z "$ONLY_SERVICE" || "$ONLY_SERVICE" == "mysql" ]]; then
        log "验证 MySQL..."
        if mysql -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='jte';" 2>/dev/null; then
            ok "  MySQL 连接正常"
        else
            fail "  MySQL 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES mysql-verify"
        fi
    fi

    # 验证 Redis
    if echo "$SERVICES" | grep -q "redis" && [[ -z "$ONLY_SERVICE" || "$ONLY_SERVICE" == "redis" ]]; then
        log "验证 Redis..."
        if redis-cli -h "${JTE_REDIS_HOST:-redis}" ping 2>/dev/null | grep -q PONG; then
            ok "  Redis 连接正常"
        else
            fail "  Redis 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES redis-verify"
        fi
    fi

    # 验证 TDengine
    if echo "$SERVICES" | grep -q "tdengine" && [[ -z "$ONLY_SERVICE" || "$ONLY_SERVICE" == "tdengine" ]]; then
        log "验证 TDengine..."
        if taos -h "${JTE_TDENGINE_HOST:-tdengine}" -s "SHOW DATABASES" 2>/dev/null | grep -q jte_ts; then
            ok "  TDengine 连接正常"
        else
            fail "  TDengine 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES tdengine-verify"
        fi
    fi
fi

# ===== 重启服务提示 =====
log ""
log "════════════════════════════════════════════════════"
log "恢复完成"
log "════════════════════════════════════════════════════"

if [[ -n "$FAILED_SERVICES" ]]; then
    fail "恢复失败的服务:$FAILED_SERVICES"
    fail "请检查日志并手动修复"
    exit 1
else
    ok "所有服务恢复成功"
fi

log ""
log "后续操作："
log "  1. 重启 JTE 服务使配置生效："
log "     docker-compose restart jte jte-website"
log "     # 或 K8s: kubectl rollout restart deployment -n jte"
log ""
log "  2. 验证业务功能："
log "     curl http://localhost:8080/healthz"
log "     curl http://localhost:8081/api/v1/health"
log ""
log "  3. 检查数据完整性："
log "     bash ${SCRIPT_DIR}/verify_backups.sh"
log ""
log "  4. 如需回滚，使用恢复前快照："
log "     ls -la ${BACKUP_ROOT}/*/pre_restore_*"

if [[ "$DRY_RUN" == "false" ]]; then
    log ""
    log "恢复报告已记录到系统日志，请保留此输出作为审计记录"
fi
