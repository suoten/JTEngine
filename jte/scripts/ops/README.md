# JTE 运维脚本

> AUTO-FIX-2026-06-30 [P1-1 ~ P1-5, P2-6, P2-7]: 部署与运维体系

本目录包含 JTE 系统的部署、升级、迁移、备份、灾难恢复脚本。

## 目录结构

```
scripts/ops/
├── tdengine_rolling_upgrade.sh   # P1-3 TDengine 集群滚动升级
├── redis_migration.sh            # P1-4 Redis 在线迁移（redis-shake 六阶段）
├── mysql_backup.sh               # P1-5 MySQL 备份与恢复（全量+binlog增量）
├── tdengine_backup.sh            # P1-5 TDengine 备份与恢复（taosdump+快照+归档）
├── redis_backup.sh               # P1-5 Redis 备份与恢复（RDB+AOF）
├── minio_replication.sh          # P1-5 MinIO 跨区域复制配置与故障切换
├── dr_drill.sh                   # P1-5 灾难恢复演练（每季度）
└── verify_backups.sh             # P1-5 备份完整性校验

deploy/
├── k8s/
│   ├── jte-blue-green.yaml       # P1-1 蓝绿部署 K8s 清单
│   └── blue-green-switch.sh      # P1-1 蓝绿切换脚本
└── cron/
    └── jte-backups.cron          # P1-5 定时备份 cron 配置
```

## P1-1 零停机部署（蓝绿）

```bash
# 首次部署
kubectl apply -f deploy/k8s/jte-blue-green.yaml

# 升级：更新 Green 镜像后切换流量
kubectl -n jte set image deployment/jte-green jte=jte-engine:v2.0
./deploy/k8s/blue-green-switch.sh            # Blue → Green
./deploy/k8s/blue-green-switch.sh --rollback # 回滚：Green → Blue
```

切换流程：扩容 Green → 等待就绪探针通过 → 切 Service selector → 缩容 Blue 触发 SIGTERM →
进程优雅停机（拒绝新连接 + 排空 5min + 下发 0-60s 随机重连退避）→ Blue 下线。

## P1-2 优雅停机

由进程内 `GracefulShutdown()` 实现（`cmd/jte/main.go`），配置项见 `configs/jte.yaml`：

```yaml
shutdown:
  drain_timeout_seconds: 300          # 排空等待 5 分钟
  api_shutdown_timeout_seconds: 10    # API 优雅关闭超时
  reconnect_backoff_max_seconds: 60   # 重连退避上限 60s
  drain_check_interval_seconds: 5     # 排空检查间隔
```

K8s `terminationGracePeriodSeconds: 330`（drain 300 + 30s buffer）。

## P1-3 TDengine 滚动升级

```bash
# 低峰期（凌晨 2-4 点）执行
./scripts/ops/tdengine_rolling_upgrade.sh \
    --target-version 3.3.5.0 \
    --package taos-3.3.5.0-Linux-x64.rpm

# 回滚
./scripts/ops/tdengine_rolling_upgrade.sh \
    --rollback --node tdengine2 \
    --backup-dir /data/backups/tdengine/20260630_020000
```

流程：全量备份 → 逐节点（摘除 → 升级 → 重启 → 加入 → vgroup 稳定）→ 升级后验证。

## P1-4 Redis 在线迁移

```bash
# 六阶段迁移（redis-shake 方案）
./scripts/ops/redis_migration.sh phase1   # 部署新 Cluster
./scripts/ops/redis_migration.sh phase2   # 全量+增量同步
./scripts/ops/redis_migration.sh phase3   # 开启应用双写
./scripts/ops/redis_migration.sh phase4   # 读切换到新
./scripts/ops/redis_migration.sh phase5   # 停写旧，保留 24h
./scripts/ops/redis_migration.sh phase6   # 下线旧
./scripts/ops/redis_migration.sh status   # 查看状态
```

应用侧双写通过环境变量启用（无需重启）：

```yaml
env:
- name: JTE_REDIS_MIGRATION_NEW_NODES
  value: "redis-0.redis:6379 redis-1.redis:6379 redis-2.redis:6379"
- name: JTE_REDIS_MIGRATION_MODE
  value: "cluster"
```

启用后，应用监听标志文件（由迁移脚本维护）：
- `/app/config/redis-dual-write.enabled` 存在 → 双写
- `/app/config/redis-read-target` 内容 `old`|`new` → 读路由

## P1-5 备份与灾难恢复

### 备份策略

| 系统 | 策略 | RPO | RTO | 保留 |
|------|------|-----|-----|------|
| MySQL | 每日全量 + 每小时 binlog 增量 | 1h | 2h | 全量30天/增量7天 |
| TDengine | Replica=3 + 每日 taosdump + MinIO 归档 | 0 | 30min | 全量14天/归档永久 |
| Redis | 每日 RDB + 每2h AOF | 1s | 5min | RDB7天/AOF3天 |
| MinIO | 跨区域复制 | 0 | - | 永久 |

### 定时备份部署

```bash
cp deploy/cron/jte-backups.cron /etc/cron.d/jte-backups
```

### 手动备份与恢复

```bash
./scripts/ops/mysql_backup.sh full                    # MySQL 全量
./scripts/ops/mysql_backup.sh restore 20260630_020000 # MySQL 恢复
./scripts/ops/tdengine_backup.sh full                 # TDengine 全量
./scripts/ops/redis_backup.sh rdb                     # Redis RDB
```

### 灾难恢复演练（每季度）

```bash
./scripts/ops/dr_drill.sh                 # 完整演练
./scripts/ops/dr_drill.sh --restore-only  # 仅恢复
./scripts/ops/dr_drill.sh --report 20260630_040000  # 查看报告
```

演练在隔离命名空间 `jte-dr-drill` 执行，验证备份可恢复性 + RTO/RPO 达标，
输出 Markdown 报告到 `/data/backups/dr-reports/`。

## P2-6 维护模式数据零丢失

维护模式由 `internal/maintenance/mode.go` 实现：
- `stopWrites=false`：仅停止查询（加索引等），写入继续，零数据丢失
- `stopWrites=true`：停止写入 + 终端数据入内存队列（100 万容量，满则落盘 spool）+ 广播 0x8103 通知终端"暂停上报"

```bash
# 通过 API 启动维护模式
curl -X POST http://jte-svc:8080/api/v1/maintenance/start \
    -H "Authorization: Bearer <token>" \
    -d '{"reason":"db upgrade","stop_writes":true}'

# 停止维护（自动回放队列 + 广播 0x8103 恢复上报）
curl -X POST http://jte-svc:8080/api/v1/maintenance/stop \
    -H "Authorization: Bearer <token>"
```

## P2-7 录制断片防护

由 `pkg/protocol/jt1078/recording.go` 的 `RecordSegmentTracker` 实现：
- 录制侧始终录制主码流（`StreamType=0`）
- 切换码流时记录 `switch_reason`（流断开/质量差）+ `switch_time`
- 相邻分片时间戳间隔 >5s 标记为断片
- 断片写入 alert 表（通过 `FragmentAlertWriter` 注入）

## 验收

- [x] 蓝绿部署脚本可执行（`blue-green-switch.sh`）
- [x] TDengine 滚动升级无数据丢失（Replica=3 + 全量备份 + vgroup 稳定等待）
- [x] Redis 迁移在线状态不丢失（redis-shake 增量同步 + 应用双写 + 一致性校验）
- [x] 灾难恢复演练通过（`dr_drill.sh` 六步恢复 + RTO/RPO 报告）
- [x] 维护模式数据零丢失（stopWrites 缓冲队列 + 0x8103 暂停上报）
