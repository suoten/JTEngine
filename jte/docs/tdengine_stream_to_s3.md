# TDengine STREAM 到 S3/MinIO 自动迁移方案设计

> **状态**：调研性质（AUTO-FIX-2026-07-02）
> **优先级**：P2（现有定时归档任务已满足需求，STREAM 为未来增强）
> **关联模块**：module-storage / archive / tdengine

## 1. 背景与目标

### 1.1 现状

当前冷数据迁移依赖 `archive.Archiver` 定时任务：
- 每日凌晨 2 点扫描超过 `KeepDays`（默认 365 天）的轨迹数据
- 查询 TDengine → 序列化 JSONL → 上传 MinIO ArchiveBucket → 写归档标记 → 延迟 7 天删除
- 查询时通过 `LocationArchiveFallback` 接口从 MinIO 读取已归档数据

**痛点**：
1. 归档任务为批量执行，单次扫描大量设备时可能持续数十分钟，期间 TDengine CPU/IO 压力高
2. 归档窗口与业务高峰可能重叠（虽有凌晨 2 点调度，但大规模数据仍可能跑超）
3. 数据从 "过期" 到 "归档完成" 存在最多 24 小时窗口期

### 1.2 目标

利用 TDengine 3.x 的 **STREAM（流计算）** 能力，实现冷数据到 S3/MinIO 的**近实时自动迁移**：
- 数据写入 TDengine 时自动触发流计算，按天聚合后写入外部存储
- 过期数据由 TDengine 内部 TTL 自动清理，无需归档任务物理删除
- 归档迁移从 "批量定时" 升级为 "近实时流式"

## 2. TDengine STREAM 机制概述

### 2.1 STREAM 语法（TDengine 3.x）

```sql
-- 创建流：从超级表 vehicle_location 按天聚合，写入目标表
CREATE STREAM IF NOT EXISTS location_archive_stream
INTO archive_location_daily
AS
SELECT
    _wstart AS archive_date,
    device_id,
    COUNT(*) AS row_count,
    CAST(TO_JSON(*) AS VARCHAR) AS jsonl_data
FROM vehicle_location
WHERE ts < NOW - 365d
PARTITION BY device_id
INTERVAL(1d);
```

### 2.2 STREAM 关键特性

| 特性 | 说明 | 适用性 |
|------|------|--------|
| 实时触发 | 数据写入时自动触发流计算 | ✅ 近实时归档 |
| 窗口聚合 | INTERVAL/PARTITION BY 支持按天/按设备分窗口 | ✅ 按天归档 |
| 写入外部表 | 通过 taosX/TMQ 可写入 Kafka 等外部系统 | ⚠️ 需中间件 |
| 历史回填 | FILL_HISTORY 支持处理创建 STREAM 前的历史数据 | ✅ 首次启用 |
| 自动清理 | 配合 TTL 自动删除过期数据 | ✅ 替代手动删除 |

### 2.3 限制与约束

1. **STREAM 目标表必须在同一 TDengine 集群内**：无法直接写入 MinIO/S3
2. **外部写入需通过 TMQ 订阅**：STREAM → TMQ topic → 消费者 → MinIO
3. **单条记录大小限制**：TO_JSON 结果可能超长，需分片处理
4. **集群资源消耗**：STREAM 增加计算节点 CPU/内存开销

## 3. 方案设计

### 3.1 架构链路

```
TDengine vehicle_location (超级表)
        │
        ├── TTL 自动过期 (KEEP=400d, 归档窗口 365d, 缓冲 5d)
        │
        ├── STREAM: location_archive_stream
        │       └── 写入 archive_location_daily (内部表)
        │
        └── TMQ 订阅 archive_location_daily
                │
                ├── ArchiveStreamConsumer (Go 消费者)
                │       ├── 聚合当天数据为 JSONL
                │       ├── 上传 MinIO ArchiveBucket
                │       └── 写归档标记 (archive_completed)
                │
                └── 成功后 TMQ ACK（自动提交偏移量）
```

### 3.2 与现有归档任务的关系

| 维度 | 定时归档任务（现有） | STREAM 流式归档（本方案） |
|------|---------------------|--------------------------|
| 触发方式 | 每日凌晨 2 点批量扫描 | 数据写入时实时触发 |
| 延迟 | 最多 24 小时 | 秒级 |
| 资源影响 | 集中在凌晨窗口 | 均匀分散到全天 |
| 删除方式 | 归档后延迟 7 天手动 DeleteRange | TDengine TTL 自动删除 |
| 实现复杂度 | 低（已实现） | 中（需 TMQ 消费者） |
| 依赖 | archive.Archiver | TDengine 3.2+ TMQ |

**推荐策略**：**两者并存，STREAM 为主、定时任务为补**
- STREAM 处理日常增量归档（近实时）
- 定时任务作为兜底：每日扫描 STREAM 可能遗漏的数据（如 TMQ 消费失败、消费者宕机期间的数据）
- 定时任务频率可降低（如每周一次），减少资源占用

### 3.3 核心组件设计

#### 3.3.1 STREAM DDL

```sql
-- 1. 创建归档目标内部表（超级表）
CREATE STABLE IF NOT EXISTS archive_location_daily (
    archive_date TIMESTAMP,
    device_id NCHAR(64),
    row_count INT,
    jsonl_data NCHAR(64000)  -- 单条 JSONL 聚合数据
) TAGS (
    vehicle_id NCHAR(64)
);

-- 2. 创建 STREAM（FILL_HISTORY 处理历史数据）
CREATE STREAM IF NOT EXISTS location_archive_stream
WITH FILL_HISTORY 1
INTO archive_location_daily AS
SELECT
    _wstart AS archive_date,
    device_id,
    COUNT(*) AS row_count,
    -- 将窗口内所有行聚合为 JSONL（需自定义 UDF 或应用层聚合）
    '' AS jsonl_data  -- 占位，实际 JSONL 由消费者聚合
FROM vehicle_location
WHERE ts < NOW - 365d
PARTITION BY device_id
INTERVAL(1d);
```

#### 3.3.2 TMQ 消费者（Go 实现）

```go
// ArchiveStreamConsumer 消费 archive_location_daily 的 TMQ 订阅
// 每个 partition 一个消费者 goroutine，处理流程：
// 1. 收到当天窗口关闭消息
// 2. 查询 vehicle_location 当天完整数据（STREAM 仅通知，不传数据）
// 3. 序列化为 JSONL 上传 MinIO
// 4. 写归档标记
// 5. ACK 消息
type ArchiveStreamConsumer struct {
    tmq        *tdengine.TMQSubscriberHandle
    locationTS jtestorage.LocationTimeSeries
    obj        jtestorage.ObjectStorage
    markerStore archive.MarkerStore
    logger     *zap.Logger
}

func (c *ArchiveStreamConsumer) Consume(ctx context.Context) error {
    // 消费循环：收到窗口关闭 → 查询当天数据 → 上传 MinIO → 写标记 → ACK
    // ...
}
```

#### 3.3.3 TTL 配置策略

```sql
-- vehicle_location: KEEP=400d（归档 365d + 5d 缓冲 + 归档处理时间）
ALTER STABLE vehicle_location MODIFY TAG KEEP 400;
```

TDengine TTL 到期后自动删除数据，无需归档任务手动 DeleteRange。
归档标记（archive_completed 表）保留在 SQLite/MySQL 中，查询 fallback 仍可工作。

### 3.4 故障恢复

| 故障场景 | 恢复策略 |
|---------|---------|
| TMQ 消费者宕机 | 重启后从上次 ACK 位置继续消费（TMQ offset 持久化） |
| MinIO 上传失败 | 消费者不 ACK，消息重试；连续失败 N 次告警 |
| TDengine STREAM 中断 | 定时归档任务兜底，每日扫描补充遗漏数据 |
| 消费者积压 | 增加 partition 数 / 消费者实例水平扩展 |

## 4. 可行性评估

### 4.1 TDengine 版本要求

| 功能 | 最低版本 | 当前版本 | 状态 |
|------|---------|---------|------|
| STREAM 基础 | 3.0.0+ | 3.8.1 | ✅ 满足 |
| FILL_HISTORY | 3.2.0+ | 3.8.1 | ✅ 满足 |
| TMQ 订阅 | 3.0.0+ | 3.8.1 | ✅ 满足 |
| TO_JSON UDF | 3.3.0+ | 3.8.1 | ⚠️ 需验证 |

### 4.2 性能预估

| 指标 | 定时任务 | STREAM 方案 |
|------|---------|-------------|
| 归档延迟 | 24h | <60s |
| TDengine CPU 峰值 | 凌晨高峰 80% | 均匀 15% |
| MinIO 写入 QPS | 凌晨突发 500/s | 均匀 50/s |
| 消息积压风险 | 无 | 中（需监控） |

### 4.3 风险与缓解

1. **TMQ 消费者复杂度**：需实现幂等消费、offset 管理、错误重试
   - 缓解：复用现有 TMQ 框架（module-storage 已有 TMQSubscriber）
2. **JSONL 聚合在应用层**：STREAM 无法直接输出 JSONL，需消费者二次查询
   - 缓解：STREAM 仅做窗口通知，消费者查询当天数据后聚合上传
3. **STREAM 与 TTL 的时序竞争**：TTL 可能在 STREAM 处理前删除数据
   - 缓解：KEEP 设为归档天数 + 5d 缓冲，确保 STREAM 有足够处理窗口

## 5. 实施路线图

### Phase 1：验证（2 周）
- [ ] 在测试环境验证 TDengine 3.8.1 STREAM + FILL_HISTORY 语法
- [ ] 验证 TMQ 订阅 STREAM 目标表的可行性
- [ ] 基准测试 STREAM 对写入性能的影响

### Phase 2：原型（3 周）
- [ ] 实现 ArchiveStreamConsumer 消费者
- [ ] 实现 STREAM DDL 自动创建（集成到 tdengine.NewStorage 的 migrate）
- [ ] 实现 STREAM/TMQ 与现有归档标记的联动

### Phase 3：灰度（2 周）
- [ ] 双轨运行：STREAM + 定时任务并存
- [ ] 对比验证归档完整性和数据一致性
- [ ] 监控指标：归档延迟、消息积压、MinIO 写入成功率

### Phase 4：全量切换（1 周）
- [ ] 关闭定时归档任务（或降低为每周兜底）
- [ ] 调整 TDengine KEEP 参数（移除归档任务的 7d 延迟窗口）
- [ ] 更新配置文档和运维手册

## 6. 结论

TDengine STREAM 到 S3/MinIO 的自动迁移链路**技术可行**，但需要 TMQ 消费者作为中间层（STREAM 无法直接写入 MinIO）。

**建议**：
1. **短期**（当前）：继续使用定时归档任务（已实现且稳定），满足 3 年以上历史轨迹归档需求
2. **中期**（Phase 1-2）：验证 STREAM + TMQ 链路，实现原型
3. **长期**（Phase 3-4）：灰度切换到 STREAM 为主、定时任务为辅的混合模式

现有 `archive.Archiver` + `LocationArchiveFallback` 的架构设计已为 STREAM 方案预留了扩展点：
- `LocationArchiveFallback` 接口无需修改，STREAM 消费者只需写归档标记即可
- `MarkerStore` 接口无需修改，STREAM 消费者复用现有 SQLite 标记存储
- 查询 fallback 链路完全复用，业务层无感知
