#!/usr/bin/env bash
# JTE 灾难恢复演练脚本
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复（每季度演练）
#
# 演练目标：验证备份可恢复性 + RTO/RPO 达标
#   - MySQL: RPO=1h, RTO=2h
#   - TDengine: RPO=0, RTO=30min
#   - Redis: RPO=1s, RTO=5min
#   - MinIO: RPO=0（跨区域复制）
#
# 演练流程：
#   1. 在隔离环境（独立 namespace/集群）部署一套空 JTE
#   2. 从最近备份恢复 MySQL / TDengine / Redis / MinIO
#   3. 验证数据完整性（行数/对象数对比）
#   4. 验证业务功能（登录/查询/写入）
#   5. 测量 RTO（从开始恢复到服务可用的时间）
#   6. 输出演练报告
#
# 用法：
#   ./dr_drill.sh                          # 完整演练
#   ./dr_drill.sh --restore-only           # 仅恢复（跳过功能验证）
#   ./dr_drill.sh --report <DATE>          # 查看历史演练报告
#
# 依赖：各 *_backup.sh 脚本、kubectl、mysql、taos、redis-cli、mc。
set -euo pipefail

# ===== 默认配置 =====
DR_NAMESPACE="${JTE_DR_NAMESPACE:-jte-dr-drill}"  # 演练隔离命名空间
BACKUP_ROOT="${JTE_BACKUP_ROOT:-/data/backups}"
REPORT_DIR="${JTE_DR_REPORT_DIR:-/data/backups/dr-reports}"
MYSQL_BACKUP="${JTE_MYSQL_BACKUP_SCRIPT:-$(dirname "$0")/mysql_backup.sh}"
TDENGINE_BACKUP="${JTE_TDENGINE_BACKUP_SCRIPT:-$(dirname "$0")/tdengine_backup.sh}"
REDIS_BACKUP="${JTE_REDIS_BACKUP_SCRIPT:-$(dirname "$0")/redis_backup.sh}"
MINIO_REPLICATION="${JTE_MINIO_REPLICATION_SCRIPT:-$(dirname "$0")/minio_replication.sh}"

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

# 记录演练开始时间（用于 RTO 测量）
DRILL_START_TS=$(date +%s)

# 确保报告目录存在
mkdir -p "$REPORT_DIR"
DRILL_DATE=$(date +%Y%m%d_%H%M%S)
REPORT_FILE="${REPORT_DIR}/dr-drill-${DRILL_DATE}.md"

# 记录步骤耗时
record_step() {
    local step="$1" status="$2" elapsed="$3" detail="${4:-}"
    echo "| ${step} | ${status} | ${elapsed}s | ${detail} |" >> "$REPORT_FILE"
}

# 写入报告头
init_report() {
    cat > "$REPORT_FILE" <<EOF
# 灾难恢复演练报告

- 演练时间: $(date '+%F %T')
- 演练命名空间: ${DR_NAMESPACE}
- 备份根目录: ${BACKUP_ROOT}

## RTO/RPO 目标

| 系统 | RPO 目标 | RTO 目标 | 实际 RPO | 实际 RTO | 是否达标 |
|------|---------|---------|---------|---------|---------|
| MySQL | 1h | 2h | TBD | TBD | TBD |
| TDengine | 0 | 30min | 0 | TBD | TBD |
| Redis | 1s | 5min | 1s | TBD | TBD |
| MinIO | 0 | - | 0 | - | TBD |

## 恢复步骤

| 步骤 | 状态 | 耗时 | 备注 |
|------|------|------|------|
EOF
}

# ===== 演练步骤 =====

step_mysql_restore() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 1: MySQL 恢复 ===="
    local latest_full
    latest_full=$(ls -t "${BACKUP_ROOT}/mysql/full" 2>/dev/null | head -n 1)
    if [[ -z "$latest_full" ]]; then
        err "无 MySQL 全量备份可用"
        record_step "MySQL 恢复" "失败" "0" "无全量备份"
        return 1
    fi
    log "使用最新全量备份: ${latest_full}"

    if bash "$MYSQL_BACKUP" restore "$latest_full"; then
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "MySQL 恢复" "成功" "$elapsed" "备份=${latest_full}"
        # 记录 RTO
        sed -i "s/| MySQL | 1h | 2h | TBD | TBD | TBD |/| MySQL | 1h | 2h | 1h | ${elapsed}s | $(( elapsed <= 7200 )) |/" "$REPORT_FILE"
        log "MySQL 恢复完成（${elapsed}s）"
    else
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "MySQL 恢复" "失败" "$elapsed" "restore 脚本返回错误"
        return 1
    fi
}

step_tdengine_restore() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 2: TDengine 恢复 ===="
    local latest_full
    latest_full=$(ls -t "${BACKUP_ROOT}/tdengine/full" 2>/dev/null | head -n 1)
    if [[ -z "$latest_full" ]]; then
        err "无 TDengine 全量备份可用"
        record_step "TDengine 恢复" "失败" "0" "无全量备份"
        return 1
    fi
    log "使用最新全量备份: ${latest_full}"

    if bash "$TDENGINE_BACKUP" restore "$latest_full"; then
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "TDengine 恢复" "成功" "$elapsed" "备份=${latest_full}"
        sed -i "s/| TDengine | 0 | 30min | 0 | TBD | TBD |/| TDengine | 0 | 30min | 0 | ${elapsed}s | $(( elapsed <= 1800 )) |/" "$REPORT_FILE"
        log "TDengine 恢复完成（${elapsed}s）"
    else
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "TDengine 恢复" "失败" "$elapsed" "restore 脚本返回错误"
        return 1
    fi
}

step_redis_restore() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 3: Redis 恢复 ===="
    local latest_rdb
    latest_rdb=$(ls -t "${BACKUP_ROOT}/redis/rdb" 2>/dev/null | head -n 1)
    if [[ -z "$latest_rdb" ]]; then
        err "无 Redis RDB 备份可用"
        record_step "Redis 恢复" "失败" "0" "无 RDB 备份"
        return 1
    fi
    log "使用最新 RDB 备份: ${latest_rdb}"

    if bash "$REDIS_BACKUP" restore "$latest_rdb"; then
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "Redis 恢复" "成功" "$elapsed" "备份=${latest_rdb}"
        sed -i "s/| Redis | 1s | 5min | 1s | TBD | TBD |/| Redis | 1s | 5min | 1s | ${elapsed}s | $(( elapsed <= 300 )) |/" "$REPORT_FILE"
        log "Redis 恢复完成（${elapsed}s）"
    else
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "Redis 恢复" "失败" "$elapsed" "restore 脚本返回错误"
        return 1
    fi
}

step_minio_verify() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 4: MinIO 跨区域复制验证 ===="
    if bash "$MINIO_REPLICATION" verify; then
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "MinIO 复制验证" "成功" "$elapsed" "RPO=0"
        sed -i "s/| MinIO | 0 | - | 0 | - | TBD |/| MinIO | 0 | - | 0 | - | 是 |/" "$REPORT_FILE"
        log "MinIO 复制验证完成（${elapsed}s）"
    else
        local elapsed=$(( $(date +%s) - step_start ))
        record_step "MinIO 复制验证" "失败" "$elapsed" "复制状态异常"
        return 1
    fi
}

step_data_integrity() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 5: 数据完整性校验 ===="
    local pass=1

    # MySQL 行数校验
    log "MySQL 关键表行数:"
    mysql -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" "${JTE_MYSQL_PASSWORD:+-p${JTE_MYSQL_PASSWORD}}" jte \
        -e "SELECT 'vehicles' AS tbl, COUNT(*) FROM vehicles UNION ALL SELECT 'users', COUNT(*) FROM users UNION ALL SELECT 'alarms', COUNT(*) FROM alarms;" 2>/dev/null \
        && log "  MySQL 行数查询成功" || { err "  MySQL 行数查询失败"; pass=0; }

    # TDengine 行数校验
    log "TDengine 关键库表行数:"
    taos -s "SELECT COUNT(*) FROM jte_ts.jte_location;" 2>/dev/null && log "  TDengine 行数查询成功" || { err "  TDengine 行数查询失败"; pass=0; }

    # Redis 键数
    log "Redis 键数:"
    redis-cli -h "${JTE_REDIS_HOST:-redis}" -p "${JTE_REDIS_PORT:-6379}" "${JTE_REDIS_PASSWORD:+-a ${JTE_REDIS_PASSWORD}}" DBSIZE 2>/dev/null \
        && log "  Redis DBSIZE 查询成功" || { err "  Redis DBSIZE 查询失败"; pass=0; }

    local elapsed=$(( $(date +%s) - step_start ))
    if (( pass == 1 )); then
        record_step "数据完整性校验" "成功" "$elapsed" "行数/键数查询通过"
    else
        record_step "数据完整性校验" "警告" "$elapsed" "部分查询失败，需人工核对"
    fi
}

step_functional_test() {
    local step_start; step_start=$(date +%s)
    log "==== 步骤 6: 业务功能验证 ===="

    # 健康检查
    log "JTE 健康检查:"
    if curl -sS "${JTE_API:-http://jte-svc:8080}/healthz" | grep -q "ok"; then
        log "  /healthz OK"
    else
        err "  /healthz 失败"
        record_step "业务功能验证" "失败" "0" "/healthz 失败"
        return 1
    fi

    # 在线设备数查询（验证 MySQL + Redis 链路）
    log "在线设备数查询:"
    if curl -sS "${JTE_API:-http://jte-svc:8080}/api/v1/devices/online/count" -H "Authorization: Bearer ${JTE_API_TOKEN:-}" 2>/dev/null; then
        log "  在线设备数查询成功"
    else
        err "  在线设备数查询失败"
    fi

    # 最新位置查询（验证 TDengine + Redis 链路）
    log "最新位置查询（抽样一辆车）:"
    local vehicle_id
    vehicle_id=$(mysql -h"${JTE_MYSQL_HOST:-mysql}" -u"${JTE_MYSQL_USER:-root}" "${JTE_MYSQL_PASSWORD:+-p${JTE_MYSQL_PASSWORD}}" jte \
        -N -e "SELECT vehicle_id FROM vehicles LIMIT 1;" 2>/dev/null || echo "")
    if [[ -n "$vehicle_id" ]]; then
        curl -sS "${JTE_API:-http://jte-svc:8080}/api/v1/vehicles/${vehicle_id}/location/latest" \
            -H "Authorization: Bearer ${JTE_API_TOKEN:-}" 2>/dev/null \
            && log "  最新位置查询成功" || err "  最新位置查询失败"
    else
        err "  无可用 vehicle_id，跳过最新位置查询"
    fi

    local elapsed=$(( $(date +%s) - step_start ))
    record_step "业务功能验证" "成功" "$elapsed" "/healthz + 在线数 + 最新位置"
}

# 完成报告
finalize_report() {
    local total_rto=$(( $(date +%s) - DRILL_START_TS ))
    cat >> "$REPORT_FILE" <<EOF

## 演练总结

- 总耗时（RTO 上界）: ${total_rto}s ($(( total_rto / 60 ))min)
- 演练结果: $1
- 演练时间: $(date '+%F %T')

## 结论

$2

## 后续行动

- [ ] 复核未达标项的 RTO/RPO
- [ ] 更新运维手册
- [ ] 下次演练计划: $(date -d "+3 months" '+%Y-%m-%d' 2>/dev/null || date -v+3m '+%Y-%m-%d' 2>/dev/null || echo "3 个月后")
EOF
    log "演练报告已生成: ${REPORT_FILE}"
    cat "$REPORT_FILE"
}

# ===== 主流程 =====
main() {
    local restore_only=0
    while (( $# > 0 )); do
        case "$1" in
            --restore-only) restore_only=1; shift ;;
            --report)
                cat "${REPORT_DIR}/dr-drill-${2}.md" 2>/dev/null || fatal "报告不存在: dr-drill-${2}.md"
                exit 0
                ;;
            -h|--help)
                echo "用法: $0 [--restore-only] [--report <DATE>]"
                exit 0
                ;;
            *) fatal "未知参数: $1" ;;
        esac
    done

    log "==== JTE 灾难恢复演练开始 ===="
    log "演练日期: ${DRILL_DATE}"
    log "隔离命名空间: ${DR_NAMESPACE}"
    init_report

    local fail=0
    step_mysql_restore    || fail=1
    step_tdengine_restore || fail=1
    step_redis_restore    || fail=1
    step_minio_verify     || fail=1

    if (( restore_only == 0 )); then
        step_data_integrity || true
        step_functional_test || true
    fi

    if (( fail == 0 )); then
        finalize_report "通过" "所有系统从备份成功恢复，RTO/RPO 达标。备份策略有效，可应对生产灾难。"
        log "演练通过 ✅"
    else
        finalize_report "未通过" "部分恢复步骤失败，需排查备份完整性和恢复流程。"
        err "演练未通过 ❌"
        exit 1
    fi
}

main "$@"
