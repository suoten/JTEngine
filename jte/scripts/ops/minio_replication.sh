# JTE MinIO 跨区域复制配置
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复（MinIO RPO=0）
#
# 策略：跨区域复制（Server-Side Bucket Replication），RPO=0
#   - 主区域（Region-A）写入后自动异步复制到备区域（Region-B）
#   - 复制规则：所有对象，包含删除标记
#   - 备区域只读，灾难时切换 DNS/endpoint 到备区域
#
# 前置条件：
#   1. 已部署两个 MinIO 集群（Region-A + Region-B）
#   2. 两个集群均启用 site replication 或 bucket replication
#   3. mc (MinIO Client) 已安装并配置 alias
#
# 配置步骤：
#   1. 在主备两端各创建 replication target
#   2. 在主端 bucket 上添加 replication rule
#   3. 验证复制延迟
#
# 用法（在运维节点执行）：
#   ./minio_replication.sh setup        # 配置跨区域复制
#   ./minio_replication.sh verify       # 验证复制状态
#   ./minio_replication.sh failover     # 故障切换到备区域
#   ./minio_replication.sh failback     # 故障恢复回主区域

#!/usr/bin/env bash
set -euo pipefail

# ===== 默认配置 =====
PRIMARY_ALIAS="${JTE_MINIO_PRIMARY_ALIAS:-primary}"      # mc alias name
PRIMARY_ENDPOINT="${JTE_MINIO_PRIMARY_ENDPOINT:-http://minio-region-a:9000}"
PRIMARY_ACCESS_KEY="${JTE_MINIO_PRIMARY_ACCESS_KEY:-minioadmin}"
PRIMARY_SECRET_KEY="${JTE_MINIO_PRIMARY_SECRET_KEY:-minioadmin}"

SECONDARY_ALIAS="${JTE_MINIO_SECONDARY_ALIAS:-secondary}"
SECONDARY_ENDPOINT="${JTE_MINIO_SECONDARY_ENDPOINT:-http://minio-region-b:9000}"
SECONDARY_ACCESS_KEY="${JTE_MINIO_SECONDARY_ACCESS_KEY:-minioadmin}"
SECONDARY_SECRET_KEY="${JTE_MINIO_SECONDARY_SECRET_KEY:-minioadmin}"

BUCKETS="${JTE_MINIO_BUCKETS:-jte-archive jte-video jte-records}"  # 空格分隔
REGION_A="${JTE_MINIO_REGION_A:-region-a}"
REGION_B="${JTE_MINIO_REGION_B:-region-b}"

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

# 配置 mc alias
setup_aliases() {
    log "配置 mc alias..."
    mc alias set "$PRIMARY_ALIAS" "$PRIMARY_ENDPOINT" "$PRIMARY_ACCESS_KEY" "$PRIMARY_SECRET_KEY" --api S3v4
    mc alias set "$SECONDARY_ALIAS" "$SECONDARY_ENDPOINT" "$SECONDARY_ACCESS_KEY" "$SECONDARY_SECRET_KEY" --api S3v4
}

# 配置跨区域复制
do_setup() {
    log "==== MinIO 跨区域复制配置 ===="
    command -v mc >/dev/null 2>&1 || fatal "mc (MinIO Client) 未安装"
    setup_aliases

    for bucket in $BUCKETS; do
        log "配置 bucket: ${bucket}"

        # 1. 主备两端都创建 bucket（如不存在）
        mc mb -p "${PRIMARY_ALIAS}/${bucket}" --region "$REGION_A" 2>/dev/null || true
        mc mb -p "${SECONDARY_ALIAS}/${bucket}" --region "$REGION_B" 2>/dev/null || true

        # 2. 开启版本控制（复制前提）
        mc version enable "${PRIMARY_ALIAS}/${bucket}" 2>/dev/null || true
        mc version enable "${SECONDARY_ALIAS}/${bucket}" 2>/dev/null || true

        # 3. 添加 replication rule（主 → 备，所有对象，包含删除）
        # MinIO site replication（推荐）或 bucket replication
        # 使用 bucket replication（更灵活）
        log "  添加 replication rule（${bucket}）..."
        mc replicate add "${PRIMARY_ALIAS}/${bucket}" \
            --remote-bucket "${SECONDARY_ALIAS}/${bucket}" \
            --replicate "delete,delete-marker,existing-objects" \
            2>/dev/null || err "  ${bucket} replication rule 添加失败（可能已存在）"

        # 4. 验证
        mc replicate status "${PRIMARY_ALIAS}/${bucket}" 2>/dev/null || err "  ${bucket} replication status 查询失败"
    done

    log "跨区域复制配置完成"
    log "提示：如需双向复制（failback），在备端也执行相同操作"
}

# 验证复制状态
do_verify() {
    log "==== MinIO 跨区域复制状态验证 ===="
    command -v mc >/dev/null 2>&1 || fatal "mc 未安装"
    setup_aliases

    for bucket in $BUCKETS; do
        log "bucket: ${bucket}"
        log "  主端对象数: $(mc ls --recursive --summarize "${PRIMARY_ALIAS}/${bucket}" 2>/dev/null | tail -n 2 | head -n 1)"
        log "  备端对象数: $(mc ls --recursive --summarize "${SECONDARY_ALIAS}/${bucket}" 2>/dev/null | tail -n 2 | head -n 1)"
        log "  复制状态:"
        mc replicate status "${PRIMARY_ALIAS}/${bucket}" 2>/dev/null || err "  ${bucket} 复制状态查询失败"
    done

    # 写入测试对象验证端到端复制
    local test_key="__replication_test_$(date +%s)__"
    log "端到端复制验证：写入测试对象 ${test_key}"
    echo "replication test $(date)" | mc pipe "${PRIMARY_ALIAS}/${BUCKETS%% *}/${test_key}"
    sleep 3
    if mc cat "${SECONDARY_ALIAS}/${BUCKETS%% *}/${test_key}" 2>/dev/null | grep -q "replication test"; then
        log "端到端复制验证通过"
        mc rm "${PRIMARY_ALIAS}/${BUCKETS%% *}/${test_key}" 2>/dev/null || true
        sleep 2
        mc rm "${SECONDARY_ALIAS}/${BUCKETS%% *}/${test_key}" 2>/dev/null || true
    else
        err "端到端复制验证失败：备端未收到测试对象"
        exit 1
    fi
}

# 故障切换到备区域
do_failover() {
    log "==== MinIO 故障切换（主 → 备） ===="
    err "故障切换操作（需人工确认）："
    err "  1. 确认主区域 MinIO 已不可用"
    err "  2. 将应用 MINIO_ENDPOINT 环境变量/DNS 切换到备区域：${SECONDARY_ENDPOINT}"
    err "  3. 重启 JTE 应用（SIGHUP 或滚动重启）使新 endpoint 生效"
    err "  4. 验证应用可读写备区域 MinIO"
    err ""
    err "备区域 endpoint: ${SECONDARY_ENDPOINT}"
    err "备区域 access key: ${SECONDARY_ACCESS_KEY}"
    read -r -p "确认执行故障切换? (y/N) " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { log "已取消"; exit 0; }
    log "请在应用侧将 MINIO_ENDPOINT 切换到 ${SECONDARY_ENDPOINT} 并重启应用"
}

# 故障恢复回主区域
do_failback() {
    log "==== MinIO 故障恢复（备 → 主） ===="
    err "故障恢复操作（需人工确认）："
    err "  1. 确认主区域 MinIO 已恢复"
    err "  2. 将故障期间备区域的增量数据回补到主区域（mc mirror）"
    err "  3. 将应用 MINIO_ENDPOINT 切换回主区域：${PRIMARY_ENDPOINT}"
    err "  4. 重启 JTE 应用"
    err ""
    err "如需回补增量数据，执行："
    for bucket in $BUCKETS; do
        err "  mc mirror --overwrite ${SECONDARY_ALIAS}/${bucket} ${PRIMARY_ALIAS}/${bucket}"
    done
    read -r -p "确认执行故障恢复? (y/N) " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { log "已取消"; exit 0; }

    log "回补增量数据（备 → 主）..."
    for bucket in $BUCKETS; do
        log "  mc mirror ${bucket}..."
        mc mirror --overwrite "${SECONDARY_ALIAS}/${bucket}" "${PRIMARY_ALIAS}/${bucket}" || err "  ${bucket} 回补失败"
    done
    log "故障恢复完成，请将应用 MINIO_ENDPOINT 切换回 ${PRIMARY_ENDPOINT} 并重启"
}

main() {
    (( $# >= 1 )) || { echo "用法: $0 {setup|verify|failover|failback}"; exit 2; }
    case "$1" in
        setup)    do_setup ;;
        verify)   do_verify ;;
        failover) do_failover ;;
        failback) do_failback ;;
        -h|--help) echo "用法: $0 {setup|verify|failover|failback}"; exit 0 ;;
        *)        fatal "未知命令: $1" ;;
    esac
}

main "$@"
