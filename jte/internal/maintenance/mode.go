package maintenance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jte-engine/jte/internal/util"
	"go.uber.org/zap"
)

// maintenanceBufferCapacity 维护模式内存队列容量上限（100 万条）
const maintenanceBufferCapacity = 1_000_000

// defaultSpoolPath 维护模式缓冲落盘文件路径
const defaultSpoolPath = "./spool/maintenance_buffer.jsonl"

type Mode struct {
	mu        sync.RWMutex
	active    bool
	reason    string
	startedAt time.Time
	configDir string
	logger    *zap.Logger

	// AUTO-FIX-2026-06-30 [P2-6]: stopWrites 区分两种维护级别。
	// false（默认）= 仅停止查询，写入继续（DB 在线维护）；
	// true = 停止写入，终端数据入内存队列（100万条）+ spool 落盘（DB 升级/迁移）。
	stopWrites bool

	// 维护模式数据缓冲（v3.0 A.6.5）
	buffer         *MaintenanceBuffer // 维护期间写入此队列，维护结束后回放
	replayCallback func(row interface{}) error
	replaying      atomic.Bool // 回放进行中标志（true 时拒绝新的维护模式启动）

	// AUTO-FIX-2026-06-30 [P2-6]: 维护模式启停通知回调。
	// 业务层注入：Start 时广播 0x8103 通知终端"暂停上报"，Stop 时广播恢复。
	notifyStartCallback func(reason string) // 维护开始 → 通知终端暂停上报
	notifyStopCallback  func()              // 维护结束 → 通知终端恢复上报
}

type MaintenanceStatus struct {
	Active     bool      `json:"active"`
	Reason     string    `json:"reason"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	StopWrites bool      `json:"stop_writes"` // 是否停止写入（true=数据入缓冲队列）
}

func NewMode(configDir string, logger *zap.Logger) *Mode {
	m := &Mode{
		configDir: configDir,
		logger:    logger,
	}
	m.load()
	return m
}

// Start 启动维护模式。
// AUTO-FIX-2026-06-30 [P2-6]:
//   - stopWrites=false：仅停止查询，写入继续（DB 在线维护，如加索引）
//   - stopWrites=true：停止写入，终端数据入内存队列（100万条）+ spool 落盘（DB 升级/迁移）
//   - 启动时调用 notifyStartCallback 广播 0x8103 通知终端"暂停上报"（仅 stopWrites=true 时）
func (m *Mode) Start(reason string, stopWrites bool) error {
	m.mu.Lock()

	if m.active {
		m.mu.Unlock()
		return fmt.Errorf("maintenance mode already active (started at %s)", m.startedAt.Format("2006-01-02 15:04:05"))
	}

	// 回放未完成时拒绝新的维护模式启动
	if m.replaying.Load() {
		m.mu.Unlock()
		return fmt.Errorf("maintenance buffer replay in progress, cannot start maintenance mode")
	}

	m.active = true
	m.reason = reason
	m.startedAt = time.Now()
	m.stopWrites = stopWrites

	// 仅在停止写入时启用数据缓冲（内存队列，容量 100 万条）
	if stopWrites {
		m.buffer = NewMaintenanceBuffer(maintenanceBufferCapacity, m.logger)
	}

	// 捕获通知回调（锁内读取，锁外调用避免死锁）
	notifyStart := m.notifyStartCallback
	m.mu.Unlock()

	if err := m.save(); err != nil {
		m.mu.Lock()
		m.active = false
		m.buffer = nil
		m.stopWrites = false
		m.mu.Unlock()
		return fmt.Errorf("save maintenance state: %w", err)
	}

	// 通知终端暂停上报（仅停止写入时需要，查询维护不影响终端上报）
	if stopWrites && notifyStart != nil {
		util.SafeGo(m.logger, "maintenance.notifyStart", func() {
			notifyStart(reason)
			m.logger.Info("broadcasted 0x8103 pause-reporting to terminals",
				zap.String("reason", reason))
		})
	}

	m.logger.Info("maintenance mode started",
		zap.String("reason", reason),
		zap.Time("started_at", m.startedAt),
		zap.Bool("stop_writes", stopWrites),
		zap.Int("buffer_capacity", maintenanceBufferCapacity))

	return nil
}

func (m *Mode) Stop() error {
	m.mu.Lock()

	if !m.active {
		m.mu.Unlock()
		return fmt.Errorf("maintenance mode not active")
	}

	wasStopWrites := m.stopWrites
	m.active = false
	m.reason = ""
	m.startedAt = time.Time{}
	m.stopWrites = false

	// 捕获通知回调（锁内读取，锁外调用避免死锁）
	notifyStop := m.notifyStopCallback

	if err := m.save(); err != nil {
		m.logger.Error("failed to save maintenance state after stop", zap.Error(err))
	}

	// 后台协程回放队列/spool 到 TDengine（回放完成前拒绝新的维护模式启动）
	buf := m.buffer
	m.buffer = nil
	if buf != nil {
		m.replaying.Store(true)
		m.mu.Unlock()
		util.SafeGo(m.logger, "maintenance.replayBuffer", func() { m.replayBuffer(buf) })
	} else {
		m.mu.Unlock()
	}

	// 通知终端恢复上报（仅之前是停止写入模式时需要）
	if wasStopWrites && notifyStop != nil {
		util.SafeGo(m.logger, "maintenance.notifyStop", func() {
			notifyStop()
			m.logger.Info("broadcasted 0x8103 resume-reporting to terminals")
		})
	}

	m.logger.Info("maintenance mode stopped")
	return nil
}

// replayBuffer 后台回放缓冲数据，调用方注入的 replayCallback 写入 TDengine
func (m *Mode) replayBuffer(buf *MaintenanceBuffer) {
	defer m.replaying.Store(false)
	cb := m.replayCallback
	if cb == nil {
		// 未注入回调：仅清空缓冲，避免数据堆积；落盘保留供人工处理
		if err := buf.FlushTo(defaultSpoolPath); err != nil {
			m.logger.Warn("flush maintenance buffer to spool failed (no replay callback)", zap.Error(err))
		}
		m.logger.Warn("replay callback not set, buffer flushed to spool only")
		return
	}
	if err := buf.Replay(cb); err != nil {
		m.logger.Error("replay maintenance buffer failed, falling back to spool", zap.Error(err))
		// 回放失败：落盘保留
		if flushErr := buf.FlushTo(defaultSpoolPath); flushErr != nil {
			m.logger.Error("flush maintenance buffer to spool failed", zap.Error(flushErr))
		}
		return
	}
	m.logger.Info("maintenance buffer replay completed")
}

// SetReplayCallback 注入回放回调（写入 TDengine），由 main.go 在初始化时调用
func (m *Mode) SetReplayCallback(cb func(row interface{}) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replayCallback = cb
}

// SetNotifyCallbacks 注入维护模式启停通知回调（P2-6）。
// onStart：维护开始时广播 0x8103 通知终端"暂停上报"
// onStop：维护结束时广播 0x8103 通知终端"恢复上报"
func (m *Mode) SetNotifyCallbacks(onStart func(reason string), onStop func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyStartCallback = onStart
	m.notifyStopCallback = onStop
}

// Buffer 返回当前维护模式缓冲队列；非维护模式期间返回 nil
func (m *Mode) Buffer() *MaintenanceBuffer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buffer
}

// ShouldBuffer 判断当前写入是否应进入缓冲队列。
// AUTO-FIX-2026-06-30 [P2-6]: 仅 stopWrites=true 的维护模式才缓冲数据；
// stopWrites=false（查询维护）时写入继续，不影响数据落盘。
func (m *Mode) ShouldBuffer() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active && m.stopWrites
}

func (m *Mode) IsActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

func (m *Mode) GetStatus() MaintenanceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MaintenanceStatus{
		Active:     m.active,
		Reason:     m.reason,
		StartedAt:  m.startedAt,
		StopWrites: m.stopWrites,
	}
}

func (m *Mode) save() error {
	if m.configDir == "" {
		return nil
	}

	_ = os.MkdirAll(m.configDir, 0700)

	status := MaintenanceStatus{
		Active:     m.active,
		Reason:     m.reason,
		StartedAt:  m.startedAt,
		StopWrites: m.stopWrites,
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(m.configDir, "maintenance.json"), data, 0600)
}

func (m *Mode) load() {
	if m.configDir == "" {
		return
	}

	data, err := os.ReadFile(filepath.Join(m.configDir, "maintenance.json"))
	if err != nil {
		return
	}

	var status MaintenanceStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return
	}

	m.active = status.Active
	m.reason = status.Reason
	m.startedAt = status.StartedAt
	m.stopWrites = status.StopWrites
	// 重启后若处于 stopWrites 维护模式，重建缓冲队列
	if m.active && m.stopWrites {
		m.buffer = NewMaintenanceBuffer(maintenanceBufferCapacity, m.logger)
	}
}

// ===================================================================
// MaintenanceBuffer 维护模式数据缓冲（v3.0 A.6.5）
// 维护期间终端上报数据先入内存队列（容量 100 万条），
// 队列满 → 落盘 ./spool/maintenance_buffer.jsonl，
// 维护结束后回放队列/spool 到 TDengine。
// ===================================================================

// MaintenanceBuffer 维护模式数据缓冲队列
type MaintenanceBuffer struct {
	mu       sync.Mutex
	queue    []interface{} // 内存队列
	capacity int           // 队列容量上限
	spooled  int64         // 已落盘条数（仅用于日志统计）
	logger   *zap.Logger
}

// NewMaintenanceBuffer 创建维护模式缓冲队列
func NewMaintenanceBuffer(capacity int, logger *zap.Logger) *MaintenanceBuffer {
	if capacity <= 0 {
		capacity = maintenanceBufferCapacity
	}
	return &MaintenanceBuffer{
		queue:    make([]interface{}, 0, capacity/16+1),
		capacity: capacity,
		logger:   logger,
	}
}

// Push 写入一行数据；队列满时自动落盘到 spool 文件
func (b *MaintenanceBuffer) Push(row interface{}) error {
	b.mu.Lock()
	if len(b.queue) < b.capacity {
		b.queue = append(b.queue, row)
		b.mu.Unlock()
		return nil
	}
	// 队列满 → 取出当前队列准备落盘
	queueCopy := b.queue
	b.queue = make([]interface{}, 0, b.capacity/16+1)
	b.mu.Unlock()

	// 落盘（不持锁，避免长时间阻塞 Pop/Replay）
	if err := b.appendRowsToSpool(queueCopy, defaultSpoolPath); err != nil {
		// 落盘失败：数据放回队列头部，调用方应感知（返回 error）
		b.mu.Lock()
		b.queue = append(queueCopy, b.queue...)
		if len(b.queue) > b.capacity {
			// 仍超限：截断尾部，避免无限增长（极端场景丢数据并记录）
			dropped := len(b.queue) - b.capacity
			b.queue = b.queue[:b.capacity]
			if b.logger != nil {
				b.logger.Error("maintenance buffer overflow, data dropped",
					zap.Int("dropped", dropped))
			}
		}
		b.mu.Unlock()
		return fmt.Errorf("flush to spool failed: %w", err)
	}

	// 落盘成功：把当前 row 入队
	b.mu.Lock()
	b.queue = append(b.queue, row)
	b.mu.Unlock()
	atomic.AddInt64(&b.spooled, int64(len(queueCopy)))
	return nil
}

// Pop 从队列头部取出一行；队列空返回 false
func (b *MaintenanceBuffer) Pop() (interface{}, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.queue) == 0 {
		return nil, false
	}
	row := b.queue[0]
	b.queue = b.queue[1:]
	return row, true
}

// FlushTo 将内存队列剩余数据落盘到 spoolPath（JSONL 格式）
func (b *MaintenanceBuffer) FlushTo(spoolPath string) error {
	b.mu.Lock()
	queueCopy := b.queue
	b.queue = make([]interface{}, 0, b.capacity/16+1)
	b.mu.Unlock()

	if len(queueCopy) == 0 {
		return nil
	}
	return b.appendRowsToSpool(queueCopy, spoolPath)
}

// appendRowsToSpool 将多行数据以 JSONL 追加写入 spool 文件
func (b *MaintenanceBuffer) appendRowsToSpool(rows []interface{}, spoolPath string) error {
	if spoolPath == "" {
		spoolPath = defaultSpoolPath
	}
	if err := os.MkdirAll(filepath.Dir(spoolPath), 0750); err != nil {
		return fmt.Errorf("create spool dir: %w", err)
	}
	f, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("open spool file: %w", err)
	}
	defer f.Close()

	writer := bufio.NewWriter(f)
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			if b.logger != nil {
				b.logger.Warn("marshal spool row failed, skip", zap.Error(err))
			}
			continue
		}
		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("write spool row: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("write spool newline: %w", err)
		}
	}
	return writer.Flush()
}

// Replay 回放队列与 spool 文件中的数据，逐条调用 callback 写入 TDengine
// 回放顺序：先内存队列，后 spool 文件
func (b *MaintenanceBuffer) Replay(callback func(row interface{}) error) error {
	if callback == nil {
		return fmt.Errorf("replay callback is nil")
	}

	// 1. 回放内存队列
	b.mu.Lock()
	queueCopy := b.queue
	b.queue = make([]interface{}, 0, b.capacity/16+1)
	b.mu.Unlock()

	for _, row := range queueCopy {
		if err := callback(row); err != nil {
			// 回放失败：把当前行放回队列头部，避免数据丢失
			b.mu.Lock()
			b.queue = append([]interface{}{row}, b.queue...)
			b.mu.Unlock()
			if b.logger != nil {
				b.logger.Error("replay queue row failed, row returned to queue",
					zap.Error(err))
			}
			return fmt.Errorf("replay queue row: %w", err)
		}
	}

	// 2. 回放 spool 文件（如果存在）
	if err := b.replaySpool(defaultSpoolPath, callback); err != nil {
		return fmt.Errorf("replay spool: %w", err)
	}

	// 3. 回放成功后清空 spool 文件
	_ = os.Remove(defaultSpoolPath)
	if b.logger != nil {
		b.logger.Info("maintenance buffer replay completed",
			zap.Int("queued", len(queueCopy)),
			zap.Int64("spooled_total", atomic.LoadInt64(&b.spooled)))
	}
	return nil
}

// replaySpool 逐行读取 spool 文件并回放
func (b *MaintenanceBuffer) replaySpool(spoolPath string, callback func(row interface{}) error) error {
	f, err := os.Open(spoolPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 无 spool 文件，正常
		}
		return fmt.Errorf("open spool file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 单行上限 1MB（位置/报警行通常 <1KB）
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row interface{}
		if err := json.Unmarshal(line, &row); err != nil {
			if b.logger != nil {
				b.logger.Warn("unmarshal spool row failed, skip", zap.Error(err))
			}
			continue
		}
		if err := callback(row); err != nil {
			return fmt.Errorf("replay spool row: %w", err)
		}
	}
	return scanner.Err()
}
