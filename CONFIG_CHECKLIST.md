# JTE 生产配置核对清单

上线前请逐项核对以下配置，确保生产环境安全、合规、高性能。

> 标记说明：🔴 必改（不改无法启动或有安全风险）| 🟡 安全项（等保 2.0 要求）| 🟢 性能项（按规模调整）

---

## 🔴 必改项（不改无法启动或严重安全风险）

### JWT 密钥

- [ ] `api.jwt.kms_source` 设为 `"env"`（禁止配置文件明文存储密钥）
- [ ] 环境变量 `JTE_JWT_SECRET_<KID>` 已设置（≥32 字节强随机）
- [ ] 生成命令：`openssl rand -base64 48`
- [ ] `api.jwt.active_kid` 指向已设置的 kid
- [ ] 开发环境如需跳过校验：`JTE_ALLOW_INSECURE_JWT=1`（生产**禁止**）

```yaml
api:
  jwt:
    kms_source: "env"          # 从环境变量加载
    secrets: {}                 # 留空，从 env 加载
    active_kid: "kid-2026-07"  # 对应 JTE_JWT_SECRET_KID_2026_07
    rotate_days: 90             # 90 天轮换
```

### 数据库密码

- [ ] MySQL/达梦 密码已修改（非默认密码）
- [ ] TDengine root 密码已修改
- [ ] Redis 密码已设置
- [ ] MinIO access key/secret 已修改
- [ ] `storage.dsn` 中密码已替换
- [ ] `storage.time_series.ws_dsn` 中密码已替换

### SM4 加密密钥

- [ ] 环境变量 `JTE_SM4_KEY` 已设置（16/24/32 字节）
- [ ] 敏感数据（手机号/身份证/车牌）使用 SM4-GCM 加密存储
- [ ] 密文前缀为 `enc:v1:`

### 证书

- [ ] `api.tls.enabled` = `true`（生产必须 HTTPS）
- [ ] `api.tls.cert_file` / `key_file` 指向有效证书
- [ ] `api.tls.min_version` = `"TLS1.2"`（禁用 TLS 1.0/1.1）
- [ ] `api.require_tls` = `true`（HTTP 返回 426）
- [ ] 证书自动续期已配置（Let's Encrypt ACME 或 cron）

---

## 🟡 安全项（等保 2.0 三级要求）

### 传输安全

- [ ] 所有 API 强制 HTTPS（`api.require_tls: true`）
- [ ] WebSocket 使用 WSS（TLS 启用后自动升级）
- [ ] HSTS 头仅在 HTTPS 下设置（中间件已实现）
- [ ] CORS 白名单已配置具体域名（**禁止** `["*"]`）
- [ ] `api.security.conn_limit_per_ip` = `100`（防 Slowloris）
- [ ] `api.security.body_limit_bytes` = `10485760`（10MB，防大请求 OOM）

### 身份认证

- [ ] 登录鉴权绑定手机号 + 设备指纹
- [ ] 登录失败 5 次锁定 15 分钟
- [ ] 多 IP 登录告警已启用
- [ ] 异地登录告警已启用
- [ ] 非常用设备登录告警已启用
- [ ] Token 刷新仅在 refresh token 过期时跳转登录
- [ ] 多标签页 token 同步已启用（localStorage 事件）

### 三权分立

- [ ] 系统管理员（日常运维）
- [ ] 安全管理员（安全策略/密钥/证书）
- [ ] 审计管理员（审计日志查看，仅读）
- [ ] 三个角色权限互不交叉

### 数据安全

- [ ] 敏感数据脱敏：手机号 `138****8000`
- [ ] 敏感数据脱敏：身份证 `110101********1234`
- [ ] 敏感数据脱敏：车牌 `京A***45`
- [ ] 密码存储使用 SM3 哈希（10000 次迭代 + 随机盐，格式 `sm3$salt$iter$hash`）
- [ ] 国密 SM2/SM3/SM4 已启用（gmssl 或 tjfoc/gmsm）
- [ ] 国密 SSL/TLS 通信已启用

### 审计日志

- [ ] 审计日志记录所有管理操作（who/when/what/result）
- [ ] HMAC-SM3 链式防篡改已启用
- [ ] 审计日志文件权限 `0640`
- [ ] 审计日志留存期 ≥180 天
- [ ] 审计日志独立存储，仅审计管理员可查看
- [ ] 审计日志含 UserAgent/SessionID/Before/After/Category/ResultCode

### 注入防护

- [ ] SQL 注入防护（safesql 包 + OrderBy 白名单校验）
- [ ] XSS 防护（前端 escapeHtml/sanitizeHtml + 后端输出过滤）
- [ ] CSRF 防护（Double-submit cookie + SameSite=Strict）
- [ ] 文件上传安全（扩展名白名单 + MIME 白名单 + 魔数校验 + 病毒扫描）

---

## 🟢 性能项（按设备规模调整）

### 网关

- [ ] `gateway.max_connections` 按设备数设置（1 万设备 → 10000+）
- [ ] `gateway.max_devices` 按规模设置
- [ ] `gateway.oom_protect.enabled` = `true`（内存熔断）
- [ ] `gateway.oom_protect.warn_mb` / `critical_mb` / `fatal_mb` 按内存设置

### 时序数据库（TDengine）

- [ ] `storage.time_series.ws_enabled` = `true`（ws/unified 连接，千万点/秒）
- [ ] `storage.time_series.ws_dsn` 配置正确（含密码）
- [ ] `storage.time_series.batch_size` = `1000`（批量写入）
- [ ] `storage.time_series.buffer_size` = `10000`（异步缓冲）
- [ ] `storage.time_series.vgroups` 按规模设置（1 万设备 → 10，10 万 → 100+）
- [ ] `storage.time_series.replica` = `3`（生产高可用）
- [ ] `storage.time_series.wp_enabled` = `true`（Worker Pool 并行写入，>1 万点/秒）

### 归档

- [ ] `storage.archive.enabled` = `true`（默认启用）
- [ ] `storage.archive.schedule_hour` = `2`（凌晨 2 点执行）
- [ ] `storage.archive.keep_days` 按需设置（与 TDengine KEEP 对齐）
- [ ] `storage.archive.delete_delay_days` = `7`（延迟删除确保 fallback 窗口）
- [ ] `storage.archive.alert.webhook_url` 已配置（失败告警）

### 缓存与连接池

- [ ] Redis 连接池大小已配置
- [ ] MySQL 连接池 `max_open_conns` / `max_idle_conns` 已设置
- [ ] RTP 连接池空闲超时 5 分钟自动关闭

---

## 协议配置核对

### JT/T 808

- [ ] 鉴权码与终端手机号（0x0100 注册）绑定校验
- [ ] 同一鉴权码多 IP/多会话 → 告警 + 拒绝
- [ ] 单设备仅 1 个活跃会话
- [ ] 非分包、非 0x0001 终端通用应答消息 SeqNum 去重
- [ ] 分包重组超时 60s

### JT/T 809

- [ ] 主链路(0x1001/0x1005)和从链路(0x1002/0x1006)独立管理重连
- [ ] 断线重连指数退避（1s→2s→4s→8s→16s→32s→60s 上限）
- [ ] 连续失败 10 次触发告警
- [ ] 重连成功后按 SN 顺序补发缓存数据

### JT/T 1078

- [ ] RTP over TCP 按 RFC 4571 封装（2 字节长度前缀）
- [ ] UDP 不通时自动 fallback 到 TCP
- [ ] SRTP IV 折叠 ROC（RFC 3711 §4.1.1）
- [ ] 关键帧请求通过 POST /media/keyframe（0x9203 Command=4）

### JT/T 905

- [ ] 0x0200 报警标志位完整解析（16 位，bit9-15 出租车特有）
- [ ] 报警三级分类（Emergency/General/Info）+ AI 过滤
- [ ] 出租车状态机 7 态（空车/载客/电召/包车/停运/预约/未知）
- [ ] 电召派单 haversine 球面距离 + 抢答机制
- [ ] 统一 CommandSender905 接口（Send/SendAndWait/SendWithCallback）

---

## 上线检查命令

```bash
# 1. 配置校验（启动时自动校验，失败会拒绝启动）
./bin/jte serve --config configs/jte.yaml --dry-run

# 2. 端口确认
netstat -tlnp | grep -E '7611|8080|6030|6041|3306|6379|9000'

# 3. 健康检查
curl -k https://localhost:8080/api/v1/health

# 4. Prometheus 指标
curl -k https://localhost:8080/metrics | grep jte_

# 5. 等保合规检查（需 module-security-audit）
curl -k https://localhost:8080/api/v1/security/check

# 6. 审计链完整性
curl -k https://localhost:8080/api/v1/security/report

# 7. 验收脚本
./scripts/acceptance_e2e.sh
```

---

## 常见配置错误

| 错误 | 后果 | 正确做法 |
|------|------|----------|
| JWT 密钥写在配置文件 | 密钥泄露风险 | 用 `kms_source: env` 从环境变量加载 |
| CORS 配置 `["*"]` | 任意网站可跨域携带凭证 | 配置具体域名白名单 |
| `require_tls: false` | 等保不合规 | 设为 `true` + 启用 TLS |
| `ws_enabled: false` | 写入性能下降 10 倍+ | 启用 ws/unified 连接 |
| `archive.enabled: false` | TDengine 数据膨胀 | 默认 `true` 自动归档 |
| 审计文件权限 0644 | 审计日志可被篡改 | 设为 `0640` |
| 密码明文存储 | 等保严重违规 | SM3 哈希 10000 次迭代 + 盐 |
