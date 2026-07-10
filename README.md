<p align="center">
  <h1 align="center">🚗 JTE — 车联网部标协议智能引擎</h1>
</p>

<p align="center">
  <strong>开源版 · JT/T 808 + JT/T 1078 · AGPL-3.0</strong>
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#免费与付费功能">免费 vs 付费</a> ·
  <a href="#一键部署">一键部署</a> ·
  <a href="DEPLOYMENT.md">部署指南</a> ·
  <a href="CONFIG_CHECKLIST.md">配置清单</a>
</p>

---

## 简介

JTE（JT Engine）是一款车联网 JT/T 部标协议智能引擎，解决道路运输车辆卫星定位系统的**协议接入、数据存储、智能分析**三大痛点。

本仓库是 **JTE 开源版**，基于 AGPL-3.0 协议开源，包含核心引擎和 JT/T 808 + JT/T 1078 两大基础协议，可独立部署运行。809/905/1045/1253/32960/AI/集群等扩展协议和能力以付费模块形式提供，请到 [官网](#商业授权) 获取。

### 为什么选择 JTE？

| 痛点 | JTE 方案 |
|------|----------|
| 部标协议复杂，开发周期长 | 开箱即用，开源版含 808/1078，付费模块覆盖全协议 |
| 百万设备并发性能瓶颈 | 单机 10 万连接验证，TDengine 时序存储千万点/秒写入 |
| 等保 2.0 合规难 | 内置三权分立、国密 SM2/SM3/SM4、链式审计日志、HTTPS/WSS |
| 运维排障困难 | AI 智能报警过滤 + 自然语言查询（付费模块） |

---

## 免费与付费功能

### 🆓 开源版（本仓库，AGPL-3.0）

| 功能 | 说明 |
|------|------|
| **JT/T 808-2019** | 终端注册/鉴权/心跳/位置/报警/指令/区域/多媒体/分包重组/SeqNum 去重 |
| **JT/T 1078-2022** | 实时音视频/回放/下载/云台/RTP over TCP/UDP/双码流/SRTP |
| **核心引擎** | 网关/API/会话管理/模块加载/鉴权/配置热加载 |
| **存储** | SQLite/MySQL/TDengine/Redis/MinIO 四层分离 + 离线归档 |
| **安全** | 国密 SM2/SM3/SM4 + 等保 2.0 合规（审计/脱敏/三权分立/JWT/CSRF/SQL 注入防护） |
| **可观测性** | Prometheus 指标 + 健康检查 + OpenTelemetry 链路追踪 |
| **前端仪表盘** | Vue3 实时监控大屏/设备/车辆/报警/轨迹/视频/指令/报表/系统管理 |
| **License 框架** | 授权码激活/绑定/版本锁定/离线解绑（框架代码开源，付费模块需授权码） |

### 💎 付费模块（官网获取）

| 模块 | 价格 | 说明 |
|------|------|------|
| module-protocol-809 | ¥3,800/年 | JT/T 809-2019 平台级联（主从链路/指数退避/视频协商） |
| module-protocol-905 | ¥2,800/年 | JT/T 905-2014 出租车/网约车（电召调度/计价器/CAN） |
| module-protocol-1045 | ¥2,800/年 | JT/T 1045 主动安全（DSM/ADAS/盲区/胎压） |
| module-protocol-1253 | ¥2,800/年 | JT/T 1253-2019 JSON 版 809 |
| module-protocol-32960 | ¥2,800/年 | GB/T 32960 新能源汽车 |
| module-legacy | ¥3,800/年 + ¥5,000/省 | 旧版协议 + 地方标准（苏/粤/浙/川/陕/沪/京/鲁） |
| module-ai | ¥3,800/年 | AI 智能分析（报警过滤/风险评分/ONNX 推理） |
| module-ai-nlp | ¥2,800/年 | AI 自然语言（RAG/NL2SQL/AI 报表/协议调试） |
| module-cluster | ¥6,800/年 | 集群部署（蓝绿/滚动升级/多节点） |
| module-crypto | ¥3,800/年 | 国密加密增强（SM4-GCM/国密 SSL/TLCP） |
| module-security-audit | ¥2,800/年 | 等保 2.0 合规审计（6 项检查/异常检测） |
| module-monitor | ¥2,800/年 | 监控告警（短信/邮件/Webhook） |
| module-storage | ¥4,800~88,800/年 | 四层存储增强（TDengine ws/Stmt2/TMQ/Schemaless） |
| module-fleet | ¥4,800/年 | 车队运营管理 |
| module-tts | ¥1,800/年 | TTS 语音播报（808 0x8300） |
| module-loadtest | ¥1,800/年 | 压测工具（10 万连接） |
| module-adapter | ¥2,800/年 | 终端厂商适配 |

> 付费模块提供 30 天免费试用。获取方式：访问官网 → 注册 → 下载模块 → 放入 `jte/modules/` 目录 → 重启引擎。

---

## 快速开始

### 方式一：Docker 一键部署（推荐）

```bash
# 1. 克隆项目
git clone https://github.com/suoten/jt-engine.git
cd jt-engine

# 2. 一键启动（含 MySQL + TDengine + Redis + MinIO + JTE）
cd jte && docker-compose up -d

# 3. 验证服务
curl http://localhost:8080/api/v1/health
# → {"status":"ok","version":"3.0.0"}
```

打开浏览器访问 `http://localhost:5173` 即可看到 Web 仪表盘。

### 方式二：源码编译

```bash
# 前置条件：Go 1.22+、MySQL/TDengine/Redis/MinIO 已运行

# 1. 编译
cd jte && make build-binary

# 2. 编辑配置
cp configs/jte.yaml.example configs/jte.yaml
# 修改数据库密码、JWT 密钥等（参见 CONFIG_CHECKLIST.md）

# 3. 启动
./bin/jte serve --config configs/jte.yaml
```

> 📖 完整部署步骤请参阅 [DEPLOYMENT.md](DEPLOYMENT.md)
> ✅ 上线前请对照 [CONFIG_CHECKLIST.md](CONFIG_CHECKLIST.md) 逐项核对

---

## 一键部署

### Docker Compose（生产推荐）

```bash
cd jte && docker-compose -f docker-compose-prod.yml up -d
```

包含服务：
| 服务 | 端口 | 说明 |
|------|------|------|
| JTE 引擎 | 7611 (TCP) / 8080 (API) | 协议接入 + REST API |
| MySQL | 3306 | 关系数据存储 |
| TDengine | 6030 / 6041 | 时序数据存储 |
| Redis | 6379 | 缓存 |
| MinIO | 9000 / 9001 | 对象存储（轨迹归档） |
| Web 前端 | 5173 | Vue3 仪表盘 |

---

## 项目结构

```
jt-engine/                     # 开源仓库根
├── jte/                       # 核心引擎
│   ├── cmd/                   # 命令行入口（jte / loadtest）
│   ├── internal/              # 内部逻辑（API/网关/安全/审计/迁移/模块加载）
│   ├── pkg/                   # 公共库（JT808/JT1078 协议/存储/加密/注册）
│   ├── web/                   # Vue3 前端仪表盘
│   ├── configs/               # 配置文件
│   └── deploy/                # 部署配置（Dockerfile/K8s/docker-compose）
├── scripts/                   # 验收脚本
├── README.md                  # ← 你在这里
├── LICENSE                    # AGPL-3.0
├── DEPLOYMENT.md              # 生产部署指南
├── CONFIG_CHECKLIST.md        # 生产配置核对清单
└── CONTRIBUTING.md            # 贡献指南
```

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.22+ / Gin / Zap / Viper |
| 前端 | Vue 3 / Vite / Element Plus / Pinia |
| 时序库 | TDengine 3.8+（ws/unified 连接 + Stmt2 批量写入）|
| 关系库 | MySQL / 达梦 / 金仓 / 高斯 / SQLite |
| 缓存 | Redis |
| 对象存储 | MinIO |
| 视频 | ZLMediaKit / GB28181 SIP |
| 加密 | 国密 SM2/SM3/SM4（gmssl）|

---

## 模块开发

JTE 支持动态加载模块（Go Plugin / gRPC 进程模式）。开源版核心引擎已内置模块加载框架，你可以：

1. **自行开发免费模块**：实现 `ModuleInterface` 接口，编译为 `.so` 放入 `modules/` 目录
2. **购买付费模块**：从官网下载已签名的模块二进制，放入 `modules/` 目录，激活授权码即可使用

模块开发示例参考 `jte/pkg/registry/` 和 `jte/internal/module/loader.go`。

---

## 商业授权

### 为什么需要付费模块？

开源版（AGPL-3.0）适合小规模车队（≤100 台车）的基础定位和视频需求。当你需要以下能力时，需要购买付费模块：

- **平台级联**（809）：接入上级监管平台
- **出租车/网约车**（905）：电召调度、计价器
- **主动安全**（1045）：DSM/ADAS 驾驶监测
- **新能源汽车**（32960）：电池/充电监控
- **地方标准**：各省地方协议合规
- **AI 智能分析**：报警过滤、风险评分、自然语言查询
- **集群部署**：百万级设备高可用
- **等保 2.0 审计**：合规审计报告

### 授权方式

- **订阅制**：按年付费，含技术支持 + 版本升级
- **永久授权**：一次购买，锁定主版本号，大版本升级 5 折
- **试用**：所有付费模块提供 30 天免费试用

### 获取方式

1. 访问 JTE 官网注册账号
2. 选择需要的模块，在线下单
3. 支付后自动生成授权码（RSA 签名）
4. 下载模块二进制（`.so` + `.sig` 签名文件）
5. 放入 `jte/modules/` 目录，启动引擎时输入授权码激活

---

## 验收

部署完成后，运行验收脚本确认一切正常：

```bash
# Windows
powershell -ExecutionPolicy Bypass -File scripts/acceptance_e2e.ps1

# Linux / macOS
chmod +x scripts/acceptance_e2e.sh && ./scripts/acceptance_e2e.sh
```

---

## 开源协议

**AGPL-3.0** — 任何人可以自由使用、修改、分发，但网络服务使用也必须开源。

企业商用如需闭源，必须购买商业授权。详见 [LICENSE](LICENSE)。

> ⚠️ AGPL-3.0 的网络条款（第 13 条）要求：如果你通过网络提供基于本软件的服务，必须向所有用户提供完整源码。购买商业授权可免除该义务。

---

## 相关文档

- [DEPLOYMENT.md](DEPLOYMENT.md) — 生产部署完整指南
- [CONFIG_CHECKLIST.md](CONFIG_CHECKLIST.md) — 上线前配置核对清单
- [CONTRIBUTING.md](CONTRIBUTING.md) — 贡献指南
- [jte/configs/jte.yaml](jte/configs/jte.yaml) — 完整配置文件示例

---

## 联系我们

- **GitHub**: https://github.com/suoten/jt-engine
- **Gitee**: https://gitee.com/suoten/jt-engine
- **官网**: 获取付费模块、技术支持、商业授权
