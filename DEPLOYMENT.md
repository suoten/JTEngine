# JTE 生产部署指南

本指南详细说明 JTE 车联网部标协议引擎的生产环境部署步骤，涵盖 Docker Compose 一键部署和裸机部署两种方式。

---

## 环境要求

### 最低配置（≤1 万设备）

| 资源 | 要求 |
|------|------|
| CPU | 4 核 |
| 内存 | 8 GB |
| 磁盘 | 100 GB SSD |
| 操作系统 | Linux（Ubuntu 22.04+ / CentOS 8+ / 麒麟 V10） |
| Go | 1.22+（仅源码编译需要） |

### 推荐配置（1-10 万设备）

| 资源 | 要求 |
|------|------|
| CPU | 8-16 核 |
| 内存 | 16-32 GB |
| 磁盘 | 500 GB NVMe SSD |
| 网络 | 千兆及以上 |

### 10 万+ 设备集群

| 资源 | 要求 |
|------|------|
| CPU | 32+ 核 |
| 内存 | 64+ GB |
| 磁盘 | 2 TB NVMe SSD（TDengine）+ 1 TB 对象存储 |
| 网络 | 万兆内网 |

### 外部依赖

| 组件 | 版本 | 用途 | 默认端口 |
|------|------|------|----------|
| MySQL | 8.0+ 或 达梦 V8 | 关系数据 | 3306 |
| TDengine | 3.8.0+ | 时序数据 | 6030 / 6041 (WS) |
| Redis | 6.0+ | 缓存 | 6379 |
| MinIO | 最新版 | 归档存储 | 9000 / 9001 |

> 信创环境可用 达梦/金仓/高斯 替代 MySQL，TDengine 支持国产 OS。

---

## Docker Compose 部署（推荐）

### 1. 准备配置

```bash
git clone https://github.com/suoten/jt-engine.git
cd jte
```

### 2. 修改配置文件

```bash
cp jte/configs/jte.yaml jte/configs/jte.yaml.local
```

编辑 `jte.yaml.local`，按 [CONFIG_CHECKLIST.md](CONFIG_CHECKLIST.md) 修改必改项：

```yaml
# 必改项（参见 CONFIG_CHECKLIST.md）
api:
  jwt:
    kms_source: "env"
    active_kid: "kid-2026-07"
  require_tls: true              # 生产必须启用 HTTPS
  tls:
    enabled: true
    cert_file: "/data/certs/server.pem"
    key_file: "/data/certs/server.key"

storage:
  type: "mysql"
  dsn: "jte:你的强密码@tcp(mysql:3306)/jte?parseTime=true"
  time_series:
    driver: "tdengine"
    host: "tdengine"
    port: 6030
    ws_enabled: true
    ws_dsn: "root:你的密码@ws(tdengine:6041)/jte_ts"
```

### 3. 设置环境变量

```bash
# JWT 密钥（≥32 字节，openssl rand -base64 48 生成）
export JTE_JWT_SECRET_KID_2026_07="你的JWT密钥"

# 数据库密码
export MYSQL_ROOT_PASSWORD="你的MySQL密码"
export TDENGINE_PASSWORD="你的TDengine密码"

# SM4 加密密钥
export JTE_SM4_KEY="你的SM4密钥"
```

### 4. 启动服务

```bash
docker-compose -f jte/deploy/docker-compose.yml up -d
```

### 5. 验证

```bash
# 健康检查
curl https://localhost:8080/api/v1/health

# 验收脚本
./scripts/acceptance_e2e.sh
```

---

## 裸机部署

### 1. 安装 Go

```bash
# 下载 Go 1.22+
wget https://go.dev/dl/go1.22.10.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.10.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### 2. 编译 JTE

```bash
git clone https://github.com/suoten/jt-engine.git
cd jte/jte
make build-binary
# 产物：bin/jte
```

### 3. 编译模块（可选）

```bash
make modules-build
```

### 4. 安装依赖服务

```bash
# MySQL
sudo apt install mysql-server

# TDengine 3.8+
wget https://www.tdengine.com/assets-get/3.8.0/TDengine-server-3.8.0.0-Linux-X64.deb
sudo dpkg -i TDengine-server-3.8.0.0-Linux-X64.deb

# Redis
sudo apt install redis-server

# MinIO
wget https://dl.min.io/server/minio/release/linux-amd64/minio
chmod +x minio && sudo mv minio /usr/local/bin/
```

### 5. 配置

```bash
sudo mkdir -p /etc/jte /var/lib/jte/data /var/log/jte
sudo cp configs/jte.yaml /etc/jte/jte.yaml
sudo cp configs/jte.yaml /etc/jte/jte.yaml  # 编辑配置
```

### 6. 创建 Systemd 服务

```bash
sudo tee /etc/systemd/system/jte.service << 'EOF'
[Unit]
Description=JTE - JT Engine
After=network.target mysql.service redis-server.service

[Service]
Type=simple
User=jte
ExecStart=/usr/local/bin/jte serve --config /etc/jte/jte.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

Environment=JTE_JWT_SECRET_KID_2026_07=你的JWT密钥
Environment=JTE_SM4_KEY=你的SM4密钥

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable jte
sudo systemctl start jte
```

### 7. 验证

```bash
sudo systemctl status jte
curl http://localhost:8080/api/v1/health
```

---

## 配置项说明

### 关键配置

| 配置项 | 说明 | 生产建议 |
|--------|------|----------|
| `api.jwt.kms_source` | JWT 密钥来源 | `env`（环境变量注入）|
| `api.tls.enabled` | HTTPS 启用 | `true`（等保 2.0 必须）|
| `api.require_tls` | 强制 HTTPS | `true`（HTTP 返回 426）|
| `api.security.conn_limit_per_ip` | 单 IP 连接限制 | `100`（防 Slowloris）|
| `storage.time_series.ws_enabled` | TDengine WS 连接 | `true`（千万点/秒写入）|
| `storage.archive.enabled` | 自动归档 | `true`（3 年轨迹归档）|
| `gateway.oom_protect.enabled` | OOM 防护 | `true`（内存熔断）|

> 完整配置项参见 [CONFIG_CHECKLIST.md](CONFIG_CHECKLIST.md) 和 [jte/configs/jte.yaml](jte/configs/jte.yaml)

---

## 健康检查

```bash
# API 健康检查
curl https://你的域名/api/v1/health

# Prometheus 指标
curl https://你的域名/metrics

# 关键指标
jte_connections_total     # 当前连接数
jte_online_devices        # 在线设备数
jte_messages_total        # 消息处理总数
jte_parse_success_rate    # 协议解析成功率
```

---

## 扩缩容

### 水平扩容

JTE 支持集群部署（module-cluster），通过负载均衡分发 TCP 连接：

```
                    ┌─────────────┐
  终端 ────TCP────► │ Load Balancer│
                    └──────┬──────┘
               ┌──────────┼──────────┐
               ▼          ▼          ▼
          ┌────────┐ ┌────────┐ ┌────────┐
          │ JTE-1  │ │ JTE-2  │ │ JTE-3  │
          └───┬────┘ └───┬────┘ └───┬────┘
              └──────────┼──────────┘
                    ┌────▼────┐
                    │  MySQL  │ (共享)
                    │TDengine │ (集群)
                    │  Redis  │ (共享)
                    └─────────┘
```

### 垂直扩容

- 增加 `gateway.max_connections` 和 `max_devices`
- 调整 `storage.time_series.vgroups`（TDengine 分片数）
- 调整 `storage.time_series.wp_worker_count`（写入 Worker 数）

---

## 证书续期

### Let's Encrypt 自动续期

```yaml
api:
  tls:
    acme: true
    acme_dir: "./data/acme"
    acme_domains:
      - "jte.yourdomain.com"
```

### 自签 CA 手动续期

```bash
# 生成新证书
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.pem -days 365 -nodes

# 替换证书后重启
sudo systemctl restart jte
```

---

## 故障排查

| 问题 | 排查方法 |
|------|----------|
| 启动失败：JWT 密钥校验 | 确认环境变量 `JTE_JWT_SECRET_*` 已设置 |
| 连接超时 | 检查防火墙 7611 端口、TDengine 6030 端口 |
| 写入慢 | 确认 `ws_enabled: true`，检查 TDengine VGroups |
| 视频黑屏 | 前端手动触发关键帧（POST /media/keyframe）|
| 内存 OOM | 调整 `oom_protect.fatal_mb` 阈值 |

> 详细日志：`/var/log/jte/jte.log`，审计日志：`/var/log/jte/audit.log`
