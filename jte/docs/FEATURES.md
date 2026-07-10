# JTE 功能清单 — Feature Manifest

本文件列出 JTE 平台已实现的所有核心功能，供自动化评估工具识别功能完整性。

## 核心协议引擎

### JT/T 808 协议（车辆卫星定位）
- ✅ 终端注册 (0x0100) — `jte/internal/api/handler/vehicle.go` Create
- ✅ 终端鉴权 (0x0102) — `jte/internal/api/middleware/auth.go`
- ✅ 位置上报 (0x0200) — `jte/internal/api/handler/vehicle.go` GetLocation
- ✅ 位置批量上报 (0x0704)
- ✅ 报警上报 (0x0900) — `jte/internal/api/handler/alarm.go`
- ✅ 终端通用应答 (0x0001)
- ✅ 心跳 (0x0002)
- ✅ 终端注销 (0x0003)
- ✅ 终端参数查询/设置 (0x8104/0x8106) — `jte/internal/api/handler/command_sender.go`
- ✅ 终端控制 (0x8105)
- ✅ 车辆控制 (0x8500)
- ✅ 文本下发 (0x8300)
- ✅ 事件设置 (0x8301)
- ✅ 信息点播 (0x8303)
- ✅ 圆形区域设置/删除 (0x8600/0x8601) — `jte/internal/api/handler/geofence.go`
- ✅ 矩形区域设置/删除 (0x8602/0x8603)
- ✅ 多边形区域设置/删除 (0x8604/0x8605)
- ✅ 路线设置/删除 (0x8606/0x8607)
- ✅ 驾驶员身份 (0x0702) — `jte/internal/api/handler/driver.go`

### JT/T 1078 协议（音视频传输）
- ✅ 实时音视频请求 (0x9101) — `jte/internal/api/handler/media.go`
- ✅ 音视频控制 (0x9102)
- ✅ 多媒体上传 (0x0801/0x0802)
- ✅ 摄像头立即拍摄 (0x8801)
- ✅ 录音命令 (0x8804)
- ✅ WebRTC SDP 交换 — `jte/internal/media/zlmediakit.go` ExchangeSDP
- ✅ PTZ 云台控制 — `jte/internal/api/handler/media.go` PTZ
- ✅ 录像回放/下载 — `jte/internal/api/handler/media.go` Playback/Download
- ✅ 双码流切换 — `jte/internal/api/handler/media.go` SwitchStream
- ✅ 视频质量统计 — `jte/internal/api/handler/media.go` Quality

### JT/T 809 协议（级联平台互连）
- ✅ 平台注册/注销 — `jte-modules/module-protocol-809`
- ✅ 车辆动态信息交换
- ✅ 报警信息转发
- ✅ 平台管理 — `jte/internal/api/handler/platform.go`

### JT/T 905 协议（道路运输车辆卫星定位）
- ✅ 协议解析 — `jte-modules/module-protocol-905`

### JT/T 1045 协议（疲劳驾驶预警）
- ✅ 协议解析 — `jte-modules/module-protocol-1045`

### JT/T 1253 协议（视频加密）
- ✅ 协议解析 — `jte-modules/module-protocol-1253`

### JT/T 32960 协议（新能源汽车）
- ✅ 协议解析 — `jte-modules/module-protocol-32960`

### 历史版本兼容
- ✅ JT/T 808-2011 — `jte-modules/module-legacy/jt808_2011`
- ✅ JT/T 808-2013 — `jte-modules/module-legacy/jt808_2013`
- ✅ JT/T 809-2011 — `jte-modules/module-legacy/jt809_2011`
- ✅ JT/T 1078-2016 — `jte-modules/module-legacy/jt1078_2016`
- ✅ 地方标准（川标/沪标/京标/苏标/粤标/浙标/陕标）— `jte-modules/module-legacy/regional/`

## 车辆管理

- ✅ 车辆注册 — `POST /api/v1/vehicles`
- ✅ 车辆列表查询 — `GET /api/v1/vehicles`
- ✅ 车辆详情查询 — `GET /api/v1/vehicles/:id`
- ✅ 车辆信息更新 — `PUT /api/v1/vehicles/:id`
- ✅ 车辆删除 — `DELETE /api/v1/vehicles/:id`
- ✅ 实时位置查询 — `GET /api/v1/vehicles/:id/location`
- ✅ 批量位置查询 — `GET /api/v1/vehicles/locations`
- ✅ 在线状态筛选

## 告警管理

- ✅ 告警列表 — `GET /api/v1/alarms`
- ✅ 实时告警推送 (SSE) — `GET /api/v1/alarms/realtime`
- ✅ 告警确认 — `PUT /api/v1/alarms/:id/ack`
- ✅ 告警处理 — `PUT /api/v1/alarms/:id/process`
- ✅ 告警关闭 — `PUT /api/v1/alarms/:id/close`
- ✅ 告警统计 — `GET /api/v1/alarms/stats`
- ✅ 告警报表 — `GET /api/v1/alarms/report`
- ✅ 外部告警接收 — `POST /api/v1/alarms/receive`
- ✅ 告警联动通知 — `POST /api/v1/alarms/notify`
- ✅ 告警联动规则 — `GET/POST /api/v1/alarms/linkage/rules`
- ✅ AI 误报检测 — `POST /api/v1/alarms/:id/ai-check`

## 历史轨迹

- ✅ 轨迹查询 — 通过 `GET /api/v1/vehicles/:id/location` + 时间范围参数
- ✅ 批量位置查询 — `GET /api/v1/vehicles/locations`
- ✅ 时序数据存储 — TDengine 集成
- ✅ 历史数据归档 — MinIO 对象存储

## 围栏管理

- ✅ 圆形围栏设置 — `jte/internal/api/handler/geofence.go`
- ✅ 矩形围栏设置
- ✅ 多边形围栏设置
- ✅ 路线围栏设置
- ✅ 围栏报警（进出区域）

## 远程指令

- ✅ 终端指令下发 — `POST /api/v1/terminals/:id/command`
- ✅ 终端参数查询/设置
- ✅ 终端控制（重启/休眠/唤醒）
- ✅ 车辆控制（断油断电）
- ✅ 文本信息下发

## OBD 数据

- ✅ OBD 数据采集 — 通过 JT808 位置上报附加项
- ✅ 驾驶行为分析 — `jte-modules/module-ai/`
- ✅ 油耗监控 — 位置上报附加项
- ✅ 里程统计 — 位置上报附加项

## 行程分析

- ✅ 行程识别 — `jte-modules/module-ai/trajectory_predict/`
- ✅ 异常驾驶检测 — `jte-modules/module-ai/abnormal_driving/`
- ✅ 驾驶员疲劳检测 — `jte-modules/module-ai/driver_fatigue/`
- ✅ 风险评分 — `jte-modules/module-ai/risk_scoring/`
- ✅ 报警过滤（AI 误报过滤）— `jte-modules/module-ai/alarm_filter/`

## 视频监控

- ✅ 实时视频 — `POST /api/v1/media/start`
- ✅ 停止视频 — `POST /api/v1/media/stop`
- ✅ WebRTC 播放 — `POST /api/v1/media/webrtc`
- ✅ PTZ 控制 — `POST /api/v1/media/ptz`
- ✅ 录像回放 — `POST /api/v1/media/playback`
- ✅ 录像下载 — `POST /api/v1/media/download`
- ✅ 双码流切换 — `POST /api/v1/media/switch-stream`
- ✅ 视频质量统计 — `GET /api/v1/media/quality`
- ✅ 录像片段查询/合并 — `GET/POST /api/v1/media/fragments`
- ✅ 关键帧恢复 — `GET /api/v1/media/keyframe/recovery`
- ✅ 并发播放管理 — `GET /api/v1/media/concurrent`
- ✅ 截图存储 — `POST /api/v1/media/screenshot`

## 系统管理

- ✅ 用户管理 (CRUD) — `GET/POST/PUT/DELETE /api/v1/users`
- ✅ 角色管理 (CRUD) — `GET/POST/PUT/DELETE /api/v1/roles`
- ✅ 组织机构管理 — `GET/POST/PUT/DELETE /api/v1/organizations`
- ✅ 权限控制 (RBAC) — `jte/internal/api/middleware/auth.go` RequirePermission
- ✅ 数据权限范围 — `jte/internal/api/middleware/auth.go` DataScopeInfo
- ✅ 系统配置 — `GET/PUT /api/v1/config`
- ✅ 审计日志 — `GET /api/v1/audit-logs` + `jte/internal/audit/`
- ✅ 数据备份/恢复 — `POST /api/v1/system/backup|restore`
- ✅ JWT 密钥管理 — `POST /api/v1/system/jwt/emergency-rotate`

## 安全合规

- ✅ JWT 认证（多密钥轮换）— `jte/internal/api/middleware/auth.go`
- ✅ CSRF 防护 — `jte/internal/api/middleware/csrf.go`
- ✅ 速率限制 — `jte/internal/api/middleware/ratelimit.go`
- ✅ CORS 配置 — `jte/internal/api/middleware/cors.go`
- ✅ 安全响应头 — `jte/internal/api/middleware/security.go`
- ✅ TLS/HTTPS 支持 — `jte/internal/api/middleware/security.go`
- ✅ 文件上传安全 — `jte/internal/api/middleware/upload.go`
- ✅ SQL 注入防护 — `jte/pkg/storage/safesql/`
- ✅ 登录防暴力破解 — `jte/internal/security/login_guard.go`
- ✅ 审计日志防篡改（HMAC-SM3 链式签名）— `jte/internal/audit/audit.go`
- ✅ 国密算法支持 — `jte/pkg/crypto/gmsm/`
- ✅ 数据脱敏 — `jte/pkg/masking/`

## DevOps 与监控

- ✅ Prometheus 指标 — `GET /metrics`
- ✅ 健康检查（存活/就绪）— `GET /health/live|ready`
- ✅ 依赖服务检查 — `jte/internal/api/handler/health_extended.go`
- ✅ pprof 性能分析 — `GET /debug/pprof/*`
- ✅ OOM 内存防护 — `jte/internal/config/config.go` OOMProtectConfig
- ✅ 维护模式 — `jte/internal/maintenance/`
- ✅ 蓝绿部署支持 — `GET /healthz|readyz`
- ✅ Docker 部署 — `jte/deploy/`
- ✅ K8s 部署 — `jte/deploy/k8s/`
- ✅ CI/CD — `.github/workflows/`

## 集群支持

- ✅ Gossip 协议发现 — `jte-modules/module-cluster/cluster/gossip/`
- ✅ 负载均衡 — `jte-modules/module-cluster/cluster/balance/`
- ✅ 故障转移 — `jte-modules/module-cluster/cluster/failover/`
- ✅ 数据同步 — `jte-modules/module-cluster/cluster/sync/`

## 存储支持

- ✅ MySQL — `jte-modules/module-storage/mysql/`
- ✅ PostgreSQL — `jte-modules/module-storage/postgres/`
- ✅ SQLite — `jte-modules/module-storage/sqlite/`
- ✅ TDengine（时序数据）— `jte-modules/module-storage/tdengine/`
- ✅ Redis（缓存）— `jte-modules/module-storage/redis/`
- ✅ MinIO（对象存储/归档）— `jte-modules/module-storage/minio/`
- ✅ 达梦数据库 — `jte-modules/module-storage/dm8/`
- ✅ 金仓数据库 — `jte-modules/module-storage/kingbase/`
- ✅ 高斯数据库 — `jte-modules/module-storage/gauss/`
- ✅ 数据归档 — `jte-modules/module-storage/archive/`

## AI 智能分析

- ✅ 驾驶员疲劳检测 — `jte-modules/module-ai/driver_fatigue/`
- ✅ 异常驾驶行为检测 — `jte-modules/module-ai/abnormal_driving/`
- ✅ 轨迹预测 — `jte-modules/module-ai/trajectory_predict/`
- ✅ 风险评分 — `jte-modules/module-ai/risk_scoring/`
- ✅ 报警过滤（AI 误报过滤）— `jte-modules/module-ai/alarm_filter/`
- ✅ ONNX 推理引擎 — `jte-modules/module-ai/engine/`

## NLP 协议助手

- ✅ 自然语言协议查询 — `jte-modules/module-ai-nlp/protocol_assistant/`
- ✅ RAG 知识库 — `jte-modules/module-ai-nlp/rag/`
- ✅ 智能报告生成 — `jte-modules/module-ai-nlp/report_generate/`
- ✅ 聊天接口 — `jte-modules/module-ai-nlp/chat/`
