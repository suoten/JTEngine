package jt1078

// AUTO-FIX-2026-07-02 [P1]: RTP 转发长连接池
//
// 设计目标（用户验收标准：1000 并发 RTP 转发，连接复用率 > 90%）：
//  1. UDP/TCP 连接按 ZLMediaKit 接收端口复用，避免每包 net.Dial + Close
//  2. 连接数上限 maxConns（默认 4096），超限时按 LRU 淘汰最久未用连接
//  3. 原子计数 hits/misses，暴露 ReuseRate() 供压力测试与运维监控
//  4. 空闲超过 idleTimeout（默认 5 分钟）的连接由后台协程自动关闭
//  5. 同时管理 UDP/TCP 两种连接，对外提供 GetUDP/GetTCP/PutActive/Close/Stats
//
// 线程安全：所有 map 操作在单 mutex 下完成；连接写入由调用方保证串行
// （ZLMediaKit 的每个 RTP 端口对应单条 socket，写串行由 ForwardRTP 调用频率保证）。

import (
	"container/list"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// RTPConnPool RTP 转发长连接池，统一管理 UDP/TCP 连接复用。
type RTPConnPool struct {
	zlmAddr     string
	idleTimeout time.Duration
	maxConns    int // 连接数上限（UDP+TCP 合计），0 表示不限

	logger *zap.Logger

	mu          sync.Mutex
	udpConns    map[int]*pooledUDPEntry // key: ZLM 接收端口
	tcpConns    map[int]*pooledTCPEntry
	lru         *list.List // *lruNode，按最近活跃时间排序，front=最久未用
	lruIndex    map[string]*list.Element
	udpLastActive map[int]time.Time
	tcpLastActive map[int]time.Time

	// 原子计数（无锁读取，高性能）
	hits   atomic.Int64 // 命中已有连接（复用）
	misses atomic.Int64 // 新建连接
	evicts atomic.Int64 // LRU 淘汰次数
	idleCloses atomic.Int64 // 空闲关闭次数

	stopCh chan struct{}
}

type pooledUDPEntry struct {
	conn *net.UDPConn
}

type pooledTCPEntry struct {
	conn net.Conn
}

type lruNode struct {
	key      string // "udp:port" | "tcp:port"
	protocol string
	port     int
}

// NewRTPConnPool 创建 RTP 长连接池。
// idleTimeout: 连接空闲超时（默认 5 分钟）；maxConns: 连接数上限（0=不限）。
func NewRTPConnPool(zlmAddr string, idleTimeout time.Duration, maxConns int, logger *zap.Logger) *RTPConnPool {
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}
	if maxConns < 0 {
		maxConns = 0
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	p := &RTPConnPool{
		zlmAddr:       zlmAddr,
		idleTimeout:   idleTimeout,
		maxConns:      maxConns,
		logger:        logger,
		udpConns:      make(map[int]*pooledUDPEntry),
		tcpConns:      make(map[int]*pooledTCPEntry),
		lru:           list.New(),
		lruIndex:      make(map[string]*list.Element),
		udpLastActive: make(map[int]time.Time),
		tcpLastActive: make(map[int]time.Time),
		stopCh:        make(chan struct{}),
	}
	return p
}

// StartSweep 启动后台空闲扫描协程（每 30 秒扫描一次）。
func (p *RTPConnPool) StartSweep() {
	go p.sweepLoop()
}

// Stop 关闭池中所有连接并停止后台扫描。
func (p *RTPConnPool) Stop() {
	select {
	case <-p.stopCh:
		// 已关闭
		return
	default:
		close(p.stopCh)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for port, e := range p.udpConns {
		e.conn.Close()
		delete(p.udpConns, port)
		delete(p.udpLastActive, port)
	}
	for port, e := range p.tcpConns {
		e.conn.Close()
		delete(p.tcpConns, port)
		delete(p.tcpLastActive, port)
	}
	p.lru.Init()
	p.lruIndex = make(map[string]*list.Element)
}

// GetUDP 获取或创建到指定 ZLM 端口的 UDP 长连接。
// 命中池返回复用连接（hits++），否则新建（misses++）。
func (p *RTPConnPool) GetUDP(port int) (*net.UDPConn, error) {
	p.mu.Lock()
	if e, ok := p.udpConns[port]; ok {
		p.udpLastActive[port] = time.Now()
		p.touchLRU("udp", port)
		p.mu.Unlock()
		p.hits.Add(1)
		return e.conn, nil
	}
	p.mu.Unlock()

	addr := &net.UDPAddr{IP: net.ParseIP(p.zlmAddr), Port: port}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s:%d: %w", p.zlmAddr, port, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// 双检：并发场景下可能已创建——此时复用已有连接，计为 hit（而非 miss）
	if e, ok := p.udpConns[port]; ok {
		conn.Close()
		p.udpLastActive[port] = time.Now()
		p.touchLRU("udp", port)
		p.hits.Add(1)
		return e.conn, nil
	}
	// 仅在真正创建新连接时计 miss
	p.misses.Add(1)
	p.evictIfNeededLocked(1)
	p.udpConns[port] = &pooledUDPEntry{conn: conn}
	p.udpLastActive[port] = time.Now()
	p.touchLRU("udp", port)
	p.logger.Debug("rtp pool: udp conn created",
		zap.String("addr", p.zlmAddr), zap.Int("port", port))
	return conn, nil
}

// GetTCP 获取或创建到指定 ZLM 端口的 TCP 长连接。
func (p *RTPConnPool) GetTCP(port int) (net.Conn, error) {
	p.mu.Lock()
	if e, ok := p.tcpConns[port]; ok {
		p.tcpLastActive[port] = time.Now()
		p.touchLRU("tcp", port)
		p.mu.Unlock()
		p.hits.Add(1)
		return e.conn, nil
	}
	p.mu.Unlock()

	addr := net.JoinHostPort(p.zlmAddr, fmt.Sprintf("%d", port))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", addr, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// 双检：并发场景下可能已创建——此时复用已有连接，计为 hit（而非 miss）
	if e, ok := p.tcpConns[port]; ok {
		conn.Close()
		p.tcpLastActive[port] = time.Now()
		p.touchLRU("tcp", port)
		p.hits.Add(1)
		return e.conn, nil
	}
	// 仅在真正创建新连接时计 miss
	p.misses.Add(1)
	p.evictIfNeededLocked(1)
	p.tcpConns[port] = &pooledTCPEntry{conn: conn}
	p.tcpLastActive[port] = time.Now()
	p.touchLRU("tcp", port)
	p.logger.Debug("rtp pool: tcp conn created",
		zap.String("addr", addr))
	return conn, nil
}

// InvalidateUDP 移除并关闭指定端口的 UDP 连接（连接失效时调用，如写入失败）。
func (p *RTPConnPool) InvalidateUDP(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.udpConns[port]; ok {
		e.conn.Close()
		delete(p.udpConns, port)
		delete(p.udpLastActive, port)
		p.removeLRU("udp", port)
	}
}

// InvalidateTCP 移除并关闭指定端口的 TCP 连接。
func (p *RTPConnPool) InvalidateTCP(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.tcpConns[port]; ok {
		e.conn.Close()
		delete(p.tcpConns, port)
		delete(p.tcpLastActive, port)
		p.removeLRU("tcp", port)
	}
}

// ReuseRate 返回连接复用率（0-1）：hits / (hits + misses)。
// 压力测试验收标准：> 0.90。
func (p *RTPConnPool) ReuseRate() float64 {
	h, m := p.hits.Load(), p.misses.Load()
	total := h + m
	if total == 0 {
		return 0
	}
	return float64(h) / float64(total)
}

// PoolStats 连接池统计快照。
type PoolStats struct {
	UDPConns    int     `json:"udp_conns"`
	TCPConns    int     `json:"tcp_conns"`
	Hits        int64   `json:"hits"`
	Misses      int64   `json:"misses"`
	Evicts      int64   `json:"evicts"`
	IdleCloses  int64   `json:"idle_closes"`
	ReuseRate   float64 `json:"reuse_rate"`
	MaxConns    int     `json:"max_conns"`
}

// Stats 返回连接池统计快照。
func (p *RTPConnPool) Stats() PoolStats {
	p.mu.Lock()
	udpN, tcpN := len(p.udpConns), len(p.tcpConns)
	p.mu.Unlock()
	h, m := p.hits.Load(), p.misses.Load()
	total := h + m
	var rate float64
	if total > 0 {
		rate = float64(h) / float64(total)
	}
	return PoolStats{
		UDPConns:   udpN,
		TCPConns:   tcpN,
		Hits:       h,
		Misses:     m,
		Evicts:     p.evicts.Load(),
		IdleCloses: p.idleCloses.Load(),
		ReuseRate:  rate,
		MaxConns:   p.maxConns,
	}
}

// sweepLoop 后台定期扫描空闲连接。
func (p *RTPConnPool) sweepLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.sweepIdle()
		}
	}
}

// sweepIdle 关闭空闲超期的连接。
func (p *RTPConnPool) sweepIdle() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for port, last := range p.udpLastActive {
		if now.Sub(last) >= p.idleTimeout {
			if e, ok := p.udpConns[port]; ok {
				e.conn.Close()
				delete(p.udpConns, port)
				p.idleCloses.Add(1)
				p.logger.Debug("rtp pool: udp conn idle closed", zap.Int("port", port))
			}
			delete(p.udpLastActive, port)
			p.removeLRU("udp", port)
		}
	}
	for port, last := range p.tcpLastActive {
		if now.Sub(last) >= p.idleTimeout {
			if e, ok := p.tcpConns[port]; ok {
				e.conn.Close()
				delete(p.tcpConns, port)
				p.idleCloses.Add(1)
				p.logger.Debug("rtp pool: tcp conn idle closed", zap.Int("port", port))
			}
			delete(p.tcpLastActive, port)
			p.removeLRU("tcp", port)
		}
	}
}

// evictIfNeededLocked 当连接总数达到上限时，按 LRU 淘汰最久未用连接。
// reserved: 本次将要新增的连接数，需为淘汰预留空间。
func (p *RTPConnPool) evictIfNeededLocked(reserved int) {
	if p.maxConns <= 0 {
		return
	}
	current := len(p.udpConns) + len(p.tcpConns)
	for current+reserved > p.maxConns {
		elem := p.lru.Front()
		if elem == nil {
			break
		}
		node := elem.Value.(*lruNode)
		p.removeLRU(node.protocol, node.port)
		switch node.protocol {
		case "udp":
			if e, ok := p.udpConns[node.port]; ok {
				e.conn.Close()
				delete(p.udpConns, node.port)
				delete(p.udpLastActive, node.port)
			}
		case "tcp":
			if e, ok := p.tcpConns[node.port]; ok {
				e.conn.Close()
				delete(p.tcpConns, node.port)
				delete(p.tcpLastActive, node.port)
			}
		}
		p.evicts.Add(1)
		current--
		p.logger.Debug("rtp pool: LRU evict", zap.String("proto", node.protocol), zap.Int("port", node.port))
	}
}

// touchLRU 将连接移到 LRU 尾部（最近使用）。调用方需持锁。
func (p *RTPConnPool) touchLRU(protocol string, port int) {
	key := lruKey(protocol, port)
	if elem, ok := p.lruIndex[key]; ok {
		p.lru.MoveToBack(elem)
		return
	}
	node := &lruNode{key: key, protocol: protocol, port: port}
	elem := p.lru.PushBack(node)
	p.lruIndex[key] = elem
}

// removeLRU 从 LRU 移除指定连接。调用方需持锁。
func (p *RTPConnPool) removeLRU(protocol string, port int) {
	key := lruKey(protocol, port)
	if elem, ok := p.lruIndex[key]; ok {
		p.lru.Remove(elem)
		delete(p.lruIndex, key)
	}
}

func lruKey(protocol string, port int) string {
	return fmt.Sprintf("%s:%d", protocol, port)
}
