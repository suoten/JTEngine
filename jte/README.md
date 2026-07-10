<p align="center">
  <img src="logo.svg" alt="JTE Logo" width="280" />
</p>

<h1 align="center">JTE - JT Engine</h1>

<p align="center">
  <strong>车联网部标协议智能引擎 — 高性能接入 · 智能分析 · 自然交互</strong>
</p>

<p align="center">
  <a href="#简介">简介</a> •
  <a href="#核心特性">核心特性</a> •
  <a href="#快速开始">快速开始</a> •
  <a href="#授权与付费模块">授权</a> •
  <a href="#技术栈">技术栈</a>
</p>

---

## 简介

JTE（JT Engine）是开源的 JT/T 部标协议引擎，解决道路运输车辆卫星定位系统协议接入与智能分析的痛点。

### 核心特性

- **全协议支持**：JT/T 808/809/905/1078/1045/1253 + GB/T 32960 + 地方标准
- **AI智能分析**：DeepSeek驱动的报警过滤、风险评分、异常检测
- **AI自然语言交互**："查一下今天超速的车辆"直接出结果
- **信创全栈**：国产数据库（达梦/金仓/高斯）+ 国产OS + 国密
- **模块化架构**：免费核心 + 付费加密模块，一个版本按需解锁

### 免费版功能

| 功能 | 说明 |
|------|------|
| JT/T 808-2019 | 终端通讯协议（注册/鉴权/心跳/位置/报警） |
| JT/T 809-2019 | 平台间数据交换（双向级联） |
| JT/T 1078-2022 | 视频通讯协议（RTP→FLV/HLS） |
| JT/T 1045-2018 | 主动安全报警（DSM/ADAS） |
| HTTP API | RESTful接口，Swagger文档 |
| WebSocket | 实时位置/报警推送 |
| 内存存储 | 20设备上限 |
| 只读仪表盘 | Vue3前端 |

## 快速开始

### 安装

```bash
# 从源码编译
go install github.com/jte-engine/jte/cmd/jte@latest

# 或使用Docker
docker-compose up -d
```

### 启动

```bash
jte serve --config configs/jte.yaml
```

### 验证

```bash
curl http://localhost:8080/api/v1/health
```

## 授权与付费模块

```bash
# 登录授权
jte auth login JTE-XXXX-XXXX-XXXX-XXXX

# 拉取付费模块
jte module pull

# 安装模块
jte module install

# 查看已安装模块
jte module list
```

### 付费模块

| 模块 | 功能 | 价格 |
|------|------|------|
| module-storage | 数据库持久化 + 国产DB + 无限设备 | 标准版+ |
| module-web | 完整Web管理后台 | 标准版+ |
| module-crypto | 国密SM2/SM3/SM4 | 标准版+ |
| module-adapter | 终端厂商适配层 | 标准版+ |
| module-cluster | 集群部署 | 专业版+ |
| module-monitor | 监控告警 | 专业版+ |
| module-regional | 地方标准（苏标/粤标等） | 专业版+ |
| module-legacy | 旧版本协议兼容 | 专业版+ |
| module-ai | AI智能分析 | 专业版+ |
| module-ai-nlp | AI自然语言交互 | 专业版+ |

## 技术栈

- Go 1.22+ / Gin / Zap / Viper
- Vue 3 + Vite + Element Plus
- ZLMediaKit (视频分发)
- DeepSeek API (AI推理)
- MySQL / PostgreSQL / 达梦 / 金仓 / TDengine

## 开源协议

AGPL-3.0 — 防止云厂商白嫖，企业商用必须购买授权

## 官网

https://jte.dev