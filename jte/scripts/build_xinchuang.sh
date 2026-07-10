#!/bin/bash
set -e

VERSION="${1:-1.0.0}"
DIST_DIR="./dist/xinchuang-${VERSION}"

echo "=== JTE 信创全栈构建 v${VERSION} ==="

mkdir -p "${DIST_DIR}"

echo "[1/5] 检测Go环境..."
if ! command -v go &> /dev/null; then
    echo "错误: Go 未安装"
    exit 1
fi
echo "Go版本: $(go version)"

echo "[2/5] 构建银河麒麟 ARM64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o "${DIST_DIR}/jte-kylin-arm64" ./cmd/jte/

echo "[3/5] 构建银河麒麟 AMD64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o "${DIST_DIR}/jte-kylin-amd64" ./cmd/jte/

echo "[4/5] 构建龙芯 LoongArch64..."
CGO_ENABLED=0 GOOS=linux GOARCH=loong64 go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o "${DIST_DIR}/jte-loongson" ./cmd/jte/ 2>/dev/null || echo "警告: 龙芯构建需要Go 1.19+，跳过"

echo "[5/5] 生成部署配置..."
cat > "${DIST_DIR}/deploy-kylin.sh" << 'DEPLOY'
#!/bin/bash
JTE_USER="jte"
JTE_HOME="/opt/jte"
JTE_CONFIG="${JTE_HOME}/config"
JTE_DATA="${JTE_HOME}/data"
JTE_LOGS="${JTE_HOME}/logs"

echo "=== JTE 银河麒麟部署 ==="

id ${JTE_USER} &>/dev/null || useradd -r -s /sbin/nologin ${JTE_USER}

mkdir -p ${JTE_HOME} ${JTE_CONFIG} ${JTE_DATA} ${JTE_LOGS}

cp jte-kylin-* ${JTE_HOME}/jte
chmod +x ${JTE_HOME}/jte

cat > /etc/systemd/system/jte.service << EOF
[Unit]
Description=JTE - JT Engine
After=network.target

[Service]
Type=simple
User=${JTE_USER}
WorkingDirectory=${JTE_HOME}
ExecStart=${JTE_HOME}/jte
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable jte
echo "部署完成。运行 'systemctl start jte' 启动服务"
DEPLOY
chmod +x "${DIST_DIR}/deploy-kylin.sh"

echo ""
echo "=== 构建完成 ==="
echo "输出目录: ${DIST_DIR}"
ls -la "${DIST_DIR}/"