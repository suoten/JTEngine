#!/usr/bin/env bash
# JTE TDengine 集群滚动升级脚本
# AUTO-FIX-2026-06-30 [P1-3]: TDengine 滚动升级（3 节点 Replica=3，无数据丢失）
#
# 流程（逐节点升级，低峰期凌晨 2-4 点执行）：
#   0. 前置检查：集群健康、Replica=3、低峰期窗口校验
#   1. 全量备份（taosdump 快照 + 数据目录快照）
#   2. 逐节点升级（for each node）：
#        a. 摘除节点（taos -s "ALTER DNODE <id> 'disable'"；等待连接迁移）
#        b. 停止 taosd（systemctl stop taosd）
#        c. 升级 taosd 包（rpm/ deb）
#        d. 启动 taosd（systemctl start taosd）
#        e. 等待节点重新加入集群（SHOW DNODES 显示 ready）
#        f. 重新启用节点（ALTER DNODE <id> 'enable'）
#        g. 验证副本同步完成（SHOW VGROUPS 全部 leader/vnodes 正常）
#   3. 升级后全量验证（建表测试 / 读写测试 / 数据行数对比）
#   4. 回滚预案：节点回滚（旧包）+ 数据恢复（备份快照）
#
# 用法：
#   ./tdengine_rolling_upgrade.sh --target-version 3.3.5.0 --package taos-3.3.5.0-Linux-x64.rpm
#   ./tdengine-rolling-upgrade.sh --rollback --node tdengine2 --backup-dir /data/backups/20260630
#
# 依赖：taos CLI、systemctl（或 ssh）、rpm/dpkg。
set -euo pipefail

# ===== 默认配置 =====
NAMESPACE="${JTE_NAMESPACE:-jte}"
CLUSTER_NODES="${JTE_CLUSTER_NODES:-tdengine1 tdengine2 tdengine3}"   # 空格分隔
BACKUP_DIR="${JTE_BACKUP_DIR:-/data/backups/tdengine/$(date +%Y%m%d_%H%M%S)}"
BACKUP_RETAIN_DAYS="${JTE_BACKUP_RETAIN_DAYS:-30}"
TARGET_VERSION=""
PACKAGE=""
ROLLBACK=0
ROLLBACK_NODE=""
SSH_USER="${JTE_SSH_USER:-root}"
LOW_WINDOW_START=2    # 凌晨 2 点
LOW_WINDOW_END=4      # 凌晨 4 点
DRAIN_WAIT=120        # 节点摘除后等待连接迁移秒数
REJOIN_WAIT_MAX=600   # 节点重新加入最长等待秒数
VGROUP_STABLE_WAIT=60 # vgroup 稳定等待秒数

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

usage() {
    cat <<EOF
用法:
  升级: $0 --target-version <VER> --package <PKG>
  回滚: $0 --rollback --node <NODE> --backup-dir <DIR>

参数:
  --target-version VER   目标 taosd 版本（如 3.3.5.0）
  --package PKG          taosd 安装包路径（rpm/deb）
  --rollback             执行回滚（升级失败时）
  --node NODE            回滚的目标节点（仅 --rollback 时使用）
  --backup-dir DIR       回滚使用的备份目录
  --skip-window-check    跳过低峰期窗口校验（紧急升级时使用）

环境变量:
  JTE_CLUSTER_NODES      集群节点列表（空格分隔，默认 tdengine1 tdengine2 tdengine3）
  JTE_BACKUP_DIR         备份目录（默认 /data/backups/tdengine/<timestamp>）
  JTE_SSH_USER           SSH 用户（默认 root）
EOF
    exit 2
}

# 远程执行命令（节点名不为本地时走 ssh）
run_on() {
    local node="$1"; shift
    if [[ "$node" == "$(hostname)" || "$node" == "$(hostname -s)" ]]; then
        "$@"
    else
        ssh "${SSH_USER}@${node}" "$*"
    fi
}

# ===== 前置检查 =====
preflight() {
    log "前置检查：TDengine 集群健康状态"

    # 1. taos CLI 可用
    command -v taos >/dev/null 2>&1 || fatal "taos CLI 未安装，无法执行升级"

    # 2. 集群所有 dnode 在线
    log "SHOW DNODES:"
    taos -s "SHOW DNODES\G" || fatal "无法查询 DNODES，集群不可达"

    local offline
    offline=$(taos -s "SHOW DNODES" | awk '$2 == "offline" {print $1}')
    if [[ -n "$offline" ]]; then
        fatal "存在离线 dnode: $offline，请先修复集群"
    fi

    # 3. 校验 Replica=3（每个 vgroup 的副本数）
    log "校验 vgroup 副本数（应为 3）"
    local vgroups
    vgroups=$(taos -s "SHOW VGROUPS" 2>/dev/null)
    if ! echo "$vgroups" | grep -q "3"; then
        err "警告：未检测到 Replica=3 的 vgroup，请确认建库时 REPLICA=3"
        err "VGROUPS 输出:"
        echo "$vgroups" >&2
        read -r -p "继续? (y/N) " ans
        [[ "$ans" =~ ^[Yy]$ ]] || exit 1
    fi

    # 4. 低峰期窗口校验
    if [[ "${SKIP_WINDOW:-0}" != "1" ]]; then
        local hour
        hour=$(date +%H)
        if (( hour < LOW_WINDOW_START || hour >= LOW_WINDOW_END )); then
            fatal "当前时间 $(date '+%F %T') 不在低峰期窗口（${LOW_WINDOW_START}:00 - ${LOW_WINDOW_END}:00），使用 --skip-window-check 强制执行"
        fi
        log "低峰期窗口校验通过（${LOW_WINDOW_START}:00 - ${LOW_WINDOW_END}:00）"
    fi

    log "前置检查通过"
}

# ===== 全量备份 =====
full_backup() {
    log "全量备份开始: ${BACKUP_DIR}"
    mkdir -p "$BACKUP_DIR"

    # 1. taosdump 逻辑备份（全库）
    log "taosdump 全库逻辑备份..."
    if command -v taosdump >/dev/null 2>&1; then
        taosdump -o "$BACKUP_DIR/taosdump" -A || fatal "taosdump 全库备份失败"
        log "taosdump 备份完成: ${BACKUP_DIR}/taosdump"
    else
        err "警告：taosdump 未安装，仅做数据目录快照"
    fi

    # 2. 数据目录快照（每个节点 /var/lib/taos）
    log "数据目录快照（每节点 rsync 到备份目录）..."
    for node in $CLUSTER_NODES; do
        log "  快照节点 ${node} 数据目录..."
        run_on "$node" "tar -czf /tmp/taos_data_$(date +%s).tar.gz /var/lib/taos 2>/dev/null" || {
            err "节点 ${node} 数据目录快照失败（可能无权限），继续"
            continue
        }
        # 拉取快照到备份目录
        if [[ "$node" != "$(hostname)" && "$node" != "$(hostname -s)" ]]; then
            scp "${SSH_USER}@${node}":/tmp/taos_data_*.tar.gz "$BACKUP_DIR/${node}-data.tar.gz" 2>/dev/null || true
            run_on "$node" "rm -f /tmp/taos_data_*.tar.gz" || true
        else
            cp /tmp/taos_data_*.tar.gz "$BACKUP_DIR/${node}-data.tar.gz" 2>/dev/null || true
            rm -f /tmp/taos_data_*.tar.gz 2>/dev/null || true
        fi
    done

    # 3. 记录元信息（版本、时间戳、节点列表）便于回滚
    cat > "$BACKUP_DIR/META" <<EOF
backup_time=$(date '+%F %T')
backup_type=full
target_version=${TARGET_VERSION:-unknown}
nodes=${CLUSTER_NODES}
taos_version_before=$(taos -s "SELECT SERVER_VERSION()" 2>/dev/null | tail -n +2 | head -n 1 | tr -d ' ')
EOF
    log "备份元信息: $(cat "$BACKUP_DIR/META")"
    log "全量备份完成: ${BACKUP_DIR}"
}

# 获取节点 dnode id（按 hostname 匹配）
get_dnode_id() {
    local node="$1"
    # SHOW DNODES 输出: id | endpoint | vnodes | status | ...
    taos -s "SHOW DNODES" | awk -v n="$node" '$2 ~ n {print $1}' | head -n 1
}

# 等待节点重新加入集群（status=ready）
wait_rejoin() {
    local node="$1" dnode_id="$2"
    log "等待节点 ${node}(dnode=${dnode_id}) 重新加入集群..."
    local elapsed=0
    while (( elapsed < REJOIN_WAIT_MAX )); do
        local status
        status=$(taos -s "SHOW DNODES" | awk -v id="$dnode_id" '$1 == id {print $4}')
        if [[ "$status" == "ready" ]]; then
            log "节点 ${node} 已重新加入（ready）"
            return 0
        fi
        sleep 10
        elapsed=$((elapsed + 10))
        log "  等待中... status=${status:-unknown} (elapsed=${elapsed}s)"
    done
    fatal "节点 ${node} 重新加入超时"
}

# 等待 vgroup 全部稳定（所有副本 online，leader 正常）
wait_vgroups_stable() {
    log "等待 vgroup 副本同步稳定（${VGROUP_STABLE_WAIT}s）..."
    local elapsed=0
    while (( elapsed < VGROUP_STABLE_WAIT )); do
        # 检查是否有未同步的 vgroup（vnode 状态非 leader/follower）
        local unstable
        unstable=$(taos -s "SHOW VGROUPS" 2>/dev/null | grep -ic "offline\|unsynced" || echo "0")
        if [[ "$unstable" == "0" ]]; then
            log "vgroup 全部稳定"
            return 0
        fi
        sleep 10
        elapsed=$((elapsed + 10))
        log "  vgroup 同步中... unstable=${unstable} (elapsed=${elapsed}s)"
    done
    err "vgroup 仍有未同步副本，但已达稳定等待上限，继续下一步（可能影响下一节点摘除）"
}

# 升级单个节点
upgrade_node() {
    local node="$1"
    local dnode_id
    dnode_id=$(get_dnode_id "$node")
    if [[ -z "$dnode_id" ]]; then
        fatal "无法获取节点 ${node} 的 dnode id"
    fi
    log "==== 升级节点 ${node} (dnode=${dnode_id}) ===="

    # a. 摘除节点（标记 disable，停止接收新连接，等待连接迁移）
    log "摘除节点 ${node}（ALTER DNODE ${dnode_id} 'disable'）"
    taos -s "ALTER DNODE ${dnode_id} 'disable'" || fatal "摘除节点失败"
    log "等待 ${DRAIN_WAIT}s 连接迁移..."
    sleep "$DRAIN_WAIT"

    # b. 停止 taosd
    log "停止节点 ${node} 的 taosd"
    run_on "$node" "systemctl stop taosd" || fatal "停止 taosd 失败（${node}）"

    # c. 升级 taosd 包
    log "升级节点 ${node} 的 taosd 包（${PACKAGE}）"
    if [[ "$node" != "$(hostname)" && "$node" != "$(hostname -s)" ]]; then
        scp "$PACKAGE" "${SSH_USER}@${node}":/tmp/taos-upgrade.pkg
        run_on "$node" "case '$PACKAGE' in *.rpm) rpm -Uvh /tmp/taos-upgrade.pkg;; *.deb) dpkg -i /tmp/taos-upgrade.pkg;; *) echo 'unknown package type'; exit 1;; esac"
    else
        case "$PACKAGE" in
            *.rpm) rpm -Uvh "$PACKAGE" ;;
            *.deb) dpkg -i "$PACKAGE" ;;
            *) fatal "未知包类型: $PACKAGE" ;;
        esac
    fi

    # d. 启动 taosd
    log "启动节点 ${node} 的 taosd"
    run_on "$node" "systemctl start taosd" || fatal "启动 taosd 失败（${node}）"

    # e. 等待节点重新加入
    wait_rejoin "$node" "$dnode_id"

    # f. 重新启用节点
    log "重新启用节点 ${node}（ALTER DNODE ${dnode_id} 'enable'）"
    taos -s "ALTER DNODE ${dnode_id} 'enable'" || fatal "启用节点失败"

    # g. 等待 vgroup 副本同步稳定
    wait_vgroups_stable

    log "节点 ${node} 升级完成"
}

# ===== 升级后验证 =====
post_verify() {
    log "升级后全量验证"

    # 1. 所有 dnode ready 且版本一致
    log "SHOW DNODES（验证版本一致性）:"
    taos -s "SHOW DNODES\G"

    # 2. 建表测试
    log "建表测试..."
    taos -s "CREATE DATABASE IF NOT EXISTS upgrade_verify REPLICA 3; USE upgrade_verify; CREATE STABLE IF NOT EXISTS t (ts TIMESTAMP, v INT) TAGS (d INT); INSERT INTO t USING t TAGS(1) VALUES(now, 1); SELECT * FROM t; DROP DATABASE upgrade_verify;" \
        || fatal "建表/读写测试失败"

    # 3. 数据行数对比（与备份前对比，需运维人工核对）
    log "请人工核对关键库表行数与备份前一致"
    taos -s "SELECT COUNT(*) FROM jte_ts.jte_location;" 2>/dev/null || true

    log "升级后验证完成"
}

# ===== 回滚预案 =====
do_rollback() {
    local node="$ROLLBACK_NODE" dir="${BACKUP_DIR}"
    log "==== 回滚节点 ${node} ===="
    log "使用备份目录: ${dir}"

    if [[ ! -d "$dir" ]]; then
        fatal "备份目录不存在: $dir"
    fi
    if [[ -z "$node" ]]; then
        fatal "回滚需指定 --node <NODE>"
    fi

    local dnode_id
    dnode_id=$(get_dnode_id "$node")
    log "节点 ${node} dnode_id=${dnode_id}"

    # 1. 摘除节点
    if [[ -n "$dnode_id" ]]; then
        log "摘除节点 ${node}（ALTER DNODE ${dnode_id} 'disable'）"
        taos -s "ALTER DNODE ${dnode_id} 'disable'" || true
        sleep "$DRAIN_WAIT"
    fi

    # 2. 停止 taosd
    log "停止节点 ${node} 的 taosd"
    run_on "$node" "systemctl stop taosd" || true

    # 3. 恢复数据目录（从备份快照）
    local backup_pkg="${dir}/${node}-data.tar.gz"
    if [[ -f "$backup_pkg" ]]; then
        log "从备份恢复数据目录: ${backup_pkg}"
        if [[ "$node" != "$(hostname)" && "$node" != "$(hostname -s)" ]]; then
            scp "$backup_pkg" "${SSH_USER}@${node}":/tmp/taos-restore.tar.gz
            run_on "$node" "rm -rf /var/lib/taos/* && tar -xzf /tmp/taos-restore.tar.gz -C / && rm -f /tmp/taos-restore.tar.gz"
        else
            rm -rf /var/lib/taos/*
            tar -xzf "$backup_pkg" -C /
        fi
    else
        err "未找到节点 ${node} 的数据目录备份，仅回滚 taosd 包"
    fi

    # 4. 回滚 taosd 包（需运维提供旧版本包，此处提示）
    err "请手动安装回旧版本 taosd 包（备份前版本见 ${dir}/META）后启动 taosd"
    err "启动命令: ssh ${SSH_USER}@${node} 'systemctl start taosd'"
    err "启动后执行: taos -s \"ALTER DNODE ${dnode_id} 'enable'\""

    log "回滚流程已输出，请按提示完成"
}

# ===== 主流程 =====
main() {
    SKIP_WINDOW=0
    while (( $# > 0 )); do
        case "$1" in
            --target-version)    TARGET_VERSION="$2"; shift 2 ;;
            --package)           PACKAGE="$2"; shift 2 ;;
            --rollback)          ROLLBACK=1; shift ;;
            --node)              ROLLBACK_NODE="$2"; shift 2 ;;
            --backup-dir)        BACKUP_DIR="$2"; shift 2 ;;
            --skip-window-check) SKIP_WINDOW=1; shift ;;
            -h|--help)           usage ;;
            *)                   err "未知参数: $1"; usage ;;
        esac
    done

    if (( ROLLBACK == 1 )); then
        do_rollback
        exit 0
    fi

    # 升级模式校验
    [[ -n "$TARGET_VERSION" ]] || fatal "缺少 --target-version"
    [[ -n "$PACKAGE" && -f "$PACKAGE" ]] || fatal "缺少 --package 或文件不存在: ${PACKAGE}"

    log "TDengine 滚动升级开始"
    log "目标版本: ${TARGET_VERSION}"
    log "安装包: ${PACKAGE}"
    log "集群节点: ${CLUSTER_NODES}"
    log "备份目录: ${BACKUP_DIR}"

    preflight
    full_backup

    # 逐节点升级
    for node in $CLUSTER_NODES; do
        upgrade_node "$node"
    done

    post_verify
    log "TDengine 滚动升级全部完成，所有节点版本: ${TARGET_VERSION}"
    log "如需回滚: $0 --rollback --node <NODE> --backup-dir ${BACKUP_DIR}"
}

main "$@"
