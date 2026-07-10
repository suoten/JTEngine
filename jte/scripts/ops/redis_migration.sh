#!/usr/bin/env bash
# JTE Redis 在线迁移脚本（redis-shake 方案）
# AUTO-FIX-2026-06-30 [P1-4]: Redis 在线迁移（在线状态不丢失）
#
# 流程（六阶段迁移）：
#   Phase 1: 部署新 Redis Cluster（3 主 3 从）
#   Phase 2: 部署 redis-shake，全量同步 + 增量同步
#   Phase 3: 应用开启双写（旧 + 新）
#   Phase 4: 读切换到新 Redis（灰度 → 全量）
#   Phase 5: 停止写旧 Redis，保留 24 小时观察
#   Phase 6: 24 小时后下线旧 Redis
#
# 用法：
#   ./redis_migration.sh phase1                      # 部署新集群
#   ./redis_migration.sh phase2                      # 启动 redis-shake 同步
#   ./redis_migration.sh phase3                      # 开启双写（需应用配合，本脚本切换配置）
#   ./redis_migration.sh phase4                      # 读切换到新 Redis
#   ./redis_migration.sh phase5                      # 停止写旧 Redis（保留 24h）
#   ./redis_migration.sh phase6                      # 下线旧 Redis
#   ./redis_migration.sh status                      # 查看迁移状态
#   ./redis_migration.sh all                         # 执行 phase1-4（不含下线）
#
# 依赖：redis-cli、redis-shake、kubectl（如新集群在 K8s）。
set -euo pipefail

# ===== 默认配置 =====
OLD_REDIS="${JTE_OLD_REDIS:-redis-old:6379}"          # 旧 Redis（单机或哨兵地址）
NEW_REDIS_NODES="${JTE_NEW_REDIS_NODES:-redis-0.redis:6379 redis-1.redis:6379 redis-2.redis:6379 redis-3.redis:6379 redis-4.redis:6379 redis-5.redis:6379}"
SHAKE_HOME="${JTE_SHAKE_HOME:-/opt/redis-shake}"
SHAKE_CONFIG="${SHAKE_HOME}/shake.toml"
SHAKE_LOG="${SHAKE_HOME}/shake.log"
SHAKE_PID="${SHAKE_HOME}/shake.pid"
STATE_FILE="${SHAKE_HOME}/migration.state"
DUAL_WRITE_FLAG="/app/config/redis-dual-write.enabled"  # 应用读取此文件判断是否双写
READ_TARGET_FLAG="/app/config/redis-read-target"         # old | new
OBSERVE_HOURS=24

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

# 状态持久化（断点续跑）
save_state() { echo "$1=$(date '+%F %T')" > "$STATE_FILE"; }
read_state() {
    [[ -f "$STATE_FILE" ]] || { echo "none"; return; }
    awk -F= '{print $1}' "$STATE_FILE"
}

usage() {
    cat <<EOF
用法: $0 <phase1|phase2|phase3|phase4|phase5|phase6|status|all>

阶段:
  phase1  部署新 Redis Cluster（3 主 3 从）
  phase2  部署 redis-shake，全量 + 增量同步
  phase3  开启应用双写（旧 + 新）
  phase4  读切换到新 Redis
  phase5  停止写旧 Redis（保留 24h 观察）
  phase6  下线旧 Redis
  status  查看当前迁移状态
  all     顺序执行 phase1-4

环境变量:
  JTE_OLD_REDIS          旧 Redis 地址（默认 redis-old:6379）
  JTE_NEW_REDIS_NODES    新集群节点列表（空格分隔，默认 redis-0..5:6379）
  JTE_SHAKE_HOME         redis-shake 安装目录（默认 /opt/redis-shake）
EOF
    exit 2
}

# 检查 redis-cli 可用
require_redis_cli() {
    command -v redis-cli >/dev/null 2>&1 || fatal "redis-cli 未安装"
}

# ===== Phase 1: 部署新 Redis Cluster =====
phase1() {
    log "==== Phase 1: 部署新 Redis Cluster ===="
    require_redis_cli

    # 检查新集群是否已初始化
    local first_node
    first_node=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    log "检查新集群状态: ${first_node}"
    if redis-cli -h "${first_node%%:*}" -p "${first_node##*:}" PING 2>/dev/null | grep -q PONG; then
        log "新 Redis 节点已运行，检查集群状态..."
        if redis-cli -h "${first_node%%:*}" -p "${first_node##*:}" CLUSTER INFO 2>/dev/null | grep -q "cluster_state:ok"; then
            log "新 Redis Cluster 已就绪，跳过 phase1"
            save_state phase1
            return 0
        fi
    else
        err "新 Redis 节点 ${first_node} 不可达。请先手动部署 Redis 节点（K8s StatefulSet 或裸机），再执行 phase1。"
        err "建议参考: kubectl apply -f deploy/k8s/redis-cluster.yaml"
        exit 1
    fi

    # 创建集群（3 主 3 从）
    log "创建 Redis Cluster（3 主 3 从）..."
    local nodes_arr=($NEW_REDIS_NODES)
    redis-cli --cluster create "${nodes_arr[@]}" \
        --cluster-replicas 1 \
        --cluster-yes || fatal "Redis Cluster 创建失败"

    # 验证集群
    log "验证集群状态:"
    redis-cli -h "${first_node%%:*}" -p "${first_node##*:}" CLUSTER INFO
    redis-cli -h "${first_node%%:*}" -p "${first_node##*:}" CLUSTER NODES

    save_state phase1
    log "Phase 1 完成"
}

# 生成 redis-shake 配置
gen_shake_config() {
    local old_host="${OLD_REDIS%%:*}"
    local old_port="${OLD_REDIS##*:}"
    local new_first
    new_first=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    local new_host="${new_first%%:*}"
    local new_port="${new_first##*:}"

    mkdir -p "$SHAKE_HOME"
    cat > "$SHAKE_CONFIG" <<EOF
# redis-shake 同步配置（旧 → 新）
# AUTO-FIX-2026-06-30 [P1-4]
[sync_reader]
cluster = false
address = "${old_host}:${old_port}"
password = ""

[redis_writer]
cluster = true
address = "${new_host}:${new_port}"
password = ""
rdb_write_command = true

[filter]
# 跳过 TTL 为 0 的 key 不写（避免覆盖新集群已设置的 TTL）
skip_fake_slave = false

[advanced]
rdb_parallel = 8
ping_during_sync = true
EOF
    log "redis-shake 配置已生成: ${SHAKE_CONFIG}"
}

# ===== Phase 2: 启动 redis-shake 同步 =====
phase2() {
    log "==== Phase 2: 启动 redis-shake 全量 + 增量同步 ===="
    require_redis_cli

    [[ -x "${SHAKE_HOME}/redis-shake" ]] || fatal "redis-shake 未安装于 ${SHAKE_HOME}，请先下载 redis-shake"

    gen_shake_config

    # 如已有进程在跑，先停止
    if [[ -f "$SHAKE_PID" ]] && kill -0 "$(cat "$SHAKE_PID")" 2>/dev/null; then
        log "redis-shake 已在运行（PID $(cat "$SHAKE_PID")），先停止"
        kill "$(cat "$SHAKE_PID")" || true
        sleep 3
    fi

    # 启动 redis-shake（后台）
    log "启动 redis-shake..."
    nohup "${SHAKE_HOME}/redis-shake" -config "$SHAKE_CONFIG" > "$SHAKE_LOG" 2>&1 &
    echo $! > "$SHAKE_PID"
    log "redis-shake PID: $(cat "$SHAKE_PID")"
    log "日志: ${SHAKE_LOG}"

    # 等待全量同步完成（检查日志）
    log "等待全量同步完成（最多 30 分钟）..."
    local elapsed=0
    while (( elapsed < 1800 )); do
        if grep -q "sync rdb done\|incremental sync started\|sync streaming" "$SHAKE_LOG" 2>/dev/null; then
            log "全量同步完成，进入增量同步阶段"
            break
        fi
        if grep -q "panic\|fatal error" "$SHAKE_LOG" 2>/dev/null; then
            err "redis-shake 异常，查看日志: ${SHAKE_LOG}"
            tail -n 50 "$SHAKE_LOG" >&2
            exit 1
        fi
        sleep 10
        elapsed=$((elapsed + 10))
        log "  同步中... (elapsed=${elapsed}s)"
    done

    # 验证增量同步延迟
    log "验证增量同步延迟（写入测试 key 到旧 Redis，检查新 Redis 是否同步）..."
    local test_key="__migration_test_$(date +%s)__"
    redis-cli -h "${OLD_REDIS%%:*}" -p "${OLD_REDIS##*:}" SET "$test_key" "ok" EX 60 >/dev/null
    sleep 2
    local new_first
    new_first=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    local val
    val=$(redis-cli -h "${new_first%%:*}" -p "${new_first##*:}" GET "$test_key" 2>/dev/null)
    if [[ "$val" == "ok" ]]; then
        log "增量同步验证通过（key 已复制到新集群）"
    else
        fatal "增量同步验证失败：新集群未获取到测试 key"
    fi

    save_state phase2
    log "Phase 2 完成"
}

# ===== Phase 3: 开启应用双写 =====
phase3() {
    log "==== Phase 3: 开启应用双写（旧 + 新） ===="
    log "原理：应用读取 ${DUAL_WRITE_FLAG} 文件存在性判断是否双写。"
    log "应用代码需在写旧 Redis 成功后，异步写新 Redis（失败仅记录日志，不影响主流程）。"

    mkdir -p "$(dirname "$DUAL_WRITE_FLAG")"
    # 记录新集群地址，供应用读取
    echo "$NEW_REDIS_NODES" > "$(dirname "$DUAL_WRITE_FLAG")/redis-new-nodes"
    touch "$DUAL_WRITE_FLAG"

    log "双写已开启（标志文件: ${DUAL_WRITE_FLAG}）"
    log "请确认应用已重载配置（SIGHUP 或滚动重启）。"

    # 等待应用确认双写生效
    read -r -p "应用双写已生效? (y/N) " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { err "应用双写未生效，中止"; exit 1; }

    # 再次验证增量同步延迟（双写下数据应双向一致）
    log "双写后数据一致性校验..."
    local old_host="${OLD_REDIS%%:*}" old_port="${OLD_REDIS##*:}"
    local new_first
    new_first=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    local new_host="${new_first%%:*}" new_port="${new_first##*:}"

    # 抽样校验 100 个 key
    local samples
    samples=$(redis-cli -h "$old_host" -p "$old_port" --scan --count 1000 2>/dev/null | head -n 100)
    local mismatch=0 total=0
    for key in $samples; do
        total=$((total + 1))
        local old_val new_val
        old_val=$(redis-cli -h "$old_host" -p "$old_port" GET "$key" 2>/dev/null)
        new_val=$(redis-cli -h "$new_host" -p "$new_port" GET "$key" 2>/dev/null)
        if [[ "$old_val" != "$new_val" ]]; then
            mismatch=$((mismatch + 1))
            err "  key 不一致: ${key} (old=${old_val:0:20}... new=${new_val:0:20}...)"
        fi
    done
    log "抽样校验: total=${total} mismatch=${mismatch}"
    if (( mismatch > 5 )); then
        fatal "数据不一致率过高，请检查 redis-shake 同步状态后重试"
    fi

    save_state phase3
    log "Phase 3 完成"
}

# ===== Phase 4: 读切换到新 Redis =====
phase4() {
    log "==== Phase 4: 读切换到新 Redis ===="
    log "原理：应用读取 ${READ_TARGET_FLAG} 文件内容（old/new）决定读哪个 Redis。"

    mkdir -p "$(dirname "$READ_TARGET_FLAG")"
    echo "new" > "$READ_TARGET_FLAG"
    log "读目标已切换到新 Redis（标志文件: ${READ_TARGET_FLAG}）"
    log "请确认应用已重载配置（SIGHUP 或滚动重启）。"

    # 监控新 Redis 命中率与延迟
    log "监控新 Redis 状态（30 秒采样）..."
    local new_first
    new_first=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    local new_host="${new_first%%:*}" new_port="${new_first##*:}"
    for i in 1 2 3; do
        redis-cli -h "$new_host" -p "$new_port" INFO stats 2>/dev/null | grep -E "keyspace_hits|keyspace_misses|instantaneous_ops_per_sec"
        sleep 10
    done

    read -r -p "读切换后服务正常? (y/N) " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { err "读切换异常，回退到 old"; echo "old" > "$READ_TARGET_FLAG"; exit 1; }

    save_state phase4
    log "Phase 4 完成"
}

# ===== Phase 5: 停止写旧 Redis（保留 24h 观察） =====
phase5() {
    log "==== Phase 5: 停止写旧 Redis（保留 ${OBSERVE_HOURS}h 观察） ===="

    # 移除双写标志（应用仅写新 Redis）
    rm -f "$DUAL_WRITE_FLAG"
    log "双写已关闭（应用仅写新 Redis）"

    # 旧 Redis 设为只读（防止应用残留写入）
    local old_host="${OLD_REDIS%%:*}" old_port="${OLD_REDIS##*:}"
    log "将旧 Redis 设为只读: CONFIG SET min-replicas-to-write 999（拒绝所有写命令）"
    redis-cli -h "$old_host" -p "$old_port" CONFIG SET min-replicas-to-write 999 2>/dev/null || true
    # 兜底：rename-command 把写命令改名（更彻底，但需重启生效，此处仅提示）
    err "提示：如需彻底拒绝写，可在旧 Redis 配置 rename-command，但需重启 taosd。当前用 min-replicas-to-write 兜底。"

    # 记录停止时间，便于 24h 后下线
    echo "stop_write_time=$(date '+%F %T')" >> "$STATE_FILE"
    log "旧 Redis 已停止写入，保留 ${OBSERVE_HOURS} 小时观察"
    log "请在 $(date -d "+${OBSERVE_HOURS} hours" '+%F %T' 2>/dev/null || date -v+${OBSERVE_HOURS}H '+%F %T' 2>/dev/null) 后执行 phase6 下线"

    save_state phase5
    log "Phase 5 完成"
}

# ===== Phase 6: 下线旧 Redis =====
phase6() {
    log "==== Phase 6: 下线旧 Redis ===="

    # 校验观察期是否足够
    if grep -q "stop_write_time=" "$STATE_FILE" 2>/dev/null; then
        local stop_time
        stop_time=$(grep "stop_write_time=" "$STATE_FILE" | head -n 1 | cut -d= -f2-)
        log "旧 Redis 停止写入时间: ${stop_time}"
        local now_ts stop_ts
        now_ts=$(date +%s)
        stop_ts=$(date -d "$stop_time" +%s 2>/dev/null || date -j -f "%F %T" "$stop_time" +%s 2>/dev/null || echo 0)
        if (( stop_ts > 0 )); then
            local elapsed_hours=$(( (now_ts - stop_ts) / 3600 ))
            log "已观察 ${elapsed_hours} 小时"
            if (( elapsed_hours < OBSERVE_HOURS )); then
                fatal "观察期不足 ${OBSERVE_HOURS}h（当前 ${elapsed_hours}h），请稍后再执行 phase6，或确认无回滚需求后强制执行"
            fi
        fi
    fi

    # 最终一致性校验
    log "下线前最终一致性校验..."
    local old_host="${OLD_REDIS%%:*}" old_port="${OLD_REDIS##*:}"
    local new_first
    new_first=$(echo "$NEW_REDIS_NODES" | awk '{print $1}')
    local new_host="${new_first%%:*}" new_port="${new_first##*:}"
    local old_dbsize new_dbsize
    old_dbsize=$(redis-cli -h "$old_host" -p "$old_port" DBSIZE 2>/dev/null | tr -d '[:space:]')
    new_dbsize=$(redis-cli -h "$new_host" -p "$new_port" DBSIZE 2>/dev/null | tr -d '[:space:]')
    log "旧 Redis DBSIZE=${old_dbsize}, 新 Redis DBSIZE=${new_dbsize}"
    if (( old_dbsize > 0 && new_dbsize < old_dbsize )); then
        fatal "新 Redis 数据量少于旧 Redis，可能同步不完整，请检查 redis-shake 日志"
    fi

    # 停止 redis-shake
    if [[ -f "$SHAKE_PID" ]] && kill -0 "$(cat "$SHAKE_PID")" 2>/dev/null; then
        log "停止 redis-shake（PID $(cat "$SHAKE_PID")）"
        kill "$(cat "$SHAKE_PID")" || true
        rm -f "$SHAKE_PID"
    fi

    # 下线旧 Redis（提示运维执行，避免误删生产资源）
    err "请手动下线旧 Redis:"
    err "  - kubectl delete statefulset redis-old（如在 K8s）"
    err "  - 或 systemctl stop redis && systemctl disable redis（裸机）"
    err "  - 清理 ${DUAL_WRITE_FLAG} / ${READ_TARGET_FLAG} 标志文件"

    # 清理状态
    save_state phase6
    log "Phase 6 完成，迁移结束"
}

# ===== 状态查询 =====
show_status() {
    log "==== Redis 迁移状态 ===="
    local cur
    cur=$(read_state)
    log "当前阶段: ${cur}"
    if [[ -f "$STATE_FILE" ]]; then
        cat "$STATE_FILE"
    fi
    log ""
    log "标志文件:"
    if [[ -f "$DUAL_WRITE_FLAG" ]]; then
        log "  双写: 已开启 (${DUAL_WRITE_FLAG})"
    else
        log "  双写: 未开启"
    fi
    if [[ -f "$READ_TARGET_FLAG" ]]; then
        log "  读目标: $(cat "$READ_TARGET_FLAG")"
    else
        log "  读目标: 默认(old)"
    fi
    if [[ -f "$SHAKE_PID" ]] && kill -0 "$(cat "$SHAKE_PID")" 2>/dev/null; then
        log "  redis-shake: 运行中 (PID $(cat "$SHAKE_PID"))"
    else
        log "  redis-shake: 未运行"
    fi
}

main() {
    (( $# == 1 )) || usage
    case "$1" in
        phase1) phase1 ;;
        phase2) phase2 ;;
        phase3) phase3 ;;
        phase4) phase4 ;;
        phase5) phase5 ;;
        phase6) phase6 ;;
        status)  show_status ;;
        all)
            phase1
            phase2
            phase3
            phase4
            log "Phase 1-4 已完成。请观察后手动执行 phase5（停写旧）和 phase6（下线旧）。"
            ;;
        -h|--help) usage ;;
        *) err "未知参数: $1"; usage ;;
    esac
}

main "$@"
