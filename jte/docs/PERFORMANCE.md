# JTE 性能优化指南

## 概述

JTE 引擎针对高并发车联网场景进行了深度性能优化。本文档描述关键性能特性和调优建议。

## 关键性能特性

### 1. 协议编解码优化

- **JT808 转义/反转义**：使用预分配字节切片，避免频繁 GC
  - `Escape()` / `Unescape()` — 批量处理，减少内存分配
  - 基准测试：`jte/pkg/protocol/jt808/benchmark_test.go`

- **报文分包重组**：`PacketReassembler` 处理 TCP 粘包/分包
  - 滑动窗口算法，支持乱序到达
  - 零拷贝设计，避免不必要的数据复制

### 2. 存储写入优化

- **批量写入队列**：`WriteQueue` 内存缓冲 + 批量写入
  - 内存队列容量：100 万条
  - 批量大小：1000 条/100ms
  - 失败自动重试（3 次）+ 落盘 spool 补偿
  - 后台补偿协程（每 10s）自动恢复

- **时序数据优化**：TDengine 超表设计
  - 按设备 ID 自动建子表
  - 写入缓冲 + 批量 INSERT
  - 查询下推 + 时间范围分区

### 3. 内存管理

- **OOM 防护**：`OOMProtectConfig` 多级内存阈值
  - 告警阈值（默认 2GB）
  - 危险阈值（默认 3GB）
  - 致命阈值（默认 4GB）→ 主动熔断

- **对象池**：高频对象复用
  - `sync.Pool` 用于协议消息体
  - 减少 GC 压力

### 4. 并发模型

- **goroutine 安全管理**：`util.SafeGo` 包装
  - panic 自动恢复
  - goroutine 泄漏检测
  - 优雅关闭（stopCh 信号）

- **连接管理**：
  - 最大连接数限制（防资源耗尽）
  - 空闲连接超时清理
  - 连接复用（keep-alive）

### 5. 缓存策略

- **Redis 热点缓存**：实时位置/在线状态
  - TTL 30s（位置）/ 60s（状态）
  - 批量管道操作
  - 发布/订阅实时推送

## 性能调优建议

### 生产环境配置

```yaml
# configs/config.yaml
gateway:
  max_connections: 50000      # 根据内存调整
  max_devices: 100000         # 根据业务规模调整
  heartbeat_interval: 60      # 秒
  heartbeat_timeout: 180      # 3 次心跳未收到则断开

storage:
  type: mysql
  time_series:
    type: tdengine
    keep_days: 90             # TDengine 数据保留天数
  cache:
    type: redis
    pool_size: 50             # Redis 连接池大小

api:
  rate_limit: 1000            # API 限流（请求/秒）
```

### 性能监控

```bash
# 运行基准测试
make benchmark

# 查看实时指标
curl http://localhost:8080/metrics

# pprof 性能分析
go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30

# 内存分析
go tool pprof http://localhost:8080/debug/pprof/heap
```

### 关键指标

| 指标 | 目标值 | 说明 |
|------|--------|------|
| 设备并发连接 | 50,000+ | 单节点 |
| 消息处理吞吐 | 100,000+ msg/s | 协议解析 |
| 位置写入延迟 | < 10ms (P99) | TDengine |
| API 响应延迟 | < 100ms (P99) | REST API |
| 内存使用 | < 2GB | 50K 连接 |
| CPU 使用 | < 70% | 50K 连接 |

## 压力测试

使用 `cmd/loadtest` 进行压力测试：

```bash
# 构建压测工具
go build -o bin/loadtest ./cmd/loadtest

# 模拟 10000 台设备
./bin/loadtest -devices 10000 -duration 300s -interval 30s
```
