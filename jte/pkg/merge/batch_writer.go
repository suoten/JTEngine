package merge

import (
	"context"
	"sync"
	"time"

	"github.com/jte-engine/jte/internal/util"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-26: 第二轮链路修复 - 实现存储批量写入缓冲器
// 文档要求：批次大小 1000 条，超时 100ms 强制写入
// 原状态：BatchSaveLocations/BatchSaveAlarms 接口已实现但未被数据流调用，
//         merge.Engine 直接调用 SaveLocation 单条写入，性能低下。
// 修复：增加 BatchWriter 将单条写入聚合为批量写入。

const (
	defaultBatchSize    = 1000
	defaultFlushTimeout = 100 * time.Millisecond
)

// LocationBatchWriter 位置数据批处理写入器。
// 缓冲单条 LocationData，当达到 batchSize 或 flushTimeout 时触发 BatchSaveLocations。
type LocationBatchWriter struct {
	storage     storage.Interface
	logger      *zap.Logger
	batchSize   int
	flushTicker *time.Ticker
	buffer      []*storage.LocationData
	mu          sync.Mutex
	flushCh     chan struct{}
	stopCh      chan struct{}
	doneCh      chan struct{} // flushLoop 退出后关闭，用于 Stop 等待
	stopOnce    sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewLocationBatchWriter 创建位置批处理写入器。
// flushTimeout 为强制刷新间隔（建议 100ms），batchSize 为批次大小（建议 1000）。
func NewLocationBatchWriter(store storage.Interface, logger *zap.Logger, batchSize int, flushTimeout time.Duration) *LocationBatchWriter {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushTimeout <= 0 {
		flushTimeout = defaultFlushTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &LocationBatchWriter{
		storage:     store,
		logger:      logger,
		batchSize:   batchSize,
		flushTicker: time.NewTicker(flushTimeout),
		buffer:      make([]*storage.LocationData, 0, batchSize),
		flushCh:     make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	util.SafeGo(w.logger, "merge.locationBatchWriter.flushLoop", w.flushLoop)
	return w
}

// Add 添加一条位置数据到缓冲区。缓冲区满时自动触发刷新。
func (w *LocationBatchWriter) Add(loc *storage.LocationData) {
	w.mu.Lock()
	w.buffer = append(w.buffer, loc)
	full := len(w.buffer) >= w.batchSize
	w.mu.Unlock()

	if full {
		select {
		case w.flushCh <- struct{}{}:
		default:
		}
	}
}

// flushLoop 定时刷新循环，处理超时强制写入和满批次立即写入。
func (w *LocationBatchWriter) flushLoop() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.flushTicker.C:
			w.Flush()
		case <-w.flushCh:
			w.Flush()
		case <-w.stopCh:
			w.Flush()
			return
		}
	}
}

// Flush 将缓冲区数据批量写入存储。
func (w *LocationBatchWriter) Flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = make([]*storage.LocationData, 0, w.batchSize)
	w.mu.Unlock()

	if err := w.storage.BatchSaveLocations(w.ctx, batch); err != nil {
		w.logger.Error("batch save locations failed",
			zap.Int("batch_size", len(batch)),
			zap.Error(err))
	}
}

// Stop 停止批处理写入器，刷新剩余缓冲区。
// 阻塞等待 flushLoop goroutine 完成最终 flush，确保缓冲区数据不丢失。
func (w *LocationBatchWriter) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh // 等待 flushLoop 完成
	w.cancel()
	w.flushTicker.Stop()
}

// AlarmBatchWriter 报警数据批处理写入器。
type AlarmBatchWriter struct {
	storage     storage.Interface
	logger      *zap.Logger
	batchSize   int
	flushTicker *time.Ticker
	buffer      []*storage.AlarmData
	mu          sync.Mutex
	flushCh     chan struct{}
	stopCh      chan struct{}
	doneCh      chan struct{} // flushLoop 退出后关闭，用于 Stop 等待
	stopOnce    sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewAlarmBatchWriter 创建报警批处理写入器。
func NewAlarmBatchWriter(store storage.Interface, logger *zap.Logger, batchSize int, flushTimeout time.Duration) *AlarmBatchWriter {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushTimeout <= 0 {
		flushTimeout = defaultFlushTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &AlarmBatchWriter{
		storage:     store,
		logger:      logger,
		batchSize:   batchSize,
		flushTicker: time.NewTicker(flushTimeout),
		buffer:      make([]*storage.AlarmData, 0, batchSize),
		flushCh:     make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	util.SafeGo(w.logger, "merge.alarmBatchWriter.flushLoop", w.flushLoop)
	return w
}

// Add 添加一条报警数据到缓冲区。
func (w *AlarmBatchWriter) Add(alarm *storage.AlarmData) {
	w.mu.Lock()
	w.buffer = append(w.buffer, alarm)
	full := len(w.buffer) >= w.batchSize
	w.mu.Unlock()

	if full {
		select {
		case w.flushCh <- struct{}{}:
		default:
		}
	}
}

func (w *AlarmBatchWriter) flushLoop() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.flushTicker.C:
			w.Flush()
		case <-w.flushCh:
			w.Flush()
		case <-w.stopCh:
			w.Flush()
			return
		}
	}
}

// Flush 将缓冲区数据批量写入存储。
func (w *AlarmBatchWriter) Flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = make([]*storage.AlarmData, 0, w.batchSize)
	w.mu.Unlock()

	if err := w.storage.BatchSaveAlarms(w.ctx, batch); err != nil {
		w.logger.Error("batch save alarms failed",
			zap.Int("batch_size", len(batch)),
			zap.Error(err))
	}
}

// Stop 停止批处理写入器，刷新剩余缓冲区。
// 阻塞等待 flushLoop goroutine 完成最终 flush，确保缓冲区数据不丢失。
func (w *AlarmBatchWriter) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh // 等待 flushLoop 完成
	w.cancel()
	w.flushTicker.Stop()
}
