# JTE 灾难恢复演练手册（DR Runbook）

## 文档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | v2.0 |
| 更新日期 | 2026-07-21 |
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

### 方式 B：分步恢复

```bash
# [P2-运维完善] 使用增强版恢复脚本（支持快照回滚、PITR、自动验证）
# 恢复前自动创建当前状态快照，恢复后自动验证数据完整性
bash /app/scripts/ops/restore.sh $RECOVERY_DATE

# 仅恢复指定服务
bash /app/scripts/ops/restore.sh $RECOVERY_DATE --only mysql

# PITR 恢复到指定时间点（MySQL binlog 重放到 03:00:00）
bash /app/scripts/ops/restore.sh $RECOVERY_DATE --pitr "03:00:00"

# 如恢复失败，回滚到恢复前快照
bash /app/scripts/ops/restore.sh --rollback
```

#### 方式 C：手动分步恢复（旧方式，保留兼容）

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

### 场景 6：数据库连接耗尽

**现象：**
- API 请求返回 `500 Internal Server Error`，日志中出现 `Too many connections`
- 设备鉴权失败，新设备无法注册
- Prometheus 告警 `JTEStorageWriteFailures` 触发

**诊断步骤：**

```bash
# 1. 检查 MySQL 连接数
mysql -h mysql -u root -p -e "SHOW STATUS LIKE 'Threads_connected';"
mysql -h mysql -u root -p -e "SHOW STATUS LIKE 'Max_used_connections';"
mysql -h mysql -u root -p -e "SHOW VARIABLES LIKE 'max_connections';"

# 2. 检查 JTE 进程的数据库连接
# 查看 JTE 日志中的连接错误
docker logs jte 2>&1 | grep -i "too many\|connection\|refused" | tail -20

# 3. 检查是否有连接泄漏
# 查看进程的文件描述符
ls /proc/$(pgrep jte)/fd 2>/dev/null | wc -l

# 4. 检查连接池配置
grep -i "pool\|max_open\|max_idle\|conn_max" configs/jte.yaml
```

**修复操作：**

```bash
# 方案 A：临时增加 MySQL 连接数上限
mysql -h mysql -u root -p -e "SET GLOBAL max_connections = 500;"

# 方案 B：重启 JTE 释放连接池（快速恢复）
docker-compose restart jte

# 方案 C：排查连接泄漏（长期修复）
# 1. 检查代码中是否有未关闭的数据库连接
# 2. 调整连接池配置：
#    在 jte.yaml 中设置：
#    storage:
#      max_open_conns: 50    # 最大打开连接数
#      max_idle_conns: 10    # 最大空闲连接数
#      conn_max_lifetime: 300 # 连接最大存活时间（秒）
```

**验证方法：**

```bash
# 1. 验证连接数恢复正常
mysql -h mysql -u root -p -e "SHOW STATUS LIKE 'Threads_connected';"

# 2. 验证 API 正常响应
curl -sf http://localhost:8080/healthz && echo "✅ API healthy"

# 3. 验证设备可以注册
curl -X POST http://localhost:8080/api/v1/devices/register \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"device_id":"CONN-TEST-001","protocol":"jt808"}'

# 4. 监控 30 分钟，确认连接数稳定
watch -n 30 'mysql -h mysql -u root -p -e "SHOW STATUS LIKE '\''Threads_connected'\'';"'
```

---

### 场景 7：TDengine 写入延迟

**现象：**
- 设备位置数据查询有数秒到数十秒延迟
- Prometheus 告警 `JTEStorageWriteFailures` 或 `JTEStorageLatencyHigh` 触发
- JTE 日志中出现 `TDengine write timeout` 或 `write queue full`
- 仪表盘实时位置不更新

**诊断步骤：**

```bash
# 1. 检查 TDengine 写入延迟指标
curl -s http://localhost:8080/metrics | grep tdengine_write
# 关注 jte_tdengine_write_rate（点/秒）和 jte_tdengine_query_duration

# 2. 检查 TDengine 集群状态
taos -s "SHOW VGROUPS;"
taos -s "SHOW DNODES;"
taos -s "SHOW MNODES;"

# 3. 检查写入队列积压
taos -s "SHOW QUERIES;"
# 查看 jte_ts 库的超级表状态
taos -s "USE jte_ts; SHOW STABLES;"

# 4. 检查 TDengine 磁盘 I/O
iostat -x 2 5 | grep -A2 "Device"
df -h | grep taos

# 5. 检查 VGroup 分布是否不均
taos -s "SELECT * FROM information_schema.ins_vgroups WHERE db='jte_ts';"
```

**修复操作：**

```bash
# 方案 A：增加 VGroup 数量（提升写入并行度）
taos -s "ALTER DATABASE jte_ts VGROUPS 20;"
# 注意：增加 VGroup 需要集群有足够的节点

# 方案 B：调整 worker pool 配置
# 在 jte.yaml 中设置：
# storage:
#   time_series:
#     wp_enabled: true          # 启用 worker pool
#     wp_worker_count: 8        # worker 数量（默认 CPU 数，上限 16）
#     wp_batch_size: 2000       # 每个 worker 的批次大小
#     wp_flush_interval_ms: 50  # flush 间隔
# 重启 JTE 使配置生效
docker-compose restart jte

# 方案 C：磁盘 I/O 瓶颈
# 1. 检查是否使用 SSD：fio --name=test --filename=/var/lib/taos/test --size=1G --rw=randwrite --bs=4k --iodepth=32
# 2. 如果使用 HDD，考虑迁移到 SSD 或增加 TDengine 节点
# 3. 调整 TDengine 参数：
taos -s "ALTER DATABASE jte_ts CACHE 64;"
taos -s "ALTER DATABASE jte_ts BLOCKS 12;"

# 方案 D：临时降级（减少写入压力）
# 降低设备上报频率（下发 0x8103 暂停非关键数据上报）
# 或临时关闭 schemaless 写入
```

**验证方法：**

```bash
# 1. 验证写入延迟恢复
curl -s http://localhost:8080/metrics | grep tdengine_query_duration
# P99 延迟应 < 100ms

# 2. 验证实时位置更新
curl http://localhost:8080/api/v1/devices/locations -H "Authorization: Bearer $TOKEN"
# 设备位置时间戳应接近当前时间

# 3. 验证写入速率
curl -s http://localhost:8080/metrics | grep tdengine_write_rate
# 写入速率应恢复到正常水平

# 4. 监控 1 小时，确认无积压
watch -n 60 'curl -s http://localhost:8080/metrics | grep -E "tdengine_write|storage_write"'
```

---

### 场景 8：ZLMediaKit 连接失败

**现象：**
- 视频页面无法播放，显示 "连接超时" 或黑屏
- JTE 日志中出现 `ZLMediaKit connection refused` 或 `media server unreachable`
- 设备 1078 视频注册失败
- Prometheus 指标 `jte_video_concurrent_plays` 降为 0

**诊断步骤：**

```bash
# 1. 检查 ZLMediaKit 容器状态
docker ps | grep zlmediakit
docker logs zlmediakit 2>&1 | tail -30

# 2. 检查 ZLMediaKit HTTP API 可达性
curl -sf http://zlmediakit:80/index/api/getServerConfig && echo "✅ ZLM OK"
# 如果失败：
curl -v http://zlmediakit:80/index/api/getServerConfig

# 3. 检查端口监听
docker exec zlmediakit netstat -tlnp
# 应监听：80(HTTP), 554(RTSP), 1935(RTMP), 8000/udp(RTP)

# 4. 检查 JTE 与 ZLMediaKit 的网络连通性
docker exec jte ping -c 3 zlmediakit
docker exec jte curl -sf http://zlmediakit:80/index/api/getServerConfig

# 5. 检查 RTP 端口范围是否开放
# 在 jte.yaml 中检查 video.stream_port 配置
grep -A5 "video:" configs/jte.yaml

# 6. 检查 ZLMediaKit 配置
docker exec zlmediakit cat /opt/media/conf/config.ini | grep -A10 "[rtsp]"
```

**修复操作：**

```bash
# 方案 A：重启 ZLMediaKit 容器
docker-compose restart zlmediakit
sleep 10
curl -sf http://zlmediakit:80/index/api/getServerConfig && echo "✅ ZLM recovered"

# 方案 B：ZLMediaKit 配置问题
# 1. 检查 secret 是否匹配
docker exec zlmediakit grep -A2 "secret" /opt/media/conf/config.ini
# 对比 jte.yaml 中的 zlmediakit.secret

# 2. 修正后重启
docker-compose restart zlmediakit jte

# 方案 C：RTP 端口冲突
# 1. 检查端口是否被占用
ss -tlnp | grep -E "554|1935|8000"
# 2. 如果被占用，修改 jte.yaml 中的 video.stream_port
# 3. 重启 JTE 和 ZLMediaKit

# 方案 D：ZLMediaKit 版本不兼容
# 锁定版本避免 :latest 滚动标签问题
docker-compose pull zlmediakit
# 确保 image: zlmediakit/zlmediakit:release-12.0.0
```

**验证方法：**

```bash
# 1. 验证 ZLMediaKit API 可达
curl -sf http://zlmediakit:80/index/api/getServerConfig && echo "✅ ZLM API OK"

# 2. 验证 JTE 已连接 ZLMediaKit
docker logs jte 2>&1 | grep "ZLMediaKit client configured"

# 3. 验证视频流可播放
curl http://localhost:8080/api/v1/media/streams -H "Authorization: Bearer $TOKEN"
# 应返回活跃流列表

# 4. 验证 1078 设备视频注册
curl http://localhost:8080/api/v1/media/sessions -H "Authorization: Bearer $TOKEN"

# 5. 监控视频指标
curl -s http://localhost:8080/metrics | grep jte_video
```

---

### 场景 9：模块持续崩溃

**现象：**
- Prometheus 告警 `JTEProcessRestarted` 持续触发
- 模块状态指标 `jte_module_status` 显示 -1（failed）
- 模块重启计数 `jte_module_restart_count` 在 1 小时内超过 3 次
- JTE 日志中反复出现 `module crashed, restarting` 和堆栈信息

**诊断步骤：**

```bash
# 1. 检查模块状态和重启次数
curl -s http://localhost:8080/metrics | grep -E "module_status|module_restart"

# 2. 查看 JTE 模块加载日志
docker logs jte 2>&1 | grep -E "module|crash|panic|restart" | tail -50

# 3. 检查 supervisor 日志（模块崩溃自动重启记录）
docker logs jte 2>&1 | grep -i "supervisor\|restart\|backoff" | tail -30

# 4. 查看崩溃模块的详细堆栈
docker logs jte 2>&1 | grep -A20 "panic\|fatal" | tail -60

# 5. 检查模块二进制文件完整性
ls -la /app/modules/*.so
# 检查签名文件是否存在
ls -la /app/modules/*.so.sig

# 6. 检查模块与宿主 API 版本兼容性
docker logs jte 2>&1 | grep -i "api.*version\|incompatible\|HostAPIVersion"
```

**修复操作：**

```bash
# 方案 A：临时禁用崩溃模块（止损）
# 通过 API 禁用模块
curl -X POST http://localhost:8080/api/v1/modules/<module_name>/disable \
  -H "Authorization: Bearer $TOKEN"

# 或移动模块文件
mv /app/modules/<module_name>.so /app/modules/<module_name>.so.disabled
docker-compose restart jte

# 方案 B：更新模块到兼容版本
# 1. 拉取新版本模块
jte module pull <module_name> <version>
# 2. 安装
jte module install <module_name>
# 3. 重启 JTE
docker-compose restart jte

# 方案 C：清理模块配置缓存
# 模块可能因配置损坏导致崩溃
rm -rf /app/config/modules/<module_name>/
docker-compose restart jte

# 方案 D：调整 supervisor 重启策略
# 在 jte.yaml 中设置：
# modules:
#   max_restarts_per_hour: 3     # 每小时最大重启次数
#   restart_backoff_initial: 5  # 初始退避秒数
#   restart_backoff_max: 300    # 最大退避秒数
```

**验证方法：**

```bash
# 1. 验证模块状态恢复正常
curl -s http://localhost:8080/metrics | grep module_status
# 状态应为 1（running）

# 2. 验证重启计数不再增长
curl -s http://localhost:8080/metrics | grep module_restart_count
# 等待 5 分钟后再次检查，数值不应增长

# 3. 验证模块功能正常
curl http://localhost:8080/api/v1/modules -H "Authorization: Bearer $TOKEN"

# 4. 监控 1 小时，确认无二次崩溃
watch -n 60 'curl -s http://localhost:8080/metrics | grep -E "module_status|module_restart"'
```

---

### 场景 10：内存泄漏排查

**现象：**
- Prometheus 告警 `JTEHighMemoryUsage` 或 `JTEMemoryCritical` 触发
- 进程内存持续单调增长，不随设备连接数变化
- OOM Killer 杀死 JTE 进程（dmesg 中有 `Out of memory: Killed process`）
- 容器频繁重启

**诊断步骤：**

```bash
# 1. 检查内存使用趋势（Prometheus 查询）
# 在 Prometheus UI 查询：process_resident_memory_bytes{job="jt-engine"}
# 观察是否单调增长（泄漏特征）vs 周期性波动（正常缓存行为）

# 2. 检查 Go runtime 内存指标
curl -s http://localhost:8080/metrics | grep -E "go_memstats|go_goroutines"
# 关注：
#   go_memstats_alloc_bytes      — 已分配内存
#   go_memstats_sys_bytes        — 系统分配内存
#   go_memstats_heap_inuse_bytes — 堆使用内存
#   go_goroutines                — goroutine 数量

# 3. 获取堆 profile（关键诊断步骤）
# 通过 pprof 获取堆分配报告
curl -s http://localhost:8080/debug/pprof/heap > /tmp/heap.prof
go tool pprof -top /tmp/heap.prof

# 或获取完整内存 profile
curl -s "http://localhost:8080/debug/pprof/heap?gc=1" > /tmp/heap_after_gc.prof

# 4. 检查 goroutine 数量是否异常增长
curl -s http://localhost:8080/debug/pprof/goroutine?debug=1 | head -5
# 如果 goroutine 数量 > 10000，可能存在 goroutine 泄漏

# 5. 获取 goroutine profile 查看泄漏位置
curl -s http://localhost:8080/debug/pprof/goroutine?debug=2 > /tmp/goroutine.prof
# 查看哪些函数创建了大量 goroutine

# 6. 检查容器内存限制
docker inspect jte | grep -i "Memory"
```

**修复操作：**

```bash
# 方案 A：紧急重启（止损，释放内存）
docker-compose restart jte

# 方案 B：增加容器内存限制（临时缓解）
# 在 docker-compose-prod.yml 中添加：
#   jte:
#     mem_limit: 4g
#     memswap_limit: 4g
docker-compose up -d jte

# 方案 C：基于 pprof 分析定位泄漏源
# 1. 对比两次 heap profile（间隔 10 分钟）
curl -s http://localhost:8080/debug/pprof/heap > /tmp/heap1.prof
sleep 600
curl -s http://localhost:8080/debug/pprof/heap > /tmp/heap2.prof

# 2. 使用 pprof 对比分析
go tool pprof -base /tmp/heap1.prof /tmp/heap2.prof
# 在交互式终端中输入 "top20" 查看增长最多的分配

# 3. 生成可视化火焰图
go tool pprof -web /tmp/heap2.prof

# 方案 D：goroutine 泄漏修复
# 1. 查看 goroutine profile
go tool pprof /tmp/goroutine.prof
# 输入 "top20" 查看创建 goroutine 最多的函数
# 输入 "traces" 查看完整调用栈

# 2. 常见泄漏模式：
#    - 未关闭的 channel：发送方等待接收方，goroutine 阻塞
#    - 未取消的 context：后台 goroutine 永远不退出
#    - 未关闭的 HTTP 连接：连接池耗尽
#    - 未设置的 ticker.Stop()：定时器 goroutine 泄漏

# 方案 E：调整 GC 策略（临时缓解）
# 设置 GOGC 环境变量（默认 100，调低可更频繁 GC）
# 在 docker-compose-prod.yml 中添加：
#   environment:
#     - GOGC=50  # 更频繁的 GC（代价是 CPU 使用增加）
#     - GOMEMLIMIT=3GiB  # Go 1.19+ 软内存限制
```

**验证方法：**

```bash
# 1. 验证内存稳定（不再单调增长）
# 在 Prometheus 中查询 1 小时内存趋势
# process_resident_memory_bytes{job="jt-engine"}[1h]
# 应该看到内存趋于稳定或周期性波动，不再单调增长

# 2. 验证 goroutine 数量正常
curl -s http://localhost:8080/metrics | grep go_goroutines
# 正常范围：100-5000（取决于设备数和并发量）

# 3. 验证 GC 正常工作
curl -s http://localhost:8080/metrics | grep -E "go_gc_duration_seconds|go_memstats_last_gc_time"
# last_gc_time 应在最近几秒内更新

# 4. 持续监控 24 小时
# 在 Grafana 仪表盘中观察内存曲线
# 设置告警：jte:memory_mb > 2048 持续 5 分钟
```

---

## 5. 备份验证与恢复工具

### 5.1 备份验证工具

[P2-运维完善] 备份成功不等于可恢复。每次备份后应运行验证脚本确认数据完整性。

```bash
# 自动验证最新备份
bash /app/scripts/ops/backup_verify.sh

# 验证指定日期的备份
bash /app/scripts/ops/backup_verify.sh 20260721

# 与生产环境行数对比（需数据库连接）
bash /app/scripts/ops/backup_verify.sh --compare

# 生成 JSON 校验报告
bash /app/scripts/ops/backup_verify.sh --report
```

验证内容：
| 服务 | 验证项 | 说明 |
|------|--------|------|
| MySQL | gzip 可解压 | 备份文件未被损坏 |
| MySQL | SQL 语句存在 | 文件内容是有效的 SQL |
| MySQL | 关键表存在 | devices/users/vehicles 表结构完整 |
| TDengine | META 文件 | 元数据文件存在且非空 |
| TDengine | 文件数量 | 备份文件数 ≥2 |
| Redis | RDB 非空 | dump.rdb 文件大小 > 0 |
| Redis | RDB 魔数 | 文件头为 REDIS 格式 |
| 配置 | tar 可解压 | 压缩包完整 |
| 配置 | jte.yaml 存在 | 关键配置文件在备份中 |

### 5.2 数据恢复工具

[P2-运维完善] 增强版恢复脚本，支持快照回滚、PITR 和自动验证。

```bash
# 完整恢复（恢复前自动创建快照，恢复后自动验证）
bash /app/scripts/ops/restore.sh 20260721

# 预演恢复（不实际执行，检查流程）
bash /app/scripts/ops/restore.sh 20260721 --dry-run

# 仅恢复指定服务
bash /app/scripts/ops/restore.sh 20260721 --only mysql

# PITR 恢复到指定时间点（MySQL binlog 重放）
bash /app/scripts/ops/restore.sh 20260721 --pitr "03:00:00"

# 回滚到恢复前快照（恢复失败时使用）
bash /app/scripts/ops/restore.sh --rollback
```

恢复流程：
```
恢复前检查 → 创建快照 → 恢复配置 → 恢复 MySQL → 恢复 Redis → 恢复 TDengine → 数据验证 → 完成
     ↓                              ↓                              ↓
  备份可用？                     PITR binlog                   连通性测试
```

### 5.3 RTO/RPO 指标

| 恢复场景 | RTO 目标 | RPO 目标 | 恢复方式 |
|----------|----------|----------|----------|
| MySQL 单库损坏 | 30 分钟 | 1 小时 | `restore.sh --only mysql` |
| TDengine 节点故障 | 1 小时 | 0（实时） | `tdengine_rolling_upgrade.sh` 或 `restore.sh --only tdengine` |
| Redis 数据丢失 | 15 分钟 | 2 小时 | `restore.sh --only redis` |
| 全量恢复 | 4 小时 | 1 小时 | `restore.sh` 全量 |
| PITR 精确恢复 | 2 小时 | 0（到指定时间点） | `restore.sh --pitr` |
| 灾备切换 | 4 小时 | 0（实时复制） | `minio_replication.sh failover` |

> **RTO**（Recovery Time Objective）：恢复时间目标，从启动恢复到业务恢复的最长时间。
> **RPO**（Recovery Point Objective）：恢复点目标，可容忍的最大数据丢失时间窗口。

## 6. 演练计划

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

## 7. 应急联系人

| 角色 | 姓名 | 电话 | 职责 |
|------|------|------|------|
| 运维负责人 | ______ | ______ | 统筹恢复、决策 |
| DBA | ______ | ______ | 数据库恢复 |
| 后端工程师 | ______ | ______ | 应用层排障 |
| 网络工程师 | ______ | ______ | 网络切换、DNS |
| 业务方接口 | ______ | ______ | 业务影响通知 |

---

## 8. 备份文件位置

| 环境 | 路径 | 说明 |
|------|------|------|
| 生产 | `/data/backups/` | 本地备份 |
| 灾备 | `minio://jte-backup/backups/` | 异地备份（MinIO 跨区域复制） |
| 归档 | `s3://jte-archive/dr/` | 长期归档（冷存储） |
