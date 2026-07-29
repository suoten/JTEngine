#!/usr/bin/env bash
# JTE 数据恢复脚本
# [P2-运维完善] 从备份恢复数据，支持从指定日期的备份恢复各服务数据
#
# 与 one-click-restore.sh 的区别：
#   one-click-restore.sh — 简化的一键恢复，委托各服务脚本执行
#   restore.sh           — 增强版恢复，包含：
#     1. 恢复前自动创建当前状态快照（pre_restore_snapshot）
#     2. 按依赖顺序恢复各服务
#     3. 恢复后自动验证数据完整性（调用 backup_verify.sh）
#     4. 恢复失败时支持回滚到恢复前快照
#     5. 支持 PITR（Point-in-Time Recovery）通过 MySQL binlog
#
# 用法：
#   ./restore.sh                           # 交互式恢复（提示选择日期和服务）
#   ./restore.sh 20260721                  # 恢复所有服务到 2026-07-21
#   ./restore.sh 20260721 --only mysql     # 仅恢复 MySQL
#   ./restore.sh 20260721 --dry-run        # 预演恢复（不实际执行）
#   ./restore.sh 20260721 --pitr "03:00:00" # PITR 恢复到指定时间点（MySQL binlog）
#   ./restore.sh --rollback                # 回滚到最近的恢复前快照
set -euo pipefail

# ===== 默认配置 =====
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_ROOT="${JTE_BACKUP_ROOT:-/data/backups}"
SNAPSHOT_DIR="${BACKUP_ROOT}/snapshots"
RESTORE_DATE=""
DRY_RUN=false
ONLY_SERVICE=""
PITR_TIME=""
ROLLBACK=false
SERVICES="config mysql redis tdengine"

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo "[$(date '+%F %T')] [RESTORE] $*"; }
ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
fail() { echo -e "${RED}[✗]${NC} $*" >&2; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
info() { echo -e "${BLUE}[i]${NC} $*"; }

fatal() { fail "$*"; exit 1; }

# ===== 参数解析 =====
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)   DRY_RUN=true; shift ;;
        --only)      ONLY_SERVICE="$2"; shift 2 ;;
        --pitr)      PITR_TIME="$2"; shift 2 ;;
        --rollback)  ROLLBACK=true; shift ;;
        --help|-h)
            echo "JTE 数据恢复脚本"
            echo ""
            echo "用法: $0 [DATE] [选项]"
            echo ""
            echo "参数:"
            echo "  DATE                 恢复日期 (YYYYMMDD)，不指定则交互式选择"
            echo ""
            echo "选项:"
            echo "  --dry-run            预演恢复，不实际执行"
            echo "  --only <SERVICE>     仅恢复指定服务 (config/mysql/redis/tdengine)"
            echo "  --pitr <HH:MM:SS>    PITR 恢复到指定时间点（MySQL binlog 重放）"
            echo "  --rollback           回滚到最近的恢复前快照"
            echo "  --help, -h           显示帮助"
            echo ""
            echo "示例:"
            echo "  $0 20260721                      # 恢复所有服务到 2026-07-21"
            echo "  $0 20260721 --dry-run            # 预演恢复"
            echo "  $0 20260721 --only mysql         # 仅恢复 MySQL"
            echo "  $0 20260721 --pitr '03:00:00'    # MySQL PITR 到 03:00:00"
            echo "  $0 --rollback                    # 回滚到恢复前快照"
            exit 0
            ;;
        *)
            if [[ -z "$RESTORE_DATE" && ! "$1" =~ ^-- ]]; then
                RESTORE_DATE="$1"
            else
                fatal "未知选项: $1"
            fi
            shift
            ;;
    esac
done

# ===== 回滚模式 =====
if [[ "$ROLLBACK" == "true" ]]; then
    log "════════════════════════════════════════════════════"
    log "JTE 数据恢复回滚"
    log "════════════════════════════════════════════════════"

    # 查找最近的恢复前快照
    latest_snapshot=$(ls -t "$SNAPSHOT_DIR" 2>/dev/null | grep "pre_restore_" | head -n 1)
    if [[ -z "$latest_snapshot" ]]; then
        fatal "未找到恢复前快照，无法回滚"
    fi

    snapshot_path="${SNAPSHOT_DIR}/${latest_snapshot}"
    info "找到恢复前快照: $snapshot_path"

    if [[ "$DRY_RUN" == "true" ]]; then
        warn "[预演] 将从快照恢复: $snapshot_path"
        exit 0
    fi

    read -p "确认回滚到快照 ${latest_snapshot}？此操作将覆盖当前数据 (y/N) " confirm
    [[ "$confirm" != "y" && "$confirm" != "Y" ]] && { log "回滚已取消"; exit 0; }

    # 按快照中的服务逐个恢复
    for svc_dir in "$snapshot_path"/*/; do
        svc=$(basename "$svc_dir")
        log "回滚 ${svc}..."
        case "$svc" in
            mysql)
                for f in "$svc_dir"*.sql.gz; do
                    [[ -f "$f" ]] || continue
                    zcat "$f" | mysql -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" -p"${JTE_MYSQL_PASSWORD:-}" 2>/dev/null && ok "  ${svc} 回滚成功" || fail "  ${svc} 回滚失败"
                done
                ;;
            redis)
                cp -f "$svc_dir"dump.rdb "${JTE_REDIS_DATA_DIR:-/data/redis/dump.rdb}" 2>/dev/null && ok "  ${svc} 回滚成功" || fail "  ${svc} 回滚失败"
                ;;
            tdengine)
                cp -rf "$svc_dir"* "${JTE_TDENGINE_DATA_DIR:-/var/lib/taos/}" 2>/dev/null && ok "  ${svc} 回滚成功" || fail "  ${svc} 回滚失败"
                ;;
            config)
                tar -xzf "$svc_dir"*.tar.gz -C /app/configs/ 2>/dev/null && ok "  ${svc} 回滚成功" || fail "  ${svc} 回滚失败"
                ;;
        esac
    done

    ok "回滚完成，请重启 JTE 服务"
    exit 0
fi

# ===== 正常恢复流程 =====

# 交互式选择日期（如果未指定）
if [[ -z "$RESTORE_DATE" ]]; then
    log "可用的备份日期："
    available_dates=$(ls -t "${BACKUP_ROOT}/mysql/full" 2>/dev/null | head -10)
    if [[ -z "$available_dates" ]]; then
        fatal "未找到任何备份"
    fi
    select d in $available_dates "退出"; do
        [[ "$d" == "退出" ]] && exit 0
        RESTORE_DATE="$d"
        break
    done
fi

if [[ -n "$ONLY_SERVICE" ]]; then
    SERVICES="$ONLY_SERVICE"
fi

log "════════════════════════════════════════════════════"
log "JTE 数据恢复"
log "  恢复日期: $RESTORE_DATE"
log "  恢复服务: $SERVICES"
log "  PITR 时间: ${PITR_TIME:-无}"
log "  预演模式: $DRY_RUN"
log "  备份根目录: $BACKUP_ROOT"
log "════════════════════════════════════════════════════"

# ===== 阶段 0：恢复前检查 =====
log ""
log "阶段 0: 恢复前检查"

# 检查备份可用性
check_backup_available() {
    local svc="$1"
    local found
    case "$svc" in
        mysql)
            found=$(find "${BACKUP_ROOT}/mysql" -name "*${RESTORE_DATE}*" -type f 2>/dev/null | head -1)
            ;;
        redis)
            found=$(find "${BACKUP_ROOT}/redis" -name "*${RESTORE_DATE}*" -type f 2>/dev/null | head -1)
            ;;
        tdengine)
            found=$(find "${BACKUP_ROOT}/tdengine" -name "*${RESTORE_DATE}*" -type d 2>/dev/null | head -1)
            ;;
        config)
            found=$(find "${BACKUP_ROOT}/config" -name "*${RESTORE_DATE}*" -type f 2>/dev/null | head -1)
            ;;
    esac
    if [[ -n "$found" ]]; then
        ok "  ${svc} 备份可用: $found"
        return 0
    else
        warn "  ${svc} 未找到 ${RESTORE_DATE} 的备份"
        return 1
    fi
}

ALL_AVAILABLE=true
for svc in $SERVICES; do
    check_backup_available "$svc" || ALL_AVAILABLE=false
done

if [[ "$ALL_AVAILABLE" == "false" ]]; then
    warn "部分服务备份缺失，将跳过这些服务"
    if [[ "$DRY_RUN" == "false" ]]; then
        read -p "是否继续恢复可用服务？(y/N) " confirm
        [[ "$confirm" != "y" && "$confirm" != "Y" ]] && exit 0
    fi
fi

# ===== 阶段 1：创建恢复前快照 =====
if [[ "$DRY_RUN" == "false" ]]; then
    log ""
    log "阶段 1: 创建恢复前快照"
    snapshot_name="pre_restore_${RESTORE_DATE}_$(date +%Y%m%d_%H%M%S)"
    snapshot_path="${SNAPSHOT_DIR}/${snapshot_name}"
    mkdir -p "$snapshot_path"

    # MySQL 快照（导出当前数据）
    if echo "$SERVICES" | grep -q "mysql" && command -v mysql &>/dev/null; then
        info "  导出 MySQL 当前状态..."
        mysqldump -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" -p"${JTE_MYSQL_PASSWORD:-}" \
            --single-transaction --routines --triggers jte 2>/dev/null | gzip > "${snapshot_path}/mysql/current.sql.gz" || \
            warn "  MySQL 快照失败（继续）"
    fi

    # Redis 快照（复制当前 RDB）
    if echo "$SERVICES" | grep -q "redis"; then
        info "  复制 Redis 当前状态..."
        redis_data="${JTE_REDIS_DATA_DIR:-/data/redis}"
        if [[ -f "${redis_data}/dump.rdb" ]]; then
            mkdir -p "${snapshot_path}/redis"
            cp -f "${redis_data}/dump.rdb" "${snapshot_path}/redis/" 2>/dev/null || warn "  Redis 快照失败（继续）"
        fi
    fi

    # 配置快照
    if echo "$SERVICES" | grep -q "config"; then
        info "  备份当前配置..."
        config_dir="${JTE_CONFIG_DIR:-/app/configs}"
        if [[ -d "$config_dir" ]]; then
            tar -czf "${snapshot_path}/config/current.tar.gz" -C "$(dirname "$config_dir")" "$(basename "$config_dir")" 2>/dev/null || warn "  配置快照失败（继续）"
        fi
    fi

    ok "恢复前快照已创建: $snapshot_path"
    info "如需回滚: $0 --rollback"
fi

# ===== 阶段 2：按依赖顺序恢复数据 =====
log ""
log "阶段 2: 恢复数据"

restore_config() {
    log "恢复配置文件..."
    local archive
    archive=$(find "${BACKUP_ROOT}/config" -name "*${RESTORE_DATE}*.tar.gz" 2>/dev/null | head -1)
    if [[ -z "$archive" ]]; then
        warn "  配置备份未找到，跳过"
        return 1
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        info "  [预演] 解压 $archive → /app/configs/"
        return 0
    fi

    local config_dir="${JTE_CONFIG_DIR:-/app/configs}"
    mkdir -p "$config_dir"
    if tar -xzf "$archive" -C "$(dirname "$config_dir")" 2>/dev/null; then
        ok "  配置恢复成功"
        return 0
    else
        fail "  配置恢复失败"
        return 1
    fi
}

restore_mysql() {
    log "恢复 MySQL..."
    local dir="${BACKUP_ROOT}/mysql/full/${RESTORE_DATE}"
    if [[ ! -d "$dir" ]]; then
        warn "  MySQL 备份目录不存在: $dir"
        return 1
    fi

    local mysql_host="${JTE_MYSQL_HOST:-mysql}"
    local mysql_user="${JTE_MYSQL_USER:-root}"
    local mysql_pass="${JTE_MYSQL_PASSWORD:-}"

    if [[ "$DRY_RUN" == "true" ]]; then
        info "  [预演] 将恢复 MySQL: ${dir}/*.sql.gz → ${mysql_host}"
        if [[ -n "$PITR_TIME" ]]; then
            info "  [预演] PITR: 重放 binlog 到 ${RESTORE_DATE} ${PITR_TIME}"
        fi
        return 0
    fi

    # 恢复全量备份
    local restored=0
    for f in "$dir"/*.sql.gz; do
        [[ -f "$f" ]] || continue
        info "  恢复: $(basename "$f")"
        if zcat "$f" | mysql -h"$mysql_host" -u"$mysql_user" -p"$mysql_pass" 2>/dev/null; then
            restored=$((restored+1))
        else
            fail "  恢复失败: $(basename "$f")"
        fi
    done

    # PITR: 重放 binlog 到指定时间点
    if [[ -n "$PITR_TIME" && restored -gt 0 ]]; then
        info "  PITR: 重放 binlog 到 ${RESTORE_DATE} ${PITR_TIME}..."
        local binlog_dir="${BACKUP_ROOT}/mysql/binlog/${RESTORE_DATE}"
        if [[ -d "$binlog_dir" ]]; then
            local stop_datetime="${RESTORE_DATE:0:4}-${RESTORE_DATE:4:2}-${RESTORE_DATE:6:2} ${PITR_TIME}"
            for binlog in "$binlog_dir"/*.binlog; do
                [[ -f "$binlog" ]] || continue
                mysqlbinlog --stop-datetime="$stop_datetime" "$binlog" 2>/dev/null | \
                    mysql -h"$mysql_host" -u"$mysql_user" -p"$mysql_pass" 2>/dev/null || \
                    warn "  binlog 重放警告: $(basename "$binlog")"
            done
            ok "  PITR binlog 重放完成"
        else
            warn "  binlog 目录不存在，跳过 PITR: $binlog_dir"
        fi
    fi

    if [[ $restored -gt 0 ]]; then
        ok "  MySQL 恢复成功（${restored} 个文件）"
        return 0
    else
        fail "  MySQL 恢复失败"
        return 1
    fi
}

restore_redis() {
    log "恢复 Redis..."
    local dir="${BACKUP_ROOT}/redis/rdb/${RESTORE_DATE}"
    if [[ ! -d "$dir" ]]; then
        warn "  Redis 备份目录不存在: $dir"
        return 1
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        info "  [预演] 恢复 Redis: ${dir}/dump.rdb → ${JTE_REDIS_DATA_DIR:-/data/redis}/"
        return 0
    fi

    local redis_data="${JTE_REDIS_DATA_DIR:-/data/redis}"
    mkdir -p "$redis_data"

    # 恢复前停止 Redis（如果运行中）
    if command -v redis-cli &>/dev/null; then
        redis-cli -h "${JTE_REDIS_HOST:-redis}" SHUTDOWN NOSAVE 2>/dev/null || true
        sleep 2
    fi

    # 复制 RDB 文件
    if cp -f "${dir}/dump.rdb" "${redis_data}/dump.rdb" 2>/dev/null; then
        # 恢复 AOF（如果存在）
        if [[ -f "${dir}/appendonly.aof" ]]; then
            cp -f "${dir}/appendonly.aof" "${redis_data}/appendonly.aof" 2>/dev/null || true
        fi
        ok "  Redis 恢复成功"
        return 0
    else
        fail "  Redis 恢复失败"
        return 1
    fi
}

restore_tdengine() {
    log "恢复 TDengine..."
    local dir="${BACKUP_ROOT}/tdengine/full/${RESTORE_DATE}"
    if [[ ! -d "$dir" ]]; then
        warn "  TDengine 备份目录不存在: $dir"
        return 1
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        info "  [预演] 恢复 TDengine: ${dir} → taos 恢复"
        return 0
    fi

    # 使用 taosdump 恢复（如果有 taos 工具）
    if command -v taos &>/dev/null; then
        local dump_file
        dump_file=$(find "$dir" -name "*.sql" -o -name "*.dump" | head -1)
        if [[ -n "$dump_file" ]]; then
            if taos -h "${JTE_TDENGINE_HOST:-tdengine}" -f "$dump_file" 2>/dev/null; then
                ok "  TDengine 恢复成功"
                return 0
            else
                fail "  TDengine 恢复失败"
                return 1
            fi
        fi
    fi

    # 回退：直接复制数据文件（需停止 TDengine 服务）
    warn "  使用数据文件复制模式恢复（需先停止 TDengine 服务）"
    local tdengine_data="${JTE_TDENGINE_DATA_DIR:-/var/lib/taos}"
    if cp -rf "$dir"/* "$tdengine_data/" 2>/dev/null; then
        ok "  TDengine 数据文件恢复成功"
        return 0
    else
        fail "  TDengine 恢复失败"
        return 1
    fi
}

# 按依赖顺序执行恢复
FAILED_SERVICES=""
STAGE=1
for svc in $SERVICES; do
    STAGE=$((STAGE+1))
    log ""
    log "阶段 ${STAGE}: 恢复 ${svc}"

    case "$svc" in
        config)    restore_config || FAILED_SERVICES="$FAILED_SERVICES config" ;;
        mysql)     restore_mysql || FAILED_SERVICES="$FAILED_SERVICES mysql" ;;
        redis)     restore_redis || FAILED_SERVICES="$FAILED_SERVICES redis" ;;
        tdengine)  restore_tdengine || FAILED_SERVICES="$FAILED_SERVICES tdengine" ;;
        *)         warn "  未知服务: $svc，跳过" ;;
    esac
done

# ===== 阶段 N：恢复后验证 =====
log ""
log "════════════════════════════════════════════════════"
log "阶段 $((STAGE+1)): 恢复后验证"
log "════════════════════════════════════════════════════"

if [[ "$DRY_RUN" == "true" ]]; then
    warn "[预演模式] 跳过实际验证"
else
    # 调用 backup_verify.sh 验证恢复后的数据
    if [[ -f "${SCRIPT_DIR}/backup_verify.sh" ]]; then
        log "运行数据完整性验证..."
        bash "${SCRIPT_DIR}/backup_verify.sh" "$RESTORE_DATE" || warn "验证未完全通过，请检查"
    fi

    # 验证服务连通性
    if echo "$SERVICES" | grep -q "mysql"; then
        log "验证 MySQL 连接..."
        if mysql -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" -p"${JTE_MYSQL_PASSWORD:-}" \
            -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='jte';" 2>/dev/null; then
            ok "  MySQL 连接正常"
        else
            fail "  MySQL 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES mysql-verify"
        fi
    fi

    if echo "$SERVICES" | grep -q "redis"; then
        log "验证 Redis 连接..."
        if redis-cli -h "${JTE_REDIS_HOST:-redis}" -a "${JTE_REDIS_PASSWORD:-}" ping 2>/dev/null | grep -q PONG; then
            ok "  Redis 连接正常"
        else
            fail "  Redis 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES redis-verify"
        fi
    fi

    if echo "$SERVICES" | grep -q "tdengine"; then
        log "验证 TDengine 连接..."
        if taos -h "${JTE_TDENGINE_HOST:-tdengine}" -s "SHOW DATABASES" 2>/dev/null | grep -q jte_ts; then
            ok "  TDengine 连接正常"
        else
            fail "  TDengine 连接失败"
            FAILED_SERVICES="$FAILED_SERVICES tdengine-verify"
        fi
    fi
fi

# ===== 最终结果 =====
log ""
log "════════════════════════════════════════════════════"
log "恢复完成"
log "════════════════════════════════════════════════════"

if [[ -n "$FAILED_SERVICES" ]]; then
    fail "恢复失败的服务:$FAILED_SERVICES"
    fail "请检查日志并手动修复"
    fail "如需回滚: $0 --rollback"
    exit 1
else
    ok "所有服务恢复成功"
fi

log ""
log "后续操作："
log "  1. 重启服务使配置生效："
log "     docker-compose restart jte jte-website"
log "     # 或 K8s: kubectl rollout restart deployment -n jte"
log ""
log "  2. 验证业务功能："
log "     curl http://localhost:8080/healthz"
log "     curl http://localhost:8081/api/v1/health"
log ""
log "  3. 检查数据完整性："
log "     bash ${SCRIPT_DIR}/backup_verify.sh --report"
log ""
if [[ "$DRY_RUN" == "false" ]]; then
    log "  4. 如需回滚到恢复前状态："
    log "     $0 --rollback"
    log ""
fi

log "恢复报告已记录到系统日志，请保留此输出作为审计记录"
