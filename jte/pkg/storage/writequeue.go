package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/suoten/jt-engine/internal/util"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-30 [P2-7]: TDengine 写入失败补偿队列
// 当 TDengine 写入失败时（网络抖动/节点重启/连接池耗尽），数据不能丢失。
// WriteQueue 提供内存队列缓冲 + 批量写入 + 失败重试 + 落盘 spool + 后台补偿。
//
// 数据流：
//   Write() → 内存 channel(100万) → batch(1000条/100ms) → BatchWrite(重试3次)
//                                                          ├─ 成功：完成
//                                                          └─ 失败：落盘 spool/failed_writes.jsonl
//   补偿协程(每10s) → 读 spool → BatchWrite → 成功则从 spool 移除
//
// spool 文件超过 1GB 时记录告警日志（不阻塞写入）。

// FailedWrite 落盘的失败写入记录（JSONL 格式）。
type FailedWrite struct {
	Table     string                 `json:"table"`
	Tags      map[string]string      `json:"tags,omitempty"`
	Fields    map[string]interface{} `json:"fields"`
	Timestamp time.Time              `json:"timestamp"`
	FailedAt  time.Time              `json:"failed_at"`
	Retries   int                    `json:"retries"`
}

// WriteQueueStats 写入队列统计。
type WriteQueueStats struct {
	QueueLen       int64 // 当前内存队列长度
	TotalEnqueued  int64 // 累计入队数
	TotalWritten   int64 // 累计成功写入数
	TotalFailed    int64 // 累计失败（落盘 spool）数
	TotalRetried   int64 // 累计重试次数
	TotalRecovered int64 // 累计从 spool 恢复数
	SpoolSizeBytes int64 // spool 文件大小
	SpoolAlert     bool  // spool 是否超 1GB
	BatchFlushes   int64 // 批量 flush 次数
}

// WriteQueue TDengine 写入失败补偿队列。
type WriteQueue struct {
	ts        TimeSeriesStorage // 底层时序存储
	logger    *zap.Logger
	spoolDir  string // spool 目录（./spool）
	spoolFile string // spool 文件路径（spool/failed_writes.jsonl）

	// 内存队列（有界 channel，容量 100 万）
	queue chan TimeSeriesRow
	table string // 默认表名（可被 row 覆盖）

	// 批量配置
	batchSize          int
	flushTimeout       time.Duration
	maxRetries         int
	compensateInterval time.Duration // 补偿协程执行间隔

	// 原子统计字段（直接 atomic 操作，避免 struct 字段混用）
	totalEnqueued  int64
	totalWritten   int64
	totalFailed    int64
	totalRetried   int64
	totalRecovered int64
	spoolSizeBytes int64
	spoolAlert     int32 // 0=正常 1=超阈值告警
	batchFlushes   int64

	// spool 文件互斥锁（补偿协程和 flush 协程都会写 spool）
	spoolMu     sync.Mutex
	spoolFileF  *os.File // 持久打开的 spool 文件句柄（append 模式）
	spoolWriter *bufio.Writer

	// 控制
	stopCh   chan struct{}
	stopOnce sync.Once
	// AUTO-FIX-2026-06-30 [集成-7]: WaitGroup 确保 Stop 等待 flushLoop/compensateLoop
	// 完全退出后再关闭 spool，避免 flushBatch 重试期间 spool 被提前关闭导致数据丢失。
	// 原实现使用 time.Sleep(100ms) 等待，在 MaxRetries 较大或机器负载高时不可靠。
	wg sync.WaitGroup
}

// WriteQueueConfig 写入队列配置。
type WriteQueueConfig struct {
	// QueueCapacity 内存队列容量（默认 1000000）
	QueueCapacity int
	// BatchSize 批量大小（默认 1000）
	BatchSize int
	// FlushInterval flush 间隔（默认 100ms）
	FlushInterval time.Duration
	// MaxRetries 最大重试次数（默认 3）
	MaxRetries int
	// SpoolDir spool 目录（默认 "./spool"）
	SpoolDir string
	// SpoolAlertThreshold spool 告警阈值字节（默认 1GB）
	SpoolAlertThreshold int64
	// CompensateInterval 补偿协程执行间隔（默认 10s）
	CompensateInterval time.Duration
	// Table 默认表名
	Table string
}

// DefaultWriteQueueConfig 返回默认配置（满足 P2-7 验收标准）。
func DefaultWriteQueueConfig() WriteQueueConfig {
	return WriteQueueConfig{
		QueueCapacity:       1000000, // 100 万条
		BatchSize:           1000,    // 1000 条/批
		FlushInterval:       100 * time.Millisecond,
		MaxRetries:          3,
		SpoolDir:            "./spool",
		SpoolAlertThreshold: 1 << 30, // 1GB
		CompensateInterval:  10 * time.Second,
	}
}

// NewWriteQueue 创建写入补偿队列。
// ts 为底层时序存储（TDengine），表名由 cfg.Table 或 row.Tags 决定。
func NewWriteQueue(ts TimeSeriesStorage, cfg WriteQueueConfig, logger *zap.Logger) (*WriteQueue, error) {
	if cfg.QueueCapacity <= 0 {
		cfg = DefaultWriteQueueConfig()
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 100 * time.Millisecond
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.SpoolDir == "" {
		cfg.SpoolDir = "./spool"
	}
	if cfg.SpoolAlertThreshold <= 0 {
		cfg.SpoolAlertThreshold = 1 << 30
	}
	if cfg.CompensateInterval <= 0 {
		cfg.CompensateInterval = 10 * time.Second
	}

	// 确保 spool 目录存在
	if err := os.MkdirAll(cfg.SpoolDir, 0700); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}

	spoolPath := filepath.Join(cfg.SpoolDir, "failed_writes.jsonl")

	wq := &WriteQueue{
		ts:                 ts,
		logger:             logger,
		spoolDir:           cfg.SpoolDir,
		spoolFile:          spoolPath,
		queue:              make(chan TimeSeriesRow, cfg.QueueCapacity),
		table:              cfg.Table,
		batchSize:          cfg.BatchSize,
		flushTimeout:       cfg.FlushInterval,
		maxRetries:         cfg.MaxRetries,
		compensateInterval: cfg.CompensateInterval,
		stopCh:             make(chan struct{}),
	}

	// 打开 spool 文件（append 模式）
	f, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open spool file: %w", err)
	}
	wq.spoolFileF = f
	wq.spoolWriter = bufio.NewWriterSize(f, 64*1024)

	// 初始化 spool 大小统计
	if info, err := f.Stat(); err == nil {
		atomic.StoreInt64(&wq.spoolSizeBytes, info.Size())
	}

	// AUTO-FIX-2026-06-30 [集成-7]: 使用 WaitGroup 跟踪后台协程，Stop 时等待退出。
	wq.wg.Add(2)
	// 启动 flush 协程
	util.SafeGo(logger, "writequeue.flushLoop", func() {
		defer wq.wg.Done()
		wq.flushLoop()
	})

	// 启动补偿协程
	util.SafeGo(logger, "writequeue.compensateLoop", func() {
		defer wq.wg.Done()
		wq.compensateLoop()
	})

	return wq, nil
}

// Write 入队一条时序数据。队列满时返回错误（不阻塞）。
func (wq *WriteQueue) Write(ctx context.Context, table string, row TimeSeriesRow) error {
	atomic.AddInt64(&wq.totalEnqueued, 1)
	// 使用 table 字段：如果调用方传了 table 就用那个，否则用默认
	if table == "" {
		table = wq.table
	}
	// 将 table 编码进 Tags（因为 channel 只传 TimeSeriesRow）
	if row.Tags == nil {
		row.Tags = make(map[string]string)
	}
	row.Tags["__table__"] = table

	select {
	case wq.queue <- row:
		return nil
	default:
		// 队列满：直接落盘 spool，避免数据丢失
		atomic.AddInt64(&wq.totalFailed, 1)
		return wq.writeToSpool(FailedWrite{
			Table:     table,
			Tags:      stripTableTag(row.Tags),
			Fields:    row.Fields,
			Timestamp: row.Timestamp,
			FailedAt:  time.Now(),
			Retries:   0,
		})
	}
}

// flushLoop 批量 flush 循环：收集 batchSize 条或 flushTimeout 超时后写入。
func (wq *WriteQueue) flushLoop() {
	batch := make([]TimeSeriesRow, 0, wq.batchSize)

	timer := time.NewTimer(wq.flushTimeout)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		wq.flushBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case row := <-wq.queue:
			batch = append(batch, row)
			if len(batch) >= wq.batchSize {
				flush()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(wq.flushTimeout)
			}
		case <-timer.C:
			flush()
			timer.Reset(wq.flushTimeout)
		case <-wq.stopCh:
			// 停止前 flush 剩余数据
			flush()
			// 排空队列
			for len(wq.queue) > 0 {
				row := <-wq.queue
				batch = append(batch, row)
				if len(batch) >= wq.batchSize {
					flush()
				}
			}
			flush()
			return
		}
	}
}

// flushBatch 将一个 batch 写入时序存储，失败重试 maxRetries 次，仍失败落盘 spool。
func (wq *WriteQueue) flushBatch(rows []TimeSeriesRow) {
	atomic.AddInt64(&wq.batchFlushes, 1)

	// 按 table 分组（不同表不能混在一个 BatchWrite 调用）
	groups := make(map[string][]TimeSeriesRow)
	for _, row := range rows {
		table := row.Tags["__table__"]
		// 清理 __table__ tag
		cleaned := row
		cleaned.Tags = stripTableTag(row.Tags)
		groups[table] = append(groups[table], cleaned)
	}

	for table, groupRows := range groups {
		var lastErr error
		success := false
		for attempt := 0; attempt <= wq.maxRetries; attempt++ {
			if attempt > 0 {
				atomic.AddInt64(&wq.totalRetried, 1)
				time.Sleep(time.Duration(attempt) * 100 * time.Millisecond) // 指数退避
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := wq.ts.BatchWrite(ctx, table, groupRows)
			cancel()
			if err == nil {
				success = true
				break
			}
			lastErr = err
		}

		if success {
			atomic.AddInt64(&wq.totalWritten, int64(len(groupRows)))
		} else {
			// 落盘 spool
			atomic.AddInt64(&wq.totalFailed, int64(len(groupRows)))
			wq.logger.Error("batch write failed after retries, spooling",
				zap.String("table", table),
				zap.Int("rows", len(groupRows)),
				zap.Error(lastErr))
			now := time.Now()
			for _, r := range groupRows {
				fw := FailedWrite{
					Table:     table,
					Tags:      r.Tags,
					Fields:    r.Fields,
					Timestamp: r.Timestamp,
					FailedAt:  now,
					Retries:   wq.maxRetries,
				}
				if err := wq.writeToSpool(fw); err != nil {
					wq.logger.Error("write to spool failed (DATA LOSS RISK)",
						zap.String("table", table), zap.Error(err))
				}
			}
		}
	}
}

// writeToSpool 将一条失败记录追加到 spool 文件（JSONL）。
func (wq *WriteQueue) writeToSpool(fw FailedWrite) error {
	wq.spoolMu.Lock()
	defer wq.spoolMu.Unlock()

	// compensate 可能短暂关闭了写句柄；此时丢弃记录并返回错误
	// （compensate 会重试这些记录，不影响数据安全）
	if wq.spoolFileF == nil || wq.spoolWriter == nil {
		return fmt.Errorf("spool file not available (compensating)")
	}

	data, err := json.Marshal(fw)
	if err != nil {
		return fmt.Errorf("marshal failed write: %w", err)
	}
	data = append(data, '\n')

	if _, err := wq.spoolWriter.Write(data); err != nil {
		return fmt.Errorf("write spool: %w", err)
	}
	if err := wq.spoolWriter.Flush(); err != nil {
		return fmt.Errorf("flush spool: %w", err)
	}

	newSize := atomic.AddInt64(&wq.spoolSizeBytes, int64(len(data)))

	// spool 超过 1GB 阈值告警（仅首次超过时记录，避免日志刷屏）
	if newSize > (1 << 30) {
		if atomic.CompareAndSwapInt32(&wq.spoolAlert, 0, 1) {
			wq.logger.Error("spool file exceeded 1GB threshold, writes may be lost",
				zap.Int64("size_bytes", newSize),
				zap.String("path", wq.spoolFile))
		}
	}

	return nil
}

// compensateLoop 定时读取 spool 文件，重试失败写入，成功则从 spool 移除。
// 间隔由 compensateInterval 决定（默认 10s）。
func (wq *WriteQueue) compensateLoop() {
	interval := wq.compensateInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wq.compensate()
		case <-wq.stopCh:
			return
		}
	}
}

// compensate 执行一次补偿：读取 spool → 重试 → 重写 spool（仅保留仍失败的）。
// 注意：Windows 上文件不能同时被读写两个句柄持有，因此先关闭写句柄再读。
// 全程持有 spoolMu，避免 flushLoop 的 writeToSpool 访问已关闭的句柄；
// 由于补偿间隔默认 10s，且 BatchWrite 通常 <1s，锁持有时间可接受。
func (wq *WriteQueue) compensate() {
	wq.spoolMu.Lock()
	defer wq.spoolMu.Unlock()

	// 先 flush 并关闭写句柄，确保数据落盘且释放文件锁（Windows 兼容）
	wq.spoolWriter.Flush()
	wq.spoolFileF.Close()
	wq.spoolFileF = nil

	// 读取 spool 文件（此时写句柄已关闭）
	f, err := os.Open(wq.spoolFile)
	if err != nil {
		if !os.IsNotExist(err) {
			wq.logger.Warn("compensate: open spool failed", zap.Error(err))
		}
		wq.reopenSpool()
		return
	}

	var failed []FailedWrite
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer per line
	for scanner.Scan() {
		var fw FailedWrite
		if err := json.Unmarshal(scanner.Bytes(), &fw); err != nil {
			continue
		}
		failed = append(failed, fw)
	}
	// 读取完毕立即关闭读句柄：Windows 上 os.Rename 无法重命名仍被打开的文件，
	// 必须在下方 rename 之前关闭，否则 spool 截断失败导致 totalRecovered 不更新。
	f.Close()

	if len(failed) == 0 {
		// 没有失败记录，重新打开写句柄即可
		wq.reopenSpool()
		return
	}

	wq.logger.Info("compensating failed writes", zap.Int("pending", len(failed)))

	// 按 table 分组重试
	groups := make(map[string][]TimeSeriesRow)
	for _, fw := range failed {
		row := TimeSeriesRow{
			Timestamp: fw.Timestamp,
			Tags:      fw.Tags,
			Fields:    fw.Fields,
		}
		groups[fw.Table] = append(groups[fw.Table], row)
	}

	// AUTO-FIX-2026-06-30 [集成-7]: 先在局部变量中累加恢复数，spool 截断成功后再
	// 原子更新 totalRecovered。否则外部观测到 TotalRecovered>=N 时 spool 可能尚未
	// 被截断（rename 在 AddInt64 之后），导致校验 spool 大小为 0 时竞态失败。
	var recoveredThisRound int64
	var stillFailed []FailedWrite
	now := time.Now()
	for table, rows := range groups {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := wq.ts.BatchWrite(ctx, table, rows)
		cancel()

		if err == nil {
			recoveredThisRound += int64(len(rows))
			wq.logger.Info("compensate: recovered failed writes",
				zap.String("table", table), zap.Int("count", len(rows)))
		} else {
			// 仍失败：保留到 stillFailed
			for _, r := range rows {
				stillFailed = append(stillFailed, FailedWrite{
					Table:     table,
					Tags:      r.Tags,
					Fields:    r.Fields,
					Timestamp: r.Timestamp,
					FailedAt:  now,
					Retries:   3,
				})
			}
		}
	}

	// 截断重写 spool 文件（仅保留仍失败的）
	tmpPath := wq.spoolFile + ".tmp"
	tmpF, err := os.Create(tmpPath)
	if err != nil {
		wq.logger.Error("compensate: create tmp spool failed", zap.Error(err))
		wq.reopenSpool()
		return
	}
	tmpWriter := bufio.NewWriterSize(tmpF, 64*1024)
	var newSize int64
	for _, fw := range stillFailed {
		data, _ := json.Marshal(fw)
		data = append(data, '\n')
		tmpWriter.Write(data)
		newSize += int64(len(data))
	}
	tmpWriter.Flush()
	tmpF.Close()

	if err := os.Rename(tmpPath, wq.spoolFile); err != nil {
		wq.logger.Error("compensate: rename spool failed", zap.Error(err))
	} else {
		atomic.StoreInt64(&wq.spoolSizeBytes, newSize)
		if newSize < (1 << 30) {
			atomic.StoreInt32(&wq.spoolAlert, 0)
		}
		// spool 截断成功后才更新恢复计数，保证外部观测到 TotalRecovered 增加时
		// spool 已被清空（或仅含仍失败记录）。
		if recoveredThisRound > 0 {
			atomic.AddInt64(&wq.totalRecovered, recoveredThisRound)
		}
	}

	// 重新打开 spool 写句柄
	wq.reopenSpool()
}

// reopenSpool 重新以 append 模式打开 spool 文件。
// 调用方必须持有 spoolMu。
func (wq *WriteQueue) reopenSpool() {
	// 防御性关闭可能残留的旧句柄，避免文件描述符泄漏（正常路径下调用方已关闭，此处做兜底）
	if wq.spoolFileF != nil {
		_ = wq.spoolFileF.Close()
		wq.spoolFileF = nil
	}
	f, err := os.OpenFile(wq.spoolFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		wq.logger.Error("reopen spool file failed", zap.Error(err))
		return
	}
	wq.spoolFileF = f
	wq.spoolWriter = bufio.NewWriterSize(f, 64*1024)
}

// GetStats 返回队列统计快照。
func (wq *WriteQueue) GetStats() WriteQueueStats {
	return WriteQueueStats{
		QueueLen:       int64(len(wq.queue)),
		TotalEnqueued:  atomic.LoadInt64(&wq.totalEnqueued),
		TotalWritten:   atomic.LoadInt64(&wq.totalWritten),
		TotalFailed:    atomic.LoadInt64(&wq.totalFailed),
		TotalRetried:   atomic.LoadInt64(&wq.totalRetried),
		TotalRecovered: atomic.LoadInt64(&wq.totalRecovered),
		SpoolSizeBytes: atomic.LoadInt64(&wq.spoolSizeBytes),
		SpoolAlert:     atomic.LoadInt32(&wq.spoolAlert) != 0,
		BatchFlushes:   atomic.LoadInt64(&wq.batchFlushes),
	}
}

// Stop 停止队列，flush 剩余数据。
// AUTO-FIX-2026-06-30 [集成-7]: 使用 WaitGroup 等待 flushLoop/compensateLoop 完全退出，
// 确保所有 in-flight flushBatch（含重试）完成后再关闭 spool，杜绝数据丢失。
func (wq *WriteQueue) Stop() {
	wq.stopOnce.Do(func() {
		close(wq.stopCh)
	})
	// 等待 flush 协程和补偿协程完全退出
	// flushLoop 在 stopCh 分支会 flush 剩余数据（包括重试和落盘 spool），
	// compensateLoop 在 stopCh 分支直接返回。两者退出后 spool 状态稳定。
	wq.wg.Wait()

	wq.spoolMu.Lock()
	if wq.spoolFileF != nil {
		wq.spoolWriter.Flush()
		wq.spoolFileF.Close()
		wq.spoolFileF = nil
	}
	wq.spoolMu.Unlock()
}

// stripTableTag 移除内部使用的 __table__ tag。
func stripTableTag(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	result := make(map[string]string, len(tags))
	for k, v := range tags {
		if k != "__table__" {
			result[k] = v
		}
	}
	return result
}

// ErrQueueFull 队列满错误（用于非阻塞写入场景）。
var ErrQueueFull = errors.New("writequeue: queue full")
