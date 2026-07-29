# 宝塔面板部署 JTE 开源版完整教程

> 本教程面向**使用宝塔面板（BT Panel）的用户**，从零开始：安装环境 → 上传源码 → 构建前端 → 编译后端 → 宝塔管理进程 → 配置反向代理 + HTTPS → 激活授权码 → 上线。
>
> 全程照抄命令即可，**无需 Go 编程基础**，但需要会使用宝塔面板和 SSH 终端。

---

## 目录

- [架构说明](#架构说明)
- [第一部分：服务器与宝塔准备](#第一部分服务器与宝塔准备)
- [第二部分：上传源码到服务器](#第二部分上传源码到服务器)
- [第三部分：构建前端](#第三部分构建前端)
- [第四部分：编译后端二进制](#第四部分编译后端二进制)
- [第五部分：配置文件与环境变量](#第五部分配置文件与环境变量)
- [第六部分：在宝塔中管理 JTE 进程](#第六部分在宝塔中管理-jte-进程)
- [第七部分：配置反向代理与 HTTPS](#第七部分配置反向代理与-https)
- [第八部分：防火墙与端口放行](#第八部分防火墙与端口放行)
- [第九部分：启动验证](#第九部分启动验证)
- [第十部分：激活授权码与安装付费模块](#第十部分激活授权码与安装付费模块)
- [第十一部分：常见问题](#第十一部分常见问题)
- [附录：完整目录结构](#附录完整目录结构)

---

## 架构说明

```
                互联网用户（浏览器）
                       │
                       ▼
              宝塔 Nginx (443/HTTPS)
              ├── /  → 反向代理到 8080（前端 + API）
              └── SSL 证书（Let's Encrypt 自动续期）
                       │
                       ▼
        JTE 进程（宝塔 Go项目管理 / Supervisor 守护）
        ├── :8080  Web API + 前端静态文件（内嵌在二进制）
        └── :7611  设备网关（JT/T 808 终端 TCP 连接）
                       │
                       ▼
              SQLite 数据文件（/www/wwwroot/jte/data/jte.db）
              （生产环境可升级为 MySQL + TDengine + Redis + MinIO）
```

**关键点**：
- 前端 Vue3 在构建时通过 `//go:embed` 嵌入到 Go 二进制中，**不需要单独部署前端**
- 8080 端口同时提供前端页面和 API，通过宝塔 Nginx 反向代理对外
- 7611 端口是设备网关，**终端设备直连此端口**，必须对公网开放（或对设备 IP 段开放）

---

## 第一部分：服务器与宝塔准备

### 1.1 服务器要求

| 项目 | 最低要求 | 推荐 |
|------|---------|------|
| 操作系统 | Ubuntu 20.04 / CentOS 7.6 / Debian 10 | Ubuntu 22.04 LTS |
| CPU | 2 核 | 4 核+ |
| 内存 | 2 GB | 4 GB+（车辆数 >1000 建议 8GB+） |
| 磁盘 | 20 GB | 50 GB+ SSD |
| 带宽 | 3 Mbps | 5 Mbps+ |
| 公网 IP | 必须（设备需连接） | 固定 IP 最佳 |

### 1.2 安装宝塔面板

如果是全新服务器，SSH 登录后执行：

```bash
# Ubuntu/Debian
wget -O install.sh https://download.bt.cn/install/install-ubuntu_6.0.sh && bash install.sh ed8484bec

# CentOS
yum install -y wget && wget -O install.sh https://download.bt.cn/install/install_6.0.sh && sh install.sh ed8484bec
```

安装完成后会显示：
```
=================================================================
外网面板地址: http://你的IP:8888/xxxxxxxx
内网面板地址: http://内网IP:8888/xxxxxxxx
username: xxxxxxxx
password: xxxxxxxx
=================================================================
```

**⚠️ 务必保存以上信息**，然后用浏览器打开面板地址登录。

### 1.3 宝塔安装必要软件

登录宝塔面板后，会弹出"推荐安装套件"，选择 **Nginx 环境**：

#### 必装软件（软件商店中安装）

| 软件 | 版本 | 用途 | 安装方式 |
|------|------|------|---------|
| **Nginx** | 1.24+ | 反向代理 + HTTPS | 推荐套件中安装 |
| **Go 项目管理器** | 最新 | 管理 JTE 进程（推荐） | 软件商店搜索"Go项目" |
| **Supervisor 管理器** | 最新 | 进程守护（备选方案） | 软件商店搜索"Supervisor" |
| **Linux 工具箱** | 最新 | 系统管理 | 软件商店搜索"工具箱" |

#### 安装 Go 编译环境

宝塔的"Go 项目管理器"自带 Go 版本管理，但为了在 SSH 中使用 `go build`，建议系统级安装 Go：

```bash
# SSH 终端执行（下载 Go 1.22+）
cd /usr/local
wget https://go.dev/dl/go1.22.10.linux-amd64.tar.gz
tar -xzf go1.22.10.linux-amd64.tar.gz
rm go1.22.10.linux-amd64.tar.gz

# 配置环境变量
echo 'export GOROOT=/usr/local/go' >> /etc/profile
echo 'export GOPATH=/root/go' >> /etc/profile
echo 'export PATH=$PATH:$GOROOT/bin:$GOPATH/bin' >> /etc/profile
echo 'export GOPROXY=https://goproxy.cn,direct' >> /etc/profile
source /etc/profile

# 验证
go version
# 输出: go version go1.22.10 linux/amd64
```

#### 安装 Node.js（构建前端用）

```bash
# SSH 终端执行（安装 Node.js 20.x）
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y nodejs    # Ubuntu/Debian
# yum install -y nodejs      # CentOS

# 验证
node -v   # v20.x.x
npm -v    # 10.x.x

# 配置国内镜像加速
npm config set registry https://registry.npmmirror.com
```

---

## 第二部分：上传源码到服务器

### 2.1 创建项目目录

宝塔面板 → **文件** → 进入 `/www/wwwroot/` → 新建文件夹 `jte`。

最终项目路径：`/www/wwwroot/jte`

### 2.2 方式 A：Git 克隆（推荐）

宝塔面板 → **终端**（左侧菜单），执行：

```bash
cd /www/wwwroot
git clone https://github.com/suoten/jt-engine.git
mv jt-engine/jte jte
rm -rf jt-engine
cd jte
ls
# 应该看到 cmd/ configs/ web/ internal/ go.mod 等目录
```

### 2.2 方式 B：上传源码包

如果网络不好无法 git clone：

1. 在本地电脑从 https://github.com/suoten/jt-engine 下载 ZIP
2. 解压后取出 `jte/` 目录，重新打包成 `jte.zip`
3. 宝塔面板 → **文件** → 进入 `/www/wwwroot/` → **上传** `jte.zip`
4. 右键解压到当前目录

### 2.3 验证目录结构

SSH 终端执行：

```bash
ls /www/wwwroot/jte/
```

应包含以下关键内容：
```
cmd/         configs/    web/        internal/   pkg/
go.mod       go.sum      Makefile    README.md
```

---

## 第三部分：构建前端

前端是 Vue3 项目，必须先构建成静态文件（`web/dist/`），因为后端通过 `//go:embed` 把前端打包进二进制。

### 3.1 安装前端依赖

SSH 终端执行：

```bash
cd /www/wwwroot/jte/web
npm install
```

> ⏱️ 首次安装约 2-5 分钟，会下载几百个依赖包。如卡住可 Ctrl+C 后重试，或确认已配置 npmmirror 镜像。

### 3.2 构建前端

```bash
npm run build
```

构建成功后会看到：
```
✓ built in XXs
dist/                        # 生成 dist 目录
├── assets/                  # JS/CSS 文件
└── index.html               # 入口 HTML
```

### 3.3 验证 dist 目录

```bash
ls /www/wwwroot/jte/web/dist/
# 应看到 index.html 和 assets/ 目录
```

> **⚠️ 重要**：如果没有 `web/dist/` 目录，后端编译会失败（`//go:embed all:web` 找不到文件）。

---

## 第四部分：编译后端二进制

### 4.1 下载 Go 依赖

```bash
cd /www/wwwroot/jte
go mod download
```

> ⏱️ 首次约 1-3 分钟。如遇网络超时，确认已配置 `GOPROXY=https://goproxy.cn,direct`。

### 4.2 编译二进制

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o jte ./cmd/jte/
```

参数说明：
- `CGO_ENABLED=0`：纯 Go 编译，不依赖 C 库。**此时 SQLite 驱动不可用**（mattn/go-sqlite3 需 CGO），必须配置 `storage.type: memory` 或 `mysql`
- `-ldflags="-s -w"`：去除调试信息，减小二进制体积
- `-o jte`：输出文件名为 `jte`

### 4.3 验证编译结果

```bash
ls -lh /www/wwwroot/jte/jte
# 应看到约 30-50MB 的可执行文件

# 测试版本
./jte version
# 输出: JTE 1.0.0
```

> **💡 SQLite vs MySQL 选择**：
> - **不需要 CGO（推荐）**：用 `CGO_ENABLED=0` 编译 + `storage.type: mysql`（生产推荐）或 `memory`（测试）
> - **需要 SQLite**：必须安装 gcc 后用 `CGO_ENABLED=1` 编译：
>   ```bash
>   apt-get install -y gcc libc6-dev   # Ubuntu
>   # yum install -y gcc glibc-devel    # CentOS
>   CGO_ENABLED=1 go build -ldflags="-s -w" -o jte ./cmd/jte/
>   ```
>   并在 `jte.yaml` 配置 `storage.type: sqlite`

---

## 第五部分：配置文件与密钥

### 5.1 生成密钥（必须）

SSH 终端执行：

```bash
# 生成 JWT 密钥（≥32 字节，base64 编码）
openssl rand -base64 48
# 复制输出，形如: kJ3k2x9Q+aBcD...=

# 生成离线解绑密钥（≥32 字节）
openssl rand -base64 48
# 复制输出
```

**⚠️ 务必保存这两个密钥**，丢失会导致所有用户需重新登录、所有授权需重新激活。

### 5.2 编辑配置文件

宝塔面板 → **文件** → `/www/wwwroot/jte/configs/jte.yaml` → 右键编辑。

需要修改的关键配置（**JWT 密钥直接写入配置文件，最简单可靠**）：

```yaml
server:
  host: "0.0.0.0"
  port: 7611

gateway:
  tcp_port: 7611
  udp_port: 7612
  max_connections: 1000
  max_devices: 10000          # 免费版上限 10，激活后按授权等级提升

api:
  enabled: true
  port: 8080
  cors_origins:
    - "https://你的域名.com"   # 改成你的实际域名（HTTPS 上线后）
  jwt:
    # ✅ 推荐方式：密钥直接写入配置文件（最简单，小白友好）
    kms_source: "config"
    secrets:
      kid-2026-06: "这里填入你刚才 openssl rand 生成的 JWT 密钥"
    active_kid: "kid-2026-06"
    rotate_days: 90
  tls:
    enabled: false             # Nginx 层做 HTTPS，这里保持 false

auth:
  # 离线解绑密钥：填入你刚才 openssl rand 生成的第二个密钥
  offline_unbind_secret: "这里填入离线解绑密钥"

# 存储配置（三选一）

# 方案 A：内存存储（仅测试，重启丢数据）
storage:
  type: memory

# 方案 B：SQLite（单机小规模，需 CGO_ENABLED=1 编译）
# storage:
#   type: sqlite
#   dsn: "/www/wwwroot/jte/data/jte.db"

# 方案 C：MySQL（生产推荐，无需 CGO）
# storage:
#   type: mysql
#   dsn: "用户名:密码@tcp(127.0.0.1:3306)/jte?charset=utf8mb4&parseTime=true&loc=Local"

# 模块配置
modules:
  dir: "./modules"             # 付费模块 .so 文件目录
  signature_verify: true       # 验证模块签名（防止篡改）
  load_mode: "auto"            # 自动选择加载模式

# 官网地址（激活授权码时回连验证）
website:
  api_url: "https://你的官网域名.com"   # 改成你的官网地址
```

> 💡 **为什么用 `kms_source: "config"` 而不是 `env`？**
> 代码实际读取的环境变量格式是 `JTE_JWT_SECRET_<KID>`（见 [jwt_rotation.go:308](file:///e:/硕腾网络/JTE/jte-opensource/jte/internal/api/jwt_rotation.go#L308)），KID 原样作为后缀。但配置里 `active_kid: "kid-2026-06"` 含连字符 `-`，而 shell 变量名不允许连字符，导致 env 模式无法通过 `export` 设置。config 模式直接在 YAML 里写密钥，避开此坑。如必须用 env 模式，请把 `active_kid` 改成不含连字符的值（如 `kid202606`）。

### 5.3 创建必要目录

SSH 终端：

```bash
mkdir -p /www/wwwroot/jte/data
mkdir -p /www/wwwroot/jte/modules
mkdir -p /www/wwwroot/jte/logs
```

### 5.4 设置文件权限（安全加固）

```bash
# 配置文件含密钥，限制仅 root 可读
chmod 600 /www/wwwroot/jte/configs/jte.yaml
chown root:root /www/wwwroot/jte/configs/jte.yaml
```

---

## 第六部分：在宝塔中管理 JTE 进程

JTE 是常驻进程，需要进程守护（崩溃自动重启）。密钥已在第五部分写入配置文件，**无需配置环境变量**。宝塔有两种方式，**任选其一**。

### 方式 A：宝塔"Go 项目管理器"（推荐）

#### 步骤 1：安装 Go 项目管理器

宝塔面板 → **软件商店** → 搜索 `Go项目` → 安装"Go项目管理器"。

#### 步骤 2：添加项目

宝塔面板顶部菜单 → **Go项目** → **添加项目**，填写：

| 字段 | 值 |
|------|-----|
| 项目名称 | `jte` |
| 路径 | `/www/wwwroot/jte` |
| 执行文件 | `/www/wwwroot/jte/jte` |
| 启动参数 | （留空） |
| 项目端口 | `8080` |
| 启动用户 | `root` |

#### 步骤 3：启动项目

点击 **启动**，状态变为"运行中"即成功。

> ✅ 无需配置环境变量——密钥已在 `configs/jte.yaml` 中（第五部分 5.2 节）。

---

### 方式 B：宝塔"Supervisor 管理器"（通用备选）

如果宝塔没有 Go 项目管理器，用 Supervisor 更通用。

#### 步骤 1：安装 Supervisor

宝塔面板 → **软件商店** → 搜索 `Supervisor` → 安装"Supervisor管理器"。

#### 步骤 2：创建守护进程

宝塔面板 → **Supervisor管理器** → **添加守护进程**，填写：

| 字段 | 值 |
|------|-----|
| 名称 | `jte` |
| 启动用户 | `root` |
| 运行目录 | `/www/wwwroot/jte` |
| 启动命令 | `/www/wwwroot/jte/jte` |
| 进程数量 | `1` |

保存即可，Supervisor 会自动启动并守护进程。

#### 步骤 3：验证启动

```bash
supervisorctl status jte
# 输出: jte  RUNNING  pid 12345, uptime 0:00:10
```

#### 步骤 4：（可选）手动编辑配置文件精调

如需自定义日志路径，编辑 `/etc/supervisor/conf.d/jte.conf`：

```ini
[program:jte]
command=/www/wwwroot/jte/jte
directory=/www/wwwroot/jte
user=root
autostart=true
autorestart=true
startsecs=5
stopwaitsecs=30
stdout_logfile=/www/wwwroot/jte/logs/jte.out.log
stdout_logfile_maxbytes=50MB
stdout_logfile_backups=5
stderr_logfile=/www/wwwroot/jte/logs/jte.err.log
stderr_logfile_maxbytes=50MB
stderr_logfile_backups=5
```

修改后重载：

```bash
supervisorctl reread
supervisorctl update
supervisorctl restart jte
```

> ✅ 无需 `environment=` 行——密钥已在配置文件中。

---

## 第七部分：配置反向代理与 HTTPS

设备网关端口 7611 需要终端直连，Web 端口 8080 通过 Nginx 反向代理对外提供 HTTPS。

### 7.1 添加网站（绑定域名）

宝塔面板 → **网站** → **添加站点**：

| 字段 | 值 |
|------|-----|
| 域名 | `jte.你的域名.com`（需提前解析 A 记录到服务器 IP） |
| 根目录 | `/www/wwwroot/jte` |
| PHP版本 | 纯静态 |
| 数据库 | 不创建 |
| FTP | 不创建 |

### 7.2 设置反向代理

站点创建后 → 点击站点名 → **反向代理** → **添加反向代理**：

| 字段 | 值 |
|------|-----|
| 代理名称 | `jte-api` |
| 目标URL | `http://127.0.0.1:8080` |
| 发送域名 | `$host` |
| 代理目录 | `/`（默认全站代理） |

保存后，访问 `http://jte.你的域名.com` 应能看到 JTE 登录页。

### 7.3 申请 SSL 证书（Let's Encrypt 免费证书）

站点设置 → **SSL** → **Let's Encrypt**：

1. 勾选域名
2. 点击 **申请**
3. 申请成功后开启 **强制HTTPS**

> 💡 宝塔会自动续期 Let's Encrypt 证书（默认 30 天前续）。

### 7.4 手动 Nginx 配置（可选，进阶用户）

如果宝塔反向代理功能不够灵活，可手动编辑 Nginx 配置。

站点设置 → **配置文件**，在 `server` 块中添加：

```nginx
# 前端 + API 反向代理
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # WebSocket 支持（实时推送）
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";

    # 超时设置
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
}

# 设备网关端口不通过 Nginx，终端直连 7611
```

保存后重载 Nginx：

```bash
nginx -t && nginx -s reload
```

---

## 第八部分：防火墙与端口放行

JTE 需要放行两个端口，**两层防火墙都要放行**。

### 8.1 宝塔防火墙

宝塔面板 → **安全** → 放行端口：

| 端口 | 协议 | 用途 | 是否必须 |
|------|------|------|---------|
| 80 | TCP | HTTP（申请证书用） | ✅ |
| 443 | TCP | HTTPS（Web 访问） | ✅ |
| 7611 | TCP | 设备网关（终端连接） | ✅ 必须 |
| 7612 | UDP | 设备网关 UDP（可选） | 视终端配置 |
| 8080 | TCP | JTE 内部端口 | ❌ 不放行（仅 Nginx 内网访问） |
| 8888 | TCP | 宝塔面板 | ✅ |

### 8.2 云服务商安全组

如果是阿里云/腾讯云/华为云等，还需要在**云控制台安全组**中放行以上端口：

- 阿里云：ECS → 安全组 → 添加入方向规则
- 腾讯云：CVM → 安全组 → 入站规则
- 华为云：ECS → 安全组 → 入站规则

**⚠️ 7611 端口必须放行**，否则终端设备无法连接。

### 8.3 系统防火墙（如启用）

```bash
# Ubuntu (ufw)
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 7611/tcp
ufw allow 7612/udp
ufw reload

# CentOS (firewalld)
firewall-cmd --permanent --add-port=80/tcp
firewall-cmd --permanent --add-port=443/tcp
firewall-cmd --permanent --add-port=7611/tcp
firewall-cmd --permanent --add-port=7612/udp
firewall-cmd --reload
```

---

## 第九部分：启动验证

### 9.1 启动 JTE 进程

在宝塔 Go项目管理器或 Supervisor 中启动 `jte` 进程。

### 9.2 查看日志

宝塔 Go项目管理器 → jte 项目 → **日志**，或 SSH 查看：

```bash
# Supervisor 方式
tail -f /www/wwwroot/jte/logs/jte.out.log
tail -f /www/wwwroot/jte/logs/jte.err.log

# 应看到类似输出:
# JTE engine started
#   tcp_port: 7611
#   api_port: 8080
#   modules_loaded: 0
```

### 9.3 验证 API

SSH 或浏览器：

```bash
# 系统状态
curl http://127.0.0.1:8080/api/v1/system/status
# 应返回 JSON: {"code":0,"message":"ok","data":{"online_devices":0,...}}

# 模块列表（此时应只有免费功能）
curl http://127.0.0.1:8080/api/v1/system/modules
```

### 9.4 验证 Web 访问

浏览器打开 `https://jte.你的域名.com`，应看到 JTE 登录页面。

**默认管理员账号**（首次登录后请立即修改）：

```
用户名: admin
密码:   admin123
```

> ⚠️ **生产环境必须修改默认密码**：登录后 → 系统设置 → 修改密码。

### 9.5 验证设备网关

用 telnet 测试 7611 端口连通性：

```bash
# 在本地电脑执行
telnet 你的服务器IP 7611
# 或
nc -zv 你的服务器IP 7611
# 显示 connected 即正常
```

---

## 第十部分：激活授权码与安装付费模块

### 10.1 免费版能做什么

未激活任何授权时，JTE 免费版包含：
- ✅ JT/T 808 协议（终端接入、位置上报、报警）
- ✅ JT/T 1078 协议（音视频回放）
- ✅ Web API + Vue3 前端
- ✅ 内存存储 / SQLite 存储
- ❌ 车辆上限 **10 辆**
- ❌ 无 809/905/1045/32960 等扩展协议
- ❌ 无 AI 报警过滤、集群、监控等高级模块

### 10.2 到官网购买模块

1. 访问 JTE 官网 `https://你的官网域名.com`
2. 注册账号 → 选择套餐 → 微信/支付宝支付
3. 支付成功后，系统自动发送**授权码**到注册邮箱

授权码格式示例：`JTE-PRO-Y3K9-ABCD-EFGH`

### 10.3 在 JTE 中激活授权码

#### 方式 A：CLI 命令激活（推荐）

SSH 终端：

```bash
cd /www/wwwroot/jte
./jte auth activate JTE-PRO-Y3K9-ABCD-EFGH
# 输出: License activated successfully
```

激活后**重启 JTE 进程**使授权生效：

```bash
# Supervisor 方式
supervisorctl restart jte

# Go 项目管理器方式：在面板点击"重启"
```

#### 方式 B：API 激活

```bash
curl -X POST http://127.0.0.1:8080/api/v1/auth/activate \
  -H "Content-Type: application/json" \
  -d '{"license_key":"JTE-PRO-Y3K9-ABCD-EFGH"}'
```

### 10.4 下载并安装付费模块

激活授权码后，需要下载对应的付费模块文件（`.so` + `.sig`）。

#### 方式 A：CLI 自动下载（推荐）

```bash
cd /www/wwwroot/jte

# 查看已授权的模块
./jte auth status

# 下载并安装指定模块（会自动从官网下载 .so + .sig 到 modules/ 目录）
./jte module pull protocol_809
./jte module install protocol_809

# 批量下载所有已授权模块
./jte module pull protocol_809
./jte module pull protocol_905
./jte module pull protocol_1045
./jte module pull protocol_32960
./jte module pull storage
# ... 其他模块同理
```

#### 方式 B：手动上传模块文件

如果服务器无法访问官网，可在本地下载后上传：

1. 官网用户中心 → 我的订单 → 下载模块包（`.tar.gz`）
2. 解压得到 `protocol_809.so` 和 `protocol_809.so.sig`
3. 宝塔文件管理器上传到 `/www/wwwroot/jte/modules/`

最终 modules 目录结构：

```
/www/wwwroot/jte/modules/
├── protocol_809.so
├── protocol_809.so.sig
├── protocol_905.so
├── protocol_905.so.sig
├── storage.so
├── storage.so.sig
└── ...
```

### 10.5 重启并验证模块加载

```bash
# 重启 JTE
supervisorctl restart jte

# 验证模块加载
curl http://127.0.0.1:8080/api/v1/system/modules
```

应看到所有付费模块 `enabled: true`：

```json
{
  "code": 0,
  "data": [
    {"name": "jt808", "enabled": true},
    {"name": "jt1078", "enabled": true},
    {"name": "protocol_809", "enabled": true},    ← 已激活
    {"name": "protocol_905", "enabled": true},    ← 已激活
    {"name": "protocol_1045", "enabled": true},   ← 已激活
    ...
  ]
}
```

### 10.6 启用试用（可选）

部分模块支持 30 天免费试用（绑定机器指纹，每台服务器只能试用一次）：

```bash
cd /www/wwwroot/jte
./jte auth trial protocol_809
# 输出: Trial for protocol_809 started successfully (30 days)
```

---

## 第十一部分：常见问题

### Q1：启动报错 `jwt secret not configured` 或 `kms_source=env 但未找到 JTE_JWT_SECRET_* 环境变量`

**原因**：JWT 密钥未配置，或 `kms_source` 配置与密钥位置不匹配。

**解决**（按 `kms_source` 值排查）：

- **`kms_source: "config"`（推荐）**：检查 `jte.yaml` 中 `api.jwt.secrets` 下是否有 `kid-2026-06: "密钥值"`，且 `active_kid: "kid-2026-06"` 与之对应。密钥需 ≥32 字节。
- **`kms_source: "env"`**：需设置环境变量 `JTE_JWT_SECRET_kid-2026-06` 和 `JTE_JWT_ACTIVE_KID`。⚠️ 注意 kid 含连字符在 shell 中无法直接 `export`，建议改用 config 模式，或把 `active_kid` 改成不含连字符的值（如 `kid202606`，对应环境变量 `JTE_JWT_SECRET_kid202606`）。
- **临时测试**：设环境变量 `JTE_ALLOW_INSECURE_JWT=1` 跳过校验（**严禁生产使用**）。

### Q2：启动报错 `plugin open: not implemented`

**原因**：Go plugin 仅支持 Linux。Windows/macOS 无法加载 `.so` 模块。

**解决**：
- JTE 必须部署在 Linux 服务器上
- Windows 仅用于开发测试，付费模块功能不可用

### Q3：模块加载失败 `signature file missing`

**原因**：`signature_verify: true` 但模块缺少 `.sig` 签名文件。

**解决**：
- 确保每个 `.so` 文件都有对应的 `.so.sig` 文件
- 从官网下载的模块包应同时包含两个文件
- 临时测试可在 `jte.yaml` 中设 `signature_verify: false`（**严禁生产使用**）

### Q4：终端设备连不上（7611 端口不通）

**排查步骤**：
1. 确认 JTE 进程在运行：`supervisorctl status jte`
2. 确认端口监听：`ss -tlnp | grep 7611`
3. 宝塔防火墙放行 7611
4. 云安全组放行 7611
5. 系统防火墙放行 7611

### Q5：Web 页面能打开但 WebSocket 连不上

**原因**：Nginx 反向代理未配置 WebSocket 支持。

**解决**：在 Nginx 配置中添加（见 7.4 节）：
```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
```

### Q6：激活授权码报错 `machine fingerprint mismatch`

**原因**：授权码已绑定到其他服务器（机器指纹不匹配）。

**解决**：
- 在原服务器执行 `./jte auth unbind <license_id>` 解绑
- 或在官网用户中心申请解绑
- 离线环境用 `./jte auth unbind <license_id> --offline` 生成解绑凭证，发给客服处理

### Q7：内存存储重启后数据丢失

**原因**：`storage.type: memory` 是内存模式，重启即丢。

**解决**：
- 测试用：改 `storage.type: sqlite`（需 CGO 编译）
- 生产用：改 `storage.type: mysql` 或部署 TDengine（详见 DEPLOY-ACTIVATION-GUIDE.md）

### Q8：前端构建失败 `npm install` 卡住

**解决**：
```bash
# 清理缓存重试
cd /www/wwwroot/jte/web
rm -rf node_modules package-lock.json
npm cache clean --force
npm install --registry=https://registry.npmmirror.com
```

### Q9：编译报错 `package github.com/suoten/jt-engine/... not found`

**原因**：Go 模块下载失败（网络问题）。

**解决**：
```bash
export GOPROXY=https://goproxy.cn,direct
go mod download
```

### Q10：如何更新 JTE 版本

```bash
cd /www/wwwroot/jte
git pull origin main                    # 拉取最新代码
cd web && npm install && npm run build  # 重新构建前端
cd .. && go build -ldflags="-s -w" -o jte ./cmd/jte/   # 重新编译后端
supervisorctl restart jte               # 重启
```

> ⚠️ 更新前请备份 `data/` 目录和 `configs/jte.yaml`。

---

## 附录：完整目录结构

部署完成后，`/www/wwwroot/jte/` 目录结构：

```
/www/wwwroot/jte/
├── jte                          ← 编译后的二进制（可执行）
├── configs/
│   └── jte.yaml                 ← 主配置文件
├── web/
│   ├── dist/                    ← 前端构建产物（已嵌入二进制，可删）
│   ├── src/
│   └── package.json
├── cmd/                         ← Go 源码（编译后可删）
├── internal/                    ← Go 源码（编译后可删）
├── pkg/                         ← Go 源码（编译后可删）
├── go.mod
├── go.sum
│
├── data/                        ← 数据目录（SQLite/日志，勿删）
│   └── jte.db
├── modules/                     ← 付费模块目录
│   ├── protocol_809.so
│   ├── protocol_809.so.sig
│   ├── storage.so
│   └── storage.so.sig
├── logs/                        ← 运行日志
│   ├── jte.out.log
│   └── jte.err.log
├── config/                      ← 授权/审计数据（运行时生成）
│   ├── license.store            ← 授权存储（加密）
│   ├── audit.log                ← 审计日志
│   └── rbac.json                ← 权限配置
└── .env                         ← 环境变量（如用 Supervisor）
```

**最小化部署**（编译完成后，只需保留以下文件）：

```
/www/wwwroot/jte/
├── jte              ← 二进制
├── configs/
│   └── jte.yaml     ← 配置
├── data/            ← 数据
├── modules/         ← 付费模块
└── logs/            ← 日志
```

可删除 `web/`、`cmd/`、`internal/`、`pkg/`、`go.mod`、`go.sum` 等源码文件（已编译进二进制）。

---

## 快速命令速查

```bash
# === 构建 ===
cd /www/wwwroot/jte/web && npm install && npm run build
cd /www/wwwroot/jte && go build -ldflags="-s -w" -o jte ./cmd/jte/

# === 启动/停止/重启 ===
supervisorctl start jte
supervisorctl stop jte
supervisorctl restart jte
supervisorctl status jte

# === 日志 ===
tail -f /www/wwwroot/jte/logs/jte.out.log
tail -f /www/wwwroot/jte/logs/jte.err.log

# === 授权 ===
./jte auth activate <授权码>      # 激活
./jte auth status                 # 查看状态
./jte auth unbind <授权ID>        # 解绑
./jte auth trial <模块名>         # 试用

# === 模块 ===
./jte module list                 # 已安装模块
./jte module pull <模块名>        # 下载模块
./jte module install <模块名>     # 安装模块

# === 验证 ===
curl http://127.0.0.1:8080/api/v1/system/status
curl http://127.0.0.1:8080/api/v1/system/modules
```

---

## 部署检查清单

部署完成后，逐项确认：

- [ ] Go 1.22+ 已安装，`go version` 正常
- [ ] Node.js 20+ 已安装，`node -v` 正常
- [ ] 源码已上传到 `/www/wwwroot/jte/`
- [ ] 前端已构建，`web/dist/index.html` 存在
- [ ] 后端已编译，`jte` 二进制可执行
- [ ] `jte.yaml` 配置已修改（域名、存储、官网地址）
- [ ] JWT 密钥已生成并配置
- [ ] `data/`、`modules/`、`logs/` 目录已创建
- [ ] 宝塔 Go项目管理/Supervisor 已配置并启动
- [ ] 进程状态为 RUNNING
- [ ] 宝塔防火墙放行 80/443/7611
- [ ] 云安全组放行 80/443/7611
- [ ] 系统防火墙放行 80/443/7611
- [ ] 网站已添加，反向代理到 127.0.0.1:8080
- [ ] SSL 证书已申请，强制 HTTPS 已开启
- [ ] 浏览器访问 `https://jte.你的域名.com` 能看到登录页
- [ ] 默认密码 admin123 已修改
- [ ] `curl http://127.0.0.1:8080/api/v1/system/status` 返回正常
- [ ] telnet 服务器IP 7611 端口连通
- [ ] 授权码已激活，付费模块已下载安装
- [ ] `curl .../api/v1/system/modules` 显示所有模块 enabled

全部打勾 ✅ 即部署完成，可投入生产使用。
