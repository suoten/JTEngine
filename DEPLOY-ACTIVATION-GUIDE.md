# JTE 开源版部署 + 授权激活完整教程

> 本教程面向**第一次部署 JTE 的用户**，从零开始：下载开源版 → 部署到服务器 → 购买模块 → 激活授权码 → 安装付费模块 → 全功能上线。
>
> 全程照抄命令即可，无需 Go 编程基础。

---

## 目录

- [第一部分：部署开源版引擎](#第一部分部署开源版引擎)
  - [方式 A：Docker 一键部署（推荐）](#方式-adocker-一键部署推荐)
  - [方式 B：源码编译部署](#方式-b源码编译部署)
- [第二部分：存储配置](#第二部分存储配置)
  - [最小配置（开箱即用）](#最小配置开箱即用)
  - [生产配置（TDengine + Redis + MinIO）](#生产配置tdengine--redis--minio)
- [第三部分：启动与验证](#第三部分启动与验证)
- [第四部分：购买与激活模块](#第四部分购买与激活模块)
  - [4.1 免费版能用什么](#41-免费版能用什么)
  - [4.2 到官网购买模块](#42-到官网购买模块)
  - [4.3 在 JTE 中激活授权码](#43-在-jte-中激活授权码)
  - [4.4 下载并安装付费模块](#44-下载并安装付费模块)
  - [4.5 验证模块加载成功](#45-验证模块加载成功)
- [第五部分：常见问题](#第五部分常见问题)

---

## 第一部分：部署开源版引擎

### 前置要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux（推荐 Ubuntu 22.04 / CentOS 8+） |
| Docker 方式 | Docker 24+ 和 Docker Compose v2 |
| 源码方式 | Go 1.22+ 和 Node.js 20+ |
| 端口 | 7611（设备网关）、8080（Web API/前端） |

### 方式 A：Docker 一键部署（推荐）

#### 步骤 1：克隆仓库

```bash
git clone https://github.com/suoten/jt-engine.git
cd jt-engine/jte
```

#### 步骤 2：生成 JWT 密钥（必须）

```bash
# 生成 JWT 密钥（≥32 字节随机值）
export JTE_API_JWT_SECRET_KID_2026_06=$(openssl rand -base64 48)

# 生成离线解绑密钥（≥32 字节）
export JTE_AUTH_OFFLINE_UNBIND_SECRET=$(openssl rand -base64 48)

# 验证
echo $JTE_API_JWT_SECRET_KID_2026_06
```

> **⚠️ 重要**：这两个环境变量必须持久化。Docker 方式写入 `docker-compose.yml` 的 `environment` 段，裸机方式写入 `/etc/profile.d/jte.sh`。丢失密钥会导致所有 JWT 失效、用户需重新登录。

#### 步骤 3：构建并启动

```bash
# 构建镜像（包含前端 + 后端）
docker compose up -d --build
```

等待 2-3 分钟（前端 npm install + 后端 go build）。

#### 步骤 4：验证运行

```bash
# 查看容器状态
docker compose ps

# 查看日志
docker compose logs -f jte

# 健康检查
curl http://localhost:8080/health
# 预期返回: {"status":"ok"}
```

看到日志中出现 `JTE server started` 即启动成功。

#### 步骤 5：访问前端

浏览器打开 `http://你的服务器IP:8080`

- 默认管理员账号：`admin`
- 默认密码：`admin123`
- **首次登录后请立即修改密码**

---

### 方式 B：源码编译部署

#### 步骤 1：安装依赖

```bash
# Ubuntu
sudo apt update
sudo apt install -y golang-go nodejs npm

# 或手动安装 Go 1.22+
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 安装 Node.js 20
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

#### 步骤 2：克隆并编译

```bash
git clone https://github.com/suoten/jt-engine.git
cd jt-engine/jte

# 编译前端
cd web
npm install --registry=https://registry.npmmirror.com
npm run build
cd ..

# 编译后端
go build -ldflags="-s -w" -o bin/jte ./cmd/jte/
```

#### 步骤 3：配置环境变量

```bash
export JTE_API_JWT_SECRET_KID_2026_06=$(openssl rand -base64 48)
export JTE_AUTH_OFFLINE_UNBIND_SECRET=$(openssl rand -base64 48)
```

#### 步骤 4：启动

```bash
./bin/jte
```

后台运行：

```bash
nohup ./bin/jte > jte.log 2>&1 &
```

或使用 systemd（推荐生产环境）：

```bash
sudo tee /etc/systemd/system/jte.service << 'EOF'
[Unit]
Description=JTE Engine
After=network.target

[Service]
Type=simple
User=jte
WorkingDirectory=/opt/jte
ExecStart=/opt/jte/bin/jte
Environment=JTE_API_JWT_SECRET_KID_2026_06=你的JWT密钥
Environment=JTE_AUTH_OFFLINE_UNBIND_SECRET=你的离线解绑密钥
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable jte
sudo systemctl start jte
```

---

## 第二部分：存储配置

### 最小配置（开箱即用）

开源版默认使用 SQLite，零配置即可运行：

```yaml
# configs/jte.yaml（默认值，无需修改）
storage:
  type: "sqlite"
  dsn: "./data/jte.db"
```

适用于：测试、POC、≤100 台设备的小规模场景。

### 生产配置（TDengine + Redis + MinIO）

生产环境推荐存储分离架构（百亿级轨迹数据）：

#### 步骤 1：部署 TDengine

```bash
# Docker 部署 TDengine 3.x
docker run -d --name tdengine \
  -p 6030:6030 -p 6041:6041 \
  -v tdengine-data:/var/lib/taos \
  tdsengine/tdengine:3.3.x

# 验证
docker exec -it tdengine taos -s "SHOW DATABASES"
```

#### 步骤 2：部署 Redis

```bash
docker run -d --name redis \
  -p 6379:6379 \
  redis:7-alpine \
  redis-server --requirepass 你的Redis密码
```

#### 步骤 3：部署 MinIO

```bash
docker run -d --name minio \
  -p 9000:9000 -p 9001:9001 \
  -e MINIO_ROOT_USER=jte \
  -e MINIO_ROOT_PASSWORD=你的MinIO密码 \
  -v minio-data:/data \
  minio/minio server /data --console-address ":9001"
```

#### 步骤 4：修改 jte.yaml

```yaml
storage:
  type: "sqlite"
  dsn: "./data/jte.db"

  # 时序数据层（轨迹/报警/CAN等高频数据）
  time_series:
    driver: "tdengine"
    host: "127.0.0.1"
    port: 6030
    user: "root"
    password: ""  # 通过环境变量 JTE_STORAGE_TIME_SERIES_PASSWORD 配置
    database: "jte_ts"
    keep_days: 365
    batch_size: 1000
    ws_enabled: true          # 启用 WebSocket 连接（千万点/秒写入）
    ws_dsn: ""                 # 留空则根据 host/port 自动构造

  # 缓存层（在线状态/最新位置/会话）
  cache:
    driver: "redis"
    addr: "127.0.0.1:6379"
    password: "你的Redis密码"
    db: 0
    key_prefix: "jte:"
    latest_location_ttl: 300
    online_status_ttl: 120

  # 对象存储（归档轨迹/原始视频）
  object:
    driver: "minio"
    endpoint: "127.0.0.1:9000"
    access_key: "jte"
    secret_key: "你的MinIO密码"
    bucket: "jte-archive"
    archive_bucket: "jte-archive"
    video_bucket: "jte-video"

  # 归档任务（3年以上历史轨迹自动迁移到 MinIO）
  archive:
    enabled: true
    schedule_hour: 2          # 每天凌晨2点执行
    keep_days: 365
    batch_days: 1
    delete_delay_days: 7
```

设置密码环境变量：

```bash
export JTE_STORAGE_TIME_SERIES_PASSWORD=你的TDengine密码
```

---

## 第三部分：启动与验证

### 验证清单

| 检查项 | 命令 | 预期结果 |
|--------|------|----------|
| 进程存活 | `curl http://localhost:8080/health` | `{"status":"ok"}` |
| 就绪检查 | `curl http://localhost:8080/health/ready` | 200（所有依赖就绪） |
| 模块列表 | `curl http://localhost:8080/api/v1/system/modules` | 返回模块列表 |
| Prometheus 指标 | `curl http://localhost:8080/metrics` | Prometheus 格式文本 |
| 前端访问 | 浏览器 `http://IP:8080` | 登录页面 |
| 设备网关 | `telnet localhost 7611` | 连接成功 |

### 查看已加载模块

```bash
# 获取 JWT token
TOKEN=$(curl -s http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | jq -r '.data.token')

# 查询模块状态
curl -s http://localhost:8080/api/v1/system/modules \
  -H "Authorization: Bearer $TOKEN" | jq

# 预期返回（免费版只有 808/1078）：
# [
#   {"name":"jt808","enabled":true},
#   {"name":"jt1078","enabled":true},
#   {"name":"protocol_809","enabled":false},
#   {"name":"protocol_1045","enabled":false},
#   ...
# ]
```

---

## 第四部分：购买与激活模块

### 4.1 免费版能用什么

| 功能 | 免费版 | 说明 |
|------|--------|------|
| JT/T 808 协议 | ✅ | 终端注册/鉴权/位置/报警/指令 |
| JT/T 1078 视频 | ✅ | 实时视频/回放/PTZ |
| Web 管理后台 | ✅ | 设备管理/车辆管理/轨迹回放 |
| 设备数量 | ≤10 台 | 超过需购买授权 |
| JT/T 809 转发 | ❌ | 需购买 module-protocol-809 |
| JT/T 1045 ADAS | ❌ | 需购买 module-protocol-1045 |
| JT/T 905 出租车 | ❌ | 需购买 module-protocol-905 |
| JT/T 32960 新能源 | ❌ | 需购买 module-protocol-32960 |
| 归档功能 | ❌ | 需购买 professional 以上等级 |
| AI 智能分析 | ❌ | 需购买 module-ai |
| 集群部署 | ❌ | 需购买 enterprise 等级 |

### 4.2 到官网购买模块

1. 访问 JTE 官网（部署官网后替换为实际地址，如 `https://jte.dev`）
2. 注册账号 → 选择需要的授权等级或单独模块
3. 支付完成 → 系统自动生成授权码（License Key）
4. 授权码格式：`Base64(payload).Base64(signature)`（一长串字符）

**授权等级与价格参考：**

| 等级 | 车辆上限 | 包含功能 | 适用场景 |
|------|----------|----------|----------|
| Free | 10 | 808 + 1078 | 测试/POC |
| Standard | 10,000 | + 视频 | 小型车队 |
| Professional | 100,000 | + 归档 | 中型车队 |
| Enterprise | 1,000,000 | + AI + 集群 + SRTP | 大型车队/平台 |

也可单独购买模块（如只买 809 转发）。

### 4.3 在 JTE 中激活授权码

#### 通过 Web 界面激活

1. 登录 JTE 管理后台 `http://你的IP:8080`
2. 进入「系统设置」→「授权管理」
3. 粘贴授权码 → 点击「激活」
4. 激活成功后显示已授权模块列表和到期时间

#### 通过 API 激活

```bash
# 激活授权码
curl -X POST http://localhost:8080/api/v1/auth/activate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"code":"你的授权码"}'

# 预期返回
# {"code":0,"message":"License activated successfully"}
```

#### 查看授权状态

```bash
# 查看当前授权详情
curl -s http://localhost:8080/api/v1/auth/license/status \
  -H "Authorization: Bearer $TOKEN" | jq

# 预期返回：
# {
#   "machine_fingerprint": "a1b2c3d4...",
#   "licenses": [
#     {
#       "id": "LIC-2026-xxxx",
#       "modules": ["protocol_809", "protocol_1045", ...],
#       "expires_at": "2027-07-10T00:00:00Z",
#       "expired": false
#     }
#   ],
#   "active_modules": ["protocol_809", "protocol_1045", ...]
# }
```

#### 授权码绑定机制

- 每个授权码绑定到**当前服务器的机器指纹**（CPU + 主板 + 网卡哈希）
- **一台服务器只能绑定一个授权码**
- 换服务器需要先在官网「解绑」，或使用「离线解绑凭证」
- 授权码不可复制到其他服务器使用

### 4.4 下载并安装付费模块

激活授权码只是获得了**使用许可**，还需要下载模块文件（.so）放入引擎。

#### 步骤 1：下载模块

**通过官网下载：**

1. 登录官网 → 「我的订单」→ 找到已购买的订单
2. 点击「下载模块」→ 下载 `.so` 和 `.sig` 文件

**通过 API 下载（自动）：**

```bash
# 获取下载地址（需授权码 + 机器指纹）
curl -s https://jte.dev/api/v1/modules/download \
  -H "Authorization: Bearer $WEBSITE_TOKEN" \
  -d '{"module_name":"protocol_809","license_key":"你的授权码"}' | jq

# 返回:
# {"url":"https://jte.dev/downloads/protocol_809.so","sha256":"abc123..."}
```

#### 步骤 2：安装模块

```bash
# 将下载的 .so 和 .sig 文件放入 modules 目录
cp protocol_809.so protocol_809.so.sig /opt/jte/modules/

# Docker 方式：挂载了 modules 目录，直接复制即可
docker cp protocol_809.so jte:/app/modules/
docker cp protocol_809.so.sig jte:/app/modules/
```

**模块文件说明：**

| 文件 | 作用 |
|------|------|
| `protocol_809.so` | 模块二进制（Go plugin 格式） |
| `protocol_809.so.sig` | RSA-2048 签名（防篡改） |

> **⚠️ 两个文件缺一不可**。`jte.yaml` 中 `signature_verify: true` 要求每个 .so 必须有对应 .sig 文件，否则加载失败。

#### 步骤 3：重启引擎加载模块

```bash
# Docker
docker compose restart jte

# systemd
sudo systemctl restart jte

# 裸机
# 停止后重新启动
```

### 4.5 验证模块加载成功

```bash
# 查看模块列表
curl -s http://localhost:8080/api/v1/system/modules \
  -H "Authorization: Bearer $TOKEN" | jq

# 预期：protocol_809 的 enabled 变为 true
# [
#   {"name":"jt808","enabled":true},
#   {"name":"jt1078","enabled":true},
#   {"name":"protocol_809","enabled":true},   ← 变为 true
#   ...
# ]
```

查看日志确认加载：

```bash
docker compose logs jte | grep -i "module.*loaded"
# 预期: "module loaded" name=protocol_809
```

### 4.6 试用功能（无需购买）

部分模块支持 30 天免费试用：

```bash
# 启动 809 模块试用
curl -X POST http://localhost:8080/api/v1/auth/trial \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"module_name":"protocol_809"}'
```

试用限制：
- 仅 809 模块支持试用
- 每台服务器只能试用一次（机器指纹绑定）
- 试用到期后不可重新试用，需购买正式授权

---

## 第五部分：常见问题

### Q1: 启动报错 "JWT secret not configured"

**原因**：未设置 `JTE_API_JWT_SECRET_KID_2026_06` 环境变量。

**解决**：
```bash
export JTE_API_JWT_SECRET_KID_2026_06=$(openssl rand -base64 48)
```

开发环境可临时跳过：`export JTE_ALLOW_INSECURE_JWT=1`（生产环境禁止）。

### Q2: 模块加载报错 "signature file missing"

**原因**：`.so` 文件缺少对应的 `.sig` 签名文件。

**解决**：确保从官网下载了 `.so` 和 `.sig` 两个文件，都放入 `modules/` 目录。

### Q3: 激活报错 "license already bound to another machine"

**原因**：授权码已绑定到其他服务器。

**解决**：
1. 在旧服务器上执行解绑：`DELETE /api/v1/auth/license/:id`
2. 或在官网「我的授权」页面点击「解绑」
3. 如果旧服务器已丢失，使用「离线解绑凭证」功能

### Q4: 激活报错 "permanent license locked to major version X"

**原因**：永久授权绑定了购买时的主版本号，当前引擎版本更高。

**解决**：购买大版本升级（费用 = 永久授权原价 × 50%），联系商务。

### Q5: 模块加载后功能不可用

**排查步骤**：
```bash
# 1. 检查模块状态
curl -s http://localhost:8080/api/v1/system/modules -H "Authorization: Bearer $TOKEN" | jq

# 2. 检查授权状态
curl -s http://localhost:8080/api/v1/auth/license/status -H "Authorization: Bearer $TOKEN" | jq

# 3. 检查日志
docker compose logs jte | grep -i error

# 4. 健康检查
curl http://localhost:8080/health/ready
```

### Q6: 连续 7 天断网后模块全部停用

**原因**：安全机制——连续 7 天无法联网验证授权，自动停用所有付费模块（防止断网绕过吊销检查）。

**解决**：恢复网络后引擎自动联网验证，验证通过后模块自动恢复。

### Q7: Windows 上模块无法加载

**原因**：Go plugin（.so）仅支持 Linux。

**解决**：Windows/macOS 使用「进程模式」加载模块（模块编译为独立二进制，通过 gRPC 通信），或直接在 Linux 服务器上部署。

### Q8: 如何升级引擎版本

```bash
# 1. 备份数据
cp -r data/ data_backup/

# 2. 拉取新版本
git pull origin main
docker compose up -d --build

# 3. 验证
curl http://localhost:8080/health
```

永久授权注意：小版本（MINOR.PATCH）免费升级，大版本需付费。

### Q9: 生产环境 HTTPS 配置

```yaml
# jte.yaml
api:
  tls:
    enabled: true
    cert_file: "/opt/jte/certs/server.pem"
    key_file: "/opt/jte/certs/server.key"
    auto_renew: true           # Let's Encrypt 自动续期
    acme: true
    acme_domains:
      - "jte.yourdomain.com"
  require_tls: true            # 强制 HTTPS
```

### Q10: 如何对接真实设备

1. 在管理后台添加车辆和设备（绑定终端手机号）
2. 终端配置平台 IP 和端口 7611
3. 终端上线后会自动注册（0x0100）
4. 在「设备管理」页面查看在线状态
5. 使用「轨迹回放」查看历史轨迹

---

## 附录：完整模块清单

| 模块名 | 功能 | 授权等级 |
|--------|------|----------|
| jt808 | JT/T 808 基础协议 | Free |
| jt1078 | JT/T 1078 音视频 | Free |
| protocol_809 | JT/T 809 平台级联 | 单独购买 / Standard+ |
| protocol_1045 | JT/T 1045 ADAS/DSM | 单独购买 / Standard+ |
| protocol_905 | JT/T 905 出租车/网约车 | 单独购买 |
| protocol_1253 | JT/T 1253 货运 | 单独购买 |
| protocol_32960 | GB/T 32960 新能源 | 单独购买 |
| legacy | 地方协议（苏/粤/浙/川/陕/沪/京/鲁） | 单独购买 |
| crypto | 国密 SM2/SM3/SM4 | 单独购买 |
| cluster | 集群部署 | Enterprise |
| ai | AI 智能分析 | 单独购买 / Enterprise |
| ai_nlp | AI 自然语言（NL2SQL/报表） | 单独购买 / Enterprise |
| monitor | 监控告警 | Professional+ |
| adapter | 第三方系统适配 | 单独购买 |
| fleet | 车队管理增强 | Standard+ |
| storage | 存储增强 | Professional+ |
| tts | TTS 语音 | 单独购买 |
