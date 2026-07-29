package jt808

import (
	"fmt"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/protocol"
)

// PacketReassembler 808 分包重组器。
// JT/T 808 允许大消息分多包传输，每包在消息头中携带 PackTotal/PackIndex。
// 收齐同一组的所有分片后，按序号拼接 body，还原完整消息。
//
// AUTO-FIX-2026-07-02 [P1-1.1.3]: 内存泄漏防护
// - maxGroups: 最大待重组组数，防止攻击者创建无限组
// - maxFragmentSize: 单个分片最大字节数，防止超大分片耗尽内存
// - maxTotalBytes: 所有待重组组的总字节上限，全局内存保护
type PacketReassembler struct {
	mu              sync.Mutex
	groups          map[string]*packetGroup
	expiry          time.Duration // 分片组过期时间，超时自动清理
	maxGroups       int           // 最大待重组组数
	maxFragmentSize int           // 单个分片最大字节数
	maxTotalBytes   int           // 所有待重组组总字节上限
	totalBytes      int           // 当前所有待重组组的总字节数
}

type packetGroup struct {
	msgID     uint16
	phone     string
	seqNum    uint16
	total     uint16
	fragments map[uint16][]byte // PackIndex -> body
	createdAt time.Time
}

// 默认内存保护参数
const (
	defaultMaxGroups       = 10000           // 最大 1 万个待重组组
	defaultMaxFragmentSize = 1 * 1024 * 1024 // 单分片最大 1MB
	defaultMaxTotalBytes   = 100 * 1024 * 1024 // 全部待重组组最大 100MB
)

// NewPacketReassembler 创建分包重组器，expiry 为分片组最长保留时间。
func NewPacketReassembler(expiry time.Duration) *PacketReassembler {
	r := &PacketReassembler{
		groups:          make(map[string]*packetGroup),
		expiry:          expiry,
		maxGroups:       defaultMaxGroups,
		maxFragmentSize: defaultMaxFragmentSize,
		maxTotalBytes:   defaultMaxTotalBytes,
	}
	util.SafeGo(nil, "jt808.reassembler.cleanupLoop", r.cleanupLoop)
	return r
}

// groupKey 分片组的唯一标识：手机号+消息ID+流水号
// 同一终端的同一消息（同SeqNum）的分片归为一组
func groupKey(phone string, msgID uint16, seqNum uint16) string {
	return fmt.Sprintf("%s_%04X_%d", phone, msgID, seqNum)
}

// Feed 投递一个分片。
// 如果该分片所属组尚未收齐，返回 (nil, false, nil)。
// 如果收齐所有分片，返回拼接后的完整 body 和 true。
// 如果不是分片消息（HasPack=false），直接返回原始 body 和 true。
func (r *PacketReassembler) Feed(header *protocol.MessageHeader, body []byte) ([]byte, bool, error) {
	if !header.HasPack {
		return body, true, nil
	}

	key := groupKey(header.Phone, header.MsgID, header.SeqNum)

	r.mu.Lock()
	defer r.mu.Unlock()

	// AUTO-FIX-2026-07-02 [P1-1.1.3]: 分片大小检查
	if len(body) > r.maxFragmentSize {
		return nil, false, fmt.Errorf("fragment too large: %d bytes (max %d)", len(body), r.maxFragmentSize)
	}

	// AUTO-FIX-2026-07-02 [P1-1.1.3]: 全局内存上限检查
	if r.totalBytes+len(body) > r.maxTotalBytes {
		// 驱逐最旧的组直到有足够空间
		for r.totalBytes+len(body) > r.maxTotalBytes && len(r.groups) > 0 {
			r.evictOldestGroup()
		}
	}

	group, ok := r.groups[key]
	if !ok {
		// AUTO-FIX-2026-07-02 [P1-1.1.3]: 组数量上限检查
		if len(r.groups) >= r.maxGroups {
			// 清理最旧的组以腾出空间
			r.evictOldestGroup()
		}
		// 首个分片
		if header.PackTotal == 0 {
			return nil, false, fmt.Errorf("invalid pack total: 0")
		}
		if header.PackIndex >= header.PackTotal {
			return nil, false, fmt.Errorf("pack index %d out of range (total %d)", header.PackIndex, header.PackTotal)
		}
		group = &packetGroup{
			msgID:     header.MsgID,
			phone:     header.Phone,
			seqNum:    header.SeqNum,
			total:     header.PackTotal,
			fragments: make(map[uint16][]byte),
			createdAt: time.Now(),
		}
		r.groups[key] = group
	}

	// 存储分片（深拷贝 body 避免外部修改）
	if header.PackIndex >= group.total {
		return nil, false, fmt.Errorf("pack index %d out of range (total %d)", header.PackIndex, group.total)
	}
	if _, exists := group.fragments[header.PackIndex]; exists {
		// 重复分片，忽略
		return nil, false, nil
	}
 fragCopy := make([]byte, len(body))
 copy(fragCopy, body)
	group.fragments[header.PackIndex] = fragCopy
	r.totalBytes += len(fragCopy) // AUTO-FIX-2026-07-02 [P1-1.1.3]: 跟踪总字节数

	// 检查是否收齐
	if uint16(len(group.fragments)) < group.total {
		return nil, false, nil
	}

	// 收齐，按 PackIndex 顺序拼接
	totalLen := 0
	for i := uint16(0); i < group.total; i++ {
		totalLen += len(group.fragments[i])
	}
	complete := make([]byte, 0, totalLen)
	for i := uint16(0); i < group.total; i++ {
		complete = append(complete, group.fragments[i]...)
	}

	// 清理已完成的组
	delete(r.groups, key)
	r.totalBytes -= totalLen // AUTO-FIX-2026-07-02 [P1-1.1.3]: 回收字节数
	if r.totalBytes < 0 {
		r.totalBytes = 0 // 防御性：避免计数错误导致负值
	}

	return complete, true, nil
}

// PendingCount 返回当前待重组的分片组数量
func (r *PacketReassembler) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.groups)
}

// cleanupLoop 定期清理过期的分片组，避免终端异常断线导致内存泄漏
func (r *PacketReassembler) cleanupLoop() {
	ticker := time.NewTicker(r.expiry / 2)
	if r.expiry < 2*time.Second {
		ticker = time.NewTicker(time.Second)
	}
	defer ticker.Stop()

	for range ticker.C {
		// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 Cleanup panic
		// 不会导致清理协程退出。SafeGo 的 recover 在 goroutine 级别，
		// panic 后协程退出，过期分片组永不被清理，内存泄漏。
		func() {
			defer func() {
				if r := recover(); r != nil {
					// SafeGo 已有 recover，此处二级 recover 确保循环不退出
				}
			}()
			r.Cleanup()
		}()
	}
}

// Cleanup 主动扫描并清除过期的分片组，返回被清理的组数量。
// 由 cleanupLoop 周期调用；测试与运维路径也可手动触发，避免依赖 ticker 时序。
func (r *PacketReassembler) Cleanup() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	removed := 0
	for key, group := range r.groups {
		if now.Sub(group.createdAt) > r.expiry {
			// 回收字节数
			for _, frag := range group.fragments {
				r.totalBytes -= len(frag)
			}
			delete(r.groups, key)
			removed++
		}
	}
	if r.totalBytes < 0 {
		r.totalBytes = 0
	}
	return removed
}

// evictOldestGroup 驱逐最旧的分片组以腾出空间（调用方需持有锁）
// AUTO-FIX-2026-07-02 [P1-1.1.3]: 内存泄漏防护
func (r *PacketReassembler) evictOldestGroup() {
	var oldestKey string
	var oldestTime time.Time
	for key, group := range r.groups {
		if oldestKey == "" || group.createdAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = group.createdAt
		}
	}
	if oldestKey != "" {
		group := r.groups[oldestKey]
		for _, frag := range group.fragments {
			r.totalBytes -= len(frag)
		}
		delete(r.groups, oldestKey)
	}
}
