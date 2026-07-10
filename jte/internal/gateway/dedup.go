package gateway

import "sync"

// SeqDedup 上行消息 SeqNum 去重器，基于环形缓冲区 + map 实现固定容量的近期 SeqNum 记忆。
//
// 背景：JT/T 808 终端在未收到平台 0x8001 应答时会重传相同 SeqNum 的消息，
// 导致重复的位置写入、报警触发等问题。SeqDedup 在 Session 级别记录近期 SeqNum，
// 检测到重复时通知调用方跳过业务处理（但仍发送 0x8001 应答避免终端继续重传）。
//
// 容量选择：终端重传超时通常 3-5s，重传 3 次。1Hz 位置上报下 200 条覆盖 200s，
// 远超重传窗口；0x0704 批量位置一次最多 200 条（0x0704 体限制），200 容量恰好覆盖。
// 容量满时淘汰最老的 SeqNum，保留最新的。
type SeqDedup struct {
	mu   sync.Mutex
	ring []uint16      // 固定大小环形缓冲区，存最近 N 个 SeqNum
	seen map[uint16]bool // 快速查找表
	size int           // 环形缓冲区容量
	head int           // 下一个写入位置
	full bool          // ring 是否已满（区分初始填充和环形覆写阶段）
}

// NewSeqDedup 创建指定容量的 SeqNum 去重器。size <= 0 时使用默认值 200。
func NewSeqDedup(size int) *SeqDedup {
	if size <= 0 {
		size = 200
	}
	return &SeqDedup{
		ring: make([]uint16, size),
		seen: make(map[uint16]bool, size),
		size: size,
	}
}

// IsDuplicate 检查 SeqNum 是否在近期记录中。
// 如果不存在，记录并返回 false（非重复）；如果已存在，返回 true（重复）。
// 线程安全：内部持锁，可被多个 goroutine 并发调用。
//
// 注意：本方法对"非重复"的 SeqNum 有副作用（会写入环形缓冲区并可能淘汰旧值）。
// 如需纯读取（如测试验证、监控查询），请使用 Contains。
func (d *SeqDedup) IsDuplicate(seq uint16) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.seen[seq] {
		return true
	}

	// 写入新 SeqNum
	if d.full {
		// 环形覆写：淘汰 head 位置的旧值
		old := d.ring[d.head]
		delete(d.seen, old)
	}
	d.ring[d.head] = seq
	d.seen[seq] = true

	d.head++
	if d.head >= d.size {
		d.head = 0
		d.full = true
	}
	return false
}

// Contains 检查 SeqNum 是否在近期记录中（纯读取，无副作用）。
// 仅供测试验证缓冲区状态和监控查询使用；去重路径应使用 IsDuplicate。
func (d *SeqDedup) Contains(seq uint16) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[seq]
}

// Size 返回当前已记录的 SeqNum 数量（供测试和监控使用）。
func (d *SeqDedup) Size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
