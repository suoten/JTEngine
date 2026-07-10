#!/usr/bin/env bash
# JTE Redis 备份脚本
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复
#
# 策略：每日 RDB + AOF，RPO=1s（AOF everysec），RTO=5min
#   - RDB：每日凌晨 BGSAVE 后复制 dump.rdb
#   - AOF：持续记录写命令（everysec fsync），每小时复制一次 AOF 文件
#   - 保留：RDB 7 天，AOF 3 天
#
# 用法：
#   ./redis_backup.sh rdb                # RDB 快照备份
#   ./redis_backup.sh aof                # AOF 文件备份
#   ./redis_backup.sh restore <DATE>     # 从 RDB 恢复
#
# 依赖：redis-cli、ssh（如 Redis 在远程）。
set -euo pipefail

# ===== 默认配置 =====
REDIS_HOST="${JTE_REDIS_HOST:-redis}"
REDIS_PORT="${JTE_REDIS_PORT:-6379}"
REDIS_PASSWORD="${JTE_REDIS_PASSWORD:-}"
REDIS_MODE="${JTE_REDIS_MODE:-single}"   # single | sentinel | cluster
BACKUP_ROOT="${JTE_REDIS_BACKUP_DIR:-/data/backups/redis}"
REDIS_DATA_DIR="${JTE_REDIS_DATA_DIR:-/var/lib/redis}"
SSH_USER="${JTE_SSH_USER:-root}"
RDB_RETAIN_DAYS="${JTE_REDIS_RDB_RETAIN:-7}"
AOF_RETAIN_DAYS="${JTE_REDIS_AOF_RETAIN:-3}"

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

redis_cli() {
    local args=(-h "$REDIS_HOST" -p "$REDIS_PORT")
    [[ -n "$REDIS_PASSWORD" ]] && args+=(-a "$REDIS_PASSWORD")
    redis-cli "${args[@]}" "$@"
}

run_on_redis_host() {
    if [[ "$REDIS_HOST" == "$(hostname)" || "$REDIS_HOST" == "$(hostname -s)" ]]; then
        "$@"
    else
        ssh "${SSH_USER}@${REDIS_HOST}" "$*"
    fi
}

# RDB 快照备份
do_rdb() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local dir="${BACKUP_ROOT}/rdb/${date_str}"
    mkdir -p "$dir"

    log "Redis RDB 备份开始: ${dir}"

    if [[ "$REDIS_MODE" == "cluster" ]]; then
        # Cluster 模式：每个 master 节点都要 BGSAVE
        log "Cluster 模式：对所有 master 节点执行 BGSAVE..."
        local nodes
        nodes=$(redis_cli cluster nodes 2>/dev/null | awk '$3 == "master" {split($2, a, ":"); print a[1]}')
        if [[ -z "$nodes" ]]; then
            fatal "无法获取 cluster master 节点列表"
        fi
        for node in $nodes; do
            log "  BGSAVE on ${node}..."
            ssh "${SSH_USER}@${node}" "redis-cli -h ${node} -p ${REDIS_PORT} $([[ -n "$REDIS_PASSWORD" ]] && echo -a $REDIS_PASSWORD) BGSAVE" 2>/dev/null || err "  ${node} BGSAVE 失败"
        done
        # 等待所有 BGSAVE 完成
        log "等待 BGSAVE 完成..."
        sleep 10
        # 复制每个 master 的 dump.rdb
        for node in $nodes; do
            log "  复制 ${node} 的 dump.rdb..."
            ssh "${SSH_USER}@${node}" "cat ${REDIS_DATA_DIR}/dump.rdb" > "${dir}/${node}-dump.rdb" 2>/dev/null || err "  复制 ${node} dump.rdb 失败"
        done
    else
        # single/sentinel 模式
        log "触发 BGSAVE..."
        redis_cli BGSAVE >/dev/null 2>&1 || fatal "BGSAVE 失败"

        # 等待 BGSAVE 完成（轮询 LASTSAVE 时间戳变化）
        local last_save
        last_save=$(redis_cli LASTSAVE 2>/dev/null | tr -d '[:space:]')
        log "等待 BGSAVE 完成（LASTSAVE: ${last_save}）..."
        for i in $(seq 1 60); do
            sleep 2
            local new_save
            new_save=$(redis_cli LASTSAVE 2>/dev/null | tr -d '[:space:]')
            if [[ "$new_save" != "$last_save" ]]; then
                log "BGSAVE 完成（新 LASTSAVE: ${new_save}）"
                break
            fi
        done

        # 复制 dump.rdb
        log "复制 dump.rdb..."
        run_on_redis_host cat "${REDIS_DATA_DIR}/dump.rdb" > "${dir}/dump.rdb" 2>/dev/null || fatal "复制 dump.rdb 失败"
    fi

    # 元信息
    cat > "${dir}/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=rdb
redis_host=${REDIS_HOST}
redis_mode=${REDIS_MODE}
EOF

    # 清理过期
    log "清理超过 ${RDB_RETAIN_DAYS} 天的 RDB 备份..."
    find "${BACKUP_ROOT}/rdb" -maxdepth 1 -type d -mtime +"$RDB_RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

    log "RDB 备份完成: ${dir}"
    du -sh "$dir"
}

# AOF 文件备份
do_aof() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local dir="${BACKUP_ROOT}/aof/${date_str}"
    mkdir -p "$dir"

    log "Redis AOF 备份开始: ${dir}"

    if [[ "$REDIS_MODE" == "cluster" ]]; then
        local nodes
        nodes=$(redis_cli cluster nodes 2>/dev/null | awk '$3 == "master" {split($2, a, ":"); print a[1]}')
        for node in $nodes; do
            log "  复制 ${node} 的 AOF..."
            # 触发 AOF 重写以获取一个完整的 base AOF
            ssh "${SSH_USER}@${node}" "redis-cli -h ${node} -p ${REDIS_PORT} $([[ -n "$REDIS_PASSWORD" ]] && echo -a $REDIS_PASSWORD) BGREWRITEAOF" 2>/dev/null || true
            sleep 5
            ssh "${SSH_USER}@${node}" "cat ${REDIS_DATA_DIR}/appendonly.aof.1.base.rdb 2>/dev/null || cat ${REDIS_DATA_DIR}/appendonly.aof 2>/dev/null || true" > "${dir}/${node}-appendonly.aof" 2>/dev/null || err "  ${node} AOF 复制失败"
        done
    else
        log "触发 BGREWRITEAOF..."
        redis_cli BGREWRITEAOF >/dev/null 2>&1 || err "BGREWRITEAOF 失败（可能 AOF 未启用）"
        sleep 5
        log "复制 AOF 文件..."
        # Redis 7.x 多文件 AOF 结构
        run_on_redis_host "tar -czf /tmp/redis_aof_$(date +%s).tar.gz -C ${REDIS_DATA_DIR} appendonlydir 2>/dev/null || tar -czf /tmp/redis_aof_$(date +%s).tar.gz -C ${REDIS_DATA_DIR} appendonly.aof 2>/dev/null" || err "打包 AOF 失败"
        run_on_redis_host "ls -t /tmp/redis_aof_*.tar.gz | head -1 | xargs cat" > "${dir}/aof.tar.gz" 2>/dev/null || fatal "复制 AOF 失败"
        run_on_redis_host "rm -f /tmp/redis_aof_*.tar.gz" || true
    fi

    cat > "${dir}/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=aof
redis_host=${REDIS_HOST}
EOF

    log "清理超过 ${AOF_RETAIN_DAYS} 天的 AOF 备份..."
    find "${BACKUP_ROOT}/aof" -maxdepth 1 -type d -mtime +"$AOF_RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

    log "AOF 备份完成: ${dir}"
    du -sh "$dir"
}

# 恢复
do_restore() {
    local target_date="$1"
    local rdb_dir="${BACKUP_ROOT}/rdb/${target_date}"
    if [[ ! -d "$rdb_dir" ]]; then
        fatal "RDB 备份目录不存在: ${rdb_dir}（可用: $(ls "${BACKUP_ROOT}/rdb" 2>/dev/null | tr '\n' ' ')）"
    fi

    log "==== Redis 恢复开始 ===="
    log "目标备份: ${rdb_dir}"

    # 1. 停止 Redis
    log "步骤 1: 停止 Redis..."
    run_on_redis_host "systemctl stop redis" || err "停止 redis 失败（可能服务名不同）"

    # 2. 替换 dump.rdb
    log "步骤 2: 替换 dump.rdb..."
    run_on_redis_host "rm -f ${REDIS_DATA_DIR}/dump.rdb ${REDIS_DATA_DIR}/appendonly*" || true
    if [[ "$REDIS_MODE" == "cluster" ]]; then
        err "Cluster 模式恢复需逐节点操作，请手动将 ${rdb_dir}/<node>-dump.rdb 复制到各节点 ${REDIS_DATA_DIR}/dump.rdb"
        err "恢复后需执行 cluster meet 重建拓扑（如集群配置丢失）"
    else
        run_on_redis_host "cat > ${REDIS_DATA_DIR}/dump.rdb" < "${rdb_dir}/dump.rdb" || fatal "替换 dump.rdb 失败"
    fi

    # 3. 启动 Redis
    log "步骤 3: 启动 Redis..."
    run_on_redis_host "systemctl start redis" || err "启动 redis 失败"

    sleep 3
    log "验证 Redis..."
    redis_cli PING || err "Redis PING 失败"

    log "Redis 恢复完成"
}

main() {
    (( $# >= 1 )) || { echo "用法: $0 {rdb|aof|restore <DATE>}"; exit 2; }
    case "$1" in
        rdb)       do_rdb ;;
        aof)       do_aof ;;
        restore)
            [[ -n "${2:-}" ]] || fatal "restore 需指定备份日期"
            do_restore "$2"
            ;;
        -h|--help) echo "用法: $0 {rdb|aof|restore <DATE>}"; exit 0 ;;
        *)         fatal "未知命令: $1" ;;
    esac
}

main "$@"
