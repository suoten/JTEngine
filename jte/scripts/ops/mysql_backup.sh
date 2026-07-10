#!/usr/bin/env bash
# JTE MySQL 备份脚本
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复
#
# 策略：每日全量 + 每小时增量 binlog
#   - 全量：mysqldump --single-transaction --master-data=2（一致性快照 + binlog 位点）
#   - 增量：FLUSH LOGS 后复制新 binlog 文件
#   - RPO=1h（增量频率），RTO=2h（全量恢复 + binlog 重放）
#   - 保留：全量 30 天，增量 7 天
#
# 用法：
#   ./mysql_backup.sh full                  # 全量备份
#   ./mysql_backup.sh incremental           # 增量备份（binlog）
#   ./mysql_backup.sh restore <DATE>        # 恢复到指定日期（全量 + binlog 重放到指定位点）
#   ./mysql_backup.sh restore <DATE> --to-gtid "<GTID>"  # 恢复到指定 GTID（PITR）
#
# 依赖：mysqldump、mysqlbinlog、mysql。
set -euo pipefail

# ===== 默认配置 =====
MYSQL_HOST="${JTE_MYSQL_HOST:-mysql}"
MYSQL_PORT="${JTE_MYSQL_PORT:-3306}"
MYSQL_USER="${JTE_MYSQL_USER:-root}"
MYSQL_PASSWORD="${JTE_MYSQL_PASSWORD:-}"
MYSQL_DATABASES="${JTE_MYSQL_DATABASES:-jte}"   # 空格分隔，默认 jte
BACKUP_ROOT="${JTE_MYSQL_BACKUP_DIR:-/data/backups/mysql}"
BINLOG_DIR="${MYSQL_BINLOG_DIR:-/var/lib/mysql}" # MySQL 服务器上的 binlog 目录
FULL_RETAIN_DAYS="${JTE_MYSQL_FULL_RETAIN:-30}"
INCR_RETAIN_DAYS="${JTE_MYSQL_INCR_RETAIN:-7}"
COMPRESS="${JTE_MYSQL_COMPRESS:-gzip}"

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

mysql_args() {
    echo "-h${MYSQL_HOST} -P${MYSQL_PORT} -u${MYSQL_USER}"
    [[ -n "$MYSQL_PASSWORD" ]] && echo "-p${MYSQL_PASSWORD}"
}

# 全量备份
do_full() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local dir="${BACKUP_ROOT}/full/${date_str}"
    mkdir -p "$dir"

    log "MySQL 全量备份开始: ${dir}"
    log "数据库: ${MYSQL_DATABASES}"

    # mysqldump --single-transaction: InnoDB 一致性快照（不锁表）
    # --master-data=2: 记录 binlog 位点（注释形式），用于 PITR
    # --routines --triggers --events: 包含存储过程/触发器/事件
    local dump_args
    dump_args=$(mysql_args)
    for db in $MYSQL_DATABASES; do
        log "  备份库: ${db}"
        if [[ "$COMPRESS" == "gzip" ]]; then
            mysqldump $dump_args \
                --single-transaction \
                --master-data=2 \
                --routines --triggers --events \
                --set-gtid-purged=AUTO \
                "$db" | gzip > "${dir}/${db}.sql.gz"
        else
            mysqldump $dump_args \
                --single-transaction \
                --master-data=2 \
                --routines --triggers --events \
                --set-gtid-purged=AUTO \
                "$db" > "${dir}/${db}.sql"
        fi
    done

    # 记录 binlog 位点（从 dump 文件头提取）
    local dump_file="${dir}/$(echo "$MYSQL_DATABASES" | awk '{print $1}').sql"
    [[ -f "${dump_file}.gz" ]] && dump_file="${dump_file}.gz"
    log "binlog 位点（CHANGE MASTER TO）:"
    if [[ "$dump_file" == *.gz ]]; then
        zcat "$dump_file" | head -n 50 | grep "CHANGE MASTER" || true
    else
        head -n 50 "$dump_file" | grep "CHANGE MASTER" || true
    fi

    # 触发 binlog 切换（便于后续增量备份从新 binlog 开始）
    log "FLUSH LOGS（切换 binlog）..."
    mysql $(mysql_args) -e "FLUSH LOGS;" 2>/dev/null || err "FLUSH LOGS 失败（可能无权限，增量备份改用 SHOW MASTER STATUS 位点）"

    # 记录元信息
    cat > "${dir}/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=full
databases=${MYSQL_DATABASES}
mysql_host=${MYSQL_HOST}
EOF

    # 清理过期全量备份
    log "清理超过 ${FULL_RETAIN_DAYS} 天的全量备份..."
    find "${BACKUP_ROOT}/full" -maxdepth 1 -type d -mtime +"$FULL_RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

    log "全量备份完成: ${dir}"
    du -sh "$dir"
}

# 增量备份（binlog）
do_incremental() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local dir="${BACKUP_ROOT}/incr/${date_str}"
    mkdir -p "$dir"

    log "MySQL 增量备份开始: ${dir}"

    # 获取当前 binlog 位点
    local master_status
    master_status=$(mysql $(mysql_args) -e "SHOW MASTER STATUS\G" 2>/dev/null)
    if [[ -z "$master_status" ]]; then
        fatal "无法获取 MASTER STATUS（需 REPLICATION CLIENT 权限）"
    fi
    log "当前 binlog 位点:"
    echo "$master_status"

    local current_binlog
    current_binlog=$(echo "$master_status" | awk '/File:/ {print $2}')
    local current_pos
    current_pos=$(echo "$master_status" | awk '/Position:/ {print $2}')
    echo "$master_status" > "${dir}/master_status.txt"

    # 复制上次增量以来的 binlog 文件
    # 简化方案：FLUSH LOGS 后复制所有非当前活跃的 binlog
    log "FLUSH LOGS（切换 binlog，便于复制已完成的 binlog）..."
    mysql $(mysql_args) -e "FLUSH LOGS;" 2>/dev/null || err "FLUSH LOGS 失败"

    # 列出所有 binlog 文件
    local binlogs
    binlogs=$(mysql $(mysql_args) -e "SHOW BINARY LOGS;" 2>/dev/null | awk 'NR>1 {print $1}')
    if [[ -z "$binlogs" ]]; then
        err "无法获取 binlog 列表（需 REPLICATION CLIENT 权限），增量备份跳过"
        exit 1
    fi

    # 复制除最后一个（当前活跃）外的所有 binlog 到备份目录
    # 注：生产环境建议用 mysqlbinlog --read-from-remote-server 拉取，避免直接读文件
    local last_binlog
    last_binlog=$(echo "$binlogs" | tail -n 1)
    local copied=0
    for bl in $binlogs; do
        if [[ "$bl" == "$last_binlog" ]]; then
            continue # 跳过当前活跃 binlog
        fi
        # 检查是否已在之前的增量备份中（避免重复复制）
        if find "${BACKUP_ROOT}/incr" -name "$bl" -type f 2>/dev/null | grep -q .; then
            continue
        fi
        log "  拉取 binlog: ${bl}"
        mysqlbinlog $(mysql_args) --read-from-remote-server --raw "$bl" --result-file="${dir}/" 2>/dev/null \
            || err "  拉取 ${bl} 失败"
        copied=$((copied + 1))
    done

    log "本次增量复制 ${copied} 个 binlog 文件"

    # 记录元信息
    cat > "${dir}/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=incremental
binlog_file=${current_binlog}
binlog_pos=${current_pos}
EOF

    # 清理过期增量备份
    log "清理超过 ${INCR_RETAIN_DAYS} 天的增量备份..."
    find "${BACKUP_ROOT}/incr" -maxdepth 1 -type d -mtime +"$INCR_RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

    log "增量备份完成: ${dir}"
    du -sh "$dir"
}

# 恢复
do_restore() {
    local target_date="$1"
    local gtid="${3:-}"
    local full_dir="${BACKUP_ROOT}/full/${target_date}"

    if [[ ! -d "$full_dir" ]]; then
        fatal "全量备份目录不存在: ${full_dir}（可用日期: $(ls "${BACKUP_ROOT}/full" 2>/dev/null | tr '\n' ' ')）"
    fi

    log "==== MySQL 恢复开始 ===="
    log "目标全量备份: ${full_dir}"
    log "目标 GTID/位点: ${gtid:-末尾（不限制）}"

    # 1. 恢复全量
    log "步骤 1: 恢复全量备份..."
    for dump_file in "$full_dir"/*.sql*; do
        [[ -f "$dump_file" ]] || continue
        local db; db=$(basename "$dump_file" | sed 's/\.sql.*//')
        log "  恢复库: ${db}"
        if [[ "$dump_file" == *.gz ]]; then
            zcat "$dump_file" | mysql $(mysql_args) "$db"
        else
            mysql $(mysql_args) "$db" < "$dump_file"
        fi
    done

    # 2. 重放 binlog（到指定 GTID/位点）
    log "步骤 2: 重放 binlog..."
    # 收集所有增量备份中的 binlog 文件（按时间排序）
    local binlog_files=()
    while IFS= read -r f; do
        binlog_files+=("$f")
    done < <(find "${BACKUP_ROOT}/incr" -type f ! -name "META" ! -name "master_status.txt" 2>/dev/null | sort)

    if (( ${#binlog_files[@]} == 0 )); then
        log "无增量 binlog，恢复完成"
        return 0
    fi

    log "待重放 binlog 文件数: ${#binlog_files[@]}"
    local stop_arg=""
    if [[ -n "$gtid" ]]; then
        stop_arg="--stop-never --stop-never-slave-server-id=99999 --exclude-gtids=  --until-gtid=${gtid}"
    fi

    for bl in "${binlog_files[@]}"; do
        log "  重放: ${bl}"
        if [[ "$bl" == *.gz ]]; then
            zcat "$bl" | mysqlbinlog --stop-never-slave-server-id=99999 - 2>/dev/null | mysql $(mysql_args)
        else
            mysqlbinlog $stop_arg "$bl" 2>/dev/null | mysql $(mysql_args)
        fi
    done

    log "MySQL 恢复完成"
}

main() {
    (( $# >= 1 )) || { echo "用法: $0 {full|incremental|restore <DATE> [--to-gtid <GTID>]}"; exit 2; }
    case "$1" in
        full)         do_full ;;
        incremental)  do_incremental ;;
        restore)
            [[ -n "${2:-}" ]] || fatal "restore 需指定备份日期，如 20260630_120000"
            do_restore "$2" "${3:-}" "${4:-}"
            ;;
        -h|--help)    echo "用法: $0 {full|incremental|restore <DATE> [--to-gtid <GTID>]}"; exit 0 ;;
        *)            fatal "未知命令: $1" ;;
    esac
}

main "$@"
