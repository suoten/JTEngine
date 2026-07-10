# JTE 灾难恢复演练手册（DR Runbook）

## 文档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | v1.0 |
| 更新日期 | 2026-07-02 |
| 演练频率 | 每季度一次（1/4/7/10 月） |
| 演练负责人 | 运维工程师 |
| 适用场景 | 全量灾难恢复、数据损坏、机房故障切换 |

---

## 1. 恢复目标

| 指标 | 目标 | 说明 |
|------|------|------|
| **RPO**（数据丢失容忍） | ≤ 1 小时 | MySQL binlog 增量每小时一次 |
| **RTO**（恢复时间目标） | ≤ 4 小时 | 从启动恢复到业务恢复 |
| **数据完整性** | 100% | 恢复后数据校验通过 |
| **业务可用性** | ≥ 99.9% | 恢复后 24 小时内无二次故障 |

---

## 2. 备份策略概览

| 服务 | 备份方式 | 频率 | 保留期 | RPO | 脚本 |
|------|----------|------|--------|-----|------|
| MySQL | 全量 + binlog 增量 | 每日全量 / 每小时增量 | 全量 30d / 增量 7d | 1h | `mysql_backup.sh` |
| TDengine | taosdump + 数据快照 | 每日全量 | 30d | 0 | `tdengine_backup.sh` |
| Redis | RDB + AOF | 每日 RDB / 每 2h AOF | 7d | 1s | `redis_backup.sh` |
| MinIO | 跨区域复制 | 实时 | 持久 | 0 | `minio_replication.sh` |
| 配置文件 | tar.gz 全量 | 每日 + 变更触发 | 90d | 24h | `config_backup.sh` |

---

## 3. 灾难恢复流程

### 阶段 0：评估与决策（≤ 15 分钟）

```bash
# 1. 确认灾难范围
docker ps -a                          # 检查容器状态
kubectl get pods -A                   # 检查 Pod 状态
kubectl get nodes                     # 检查节点状态

# 2. 确认备份可用性
ls -la /data/backups/mysql/full/      # MySQL 备份
ls -la /data/backups/tdengine/        # TDengine 备份
ls -la /data/backups/redis/           # Redis 备份
ls -la /data/backups/config/          # 配置备份

# 3. 确定恢复时间点
# 选择最近的可用全量备份 + 对应增量
RECOVERY_DATE=$(date -d "yesterday" +%Y%m%d)
echo "恢复目标日期: $RECOVERY_DATE"
```

**决策检查清单：**
- [ ] 确认灾难类型（数据损坏 / 硬件故障 / 误操作 / 机房故障）
- [ ] 确认影响范围（单服务 / 多服务 / 全部）
- [ ] 确认备份完整性（`verify_backups.sh`）
- [ ] 通知业务方预计恢复时间
- [ ] 决定是否切换到灾备机房

### 阶段 1：停止服务（≤ 5 分钟）

```bash
# Docker 环境
docker-compose stop jte jte-website

# K8s 环境
kubectl scale deployment jte-blue jte-green -n jte --replicas=0
kubectl scale deployment jte-website -n jte-website --replicas=0
```

### 阶段 2：恢复数据（≤ 2 小时）

#### 方式 A：一键恢复（推荐）

```bash
# 预演恢复（不实际执行，检查流程）
bash /app/scripts/ops/one-click-restore.sh $RECOVERY_DATE --dry-run

# 执行全量恢复
bash /app/scripts/ops/one-click-restore.sh $RECOVERY_DATE
```

#### 方式 B：分步恢复

```bash
# 1. 恢复配置文件
bash /app/scripts/ops/config_backup.sh restore $RECOVERY_DATE

# 2. 恢复 MySQL（全量 + binlog 重放到指定位点）
bash /app/scripts/ops/mysql_backup.sh restore $RECOVERY_DATE

# 3. 恢复 Redis
bash /app/scripts/ops/redis_backup.sh restore $RECOVERY_DATE

# 4. 恢复 TDengine
bash /app/scripts/ops/tdengine_backup.sh restore $RECOVERY_DATE

# 5. 恢复 MinIO（从副本）
bash /app/scripts/ops/minio_replication.sh failback
```

### 阶段 3：验证数据完整性（≤ 30 分钟）

```bash
# 1. 运行备份校验脚本
bash /app/scripts/ops/verify_backups.sh

# 2. 验证 MySQL 数据
mysql -h mysql -u root -p -e "
  SELECT COUNT(*) AS total_users FROM jte.users;
  SELECT COUNT(*) AS total_devices FROM jte.devices;
  SELECT COUNT(*) AS total_sessions FROM jte.sessions;
"

# 3. 验证 TDengine 数据
taos -s "SELECT COUNT(*) FROM jte_ts.gps_data WHERE ts >= NOW() - 7d;"

# 4. 验证 Redis 数据
redis-cli -h redis INFO keyspace

# 5. 验证 MinIO 数据
mc ls jte-backup/jte-archive/ --recursive | wc -l
```

### 阶段 4：重启服务（≤ 15 分钟）

```bash
# Docker 环境
docker-compose up -d jte jte-website

# K8s 环境
kubectl scale deployment jte-blue -n jte --replicas=3
kubectl scale deployment jte-website -n jte-website --replicas=2

# 等待就绪
kubectl rollout status deployment/jte-blue -n jte --timeout=300s
kubectl rollout status deployment/jte-website -n jte-website --timeout=300s
```

### 阶段 5：业务功能验证（≤ 30 分钟）

```bash
# 1. 健康检查
curl -sf http://localhost:8080/healthz && echo "✅ Engine healthy"
curl -sf http://localhost:8081/api/v1/health && echo "✅ Website healthy"

# 2. API 功能测试
# 设备注册
curl -X POST http://localhost:8080/api/v1/devices/register \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"device_id":"DR-TEST-001","protocol":"jt808"}'

# 查询设备列表
curl http://localhost:8080/api/v1/devices -H "Authorization: Bearer $TOKEN"

# 官网登录
curl -X POST http://localhost:8081/api/v1/auth/login \
  -d '{"email":"dr-test@jte.com","password":"test123"}'

# 3. 视频流测试（如有 ZLMediaKit）
curl http://localhost:8080/api/v1/media/streams -H "Authorization: Bearer $TOKEN"
```

### 阶段 6：恢复报告（≤ 15 分钟）

```bash
# 自动生成恢复报告
bash /app/scripts/ops/dr_drill.sh --report
```

报告模板：
```
═══════════════════════════════════════════
JTE 灾难恢复报告
═══════════════════════════════════════════
恢复日期: 2026-07-02
恢复时间点: 2026-07-01 02:00:00
恢复开始: 2026-07-02 10:00:00
恢复完成: 2026-07-02 12:30:00
RTO: 2小时30分钟 (目标: 4小时) ✅
RPO: 1小时 (目标: 1小时) ✅

恢复服务:
  ✓ MySQL — 数据完整性校验通过
  ✓ TDengine — 7天时序数据完整
  ✓ Redis — 缓存已恢复
  ✓ MinIO — 归档数据已同步
  ✓ 配置 — 配置文件已恢复

验证结果:
  ✓ 健康检查通过
  ✓ API 功能测试通过
  ✓ 数据计数一致

问题记录:
  - 无

后续行动:
  - [ ] 监控 24 小时业务指标
  - [ ] 恢复备份调度任务
  - [ ] 更新灾难恢复文档
═══════════════════════════════════════════
```

---

## 4. 常见故障场景与恢复方案

### 场景 1：MySQL 数据损坏

```bash
# 症状：查询报错、表损坏
# 恢复步骤：
1. 停止 JTE 服务
2. mysql_backup.sh restore $(date -d yesterday +%Y%m%d)
3. 验证数据完整性
4. 重启服务
```

### 场景 2：TDengine 节点故障

```bash
# 症状：时序数据查询失败
# 恢复步骤：
1. tdengine_rolling_upgrade.sh upgrade --node <failed-node>
2. 如节点不可恢复：tdengine_backup.sh restore $(date -d yesterday +%Y%m%d)
3. 验证时序数据
```

### 场景 3：Redis 内存数据丢失

```bash
# 症状：会话丢失、缓存命中率骤降
# 恢复步骤：
1. redis_backup.sh restore $(date -d today +%Y%m%d)
2. 重启 JTE 服务（会自动重建缓存）
3. 监控缓存命中率
```

### 场景 4：配置文件误修改

```bash
# 症状：服务启动失败、行为异常
# 恢复步骤：
1. config_backup.sh list  # 查看可用备份
2. config_backup.sh restore $(date -d yesterday +%Y%m%d)
3. 通过 API 触发热加载或重启服务
```

### 场景 5：全机房故障（切换到灾备）

```bash
# 症状：主机房不可达
# 恢复步骤：
1. 启动灾备机房服务
2. minio_replication.sh failover  # MinIO 切换到灾备
3. 从灾备备份恢复 MySQL/TDengine/Redis
4. 更新 DNS 指向灾备机房
5. 验证业务功能
```

---

## 5. 演练计划

### 每季度演练（1/4/7/10 月第一个周六）

```bash
# 1. 在隔离环境执行演练
bash /app/scripts/ops/dr_drill.sh --full

# 2. 演练内容包括：
#    - 从备份恢复全部服务
#    - 数据完整性校验
#    - 业务功能测试
#    - RTO/RPO 测量
#    - 生成演练报告

# 3. 演练后行动项：
#    - 更新 RTO/RPO 指标
#    - 修复发现的问题
#    - 更新本手册
```

### 演练检查清单

- [ ] 备份文件完整性验证通过
- [ ] MySQL 恢复成功，数据计数一致
- [ ] TDengine 恢复成功，时序数据完整
- [ ] Redis 恢复成功，缓存重建正常
- [ ] MinIO 数据同步完成
- [ ] 配置文件恢复成功
- [ ] JTE 引擎健康检查通过
- [ ] 官网健康检查通过
- [ ] API 功能测试通过
- [ ] 视频流功能测试通过（如有）
- [ ] RTO ≤ 4 小时
- [ ] RPO ≤ 1 小时
- [ ] 演练报告已归档

---

## 6. 应急联系人

| 角色 | 姓名 | 电话 | 职责 |
|------|------|------|------|
| 运维负责人 | ______ | ______ | 统筹恢复、决策 |
| DBA | ______ | ______ | 数据库恢复 |
| 后端工程师 | ______ | ______ | 应用层排障 |
| 网络工程师 | ______ | ______ | 网络切换、DNS |
| 业务方接口 | ______ | ______ | 业务影响通知 |

---

## 7. 备份文件位置

| 环境 | 路径 | 说明 |
|------|------|------|
| 生产 | `/data/backups/` | 本地备份 |
| 灾备 | `minio://jte-backup/backups/` | 异地备份（MinIO 跨区域复制） |
| 归档 | `s3://jte-archive/dr/` | 长期归档（冷存储） |
