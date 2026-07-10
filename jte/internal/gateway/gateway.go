package gateway

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jte-engine/jte/internal/config"
	"github.com/jte-engine/jte/internal/metrics"
	"github.com/jte-engine/jte/internal/registry"
	"github.com/jte-engine/jte/internal/util"
	"github.com/jte-engine/jte/pkg/protocol"
	"github.com/jte-engine/jte/pkg/protocol/jt808"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type Session struct {
	ID         string
	Phone      string
	Protocol   protocol.ProtocolType
	Conn       net.Conn
	RemoteAddr string
	Status     string
	LastActive time.Time
	DeviceInfo *storage.Vehicle
	Metadata   map[string]interface{}
	seqNum     uint16    // 下行消息序号，受 mu 保护
	dedup      *SeqDedup // 上行消息 SeqNum 去重器，受 mu 保护（懒初始化）
	mu         sync.RWMutex
	logger     *zap.Logger
	// AUTO-FIX-2026-06-29 [P0-1]: 独立发送队列。
	// 每 session 一个 sendLoop goroutine 串行化所有下行写入，从根本上避免并发写冲突，
	// 并将写阻塞（5s 超时）与元数据访问（mu）解耦——原实现 Write/Send 在 conn.Write 期间
	// 持有 s.mu，慢写会阻塞 GetPhone/SetStatus 等元数据访问长达 5s。
	sendMu      sync.Mutex
	sendCh      chan *sendTask
	sendStop    chan struct{}
	sendStarted bool           // sendLoop 是否已启动（受 sendMu 保护）
	sendClosed  bool           // session 是否已关闭（受 sendMu 保护）
	sendWg      sync.WaitGroup // 等待 sendLoop 退出，避免 goroutine 泄漏
}

// sendTask 表示一次下行写入任务。
type sendTask struct {
	frame  []byte
	result chan error // 调用方等待写入结果（nil=成功）；为 nil 表示无需回传（fire-and-forget）
}

func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActive = time.Now()
}

func (s *Session) GetLastActive() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastActive
}

func (s *Session) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

func (s *Session) GetStatus() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status
}

// CheckDuplicate 检查上行消息 SeqNum 是否重复（去重）。
// 懒初始化 dedup（首次调用时创建 200 容量环形缓冲区），避免每次创建 Session 都分配。
// 返回 true 表示重复（调用方应跳过业务处理但仍发送 0x8001 应答），false 表示非重复（已记录）。
// 仅适用于 808 协议族（JT808/1078/1045/905/1253），809/32960 有各自的传输层应答机制。
func (s *Session) CheckDuplicate(seqNum uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dedup == nil {
		s.dedup = NewSeqDedup(200)
	}
	return s.dedup.IsDuplicate(seqNum)
}

// SetPhone 在 session.mu 保护下设置 Phone 字段。
// Phone 字段被 Register（踢旧会话时清空旧会话 Phone）与各处读取并发访问，
// 必须经此方法写入以避免数据竞争（string 赋值非原子，指针/长度可被撕裂）。
func (s *Session) SetPhone(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phone = phone
}

// GetPhone 在 session.mu 保护下读取 Phone 字段。
// 所有跨 goroutine 读取（HeartbeatChecker、memoryGuard、handleConn 超时日志、
// 消息处理回调）必须经此方法，避免与 Register 的 SetPhone 竞争。
func (s *Session) GetPhone() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Phone
}

// SetProtocol 在 session.mu 保护下设置 Protocol 字段。
// Protocol 在 handleConn 中首次探测后写入，后续消息路由也可能更新；
// API 层（platform.go）、存储层（sqlite.go）会并发读取，必须经此方法写入。
func (s *Session) SetProtocol(pt protocol.ProtocolType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Protocol = pt
}

// GetProtocol 在 session.mu 保护下读取 Protocol 字段。
// 跨 goroutine 读取（API/platform.go、storage/sqlite.go、handler.go）必须经此方法，
// 避免与 handleConn 的 SetProtocol 竞争（ProtocolType 为 string，赋值非原子）。
func (s *Session) GetProtocol() protocol.ProtocolType {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Protocol
}

// Write 向终端连接写入原始数据（已组帧的完整 808 报文）。
// 所有下行指令应通过此方法或 Send 发送，由 sendLoop 串行化写入，避免多 goroutine 并发写导致数据交错。
func (s *Session) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	s.mu.RLock()
	conn := s.Conn
	s.mu.RUnlock()
	if conn == nil {
		return 0, fmt.Errorf("session %s has no connection", s.ID)
	}
	// 复制一份再入队：调用方可能在入队后复用底层数组（如连接池场景），
	// sendLoop 异步写入时数据必须独立。
	frame := make([]byte, len(data))
	copy(frame, data)
	if err := s.enqueueSend(frame); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Send 高层封装：构造完整 808 消息（含转义）并通过发送队列写入。
// 写超时 5s，超时关闭连接以触发设备重连。返回编码或写入错误。
// AUTO-FIX-2026-06-30 [P0-1]: 委托给 SendWithSeq，丢弃分配的下行序号，
// 供 fire-and-forget 下行指令（0x8103/0x8300/0x9101/0x9203 等）使用。
func (s *Session) Send(msgID uint16, body []byte) error {
	_, err := s.SendWithSeq(msgID, body)
	return err
}

// SendWithSeq 构造完整 808 消息（含转义）并通过发送队列写入，返回分配的下行序号。
// 序号在 s.mu 保护下自增，保证同 session 下行序号单调且唯一。
// 写超时 5s，超时关闭连接以触发设备重连。
// 供需要按 SeqNum 匹配终端应答的调用方（如 CommandSender.SendAndWait）使用。
func (s *Session) SendWithSeq(msgID uint16, body []byte) (uint16, error) {
	s.mu.Lock()
	if s.Conn == nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("session %s has no connection", s.ID)
	}
	// 自增下行序号（受 mu 保护）
	s.seqNum++
	seq := s.seqNum
	phone := s.Phone
	s.mu.Unlock()

	// 构造完整 808 帧：header + body + 校验码，转义后加首尾分隔符（无需持锁）
	codec := jt808.NewCodec()
	header := &protocol.MessageHeader{
		MsgID:   msgID,
		Phone:   phone,
		SeqNum:  seq,
		BodyLen: len(body),
	}
	headerBytes, err := codec.EncodeHeader(header)
	if err != nil {
		return seq, fmt.Errorf("encode 808 header: %w", err)
	}
	raw := append(headerBytes, body...)
	checksum := jt808.CalcChecksum(raw)
	raw = append(raw, checksum)
	frame := jt808.WrapWithDelimiter(jt808.Escape(raw))

	return seq, s.enqueueSend(frame)
}

// enqueueSend 将一帧数据投递到发送队列，等待 sendLoop 写入完成并回传结果。
// 若发送队列已满（256 帧），调用方阻塞直到有空位——提供天然背压，防止下行指令堆积。
// 若 session 已关闭，立即返回错误。
func (s *Session) enqueueSend(frame []byte) error {
	s.ensureSendLoop()
	s.sendMu.Lock()
	ch := s.sendCh
	stop := s.sendStop
	closed := s.sendClosed
	s.sendMu.Unlock()
	if closed || ch == nil {
		return fmt.Errorf("session %s send queue unavailable", s.ID)
	}
	task := &sendTask{frame: frame, result: make(chan error, 1)}
	select {
	case ch <- task:
	case <-stop:
		return fmt.Errorf("session %s send queue closed", s.ID)
	}
	select {
	case err := <-task.result:
		return err
	case <-stop:
		return fmt.Errorf("session %s send queue closed", s.ID)
	}
}

// ensureSendLoop 懒启动 sendLoop goroutine（首次发送时创建 sendCh/sendStop）。
// 通过 sendMu + sendStarted/sendClosed 标志保证：
//   - sendLoop 只启动一次（sendStarted）
//   - 若 Close 先于首次发送执行，不再启动 goroutine（sendClosed）
func (s *Session) ensureSendLoop() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendClosed || s.sendStarted {
		return
	}
	s.sendCh = make(chan *sendTask, 256)
	s.sendStop = make(chan struct{})
	s.sendStarted = true
	s.sendWg.Add(1)
	util.SafeGo(s.logger, "gateway.sessionSendLoop:"+s.ID, func() {
		defer s.sendWg.Done()
		s.sendLoop()
	})
}

// sendLoop 串行化处理发送队列中的所有下行写入。
// 单 goroutine 消费 sendCh，从根本上避免并发写冲突；
// 写超时 5s，超时关闭连接触发设备重连。
// 退出时排空 sendCh 中残余任务并回传错误，避免调用方永久阻塞。
func (s *Session) sendLoop() {
	for {
		select {
		case <-s.sendStop:
			s.drainSendQueue(fmt.Errorf("session %s closed", s.ID))
			return
		case task := <-s.sendCh:
			err := s.writeFrame(task.frame)
			if task.result != nil {
				task.result <- err
			}
			// 写超时已关闭连接，后续任务必然失败，快速排空
			if err != nil && s.connClosed() {
				s.drainSendQueue(fmt.Errorf("session %s connection closed", s.ID))
				return
			}
		}
	}
}

// writeFrame 执行实际的网络写入，设 5s 写超时，超时关闭连接。
func (s *Session) writeFrame(frame []byte) error {
	s.mu.RLock()
	conn := s.Conn
	s.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("session %s has no connection", s.ID)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := conn.Write(frame); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// 写超时关闭连接触发设备重连
			s.mu.Lock()
			if s.Conn != nil {
				s.Conn.Close()
			}
			s.mu.Unlock()
			return fmt.Errorf("write timeout, connection closed: %w", err)
		}
		return err
	}
	return nil
}

// connClosed 检查连接是否已被关闭/移除。
func (s *Session) connClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Conn == nil
}

// drainSendQueue 排空发送队列中残余任务，向每个任务回传 err，避免调用方永久阻塞。
func (s *Session) drainSendQueue(err error) {
	for {
		select {
		case task := <-s.sendCh:
			if task.result != nil {
				task.result <- err
			}
		default:
			return
		}
	}
}

// Close 关闭 session：停止 sendLoop 并关闭底层连接。
// 幂等（多次调用安全）。供 SessionManager.Remove/Register 调用，确保发送协程不泄漏。
func (s *Session) Close() {
	s.sendMu.Lock()
	if !s.sendClosed {
		s.sendClosed = true
		if s.sendStop != nil {
			close(s.sendStop)
		}
	}
	s.sendMu.Unlock()
	s.mu.Lock()
	if s.Conn != nil {
		s.Conn.Close()
	}
	s.mu.Unlock()
	// 等待 sendLoop 退出，避免 goroutine 泄漏
	s.sendWg.Wait()
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	byConn   map[net.Conn]*Session
	byPhone  map[string]*Session
	logger   *zap.Logger
}

func NewSessionManager(logger *zap.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		byConn:   make(map[net.Conn]*Session),
		byPhone:  make(map[string]*Session),
		logger:   logger,
	}
}

// SetLogger 用于配置热重载时替换 logger
func (sm *SessionManager) SetLogger(logger *zap.Logger) {
	sm.logger = logger
}

func (sm *SessionManager) Create(id string, conn net.Conn) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := &Session{
		ID:         id,
		Conn:       conn,
		RemoteAddr: conn.RemoteAddr().String(),
		Status:     "connected",
		LastActive: time.Now(),
		Metadata:   make(map[string]interface{}),
		logger:     sm.logger,
	}

	sm.sessions[id] = session
	sm.byConn[conn] = session
	// AUTO-FIX-2026-06-30 [集成-7]: 在线设备数指标
	metrics.OnlineDevices.Set(float64(len(sm.sessions)))
	sm.logger.Info("session created", zap.String("id", id), zap.String("remote", session.RemoteAddr))
	return session
}

func (sm *SessionManager) Register(session *Session, phone string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	// AUTO-FIX-2026-06-30 [集成-7]: 日志统一携带 device_id 字段（phone 即 JT/T 808 设备标识）
	devLog := sm.deviceLogger(phone)
	// 单设备会话限制：同一手机号仅允许一个活跃会话，踢出旧会话
	if oldSession, ok := sm.byPhone[phone]; ok && oldSession != session {
		devLog.Warn("踢出旧会话",
			zap.String("old_session", oldSession.ID),
			zap.String("new_session", session.ID))
		if oldSession.Conn != nil {
			oldSession.Close()
		}
		// 清空旧会话 Phone 并移除索引，避免旧连接退出时 Remove 误删新会话索引
		oldSession.SetPhone("")
		delete(sm.sessions, oldSession.ID)
		delete(sm.byConn, oldSession.Conn)
	}
	session.SetPhone(phone)
	session.SetStatus("registered")
	sm.byPhone[phone] = session
	devLog.Info("session registered", zap.String("id", session.ID))
}

// deviceLogger 返回带 device_id 字段的 logger（AUTO-FIX-2026-06-30 [集成-7] 可观测性完善）。
// phone 即 JT/T 808 终端手机号，在本项目中作为设备唯一标识（device_id）。
// 同时保留 phone 字段以兼容现有日志解析工具链。
func (sm *SessionManager) deviceLogger(phone string) *zap.Logger {
	if sm.logger == nil || phone == "" {
		return sm.logger
	}
	return sm.logger.With(zap.String("device_id", phone), zap.String("phone", phone))
}

func (sm *SessionManager) Authenticate(phone string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, ok := sm.byPhone[phone]
	if !ok {
		return false
	}
	session.SetStatus("authenticated")
	session.UpdateActivity()
	sm.deviceLogger(phone).Info("session authenticated")
	return true
}

func (sm *SessionManager) Get(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}

func (sm *SessionManager) GetByConn(conn net.Conn) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.byConn[conn]
	return s, ok
}

func (sm *SessionManager) GetByPhone(phone string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.byPhone[phone]
	return s, ok
}

func (sm *SessionManager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, ok := sm.sessions[id]
	if !ok {
		return
	}
	delete(sm.sessions, id)
	delete(sm.byConn, session.Conn)
	phone := session.GetPhone()
	if phone != "" {
		delete(sm.byPhone, phone)
	}
	if session.Conn != nil {
		session.Close()
	}
	// AUTO-FIX-2026-06-30 [集成-7]: 在线设备数指标 + device_id 日志字段
	metrics.OnlineDevices.Set(float64(len(sm.sessions)))
	sm.deviceLogger(phone).Info("session removed", zap.String("id", id))
}

func (sm *SessionManager) List() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		result = append(result, s)
	}
	return result
}

func (sm *SessionManager) OnlineCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, s := range sm.sessions {
		if s.GetStatus() == "authenticated" {
			count++
		}
	}
	return count
}

func (sm *SessionManager) UpdateActivity(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[id]; ok {
		s.UpdateActivity()
	}
}

type TCPServer struct {
	cfg         *config.GatewayConfig
	logger      *zap.Logger
	listener    net.Listener
	listenerMu  sync.Mutex // 保护 listener 字段的读写，防止 acceptLoop 与 Stop 并发 nil panic
	sessions    *SessionManager
	protocol    *protocol.Hub
	storage     storage.Interface
	registry    *registry.FeatureRegistry
	running     atomic.Bool
	accepting   atomic.Bool // 是否继续接受新连接（优雅停机时置 false，现有连接继续服务）
	onMessage   func(session *Session, msg *protocol.Message)
	byIP        map[string]int // 单 IP 连接数统计
	ipLimit     int            // 单 IP 最大连接数（默认 1000）
	ipMu        sync.Mutex     // 保护 byIP
	memGuard    *memoryGuard   // OOM 防护
	authLimiter *tokenBucket   // 鉴权限流：令牌桶 1000/s
	// AUTO-FIX-2026-06-30 [P1-2]: 服务端重启退避窗口。
	// 启动后 backoffWindow 时间内，鉴权限流退避时间取更大值，分散重启后的鉴权风暴。
	startupTime   time.Time
	backoffWindow time.Duration
}

func NewTCPServer(
	cfg *config.GatewayConfig,
	logger *zap.Logger,
	sessions *SessionManager,
	protocolHub *protocol.Hub,
	store storage.Interface,
	reg *registry.FeatureRegistry,
) *TCPServer {
	return &TCPServer{
		cfg:         cfg,
		logger:      logger,
		sessions:    sessions,
		protocol:    protocolHub,
		storage:     store,
		registry:    reg,
		byIP:        make(map[string]int),
		ipLimit:     1000,
		memGuard:    newMemoryGuard(sessions, logger),
		authLimiter: newTokenBucket(1000, 1000), // 容量 1000，每秒填充 1000
	}
}

func (s *TCPServer) SetMessageHandler(handler func(session *Session, msg *protocol.Message)) {
	s.onMessage = handler
}

// SetLogger 用于配置热重载时替换 logger
func (s *TCPServer) SetLogger(logger *zap.Logger) {
	s.logger = logger
	if s.sessions != nil {
		s.sessions.SetLogger(logger)
	}
}

// SetSessionManager 用于运行时替换会话管理器
func (s *TCPServer) SetSessionManager(sm *SessionManager) {
	s.sessions = sm
}

func (s *TCPServer) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.TCPPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp listen on %s: %w", addr, err)
	}
	s.listenerMu.Lock()
	s.listener = listener
	s.listenerMu.Unlock()
	s.running.Store(true)
	s.accepting.Store(true)

	// AUTO-FIX-2026-06-30 [P1-2]: 记录启动时间，重启退避窗口 5 分钟
	s.startupTime = time.Now()
	s.backoffWindow = 5 * time.Minute

	// 启动 OOM 防护
	if s.memGuard != nil {
		s.memGuard.Start()
	}

	s.logger.Info("TCP server started", zap.String("addr", addr))
	util.SafeGo(s.logger, "gateway.acceptLoop", s.acceptLoop)
	return nil
}

// StopAccept 仅停止接受新连接（关闭 listener），保留现有连接继续服务。
// 用于优雅停机：先摘流，再等待现有连接完成，最后调用 Stop 强制关闭。
func (s *TCPServer) StopAccept() {
	s.accepting.Store(false)
	s.listenerMu.Lock()
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
	s.listenerMu.Unlock()
	s.logger.Info("TCP server stopped accepting new connections")
}

func (s *TCPServer) Stop() {
	s.accepting.Store(false)
	s.running.Store(false)
	s.listenerMu.Lock()
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
	s.listenerMu.Unlock()
	// 停止 OOM 防护
	if s.memGuard != nil {
		s.memGuard.Stop()
	}
	for _, session := range s.sessions.List() {
		if session.Conn != nil {
			session.Conn.Close()
		}
	}
	s.logger.Info("TCP server stopped")
}

func (s *TCPServer) acceptLoop() {
	defer func() {
		// 兜底：即便 SafeGo 已 recover，此处也确保 acceptLoop 退出时清理 listener
		s.listenerMu.Lock()
		if s.listener != nil {
			s.listener.Close()
			s.listener = nil
		}
		s.listenerMu.Unlock()
	}()
	for s.accepting.Load() {
		// 持锁快照 listener，避免与 Stop 的 close+nil 竞争导致 nil.Accept() panic
		s.listenerMu.Lock()
		ln := s.listener
		s.listenerMu.Unlock()
		if ln == nil {
			return
		}
		conn, err := ln.Accept()
		if err != nil {
			if !s.accepting.Load() {
				return
			}
			s.logger.Error("accept connection", zap.Error(err))
			continue
		}

		if s.sessions.OnlineCount() >= s.cfg.MaxConnections {
			s.logger.Warn("max connections reached, rejecting", zap.String("remote", conn.RemoteAddr().String()))
			conn.Close()
			continue
		}

		util.SafeGoWithRecover(s.logger, "gateway.handleConn", func(r interface{}) {
			// handleConn panic 时确保连接关闭，避免文件描述符泄漏
			_ = conn.Close()
		}, func() { s.handleConn(conn) })

		// AUTO-FIX-2026-06-30 [集成-7]: 连接计数指标
		metrics.ConnectionsTotal.Inc()
	}
}

func (s *TCPServer) handleConn(conn net.Conn) {
	// OOM 防护：内存满时拒绝新连接
	if s.memGuard != nil && s.memGuard.IsMemoryFull() {
		s.logger.Warn("内存不足，拒绝新连接", zap.String("remote", conn.RemoteAddr().String()))
		conn.Close()
		return
	}

	// 单 IP 连接数限制
	remoteIP := extractIP(conn.RemoteAddr().String())
	s.ipMu.Lock()
	if s.byIP[remoteIP] >= s.ipLimit {
		s.ipMu.Unlock()
		s.logger.Warn("单IP连接数超限，拒绝连接",
			zap.String("ip", remoteIP), zap.Int("limit", s.ipLimit))
		conn.Close()
		return
	}
	s.byIP[remoteIP]++
	s.ipMu.Unlock()
	// 连接关闭时减少 byIP 计数（含心跳超时踢出场景）
	defer func() {
		s.ipMu.Lock()
		s.byIP[remoteIP]--
		if s.byIP[remoteIP] <= 0 {
			delete(s.byIP, remoteIP)
		}
		s.ipMu.Unlock()
	}()

	sessionID := generateSessionID(conn)
	session := s.sessions.Create(sessionID, conn)
	// AUTO-FIX-2026-07-02 [可观测性]: 活跃连接数 Gauge
	metrics.ActiveConnections.Inc()
	defer func() {
		metrics.ActiveConnections.Dec()
		s.sessions.Remove(sessionID)
	}()

	// 慢连接检测：连接建立后 5s 内必须收到 0x0100 注册消息
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var frameBuf *protocol.FrameBuffer

	buf := make([]byte, 8192)
	for s.running.Load() {
		n, err := conn.Read(buf)
		if err != nil {
			if !s.running.Load() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// 根据会话状态记录超时原因
				switch session.GetStatus() {
				case "connected":
					s.logger.Warn("连接建立超时", zap.String("id", sessionID),
						zap.String("remote", session.RemoteAddr))
				case "registered":
					s.logger.Warn("鉴权超时", zap.String("id", sessionID),
						zap.String("phone", session.GetPhone()))
				case "authenticated":
					// P1-3: 鉴权后 30s 无数据超时（慢连接/空闲连接防护）
					s.logger.Warn("鉴权后无数据超时", zap.String("id", sessionID),
						zap.String("phone", session.GetPhone()))
				}
				return
			}
			s.logger.Debug("connection read error", zap.String("id", sessionID), zap.Error(err))
			return
		}

		if n > 0 {
			session.UpdateActivity()
			// AUTO-FIX-2026-06-30 [P1-3]: 鉴权后每次收到数据续期 30s 读超时，
			// 防止慢连接/空闲连接长期占用资源。未鉴权阶段超时由上方 5s/10s 控制。
			if session.GetStatus() == "authenticated" {
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			}
			data := make([]byte, n)
			copy(data, buf[:n])

			if frameBuf == nil {
				frameBuf = s.detectProtocol(data)
				if frameBuf == nil {
					s.logger.Warn("cannot detect protocol", zap.String("id", sessionID))
					continue
				}
				session.SetProtocol(frameBuf.GetProtocol())
			}

			frames := frameBuf.Feed(data)
			for _, frame := range frames {
				pt, msg, err := s.protocol.Route(frame)
				if err != nil {
					// AUTO-FIX-2026-07-02 [可观测性]: 解析错误计数指标
					metrics.MessageParseErrorsTotal.IncWithLabels(map[string]string{
						"protocol": string(pt),
						"error":    "route_failed",
					})
					s.logger.Warn("protocol route failed", zap.Error(err), zap.String("id", sessionID))
					continue
				}

				if pt != "" {
					session.SetProtocol(pt)
				}

				// 慢连接检测：根据消息类型推进读超时阶段
				if msg != nil {
					switch msg.Header.MsgID {
					case 0x0100: // JT/T 808 终端注册，收到后 10s 内必须完成鉴权
						conn.SetReadDeadline(time.Now().Add(10 * time.Second))
					case 0x0102: // JT/T 808 终端鉴权
						// AUTO-FIX-2026-06-30 [P1-2]: 鉴权限流：令牌桶 1000/s，超限下发 0x8001 应答码 0x01 + 随机退避时间
						if !s.authLimiter.Allow() {
							backoffSec := s.authBackoffSec()
							s.logger.Warn("鉴权限流，下发退避时间",
								zap.String("id", sessionID),
								zap.String("phone", msg.Header.Phone),
								zap.Int("backoff_sec", backoffSec))
							resp := BuildReconnectBackoffResp(msg.Header.Phone, msg.Header.SeqNum, backoffSec)
							if _, werr := conn.Write(resp); werr != nil {
								s.logger.Warn("发送鉴权退避应答失败",
									zap.String("id", sessionID), zap.Error(werr))
							}
							conn.Close()
							return
						}
						// AUTO-FIX-2026-06-30 [P1-3]: 鉴权后不取消读超时，改为 30s 周期超时。
						// 原实现 SetReadDeadline(time.Time{}) 取消超时，导致慢连接/空闲连接不会被检测。
						// 现改为 30s 超时，每次收到数据由下方的续期逻辑刷新，无数据 30s 后断开。
						conn.SetReadDeadline(time.Now().Add(30 * time.Second))
					}
				}

				if s.onMessage != nil {
					s.onMessage(session, msg)
				}
			}
		}
	}
}

func (s *TCPServer) detectProtocol(data []byte) *protocol.FrameBuffer {
	if len(data) == 0 {
		return nil
	}

	switch {
	case data[0] == 0x7E:
		s.logger.Debug("detected JT/T 808/1078/905/1253 protocol")
		return protocol.NewFrameBuffer(protocol.ProtocolJT808)
	case data[0] == 0x5B:
		s.logger.Debug("detected JT/T 809 protocol")
		return protocol.NewFrameBuffer(protocol.ProtocolJT809)
	case len(data) >= 2 && data[0] == 0x23 && data[1] == 0x23:
		s.logger.Debug("detected GB/T 32960 protocol")
		return protocol.NewFrameBuffer(protocol.ProtocolGBT32960)
	default:
		return nil
	}
}

func generateSessionID(conn net.Conn) string {
	return fmt.Sprintf("%s-%d", conn.RemoteAddr().String(), time.Now().UnixNano())
}

// extractIP 从远程地址中提取 IP 部分
func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

type HeartbeatChecker struct {
	interval time.Duration
	timeout  time.Duration
	sessions *SessionManager
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once // 保证 Stop 多次调用只 close 一次 stopCh，避免 panic
	// AUTO-FIX-2026-06-30 [P1-5]: 心跳超时资源清理回调。
	// 超时时由调用方注入的 hook 完成资源清理：更新车辆离线状态、撤销鉴权码、
	// 停止视频流、释放缓冲区、记录离线日志。hook 内禁止长时间阻塞（在 checker 协程中执行）。
	onTimeout func(*Session)
}

func NewHeartbeatChecker(interval, timeout time.Duration, sessions *SessionManager, logger *zap.Logger) *HeartbeatChecker {
	return &HeartbeatChecker{
		interval: interval,
		timeout:  timeout,
		sessions: sessions,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// SetTimeoutHook 注入心跳超时资源清理回调（P1-5）。
// hook 在心跳超时、连接关闭前被调用，负责：更新车辆离线状态、撤销鉴权码、
// 停止设备视频流、释放缓冲区、记录离线日志等。
func (h *HeartbeatChecker) SetTimeoutHook(hook func(*Session)) {
	h.onTimeout = hook
}

func (h *HeartbeatChecker) Start() {
	util.SafeGo(h.logger, "gateway.heartbeatChecker", h.checkLoop)
	h.logger.Info("heartbeat checker started",
		zap.Duration("interval", h.interval),
		zap.Duration("timeout", h.timeout))
}

func (h *HeartbeatChecker) Stop() {
	h.stopOnce.Do(func() { close(h.stopCh) })
}

func (h *HeartbeatChecker) checkLoop() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.checkSessions()
		}
	}
}

func (h *HeartbeatChecker) checkSessions() {
	now := time.Now()
	for _, session := range h.sessions.List() {
		lastActive := session.GetLastActive()
		if now.Sub(lastActive) > h.timeout {
			// AUTO-FIX-2026-06-30 [集成-7]: 心跳超时日志携带 device_id 字段
			phone := session.GetPhone()
			logger := h.logger
			if phone != "" {
				logger = logger.With(zap.String("device_id", phone), zap.String("phone", phone))
			}
			logger.Warn("session heartbeat timeout",
				zap.String("id", session.ID),
				zap.Time("last_active", lastActive))
			// AUTO-FIX-2026-06-30 [P1-5]: 心跳超时资源清理。
			// 先执行资源清理回调（更新离线状态、撤销鉴权码、停视频流、记录日志），
			// 再关闭连接。关闭后 handleConn 的 defer sessions.Remove 会触发 SessionManager 移除。
			if h.onTimeout != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							h.logger.Error("心跳超时清理回调 panic",
								zap.Any("panic", r),
								zap.String("id", session.ID),
								zap.String("phone", session.GetPhone()))
						}
					}()
					h.onTimeout(session)
				}()
			}
			if session.Conn != nil {
				session.Conn.Close()
			}
		}
	}
}

type Limiter struct {
	maxDevices int
	registry   *registry.FeatureRegistry
}

func NewLimiter(maxDevices int, reg *registry.FeatureRegistry) *Limiter {
	return &Limiter{
		maxDevices: maxDevices,
		registry:   reg,
	}
}

func (l *Limiter) AllowRegister(currentCount int) bool {
	if l.registry.HasFeature(registry.FeatureUnlimited) {
		return true
	}
	return currentCount < l.maxDevices
}

func (l *Limiter) GetLimitInfo() (max int, unlimited bool) {
	if l.registry.HasFeature(registry.FeatureUnlimited) {
		return 0, true
	}
	return l.maxDevices, false
}

// tokenBucket 令牌桶，线程安全。capacity 为容量，rate 为每秒填充速率。
type tokenBucket struct {
	mu        sync.Mutex
	tokens    float64
	maxTokens float64
	rate      float64
	lastTime  time.Time
}

// newTokenBucket 创建令牌桶，初始令牌数等于容量。
func newTokenBucket(capacity int, rate int) *tokenBucket {
	return &tokenBucket{
		tokens:    float64(capacity),
		maxTokens: float64(capacity),
		rate:      float64(rate),
		lastTime:  time.Now(),
	}
}

// Allow 取一个令牌，有可用令牌返回 true，否则 false。
func (tb *tokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastTime = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// authBackoffSec 返回鉴权限流退避时间（秒）。
// AUTO-FIX-2026-06-30 [P1-2]: 鉴权风暴防护——退避时间 0-300s 随机，分散重连。
// 服务端重启窗口期（backoffWindow，默认 5 分钟）内取 60-300s 较大值，
// 窗口期外取 10-120s 较小值，避免正常运行时退避过久。
func (s *TCPServer) authBackoffSec() int {
	var lo, hi int
	if s.backoffWindow > 0 && time.Since(s.startupTime) < s.backoffWindow {
		lo, hi = 60, 300 // 重启窗口期：较大退避
	} else {
		lo, hi = 10, 120 // 正常运行期：较小退避
	}
	// crypto/rand 生成 [lo, hi) 随机整数
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand 失败回退到固定值（仍优于硬编码 60s 的可预测性——此处用中值）
		return (lo + hi) / 2
	}
	// 用前 4 字节构造 uint32，映射到 [lo, hi) 区间
	u := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return lo + int(u%uint32(hi-lo))
}

// BuildReconnectBackoffResp 构造 0x8001 通用应答（携带重连退避时间）。
// 应答码=0x01（失败），body 末尾追加 4 字节退避时间（秒）。
// 服务端重启后可广播此消息让设备分散重连。
func BuildReconnectBackoffResp(phone string, seq uint16, backoffSec int) []byte {
	// body: RespSeqNum(2) + RespMsgID(2) + Result(1) + BackoffTime(4)
	body := make([]byte, 9)
	binary.BigEndian.PutUint16(body[0:2], seq)             // 应答的终端消息序号
	binary.BigEndian.PutUint16(body[2:4], jt808.MsgIDAuth) // 0x0102
	body[4] = 0x01                                         // 失败
	binary.BigEndian.PutUint32(body[5:9], uint32(backoffSec))

	codec := jt808.NewCodec()
	header := &protocol.MessageHeader{
		MsgID:   jt808.MsgIDGeneralResp, // 0x8001
		Phone:   phone,
		SeqNum:  0, // 平台下行序号
		BodyLen: len(body),
	}
	headerBytes, _ := codec.EncodeHeader(header)

	raw := append(headerBytes, body...)
	checksum := jt808.CalcChecksum(raw)
	raw = append(raw, checksum)
	return jt808.WrapWithDelimiter(jt808.Escape(raw))
}

// memoryGuard OOM 防护，定期检测内存使用并按阈值执行踢出策略
type memoryGuard struct {
	mu         sync.RWMutex
	sysMB      uint64 // 最近一次采样的系统分配内存（MB）
	memWarnMB  uint64 // 8GB 预警：拒绝新连接
	memCritMB  uint64 // 9GB 告警：踢出最旧非活跃连接
	memFatalMB uint64 // 9.5GB 自保：踢出 20% 最旧连接
	sessions   *SessionManager
	logger     *zap.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once // 保证 Stop 多次调用只 close 一次 stopCh，避免 panic
}

// newMemoryGuard 创建 OOM 防护器，阈值默认 8192/9216/9728 MB
func newMemoryGuard(sessions *SessionManager, logger *zap.Logger) *memoryGuard {
	return &memoryGuard{
		memWarnMB:  8192,
		memCritMB:  9216,
		memFatalMB: 9728,
		sessions:   sessions,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

// Start 启动后台内存检测协程
func (m *memoryGuard) Start() {
	util.SafeGo(m.logger, "gateway.memoryGuard", m.loop)
	m.logger.Info("memory guard started",
		zap.Uint64("warn_mb", m.memWarnMB),
		zap.Uint64("crit_mb", m.memCritMB),
		zap.Uint64("fatal_mb", m.memFatalMB))
}

// Stop 停止内存检测
func (m *memoryGuard) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// loop 每 5s 读取 MemStats 并检查阈值
func (m *memoryGuard) loop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.check()
		}
	}
}

// check 读取内存统计并按阈值执行相应策略
func (m *memoryGuard) check() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	sysMB := ms.Sys / (1024 * 1024)

	m.mu.Lock()
	m.sysMB = sysMB
	warn, crit, fatal := m.memWarnMB, m.memCritMB, m.memFatalMB
	m.mu.Unlock()

	switch {
	case sysMB >= fatal:
		m.logger.Error("内存达到自保阈值，踢出20%最旧连接",
			zap.Uint64("sys_mb", sysMB), zap.Uint64("threshold_mb", fatal))
		m.evictOldest(0.2)
	case sysMB >= crit:
		m.logger.Error("内存达到告警阈值，踢出最旧非活跃连接",
			zap.Uint64("sys_mb", sysMB), zap.Uint64("threshold_mb", crit))
		m.evictOldestInactive()
	case sysMB >= warn:
		m.logger.Warn("内存达到预警阈值，拒绝新连接",
			zap.Uint64("sys_mb", sysMB), zap.Uint64("threshold_mb", warn))
	}
}

// IsMemoryFull 判断内存是否达到预警阈值，供 handleConn 调用决定是否拒绝新连接
func (m *memoryGuard) IsMemoryFull() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sysMB >= m.memWarnMB
}

// evictOldest 按最后活跃时间排序，踢出最旧的 fraction 比例连接
func (m *memoryGuard) evictOldest(fraction float64) {
	sessions := m.sessions.List()
	if len(sessions) == 0 {
		return
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].GetLastActive().Before(sessions[j].GetLastActive())
	})
	n := int(float64(len(sessions)) * fraction)
	if n < 1 {
		n = 1
	}
	for i := 0; i < n && i < len(sessions); i++ {
		s := sessions[i]
		m.logger.Warn("内存压力踢出连接",
			zap.String("id", s.ID), zap.String("phone", s.GetPhone()),
			zap.Time("last_active", s.GetLastActive()))
		if s.Conn != nil {
			s.Conn.Close()
		}
	}
}

// evictOldestInactive 踢出最旧的非活跃（未鉴权）连接，无则踢出最旧连接
func (m *memoryGuard) evictOldestInactive() {
	sessions := m.sessions.List()
	if len(sessions) == 0 {
		return
	}
	var oldest *Session
	for _, s := range sessions {
		if s.GetStatus() != "authenticated" {
			if oldest == nil || s.GetLastActive().Before(oldest.GetLastActive()) {
				oldest = s
			}
		}
	}
	if oldest == nil {
		for _, s := range sessions {
			if oldest == nil || s.GetLastActive().Before(oldest.GetLastActive()) {
				oldest = s
			}
		}
	}
	if oldest != nil {
		m.logger.Warn("内存压力踢出最旧非活跃连接",
			zap.String("id", oldest.ID), zap.String("phone", oldest.GetPhone()),
			zap.Time("last_active", oldest.GetLastActive()))
		if oldest.Conn != nil {
			oldest.Conn.Close()
		}
	}
}
