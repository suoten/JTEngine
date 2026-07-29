# JTE 运维手册（Operations Manual）

## 文档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | v1.0 |
| 更新日期 | 2026-07-21 |
| 适用对象 | 运维工程师、SRE、值班人员 |
| 关联文档 | `DR_RUNBOOK.md`（灾难恢复）、`PERFORMANCE.md`（性能基准） |

---

## 目录

1. [日常巡检清单](#1-日常巡检清单)
2. [容量规划指南](#2-容量规划指南)
3. [版本升级流程](#3-版本升级流程)
4. [安全审计检查清单](#4-安全审计检查清单)

---

## 1. 日常巡检清单

### 1.1 每日巡检（09:00 执行）

| # | 检查项 | 命令 | 预期结果 | 异常处理 |
|---|--------|------|----------|----------|
| 1 | JTE 引擎健康状态 | `curl -sf http://localhost:8080/healthz` | `{"status":"ok"}` | 检查容器状态 `docker ps`，重启服务 |
| 2 | 官网健康状态 | `curl -sf http://localhost:8081/api/v1/health` | `{"status":"ok"}` | 检查 jte-website 容器 |
| 3 | 在线设备数 | `curl -s http://localhost:8080/metrics \| grep jte_online_devices` | ≥ 预期设备数 | 检查网关日志、网络连通性 |
| 4 | 活跃连接数 | `curl -s http://localhost:8080/metrics \| grep jte_active_connections` | < max_connections × 0.8 | 扩容或排查异常连接 |
| 5 | MySQL 连接数 | `mysql -h mysql -u root -p -e "SHOW STATUS LIKE 'Threads_connected'"` | < max_connections × 0.7 | 检查连接池、排查泄漏 |
| 6 | Redis 内存使用 | `redis-cli -h redis -a $REDIS_PASSWORD INFO memory \| grep used_memory_human` | < maxmemory × 0.8 | 清理缓存或扩容 |
| 7 | TDengine 写入延迟 | `curl -s http://localhost:8080/metrics \| grep tdengine_query_duration` | P99 < 100ms | 见 DR_RUNBOOK 场景 7 |
| 8 | 磁盘空间 | `df -h /app/data /var/lib/mysql /var/lib/taos` | 使用率 < 80% | 清理或扩容 |
| 9 | 容器状态 | `docker ps --format "table {{.Names}}\t{{.Status}}"` | 所有容器 Up | 重启异常容器 |
| 10 | 错误日志 | `docker logs jte 2>&1 \| grep -iE "error\|fatal\|panic" \| tail -20` | 无 ERROR/FATAL | 分析日志、定位问题 |

#### 每日巡检脚本

```bash
# 一键执行每日巡检
curl -sf http://localhost:8080/healthz && echo "✅ JTE healthy" || echo "❌ JTE unhealthy"
curl -sf http://localhost:8081/api/v1/health && echo "✅ Website healthy" || echo "❌ Website unhealthy"
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep -v "healthy"
docker logs jte 2>&1 | grep -ciE "error|fatal|panic" | xargs -I{} echo "错误日志数: {}"
df -h /app/data /var/lib/mysql /var/lib/taos 2>/dev/null | awk 'NR>1{print $1": "$5" used"}'
```

### 1.2 每周巡检（周一 09:00 执行）

| # | 检查项 | 命令 | 预期结果 | 异常处理 |
|---|--------|------|----------|----------|
| 1 | 备份完整性 | `bash /app/scripts/ops/backup_verify.sh --report` | 全部 PASS | 手动备份并排查失败项 |
| 2 | TLS 证书有效期 | `openssl x509 -in cert.pem -noout -enddate` | > 30 天 | 申请续期 |
| 3 | MySQL 慢查询 | `mysql -e "SELECT * FROM mysql.slow_log ORDER BY start_time DESC LIMIT 10"` | 无异常慢查询 | 优化查询/添加索引 |
| 4 | TDengine 数据增长 | `taos -s "SELECT COUNT(*) FROM jte_ts.gps_data WHERE ts >= NOW() - 7d"` | 符合预期增长曲线 | 调整保留策略/归档 |
| 5 | Prometheus 告警历史 | 检查 Grafana/Alertmanager 告警面板 | 无持续触发告警 | 分析根因并修复 |
| 6 | 模块状态 | `curl -s http://localhost:8080/metrics \| grep module_status` | 全部 = 1 (running) | 见 DR_RUNBOOK 场景 9 |
| 7 | 内存增长趋势 | Prometheus 查询 1 周内存曲线 | 无单调增长 | 见 DR_RUNBOOK 场景 10 |
| 8 | 日志文件大小 | `du -sh /app/logs/` | < 10GB | 配置日志轮转或清理 |
| 9 | Redis 持久化 | `redis-cli -a $PWD INFO persistence \| grep rdb_last_bgsave_status` | `ok` | 手动 BGSAVE 并排查 |
| 10 | 网络策略审计 | `kubectl get networkpolicy -n jte` | 策略生效 | 检查 YAML 配置 |

### 1.3 每月巡检（每月 1 日执行）

| # | 检查项 | 命令 | 预期结果 | 异常处理 |
|---|--------|------|----------|----------|
| 1 | 灾难恢复演练 | `bash /app/scripts/ops/dr_drill.sh --full` | RTO ≤ 4h, RPO ≤ 1h | 优化恢复流程 |
| 2 | 安全补丁检查 | `docker scan jte-engine:latest` (Trivy) | 无 CRITICAL 漏洞 | 更新基础镜像并重建 |
| 3 | 密码轮换 | 轮换 MySQL/Redis/TDengine/MinIO 密码 | 全部轮换成功 | 参见密码轮换流程 |
| 4 | 授权码续期 | `jte auth status` | 有效期 > 30 天 | 联系商务续期 |
| 5 | 容量评估 | 评估设备增长趋势 | 当前容量可支撑 3 个月 | 提前扩容 |
| 6 | 配置审计 | `diff <(cat configs/jte.yaml) <(git show HEAD:configs/jte.yaml)` | 变更可追溯 | 记录变更原因 |
| 7 | K8s 节点健康 | `kubectl describe nodes \| grep -A5 Conditions` | 全部 Ready | 排查节点问题 |
| 8 | 证书轮换检查 | 检查 K8s Secret/证书过期时间 | > 60 天 | 续期 |

---

## 2. 容量规划指南

### 2.1 设备数 → 资源需求映射表

基于压测数据（10000 设备 × 5s 上报频率），线性外推不同规模的资源需求。

| 设备规模 | Pod 副本数 | CPU/Pod | 内存/Pod | MySQL | TDengine | Redis | 磁盘/月 |
|----------|-----------|---------|---------|-------|----------|-------|--------|
| 1,000 | 1 | 2 核 | 2 GB | 1C/1GB | 1C/2GB | 256MB | 10 GB |
| 5,000 | 2 | 2 核 | 3 GB | 2C/2GB | 2C/4GB | 512MB | 50 GB |
| 10,000 | 3 | 2 核 | 3 GB | 2C/4GB | 2C/8GB | 1 GB | 100 GB |
| 30,000 | 5 | 4 核 | 4 GB | 4C/8GB | 4C/16GB | 2 GB | 300 GB |
| 50,000 | 8 | 4 核 | 6 GB | 4C/8GB | 4C/16GB×3 | 2 GB | 500 GB |
| 100,000 | 12 | 8 核 | 8 GB | 8C/16GB×2 | 4C/16GB×5 | 4 GB | 1 TB |
| 500,000 | 20 | 8 核 | 8 GB | 8C/16GB×4 | 4C/16GB×10 | 8 GB | 5 TB |

> **说明**：
> - CPU 按实际使用率 70% 估算（预留 30% 余量）
> - 内存包含 Go runtime（~500MB）、缓存、批量写入缓冲
> - TDengine 多节点时为单节点配置（总资源 × 节点数）
> - 磁盘按位置数据 365 天 + 报警 1095 天 + CAN 90 天保留策略估算
> - Redis 仅用于缓存，非持久化存储，内存需求与在线设备数成正比

### 2.2 存储容量估算公式

```
月数据量(GB) = 设备数 × 上报频率(条/小时) × 24 × 30 × 单条大小(KB) / 1024 / 1024

示例：
  10,000 设备 × 720 条/小时（5s 间隔）× 24 × 30 × 0.2KB / 1024 / 1024
  = 10,000 × 720 × 24 × 30 × 0.2 / 1024 / 1024
  ≈ 98.9 GB/月
```

### 2.3 扩容判断标准

| 指标 | 阈值 | 操作 |
|------|------|------|
| CPU 使用率 > 70%（持续 10 分钟） | HPA 自动扩容 | 无需手动干预 |
| 内存使用率 > 80%（持续 5 分钟） | HPA 自动扩容 | 无需手动干预 |
| 连接数 > max × 80% | 扩容 Pod | `kubectl scale deployment jte-blue -n jte --replicas=N` |
| TDengine 写入延迟 > 100ms | 增加 VGroup | 见 DR_RUNBOOK 场景 7 |
| MySQL 连接数 > max × 70% | 增加连接池或扩容 | 调整 `max_open_conns` |
| 磁盘使用率 > 80% | 扩容 PVC 或归档 | `kubectl edit pvc jte-data-blue` |

### 2.4 资源配额参考（K8s Resource Quota）

```yaml
# Namespace 级资源配额，防止单个 namespace 资源超分配
apiVersion: v1
kind: ResourceQuota
metadata:
  name: jte-quota
  namespace: jte
spec:
  hard:
    requests.cpu: "24"
    requests.memory: "96Gi"
    requests.ephemeral-storage: "100Gi"
    limits.cpu: "96"
    limits.memory: "192Gi"
    pods: "30"
    services: "10"
    configmaps: "20"
    secrets: "20"
```

---

## 3. 版本升级流程

### 3.1 升级前准备

```bash
# 1. 检查当前版本
jte version
docker images | grep jte-engine

# 2. 查看版本变更日志
# 访问 https://github.com/jte-engine/jte/releases 查看新版本变更

# 3. 执行预检查
bash /app/scripts/ops/preflight.sh --env-file /app/config/.env

# 4. 创建备份
bash /app/scripts/ops/config_backup.sh

# 5. 确认无活跃告警
# 检查 Prometheus Alertmanager 是否有未处理的告警

# 6. 确认低峰时段（设备在线数 < 日均 50%）
curl -s http://localhost:8080/metrics | grep jte_online_devices
```

### 3.2 蓝绿部署升级（K8s）

```bash
# ─── 阶段 1：准备 Green（新版本） ───

# 1. 更新 Green Deployment 镜像为新版本
kubectl set image deployment/jte-green jte=jte-engine:v2.1.0 -n jte

# 2. 扩容 Green 到与 Blue 相同的副本数
kubectl scale deployment/jte-green -n jte --replicas=3

# 3. 等待 Green 所有 Pod 就绪
kubectl rollout status deployment/jte-green -n jte --timeout=300s

# 4. 验证 Green 版本
kubectl exec -n jte deploy/jte-green -- jte version

# 5. 验证 Green 健康
kubectl exec -n jte deploy/jte-green -- curl -sf http://localhost:8080/healthz

# 6. 验证 Green 功能（通过临时 Service 或 port-forward）
kubectl port-forward -n jte deploy/jte-green 8090:8080
curl -sf http://localhost:8090/healthz
curl http://localhost:8090/api/v1/devices -H "Authorization: Bearer $TOKEN"
# 确认数据一致性（设备数、在线数与 Blue 一致）

# ─── 阶段 2：切换流量到 Green ───

# 7. 切换 Service selector 到 green
kubectl patch svc jte-svc -n jte -p '{"spec":{"selector":{"slot":"green"}}}'

# 8. 验证流量已切换（通过 Service 访问）
curl -sf http://jte-svc:8080/healthz

# 9. 观察 5 分钟，确认无异常
kubectl get pods -n jte -l app=jte -w

# ─── 阶段 3：下线 Blue（旧版本） ───

# 10. 缩容 Blue 到 0（保留 Deployment 用于回滚）
kubectl scale deployment/jte-blue -n jte --replicas=0

# 11. 确认 Green 独立服务正常（观察 30 分钟）
# 如果 30 分钟内无异常，升级成功；否则回滚
```

### 3.3 回滚步骤

```bash
# ─── 紧急回滚：切回 Blue（旧版本） ───

# 1. 切换 Service selector 回 blue
kubectl patch svc jte-svc -n jte -p '{"spec":{"selector":{"slot":"blue"}}}'

# 2. 扩容 Blue 恢复副本
kubectl scale deployment/jte-blue -n jte --replicas=3

# 3. 等待 Blue 就绪
kubectl rollout status deployment/jte-blue -n jte --timeout=300s

# 4. 验证 Blue 健康
curl -sf http://jte-svc:8080/healthz

# 5. 缩容 Green 到 0
kubectl scale deployment/jte-green -n jte --replicas=0

# 6. 记录回滚原因并通知团队
```

### 3.4 Docker Compose 升级

```bash
# 1. 拉取新版本镜像
docker-compose pull jte

# 2. 执行预检查
bash scripts/ops/preflight.sh --env-file .env

# 3. 备份配置
bash scripts/ops/config_backup.sh

# 4. 停止 JTE（依赖服务保持运行）
docker-compose stop jte

# 5. 更新镜像 tag
# 编辑 docker-compose.yml: image: jte-engine:v2.1.0

# 6. 启动新版本
docker-compose up -d jte

# 7. 验证
curl -sf http://localhost:8080/healthz
docker logs jte 2>&1 | grep -i "version\|started"

# 8. 如需回滚
docker-compose stop jte
# 编辑 docker-compose.yml: image: jte-engine:v2.0.0
docker-compose up -d jte
```

### 3.5 数据库迁移（版本升级时）

```bash
# 1. 执行迁移（先 dry-run）
jte migrate --src-driver sqlite3 --src-dsn ./data/jte.db \
  --tgt-driver mysql --tgt-dsn "$JTE_STORAGE_DSN" --dry-run

# 2. 确认无冲突后执行迁移
jte migrate --src-driver sqlite3 --src-dsn ./data/jte.db \
  --tgt-driver mysql --tgt-dsn "$JTE_STORAGE_DSN" --verify

# 3. 或使用迁移工具
jte-migrate --source sqlite3:data/jte.db --target "mysql:$JTE_STORAGE_DSN" --verify
```

---

## 4. 安全审计检查清单

### 4.1 等保 2.0 对应项

基于《信息安全技术 网络安全等级保护基本要求》（GB/T 22239-2019）三级要求。

#### 4.1.1 安全物理环境（8.x）

| 检查项 | 等保要求 | JTE 实现 | 检查方法 | 状态 |
|--------|----------|----------|----------|------|
| 物理访问控制 | 机房出入口配置电子门禁 | IDC/K8s 节点物理安全由云厂商保障 | 检查云厂商合规认证 | □ |
| 防火/防水/防雷 | 机房配备消防设施 | 同上 | 检查 IDC 消防系统 | □ |
| 温湿度控制 | 机房配备精密空调 | 同上 | 检查机房环境监控 | □ |

#### 4.1.2 安全通信网络（8.1.2）

| 检查项 | 等保要求 | JTE 实现 | 检查方法 | 状态 |
|--------|----------|----------|----------|------|
| 网络架构 | 划分不同的网络区域 | K8s NetworkPolicy 隔离 | `kubectl get networkpolicy -n jte` | □ |
| 通信传输 | 通信过程中完整性、机密性 | TLS/HTTPS 加密 | `grep tls configs/jte.yaml`; 检查证书 | □ |
| 可信接入 | 边界设备接入认证 | API JWT 鉴权 + 设备鉴权码 | `curl -H "Authorization: Bearer..." ` | □ |
| 边界防护 | 边界安全防护设备 | Ingress + NetworkPolicy | `kubectl get ingress -n jte` | □ |

#### 4.1.3 安全区域边界（8.1.3）

| 检查项 | 等保要求 | JTE 实现 | 检查方法 | 状态 |
|--------|----------|----------|----------|------|
| 访问控制 | 边界设备访问控制策略 | API CORS 白名单 + RBAC | `grep cors configs/jte.yaml`; 检查 RBAC | □ |
| 入侵防范 | 检测/阻断网络攻击 | 登录守卫（5 次失败锁定 15 分钟） | 检查 `security.NewLoginGuard` 日志 | □ |
| 恶意代码防范 | 检测/清除恶意代码 | 模块签名验证 | `grep signature_verify configs/jte.yaml` | □ |
| 安全审计 | 边界审计日志 | 审计日志（HMAC-SM3 防篡改） | 检查 `/app/config/audit.log` | □ |

#### 4.1.4 安全计算环境（8.1.4）

| 检查项 | 等保要求 | JTE 实现 | 检查方法 | 状态 |
|--------|----------|----------|----------|------|
| 身份鉴别 | 双因素/强口令 | JWT + 设备鉴权码 + 密码 ≥16 字符 | `bash scripts/ops/preflight.sh` | □ |
| 访问控制 | 最小权限原则 | RBAC 角色权限管理 | `jte system roles list` | □ |
| 安全审计 | 安全审计日志 | 审计日志记录 + SM3 链式防篡改 | `tail -100 /app/config/audit.log` | □ |
| 入侵防范 | 系统漏洞修补 | 定期 Trivy 扫描 + 镜像更新 | `docker scan jte-engine:latest` | □ |
| 恶意代码防范 | 程序可信执行 | 模块签名验证（RSA-2048） | `ls /app/modules/*.so.sig` | □ |
| 数据完整性 | 数据传输/存储完整性 | TLS 传输 + SM4-GCM 加密存储 | `grep crypto.enabled configs/jte.yaml` | □ |
| 数据保密性 | 敏感数据加密 | SM4-GCM 加密（手机号/身份/车牌） | 检查 `DataCipher` 日志 | □ |
| 数据备份恢复 | 定期备份 + 恢复演练 | 每日备份 + 每季度 DR 演练 | `bash scripts/ops/backup_verify.sh` | □ |
| 剩余信息保护 | 敏感数据清除 | 数据归档 + TTL 清理 | 检查 TDengine KEEP 参数 | □ |
| 个人信息保护 | 最小化收集/脱敏 | 手机号脱敏显示 + 加密存储 | 检查 API 响应中手机号格式 | □ |

#### 4.1.5 安全管理中心（8.1.5）

| 检查项 | 等保要求 | JTE 实现 | 检查方法 | 状态 |
|--------|----------|----------|----------|------|
| 集中管控 | 统一安全管控 | Prometheus + Grafana 统一监控 | 检查 Grafana 仪表盘 | □ |
| 集中审计 | 统一审计日志 | 审计日志 HMAC-SM3 链式 | 检查审计日志格式 | □ |

### 4.2 密码轮换流程

```bash
# === MySQL 密码轮换 ===
# 1. 生成新密码
NEW_MYSQL_PWD=$(openssl rand -base64 24)
# 2. 更新 MySQL 密码
mysql -h mysql -u root -p"$OLD_MYSQL_PWD" -e "ALTER USER 'jte'@'%' IDENTIFIED BY '$NEW_MYSQL_PWD';"
# 3. 更新环境变量/Secret
kubectl edit secret jte-secrets -n jte  # K8s
# 或编辑 .env 文件
# 4. 重启 JTE
docker-compose restart jte  # Docker
kubectl rollout restart deployment/jte-blue -n jte  # K8s

# === Redis 密码轮换 ===
# 1. 生成新密码
NEW_REDIS_PWD=$(openssl rand -base64 24)
# 2. 更新 Redis 配置
redis-cli -h redis -a "$OLD_REDIS_PWD" CONFIG SET requirepass "$NEW_REDIS_PWD"
# 3. 更新环境变量并重启 JTE

# === TDengine 密码轮换 ===
# 1. 生成新密码
NEW_TD_PWD=$(openssl rand -base64 24)
# 2. 更新 TDengine 密码
taos -s "ALTER USER root PASS '$NEW_TD_PWD'"
# 3. 更新环境变量并重启 JTE

# === JWT 密钥轮换 ===
# 1. 生成新密钥
NEW_JWT_SECRET=$(openssl rand -hex 32)
# 2. 通过 API 轮换（热加载，不需要重启）
curl -X POST http://localhost:8080/api/v1/system/jwt/rotate \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d "{\"secret\": \"$NEW_JWT_SECRET\"}"
# 旧 kid 保留 7 天供验签，7 天后自动清理
```

### 4.3 安全扫描检查清单

| # | 扫描项 | 工具/命令 | 频率 | 处理 |
|---|--------|----------|------|------|
| 1 | 容器镜像漏洞 | `trivy image jte-engine:latest` | 每月 | CRITICAL 立即修复 |
| 2 | Go 代码安全扫描 | `gosec ./...` | 每次 PR | 所有 HIGH 修复 |
| 3 | 依赖漏洞 | `govulncheck ./...` | 每月 | 升级有漏洞的依赖 |
| 4 | K8s 安全态势 | `kube-bench run` | 每季度 | 修复不合规项 |
| 5 | 密码/密钥泄漏 | `git-secrets --scan` | 每次 PR | 移除泄漏并轮换密钥 |
| 6 | 配置安全 | `bash scripts/ops/preflight.sh` | 每次部署 | 修复所有 FAIL |
| 7 | 网络暴露面 | `nmap -sV jte-svc` | 每季度 | 关闭非必要端口 |
| 8 | 日志安全 | 检查日志中无明文密码 | 每月 | 修复日志脱敏 |

### 4.4 审计日志验证

```bash
# 验证审计日志完整性（HMAC-SM3 链式防篡改）
# 1. 检查审计日志存在且非空
ls -la /app/config/audit.log
wc -l /app/config/audit.log

# 2. 检查审计日志格式（每行应包含 HMAC 签名）
head -3 /app/config/audit.log | python3 -c "
import sys, json
for line in sys.stdin:
    entry = json.loads(line.strip())
    print(f'  {entry.get(\"timestamp\",\"\")} | {entry.get(\"action\",\"\")} | {entry.get(\"user\",\"\")} | hmac={entry.get(\"hmac\",\"\")[:16]}...')
"

# 3. 验证 HMAC 链（需要与 JTE 同环境执行）
# 如果配置了 SM4 国密，HMAC 密钥来自 SM4 主密钥的 SM3 摘要
```

---

## 附录 A：常用运维命令速查

```bash
# 服务管理
docker-compose ps                          # 查看所有服务状态
docker-compose logs -f jte                 # 跟踪 JTE 日志
docker-compose restart jte                 # 重启 JTE
docker-compose stop jte                    # 停止 JTE

# K8s 管理
kubectl get pods -n jte -o wide            # 查看 Pod 分布
kubectl describe pod -n jte -l app=jte    # 查看 Pod 详情
kubectl logs -n jte deploy/jte-blue -f     # 跟踪日志
kubectl exec -n jte deploy/jte-blue -- sh  # 进入容器

# 监控查询
curl -s http://localhost:8080/metrics | grep -E "online_devices|active_conn"
curl -s http://localhost:8080/metrics | grep -E "storage_write|tdengine"

# 备份恢复
bash scripts/ops/backup_verify.sh          # 验证备份
bash scripts/ops/restore.sh 20260721       # 恢复数据
bash scripts/ops/preflight.sh              # 部署前检查

# 模块管理
jte module list                            # 列出已安装模块
jte module pull <name> <version>           # 拉取模块
jte module install <name>                  # 安装模块
jte auth status                            # 查看授权状态
```

## 附录 B：紧急联系 escalation matrix

| 严重级别 | 响应时间 | 升级路径 |
|----------|----------|----------|
| P0（系统不可用） | 5 分钟 | 值班 → 运维负责人 → CTO |
| P1（核心功能受损） | 15 分钟 | 值班 → 运维负责人 |
| P2（部分功能异常） | 1 小时 | 值班 → 后端工程师 |
| P3（优化/咨询） | 4 小时 | 值班记录 → 工单系统 |
