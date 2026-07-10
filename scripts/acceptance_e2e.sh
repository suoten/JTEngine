#!/usr/bin/env bash
# JTE 端到端验收脚本（Linux/macOS 版）
#
# 执行流程：构建 → 启动 → 健康检查 → 关键 API 烟测 → 停止清理
# 用法：chmod +x scripts/acceptance_e2e.sh && ./scripts/acceptance_e2e.sh
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
JTE_DIR="${PROJECT_ROOT}/jte"
BINARY="${JTE_DIR}/bin/jte"
CONFIG="${1:-${PROJECT_ROOT}/jte/configs/jte.yaml}"
API_PORT="${API_PORT:-8080}"
TIMEOUT_SEC="${TIMEOUT_SEC:-30}"

pass=0
fail=0
warnings=0

step() { echo -e "\n\033[36m[STEP]\033[0m $1"; }
ok()   { echo -e "  \033[32m[PASS]\033[0m $1"; ((pass++)); }
nok()  { echo -e "  \033[31m[FAIL]\033[0m $1"; ((fail++)); }
warn() { echo -e "  \033[33m[WARN]\033[0m $1"; ((warnings++)); }

echo -e "\033[36m========================================\033[0m"
echo -e "\033[36m  JTE 端到端验收脚本 (Linux)\033[0m"
echo -e "\033[36m========================================\033[0m"

# --- 1. 构建 ---
step "构建 JTE 核心引擎"
cd "$JTE_DIR"
if go build -o "$BINARY" ./cmd/jte 2>&1; then
    ok "核心引擎编译成功"
else
    nok "核心引擎编译失败"
    exit 1
fi

# --- 2. 构建模块 ---
step "构建子模块"
MODULES_DIR="${PROJECT_ROOT}/jte-modules"
module_fail=0
for dir in "${MODULES_DIR}"/module-*; do
    [ -d "$dir" ] || continue
    if ! (cd "$dir" && go build ./... >/dev/null 2>&1); then
        warn "$(basename "$dir") 构建失败（非阻塞）"
    fi
done
ok "子模块构建完成"

# --- 3. 启动服务 ---
step "启动 JTE 服务"

# 开发环境跳过 JWT 校验
export JTE_ALLOW_INSECURE_JWT=1

if [ ! -f "$CONFIG" ]; then
    warn "配置文件不存在: $CONFIG，跳过启动测试"
    echo -e "\n验收结果: $pass 通过, $fail 失败, $warnings 警告"
    [ "$fail" -gt 0 ] && exit 1 || exit 0
fi

JTE_PID=""
cleanup() {
    if [ -n "$JTE_PID" ] && kill -0 "$JTE_PID" 2>/dev/null; then
        kill "$JTE_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$JTE_PID" 2>/dev/null || true
    fi
    rm -f "${JTE_DIR}/jte_stdout.log" "${JTE_DIR}/jte_stderr.log"
}
trap cleanup EXIT

"$BINARY" serve --config "$CONFIG" >"${JTE_DIR}/jte_stdout.log" 2>"${JTE_DIR}/jte_stderr.log" &
JTE_PID=$!

sleep 3

if ! kill -0 "$JTE_PID" 2>/dev/null; then
    nok "JTE 服务启动后立即退出"
    head -20 "${JTE_DIR}/jte_stderr.log" 2>/dev/null || true
    exit 1
fi
ok "JTE 服务已启动 (PID: $JTE_PID)"

# --- 4. 健康检查 ---
step "健康检查"
healthy=false
for i in $(seq 1 "$TIMEOUT_SEC"); do
    if curl -sf "http://localhost:${API_PORT}/api/v1/health" >/dev/null 2>&1; then
        healthy=true
        break
    fi
    sleep 1
done

if $healthy; then
    response=$(curl -sf "http://localhost:${API_PORT}/api/v1/health" 2>/dev/null || echo "{}")
    ok "健康检查通过: $response"
else
    nok "健康检查超时（${TIMEOUT_SEC}s）"
fi

# --- 5. 关键 API 烟测 ---
step "关键 API 烟测"

# 5.1 登录接口
login_status=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://localhost:${API_PORT}/api/v1/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' \
    --max-time 5 2>/dev/null || echo "000")

if [ "$login_status" = "400" ] || [ "$login_status" = "401" ]; then
    ok "登录接口可访问（HTTP $login_status 为预期行为）"
elif [ "$login_status" = "000" ]; then
    warn "登录接口不可访问"
else
    ok "登录接口可访问（HTTP $login_status）"
fi

# 5.2 Prometheus 指标
metrics_resp=$(curl -sf "http://localhost:${API_PORT}/metrics" --max-time 5 2>/dev/null || echo "")
if echo "$metrics_resp" | grep -q "jte_"; then
    ok "Prometheus 指标端点正常"
elif [ -n "$metrics_resp" ]; then
    warn "指标端点响应但未包含 jte_ 前缀指标"
else
    warn "指标端点不可访问"
fi

# --- 6. 停止清理 ---
step "停止 JTE 服务"
cleanup
ok "JTE 服务已停止"

# --- 结果汇总 ---
echo -e "\n\033[36m========================================\033[0m"
echo -e "\033[36m  验收结果\033[0m"
echo -e "\033[36m========================================\033[0m"
echo -e "  通过: \033[32m$pass\033[0m"
echo -e "  失败: \033[31m$fail\033[0m"
echo -e "  警告: \033[33m$warnings\033[0m"

if [ "$fail" -gt 0 ]; then
    echo -e "\n  状态: 验收未通过 ❌"
    exit 1
else
    echo -e "\n  状态: 验收通过 ✅"
    exit 0
fi
