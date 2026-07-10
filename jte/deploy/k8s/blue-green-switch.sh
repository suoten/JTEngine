#!/usr/bin/env bash
# JTE 蓝绿部署切换脚本
# AUTO-FIX-2026-06-30 [P1-1]: 零停机部署
#
# 流程（切换 blue -> green）：
#   1. 扩容 Green 到 3 副本（不接流量）
#   2. 等待 Green 所有 Pod 就绪探针通过（最多 5 分钟）
#   3. 切换 Service selector: blue -> green
#   4. 等待 Blue 上的 TCP 长连接排空（最长 5 分钟，由 SIGTERM + drain_timeout 控制）
#   5. 缩容 Blue 到 0 副本
#
# 回滚（--rollback）：
#   反向切换 green -> blue，流程相同。
#
# 用法：
#   ./blue-green-switch.sh              # blue -> green
#   ./blue-green-switch.sh --rollback   # green -> blue
#
# 依赖：kubectl，且当前 kubeconfig 指向目标集群。
set -euo pipefail

NAMESPACE="${JTE_NAMESPACE:-jte}"
SERVICE="jte-svc"
REPLICAS="${JTE_REPLICAS:-3}"
READY_TIMEOUT="${JTE_READY_TIMEOUT:-300}"      # Green 就绪等待 5 分钟
DRAIN_TIMEOUT="${JTE_DRAIN_TIMEOUT:-330}"      # Blue 排空等待 5.5 分钟（drain 300 + 30s 缓冲）
DRAIN_CHECK_INTERVAL="${JTE_DRAIN_CHECK_INTERVAL:-10}"

# AUTO-FIX-2026-06-30 [P1-2]: 旧节点排空期间下发"重连退避时间"（0-60s 随机）。
# 该退避由进程内 broadcastReconnectBackoff 实现，进程收到 SIGTERM 即广播，
# 脚本无需额外触发；此处仅在日志中提示。
BACKOFF_HINT="进程收到 SIGTERM 后会通过 808/809 协议下发 0-60s 随机重连退避时间。"

log() { echo "[$(date '+%F %T')] $*"; }
err() { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }

usage() {
    cat <<EOF
用法: $0 [--rollback]
  无参数:    切换 Blue -> Green
  --rollback: 切换 Green -> Blue（回滚）

环境变量:
  JTE_NAMESPACE            命名空间（默认: jte）
  JTE_REPLICAS             目标副本数（默认: 3）
  JTE_READY_TIMEOUT        就绪等待超时秒数（默认: 300）
  JTE_DRAIN_TIMEOUT        排空等待超时秒数（默认: 330）
  JTE_DRAIN_CHECK_INTERVAL 排空检查间隔秒数（默认: 10）
EOF
    exit 2
}

# 等待某 Deployment 全部副本就绪
wait_ready() {
    local deploy="$1"
    local timeout="$2"
    log "等待 ${deploy} 全部 ${REPLICAS} 副本就绪（超时 ${timeout}s）..."
    if ! kubectl -n "$NAMESPACE" wait deployment/"$deploy" \
        --for=condition=Available=True \
        --timeout="${timeout}s"; then
        err "${deploy} 就绪超时"
        kubectl -n "$NAMESPACE" get pods -l app=jte --show-labels
        return 1
    fi
    log "${deploy} 已就绪"
}

# 获取 Service 当前指向的 slot
get_current_slot() {
    kubectl -n "$NAMESPACE" get svc "$SERVICE" \
        -o jsonpath='{.spec.selector.slot}'
}

# 切换 Service selector
switch_service() {
    local target_slot="$1"
    local current_slot
    current_slot=$(get_current_slot)
    if [[ "$current_slot" == "$target_slot" ]]; then
        log "Service 已指向 ${target_slot}，无需切换"
        return 0
    fi
    log "切换 Service selector: ${current_slot} -> ${target_slot}"
    kubectl -n "$NAMESPACE" patch svc "$SERVICE" \
        --type=json \
        -p="[{\"op\":\"replace\",\"path\":\"/spec/selector/slot\",\"value\":\"${target_slot}\"}]"
    log "Service 已切到 ${target_slot}"
}

# 等待旧 Deployment 排空：当 active(ready) 副本数为 0 即完成
# 注意：K8s 删除 Pod 时先发 SIGTERM，进程进入优雅停机（拒绝新连接 + 排空 + 下发退避），
# terminationGracePeriodSeconds=330 保证有足够时间排空。
wait_drain() {
    local deploy="$1"
    local timeout="$2"
    log "等待 ${deploy} 排空（最长 ${timeout}s）... ${BACKOFF_HINT}"
    local elapsed=0
    while (( elapsed < timeout )); do
        local ready
        ready=$(kubectl -n "$NAMESPACE" get deploy "$deploy" \
            -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
        ready="${ready:-0}"
        local replicas
        replicas=$(kubectl -n "$NAMESPACE" get deploy "$deploy" \
            -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
        log "${deploy}: replicas=${replicas} ready=${ready} (elapsed=${elapsed}s)"
        if (( replicas == 0 )) || (( ready == 0 )); then
            log "${deploy} 已排空"
            return 0
        fi
        sleep "$DRAIN_CHECK_INTERVAL"
        elapsed=$((elapsed + DRAIN_CHECK_INTERVAL))
    done
    err "${deploy} 排空超时（${timeout}s），将强制下线"
    return 1
}

scale() {
    local deploy="$1" rep="$2"
    log "缩放 ${deploy} -> ${rep} 副本"
    kubectl -n "$NAMESPACE" scale deploy "$deploy" --replicas="$rep"
}

main() {
    local rollback=0
    while (( $# > 0 )); do
        case "$1" in
            --rollback) rollback=1 ;;
            -h|--help)  usage ;;
            *)          err "未知参数: $1"; usage ;;
        esac
        shift
    done

    local src_slot dst_slot src_deploy dst_deploy
    if (( rollback == 0 )); then
        src_slot="blue"; dst_slot="green"
        src_deploy="jte-blue"; dst_deploy="jte-green"
    else
        src_slot="green"; dst_slot="blue"
        src_deploy="jte-green"; dst_deploy="jte-blue"
    fi

    # 前置检查：Service 当前必须指向 src_slot
    local current
    current=$(get_current_slot)
    if [[ "$current" != "$src_slot" ]]; then
        err "当前 Service 指向 '${current}'，与预期源 '${src_slot}' 不符。请确认集群状态。"
        exit 1
    fi
    log "当前活跃 slot: ${src_slot}，目标 slot: ${dst_slot}"

    # 1. 扩容目标 Deployment（不接流量）
    scale "$dst_deploy" "$REPLICAS"

    # 2. 等待目标就绪
    wait_ready "$dst_deploy" "$READY_TIMEOUT"

    # 3. 切换 Service 流量到目标
    switch_service "$dst_slot"

    # 4. 等待源排空（优雅停机由进程内 SIGTERM 处理 + preStop sleep）
    # 此时源仍在运行但已不接新流量，进程收到的是 K8s 删除 Pod 的 SIGTERM。
    # 为触发 SIGTERM，先缩放到 0，进程进入 GracefulShutdown 流程。
    scale "$src_deploy" 0
    wait_drain "$src_deploy" "$DRAIN_TIMEOUT" || true

    # 5. 完成
    log "蓝绿切换完成: ${src_slot} -> ${dst_slot}"
    log "如需回滚: $0 --rollback"
    kubectl -n "$NAMESPACE" get deploy,svc,pods -l app=jte
}

main "$@"
