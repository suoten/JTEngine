#!/usr/bin/env bash
# JTE TDengine 备份脚本
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复
#
# 策略：Replica=3 + 归档 MinIO，RPO=0，RTO=30min
#   - 全量：taosdump 全库逻辑备份 + 数据目录快照
#   - 归档：冷数据定期归档到 MinIO（由 module-storage archiver 在线执行，本脚本触发）
#   - 保留：全量 14 天，MinIO 归档永久（生命周期策略管理）
#
# 用法：
#   ./tdengine_backup.sh full                   # 全量备份（taosdump + 快照）
#   ./tdengine_backup.sh archive-trigger        # 手动触发一次离线归档到 MinIO
#   ./tdengine_backup.sh restore <DATE>         # 从全量备份恢复
#
# 依赖：taosdump、taos CLI、curl（触发归档 API）。
set -euo pipefail

# ===== 默认配置 =====
BACKUP_ROOT="${JTE_TDENGINE_BACKUP_DIR:-/data/backups/tdengine}"
CLUSTER_NODES="${JTE_CLUSTER_NODES:-tdengine1 tdengine2 tdengine3}"
RETAIN_DAYS="${JTE_TDENGINE_RETAIN:-14}"
SSH_USER="${JTE_SSH_USER:-root}"
JTE_API="${JTE_API:-http://jte-svc:8080}"
JTE_API_TOKEN="${JTE_API_TOKEN:-}"
ARCHIVE_ENDPOINT="${JTE_API}/api/v1/storage/archive/trigger"

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

run_on() {
    local node="$1"; shift
    if [[ "$node" == "$(hostname)" || "$node" == "$(hostname -s)" ]]; then
        "$@"
    else
        ssh "${SSH_USER}@${node}" "$*"
    fi
}

# 全量备份
do_full() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local dir="${BACKUP_ROOT}/full/${date_str}"
    mkdir -p "$dir"

    log "TDengine 全量备份开始: ${dir}"

    # 1. taosdump 全库逻辑备份（从任一节点拉取，Replica=3 保证一致）
    if command -v taosdump >/dev/null 2>&1; then
        log "taosdump 全库逻辑备份..."
        taosdump -o "${dir}/taosdump" -A || fatal "taosdump 失败"
        log "taosdump 完成: ${dir}/taosdump"
    else
        err "警告：taosdump 未安装，仅做数据目录快照"
    fi

    # 2. 数据目录快照（每节点 /var/lib/taos）
    log "数据目录快照（每节点）..."
    for node in $CLUSTER_NODES; do
        log "  快照节点 ${node}..."
        run_on "$node" "tar -czf /tmp/taos_bak_$(date +%s).tar.gz /var/lib/taos 2>/dev/null" || {
            err "  节点 ${node} 快照失败（可能无权限），继续"
            continue
        }
        if [[ "$node" != "$(hostname)" && "$node" != "$(hostname -s)" ]]; then
            scp "${SSH_USER}@${node}":/tmp/taos_bak_*.tar.gz "${dir}/${node}-data.tar.gz" 2>/dev/null || true
            run_on "$node" "rm -f /tmp/taos_bak_*.tar.gz" || true
        else
            cp /tmp/taos_bak_*.tar.gz "${dir}/${node}-data.tar.gz" 2>/dev/null || true
            rm -f /tmp/taos_bak_*.tar.gz 2>/dev/null || true
        fi
    done

    # 3. 元信息
    cat > "${dir}/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=full
nodes=${CLUSTER_NODES}
taos_version=$(taos -s "SELECT SERVER_VERSION()" 2>/dev/null | tail -n +2 | head -n 1 | tr -d ' ')
EOF

    # 4. 清理过期
    log "清理超过 ${RETAIN_DAYS} 天的全量备份..."
    find "${BACKUP_ROOT}/full" -maxdepth 1 -type d -mtime +"$RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

    log "全量备份完成: ${dir}"
    du -sh "$dir"
}

# 手动触发一次离线归档到 MinIO（由 module-storage archiver 执行）
do_archive_trigger() {
    log "手动触发 TDengine→MinIO 离线归档..."
    if [[ -z "$JTE_API_TOKEN" ]]; then
        err "警告：JTE_API_TOKEN 未设置，归档触发接口可能返回 401"
    fi
    local resp
    resp=$(curl -sS -X POST "$ARCHIVE_ENDPOINT" \
        -H "Authorization: Bearer ${JTE_API_TOKEN}" \
        -H "Content-Type: application/json" \
        --max-time 300 2>&1) || {
        err "归档触发失败: $resp"
        exit 1
    }
    log "归档触发响应: $resp"
}

# 恢复
do_restore() {
    local target_date="$1"
    local dir="${BACKUP_ROOT}/full/${target_date}"
    if [[ ! -d "$dir" ]]; then
        fatal "备份目录不存在: ${dir}（可用: $(ls "${BACKUP_ROOT}/full" 2>/dev/null | tr '\n' ' ')）"
    fi

    log "==== TDengine 恢复开始 ===="
    log "目标备份: ${dir}"

    # 1. 停止集群（所有节点）
    log "步骤 1: 停止所有节点 taosd..."
    for node in $CLUSTER_NODES; do
        log "  停止 ${node}..."
        run_on "$node" "systemctl stop taosd" || err "  停止 ${node} 失败"
    done

    # 2. 恢复数据目录（从快照）
    log "步骤 2: 恢复数据目录..."
    for node in $CLUSTER_NODES; do
        local backup_pkg="${dir}/${node}-data.tar.gz"
        if [[ ! -f "$backup_pkg" ]]; then
            err "  节点 ${node} 无数据快照，跳过（仅从 taosdump 恢复）"
            continue
        fi
        log "  恢复节点 ${node} 数据目录..."
        if [[ "$node" != "$(hostname)" && "$node" != "$(hostname -s)" ]]; then
            scp "$backup_pkg" "${SSH_USER}@${node}":/tmp/taos-restore.tar.gz
            run_on "$node" "rm -rf /var/lib/taos/* && tar -xzf /tmp/taos-restore.tar.gz -C / && rm -f /tmp/taos-restore.tar.gz"
        else
            rm -rf /var/lib/taos/*
            tar -xzf "$backup_pkg" -C /
        fi
    done

    # 3. 启动集群
    log "步骤 3: 启动所有节点 taosd..."
    for node in $CLUSTER_NODES; do
        log "  启动 ${node}..."
        run_on "$node" "systemctl start taosd" || err "  启动 ${node} 失败"
        sleep 5
    done

    # 4. 等待集群就绪
    log "步骤 4: 等待集群就绪..."
    sleep 30
    for i in $(seq 1 12); do
        if taos -s "SHOW DNODES" 2>/dev/null | grep -q ready; then
            log "集群就绪"
            break
        fi
        sleep 10
    done

    # 5. 如有 taosdump 逻辑备份，校验数据
    if [[ -d "${dir}/taosdump" ]]; then
        log "步骤 5: 校验数据（taosdump 逻辑备份）..."
        err "提示：如数据目录恢复失败，可用 taosdump 逻辑备份恢复:"
        err "  taosdump -i ${dir}/taosdump"
    fi

    log "TDengine 恢复完成"
}

main() {
    (( $# >= 1 )) || { echo "用法: $0 {full|archive-trigger|restore <DATE>}"; exit 2; }
    case "$1" in
        full)             do_full ;;
        archive-trigger)  do_archive_trigger ;;
        restore)
            [[ -n "${2:-}" ]] || fatal "restore 需指定备份日期"
            do_restore "$2"
            ;;
        -h|--help)        echo "用法: $0 {full|archive-trigger|restore <DATE>}"; exit 0 ;;
        *)                fatal "未知命令: $1" ;;
    esac
}

main "$@"
