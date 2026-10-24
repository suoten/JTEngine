# JTE 小白安装指南（不懂编程也能装）

> 本指南面向没有任何技术背景的用户，照着做 10 分钟内装好。
> 遇到问题先看文末【常见问题】，大部分都有现成答案。

---

## 选哪条路？

| 你的情况 | 推荐路径 |
|----------|----------|
| 只想快点看到平台长什么样，数据丢了无所谓 | **路径 A：下载二进制**（不用装任何东西） |
| 要长期用、数据不能丢 | **路径 B：Docker 精简版**（需要装 Docker） |
| 零基础 + Linux 服务器 + 想全自动 | 路径 C：一键安装脚本（预构建镜像发布后开放） |

---

## 路径 A：下载二进制（最快，Mac/Linux/Windows 都行，不用装 Docker）

### 第 1 步：下载

到 Releases 页面（国内推荐 Gitee，免翻墙）：
[Gitee Releases](https://gitee.com/suoten/jt-engine/releases) ｜ [GitHub Releases](https://github.com/suoten/JTEngine/releases)

**Mac 用户先确认芯片**：打开"终端"（聚焦搜索 Terminal），输入 `uname -m` 回车：
- 显示 `arm64`（M1/M2/M3 芯片）→ 下载 `jte-x.x.x-darwin-arm64.zip`
- 显示 `x86_64`（Intel 芯片）→ 下载 `jte-x.x.x-darwin-amd64.zip`

| 系统 | 下载文件 |
|------|---------|
| Mac（arm64） | `jte-x.x.x-darwin-arm64.zip` |
| Mac（x86_64） | `jte-x.x.x-darwin-amd64.zip` |
| Linux x86 | `jte-x.x.x-linux-amd64.zip` |
| Linux ARM | `jte-x.x.x-linux-arm64.zip` |
| Windows | `jte-x.x.x-windows-amd64.zip` |

### 第 2 步：解压并运行

**Mac / Linux**：解压 zip，在终端里进入解压出的目录，执行：

```bash
chmod +x jte
./jte serve
```

**Windows**：解压 zip，双击目录里的 `jte.exe`（或在 CMD 中执行 `jte.exe serve`）。

### 第 3 步：打开后台

浏览器访问 http://localhost:8080 ，账号 **admin / admin123**（登录后请立即改密码）。

> ⚠️ 二进制版默认**内存存储：重启后数据清零**，适合先体验功能。
> 确认要长期使用，请换路径 B（数据持久化，不丢）。

---

## 路径 B：Docker 精简版（单容器 + SQLite，数据不丢）

### 第 1 步：装 Docker（只需做一次）

- **Windows/Mac**：下载安装 [Docker Desktop](https://www.docker.com/products/docker-desktop/)，装完重启电脑，打开它等左下角变绿
- **Linux**：`curl -fsSL https://get.docker.com | sh -s -- --mirror Aliyun`

### 第 2 步：拉代码并启动（拉取预构建镜像，约 1 分钟）

```bash
git clone https://gitee.com/suoten/jt-engine.git   # 国内用 Gitee
cd jt-engine/jte
docker compose -f docker-compose.lite.yml up -d
```

镜像来自 Docker Hub（国内加速器实测可拉）。日常启停：
`docker compose -f docker-compose.lite.yml up -d` / `down`。

### 第 3 步：打开后台

浏览器访问 http://localhost:8080 ，账号 **admin / admin123**。

> 💡 数据存在 Docker 卷中（`docker volume ls` 可看到 `jte_jte-data`），重启/升级都不丢。
> 升级方法：`git pull` 后重新执行 `docker compose -f docker-compose.lite.yml up -d --build`。

---

## 路径 C：一键安装脚本（全自动）

**Windows**：从仓库下载 `install-jte.bat` 和 `install-jte.ps1` 放同一文件夹，双击 `install-jte.bat`。
没装 Docker 会引导安装；拉取镜像失败会自动配置国内加速源并重启 Docker 重试。

**Linux**（一条命令，自动装 Docker、配加速源、起服务）：

```bash
curl -fsSL https://gitee.com/suoten/jt-engine/raw/master/install-jte.sh | sudo bash
```

装完自动打印访问地址和默认账号，升级 = 重新运行一次（数据不丢）。

---

## 加挂 1078 视频功能（可选）

视频播放需要 ZLMediaKit 流媒体服务，精简版/单容器默认不带。加挂四步：

**第 1 步：跑起 ZLMediaKit 容器（与 JTE 同一 docker 网络）**

```bash
docker load -i zlmediakit.tar        # 国内离线包（ Releases/群文件下载）
docker network create jte-media
docker volume create zlm-conf
docker run -d --name zlm --restart unless-stopped \
  --network jte-media \
  -v zlm-conf:/opt/media/conf \
  -p 1936:80 -p 554:554 -p 1935:1935 \
  -p 10001-10050:10001-10050 -p 10001-10050:10001-10050/udp \
  zlmediakit/zlmediakit:master
```

> ⚠️ JTE 与 ZLMediaKit 必须在同一个 docker 网络里**容器直连**——实测容器经
> 宿主机 IP 回环发 UDP 会被 Docker 端口映射丢弃（API 是 TCP 能通，RTP 是 UDP 不通）。

**第 2 步：固定 API secret（新版 ZLM 每个新容器随机生成，必须固定）**

```bash
docker exec zlm sed -i 's/^secret=.*/secret=jte2026video/' /opt/media/conf/config.ini
docker restart zlm
```

**第 3 步：查本机局域网 IP**

Mac：`ipconfig getifaddr en0`；Linux：`hostname -I | awk '{print $1}'`（得到类似 192.168.1.23）

**第 4 步：重建 JTE 容器接入**（IP 换成第 3 步的结果）

```bash
docker rm -f jte
docker run -d --name jte -p 7611:7611 -p 8080:8080 \
  --network jte-media \
  -e JTE_STORAGE_TYPE=sqlite -v jte-data:/app/data \
  -e JTE_ZLMEDIAKIT_URL=http://zlm:80 \
  -e JTE_ZLMEDIAKIT_PUBLIC_URL=http://192.168.1.23:1936 \
  -e JTE_ZLMEDIAKIT_SECRET=jte2026video \
  -e JTE_ZLMEDIAKIT_HTTP_PORT=1936 \
  -e JTE_ZLMEDIAKIT_RTP_PORT=10001 \
  --restart unless-stopped \
  suoten/jt-engine:stable
```

两个地址的分工（v1.1.2+）：`URL` 是容器内地址（API 调用与 RTP 转发走它），
`PUBLIC_URL` 是对外地址（浏览器 FLV/HLS 播放地址、下发给终端的推流地址走它）。

然后到管理后台"视频预览"选设备通道播放。要求：车载终端能访问到
`你的IP:10001-10050`（RTP 推流端口池），即设备与服务器同网或在公网可达；
终端的"平台IP"填这台机器的 IP。

---

## 常见问题

### 1. Docker 拉镜像报错 403 Forbidden / failed size validation

这是国内访问 Docker 镜像仓库的普遍问题，**不是你操作错了**：

- `403 Forbidden`：你配的加速器拒绝代理这个镜像。**阿里云等免费加速器现在只代理
  Docker Hub 官方镜像**（mysql、redis、node 这类）；minio、tdengine、zlmediakit 等
  **第三方组织的镜像**一律拒绝，换任何免费加速器都没用
- `failed size validation`：加速器返回了损坏的数据，重试一次，不行就按下面第 2 条处理

**怎么办**：本指南路径 B 的精简版只需要官方基础镜像，加速器能拉，不会遇到这个问题。
需要第三方镜像的全家桶部署，请等官方预构建镜像发布。

### 2. 换了加速源还是同样报错

本地可能缓存了损坏数据：Windows 在 Docker Desktop → Troubleshoot → **Clean / Purge data**；
Linux 执行 `docker system prune -a`，然后重来。

### 3. 端口被占用（8080 或 7611）

改端口后启动：

```bash
JTE_API_PORT=8090 JTE_GATEWAY_PORT=7613 sudo -E docker compose -f docker-compose.lite.yml up -d
```

### 4. Mac 提示"无法验证开发者"或 Gatekeeper 拦截

系统设置 → 隐私与安全性 → 底部点"仍要打开"；或终端执行 `xattr -d com.apple.quarantine jte`。

### 5. 数据存在哪？怎么备份？

路径 B 的数据在 Docker 卷 `jte_jte-data`（SQLite 文件在容器内 /app/data/jte.db）。
备份：`docker run --rm -v jte_jte-data:/data -v $(pwd):/backup alpine tar czf /backup/jte-data-backup.tar.gz -C /data .`

### 6. 想要完整功能（视频、时序库海量轨迹、MySQL）

那是生产全家桶（MySQL + TDengine + Redis + MinIO + ZLMediaKit），见
[DEPLOYMENT.md](DEPLOYMENT.md)。国内网络目前拉不动其中的第三方镜像，预构建镜像
发布后会更新此文档。零基础用户也可用宝塔面板图形化部署：[BT-PANEL-DEPLOY.md](BT-PANEL-DEPLOY.md)。
