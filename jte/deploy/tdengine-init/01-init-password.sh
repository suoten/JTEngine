#!/bin/sh
# TDengine 生产环境初始化脚本
# [P0-安全] 移除默认密码 taosdata，强制使用环境变量 TDENGINE_PASSWORD 配置的密码
# 此脚本由 docker-compose-prod.yml 挂载到 /docker-entrypoint-initdb.d/
# 在 TDengine 首次启动时自动执行

set -e

# 等待 TDengine 就绪
echo "[tdengine-init] Waiting for TDengine to be ready..."
for i in $(seq 1 30); do
    if taos -s "SELECT 1" >/dev/null 2>&1; then
        echo "[tdengine-init] TDengine is ready."
        break
    fi
    echo "[tdengine-init] TDengine not ready yet, retrying ($i/30)..."
    sleep 2
done

# 修改 root 用户密码：从默认的 taosdata 改为环境变量配置的密码
if [ -n "$TDENGINE_PASSWORD" ]; then
    echo "[tdengine-init] Setting TDengine root password..."
    taos -s "ALTER USER root PASS '${TDENGINE_PASSWORD}'"
    echo "[tdengine-init] TDengine root password has been updated."
else
    echo "[tdengine-init] ERROR: TDENGINE_PASSWORD environment variable is not set."
    echo "[tdengine-init] TDengine root password remains default (taosdata) - NOT SECURE!"
    exit 1
fi
