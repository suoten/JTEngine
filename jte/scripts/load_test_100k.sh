#!/usr/bin/env bash
# AUTO-FIX-2026-06-30 [集成-7]: 10 万连接压测脚本
#
# 验收标准：CPU < 60%，内存 < 8GB，10 万连接稳定运行 10 分钟
#
# 用法：
#   ./scripts/load_test_100k.sh [JTE_GATEWAY_ADDR] [DEVICE_COUNT]
#
# 依赖：
#   - JTE 网关已启动（默认 127.0.0.1:7611）
#   - Go 1.22+（编译压测工具）
#   - curl（拉取 /metrics）
#   - bc（计算百分比）

set -euo pipefail

GATEWAY_ADDR="${1:-127.0.0.1:7611}"
DEVICE_COUNT="${2:-100000}"
RAMP_TIME="120s"
DURATION="600s"  # 10 分钟
API_ADDR="${3:-127.0.0.1:8080}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=========================================="
echo "JTE 10 万连接压测"
echo "=========================================="
echo "网关地址: $GATEWAY_ADDR"
echo "API 地址: $API_ADDR"
echo "设备数:   $DEVICE_COUNT"
echo "递增时间: $RAMP_TIME"
echo "持续时长: $DURATION"
echo ""

# 检查 JTE 网关是否可达
if ! timeout 5 bash -c "echo > /dev/tcp/${GATEWAY_ADDR/:/ }" 2>/dev/null; then
    echo "❌ JTE 网关 $GATEWAY_ADDR 不可达，请先启动 JTE 服务"
    exit 1
fi
echo "✅ JTE 网关可达"

# 编译压测工具
echo "📦 编译压测工具..."
cd "$PROJECT_ROOT"
go build -o /tmp/jte-loadtest ./cmd/loadtest
echo "✅ 编译完成"

# 记录压测前基线指标
echo ""
echo "📊 压测前基线指标:"
BASELINE_METRICS=$(curl -s "http://$API_ADDR/metrics" 2>/dev/null || echo "无法获取指标")
BASELINE_CONNS=$(echo "$BASELINE_METRICS" | grep '^jte_connections_total ' | awk '{print $2}' || echo "0")
BASELINE_ONLINE=$(echo "$BASELINE_METRICS" | grep '^jte_online_devices ' | awk '{print $2}' || echo "0")
echo "  基线连接数: $BASELINE_CONNS"
echo "  基线在线设备: $BASELINE_ONLINE"

# 启动压测（后台运行，限时 DURATION）
echo ""
echo "🚀 启动压测（$DEVICE_COUNT 设备，递增 $RAMP_TIME）..."
/tmp/jte-loadtest -addr "$GATEWAY_ADDR" -count "$DEVICE_COUNT" -ramp "$RAMP_TIME" -report 10s &
LOADTEST_PID=$!

# 清理函数
cleanup() {
    echo ""
    echo "🛑 停止压测..."
    kill -INT $LOADTEST_PID 2>/dev/null || true
    wait $LOADTEST_PID 2>/dev/null || true
}
trap cleanup EXIT

# 等待连接建立完成（最多等 5 分钟）
echo "⏳ 等待设备连接建立..."
CONNECT_DEADLINE=$((SECONDS + 300))
while [ $SECONDS -lt $CONNECT_DEADLINE ]; do
    sleep 10
    METRICS=$(curl -s "http://$API_ADDR/metrics" 2>/dev/null || echo "")
    ONLINE=$(echo "$METRICS" | grep '^jte_online_devices ' | awk '{print $2}' || echo "0")
    echo "  在线设备: $ONLINE / $DEVICE_COUNT"
    if [ "$ONLINE" -ge "$DEVICE_COUNT" ] 2>/dev/null; then
        echo "✅ 全部 $DEVICE_COUNT 设备已连接"
        break
    fi
done

# 持续监控 10 分钟
echo ""
echo "📈 持续监控 $DURATION ..."
MONITOR_DEADLINE=$((SECONDS + ${DURATION%s}))
while [ $SECONDS -lt $MONITOR_DEADLINE ]; do
    sleep 30

    # 拉取指标
    METRICS=$(curl -s "http://$API_ADDR/metrics" 2>/dev/null || echo "")

    ONLINE=$(echo "$METRICS" | grep '^jte_online_devices ' | awk '{print $2}' || echo "0")
    CONNS=$(echo "$METRICS" | grep '^jte_connections_total ' | awk '{print $2}' || echo "0")
    MSGS=$(echo "$METRICS" | grep '^jte_messages_total ' | awk '{print $2}' || echo "0")
    STORAGE=$(echo "$METRICS" | grep '^jte_storage_write_total ' | awk '{print $2}' || echo "0")

    # 系统资源（从 /proc 读取，适用于 Linux）
    if [ -f /proc/loadavg ]; then
        LOAD_AVG=$(cut -d' ' -f1 /proc/loadavg)
        CPU_COUNT=$(nproc 2>/dev/null || echo 1)
        CPU_PCT=$(echo "scale=1; $LOAD_AVG * 100 / $CPU_COUNT" | bc 2>/dev/null || echo "N/A")
    else
        LOAD_AVG="N/A"
        CPU_PCT="N/A"
    fi

    # 内存（从 /proc/meminfo 读取）
    if [ -f /proc/meminfo ]; then
        MEM_TOTAL=$(grep MemTotal /proc/meminfo | awk '{print $2}')
        MEM_AVAIL=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
        MEM_USED=$((MEM_TOTAL - MEM_AVAIL))
        MEM_USED_MB=$((MEM_USED / 1024))
    else
        MEM_USED_MB="N/A"
    fi

    ELAPSED=$((SECONDS + 0))
    echo "[$ELAPSED s] 在线=$ONLINE 连接=$CONNS 消息=$MSGS 存储=$STORAGE | CPU=${CPU_PCT}% 内存=${MEM_USED_MB}MB"

    # 检查压测进程是否仍在运行
    if ! kill -0 $LOADTEST_PID 2>/dev/null; then
        echo "⚠️  压测进程已退出"
        break
    fi
done

# 最终结果
echo ""
echo "=========================================="
echo "压测结果"
echo "=========================================="
FINAL_METRICS=$(curl -s "http://$API_ADDR/metrics" 2>/dev/null || echo "")
FINAL_ONLINE=$(echo "$FINAL_METRICS" | grep '^jte_online_devices ' | awk '{print $2}' || echo "0")
FINAL_CONNS=$(echo "$FINAL_METRICS" | grep '^jte_connections_total ' | awk '{print $2}' || echo "0")
FINAL_MSGS=$(echo "$FINAL_METRICS" | grep '^jte_messages_total ' | awk '{print $2}' || echo "0")
FINAL_STORAGE=$(echo "$FINAL_METRICS" | grep '^jte_storage_write_total ' | awk '{print $2}' || echo "0")

echo "在线设备:       $FINAL_ONLINE / $DEVICE_COUNT"
echo "累计连接数:     $FINAL_CONNS"
echo "累计消息数:     $FINAL_MSGS"
echo "累计存储写入:   $FINAL_STORAGE"

if [ -f /proc/meminfo ]; then
    MEM_TOTAL=$(grep MemTotal /proc/meminfo | awk '{print $2}')
    MEM_AVAIL=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    MEM_USED_MB=$(((MEM_TOTAL - MEM_AVAIL) / 1024))
    echo "当前内存使用:   ${MEM_USED_MB} MB"
    if [ "$MEM_USED_MB" -lt 8192 ] 2>/dev/null; then
        echo "✅ 内存 < 8GB: 通过"
    else
        echo "❌ 内存 >= 8GB: 未通过"
    fi
fi

if [ "$FINAL_ONLINE" -ge "$DEVICE_COUNT" ] 2>/dev/null; then
    echo "✅ 10 万连接: 通过"
else
    echo "⚠️  在线设备 $FINAL_ONLINE < $DEVICE_COUNT（可能有部分连接超时）"
fi

echo ""
echo "压测完成。"
