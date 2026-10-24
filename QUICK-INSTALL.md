# JTE 小白安装指南（不懂编程也能装）

> 本指南面向没有任何技术背景的用户。全部照着做，10 分钟内能装好。
> 遇到问题先看文末【常见问题】，大部分都有现成答案。

---

## 你需要准备什么

| 事项 | 说明 |
|------|------|
| 一台电脑/服务器 | Windows 10 以上，或 Linux（Ubuntu/Debian/CentOS/麒麟等） |
| 内存 4GB 以上 | 精简版只需跑 1 个容器，要求很低 |
| 能上网 | 安装过程需要下载约 1GB 的软件 |

---

## Windows 安装（3 步）

### 第 1 步：安装 Docker Desktop（只需做一次）

1. 打开官网下载页：https://www.docker.com/products/docker-desktop/
2. 下载 **Docker Desktop for Windows**，双击安装（一路默认下一步）
3. 安装完**重启电脑**，然后打开桌面上的 **Docker Desktop** 图标
4. 等左下角变成绿色 **Running** 就绪（首次约 1 分钟）

> 安装过程中如果提示"需要启用 WSL 2"，按它的提示点确定并重启即可。

### 第 2 步：下载安装脚本（2 个文件）

到 Gitee 仓库（国内速度快）下载两个文件，放在**同一个文件夹**里：

- 仓库地址：https://gitee.com/suoten/jt-engine
- 点网页上的文件名 → 右上角【原始数据】→ 右键另存为

需要下载的 2 个文件：

| 文件 | 用途 |
|------|------|
| `install-jte.bat` | 双击这个 |
| `install-jte.ps1` | 幕后干活的（必须和 bat 放同一文件夹） |

### 第 3 步：双击 `install-jte.bat`

剩下的全自动：拉取镜像（国内会自动配加速源）→ 启动 → 浏览器自动打开管理后台。

登录默认账号：**admin / admin123**（登录后请立即改密码）。

装好了。车载终端把"平台地址"填成 `你的电脑IP:7611`（TCP）即可接入。

---

## Linux 服务器安装（1 条命令）

```bash
curl -fsSL https://gitee.com/suoten/jt-engine/raw/master/install-jte.sh | sudo bash
```

装完会自动打印管理后台地址和默认账号。
别忘了在防火墙/云安全组放行 **7611（TCP）** 和 **8080（TCP）** 端口。

---

## 装完之后

| 你想做 | 命令 / 操作 |
|--------|------------|
| 打开管理后台 | 浏览器访问 `http://服务器IP:8080` |
| 停止 | `docker stop jte` |
| 再启动 | `docker start jte` |
| 看运行日志 | `docker logs -f jte` |
| 升级新版本 | Windows：重新双击 `install-jte.bat`；Linux：重新执行上面那条命令 |
| 卸载 | `docker rm -f jte`（数据在 `jte-data` 卷里，想彻底清掉再执行 `docker volume rm jte-data jte-spool`） |

> 数据保存在 Docker 卷 `jte-data` 中，升级、重启都不会丢。

---

## 常见问题

### 1. 拉取镜像报错 `failed size validation` / 一直卡在拉取

这是国内访问 Docker 镜像仓库不通导致的（截图里 minio/zlmediakit 那种红叉就是它）。
安装脚本遇到这种情况会**自动换加速源重试**。如果手动处理：

1. 注册阿里云（免费），打开 https://cr.console.aliyun.com/cn-hangzhou/instances/mirrors ，复制你的**专属加速器地址**（形如 `https://xxxxxx.mirror.aliyuncs.com`）
2. Windows：Docker Desktop → Settings → Docker Engine，把地址填进去：
   ```json
   {
     "registry-mirrors": ["https://xxxxxx.mirror.aliyuncs.com"]
   }
   ```
   点 **Apply & Restart**；
   Linux：编辑 `/etc/docker/daemon.json` 写入同样内容，然后 `systemctl restart docker`
3. 重新双击安装脚本 / 重新执行安装命令

也可以在运行脚本前指定加速源：

```bash
# Linux 示例
JTE_MIRROR=https://xxxxxx.mirror.aliyuncs.com sudo -E bash install-jte.sh
```

### 2. 换了加速源还是报 `failed size validation`

多半是本地缓存坏了。Windows：Docker Desktop → Troubleshoot → **Clean / Purge data**（会删掉已有镜像和容器）；Linux：`docker system prune -a`。然后重新安装。

### 3. 端口被占用

- 8080 被占用：管理后台起不来。改端口：Windows 安装时在 PowerShell 里运行
  `powershell -File install-jte.ps1 -ApiPort 8090`；Linux：`JTE_API_PORT=8090 sudo -E bash install-jte.sh`
- 7611 被占用：终端接入端口，同样可用 `-GatewayPort` / `JTE_GATEWAY_PORT` 修改

### 4. Windows 提示"无法运行脚本"

直接双击 `install-jte.bat`（不要单独双击 ps1）。bat 已自动处理运行权限。

### 5. 设备不多，精简版够用吗？

精简版用 SQLite 单容器，几百台设备规模没有问题。数据量大（万级设备、海量轨迹）时
再换全家桶部署，见仓库内 [DEPLOYMENT.md](DEPLOYMENT.md)（MySQL + TDengine + Redis + MinIO + 视频）。

### 6. 想要纯图形界面（不用命令行）

用宝塔面板：见 [BT-PANEL-DEPLOY.md](BT-PANEL-DEPLOY.md) 截图级教程。

---

## 两种部署形态对比

| | 精简版（本指南/一键脚本） | 全家桶（docker-compose.yml） |
|--|--|--|
| 容器数量 | 1 个 | 6 个 |
| 依赖 | 无（SQLite 内嵌） | MySQL + TDengine + Redis + MinIO + ZLMediaKit |
| 适合 | 体验、演示、小型项目 | 生产、大量设备、长期运营 |
| 数据库 | SQLite（文件） | MySQL + TDengine |
| 升级路径 | 直接换全家桶，数据可导出迁移 | — |
