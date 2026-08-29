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

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/metrics"
	"github.com/suoten/jt-engine/internal/registry"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/protocol"
	"github.com/suoten/jt-engine/pkg/protocol/jt808"
	"github.com/suoten/jt-engine/pkg/storage"
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

// SendQueueDepth 返回当前发送队列深度（INDUSTRIAL-FIX-2026-07-24: 慢客户端监控）。
// 队列深度持续高说明客户端写入缓慢，可能需要踢出以释放资源。
func (s *Session) SendQueueDepth() int {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendCh == nil {
		return 0
	}
	return len(s.sendCh)
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
	// R44-FIX-2026-07-26 [P2]: 保护 logger 字段的并发读写。
	// SetLogger 在配置热重载时写入，deviceLogger/Create 等并发读取，
	// 不加锁会产生数据竞争（*zap.Logger 指针赋值非原子）。
	// 使用独立 mutex 而非 sm.mu，避免 deviceLogger 在 sm.mu.Lock 上下文中调用时的重入死锁。
	loggerMu sync.RWMutex
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
// R44-FIX-2026-07-26 [P2]: 使用 loggerMu 保护 logger 字段的并发读写，
// 防止与 deviceLogger/Create 等读取 goroutine 的数据竞争。
func (sm *SessionManager) SetLogger(logger *zap.Logger) {
	sm.loggerMu.Lock()
	defer sm.loggerMu.Unlock()
	sm.logger = logger
}

func (sm *SessionManager) Create(id string, conn net.Conn) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// R44-FIX-2026-07-26 [P2]: 通过 getLogger() 安全读取 logger 快照
	session := &Session{
		ID:         id,
		Conn:       conn,
		RemoteAddr: conn.RemoteAddr().String(),
		Status:     "connected",
		LastActive: time.Now(),
		Metadata:   make(map[string]interface{}),
		logger:     sm.getLogger(),
	}

	sm.sessions[id] = session
	sm.byConn[conn] = session
	// AUTO-FIX-2026-06-30 [集成-7]: 在线设备数指标
	metrics.OnlineDevices.Set(float64(len(sm.sessions)))
	// R44-FIX-2026-07-26 [P2]: 通过 getLogger() 安全读取
	sm.getLogger().Info("session created", zap.String("id", id), zap.String("remote", session.RemoteAddr))
	return session
}

func (sm *SessionManager) Register(session *Session, phone string) {
	// R38-FIX-2026-07-26 [P1]: 将 oldSession.Close() 移到锁外执行。
	// Close() 内部调用 sendWg.Wait() 等待 sendLoop 退出，最长阻塞 5s（写超时）。
	// 原实现在 sm.mu.Lock() 内调用 Close()，导致高并发重连场景下
	// 所有会话操作（Create/Get/Remove/Register）被阻塞长达 5s。
	var oldSessionToClose *Session

	sm.mu.Lock()
	// AUTO-FIX-2026-06-30 [集成-7]: 日志统一携带 device_id 字段（phone 即 JT/T 808 设备标识）
	devLog := sm.deviceLogger(phone)
	// 单设备会话限制：同一手机号仅允许一个活跃会话，踢出旧会话
	if oldSession, ok := sm.byPhone[phone]; ok && oldSession != session {
		devLog.Warn("踢出旧会话",
			zap.String("old_session", oldSession.ID),
			zap.String("new_session", session.ID))
		// 清空旧会话 Phone 并移除索引，避免旧连接退出时 Remove 误删新会话索引
		oldSession.SetPhone("")
		delete(sm.sessions, oldSession.ID)
		if oldSession.Conn != nil {
			delete(sm.byConn, oldSession.Conn)
		}
		oldSessionToClose = oldSession
	}
	session.SetPhone(phone)
	session.SetStatus("registered")
	sm.byPhone[phone] = session
	sm.mu.Unlock()

	// 锁外关闭旧会话：sendWg.Wait() 可能阻塞 5s，不能持锁等待
	if oldSessionToClose != nil {
		oldSessionToClose.Close()
	}
	devLog.Info("session registered", zap.String("id", session.ID))
}

// deviceLogger 返回带 device_id 字段的 logger（AUTO-FIX-2026-06-30 [集成-7] 可观测性完善）。
// phone 即 JT/T 808 终端手机号，在本项目中作为设备唯一标识（device_id）。
// 同时保留 phone 字段以兼容现有日志解析工具链。
// getLogger 在 loggerMu 保护下读取 logger 快照（线程安全）。
func (sm *SessionManager) getLogger() *zap.Logger {
	sm.loggerMu.RLock()
	defer sm.loggerMu.RUnlock()
	return sm.logger
}

func (sm *SessionManager) deviceLogger(phone string) *zap.Logger {
	l := sm.getLogger()
	if l == nil || phone == "" {
		return l
	}
	return l.With(zap.String("device_id", phone), zap.String("phone", phone))
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
	// R38-FIX-2026-07-26 [P1]: 将 session.Close() 移到锁外执行。
	// Close() 内部调用 sendWg.Wait() 等待 sendLoop 退出，最长阻塞 5s（写超时）。
	// 原实现在 sm.mu.Lock() 内调用 Close()，导致高并发断连场景下
	// 所有会话操作被阻塞长达 5s。
	var sessionToClose *Session

	sm.mu.Lock()
	session, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return
	}
	delete(sm.sessions, id)
	if session.Conn != nil {
		delete(sm.byConn, session.Conn)
	}
	phone := session.GetPhone()
	if phone != "" {
		delete(sm.byPhone, phone)
	}
	if session.Conn != nil {
		sessionToClose = session
	}
	// AUTO-FIX-2026-06-30 [集成-7]: 在线设备数指标
	metrics.OnlineDevices.Set(float64(len(sm.sessions)))
	sm.mu.Unlock()

	// 锁外关闭会话：sendWg.Wait() 可能阻塞 5s，不能持锁等待
	if sessionToClose != nil {
		sessionToClose.Close()
	}
	sm.deviceLogger(phone).Info("session removed", zap.String("id", id))
}

// RemoveIfStale 条件清理指定手机号的会话登记（AUTO-FIX-2026-08-29 [P0-2]）。
// 供故障转移会话迁移钩子使用：故障节点失效后，本节点可能残留该终端的
// 未清理登记。仅清理"非活跃"会话（状态非 authenticated）；
// 终端已在本节点重连（authenticated）时不动，避免误杀健康连接。
// 返回是否发生删除以及遇到的会话状态（无登记时 status 为空）。
func (sm *SessionManager) RemoveIfStale(phone string) (removed bool, status string) {
	var sessionToClose *Session
	var id string

	sm.mu.Lock()
	session, ok := sm.byPhone[phone]
	if !ok {
		sm.mu.Unlock()
		return false, ""
	}
	status = session.GetStatus()
	if status == "authenticated" {
		// 终端在本节点活跃：重连先于故障通知到达，跳过
		sm.mu.Unlock()
		return false, status
	}
	// 复用 Remove 的删除语义（按 phone 定位，按 id 删除）
	id = session.ID
	delete(sm.sessions, id)
	if session.Conn != nil {
		delete(sm.byConn, session.Conn)
	}
	delete(sm.byPhone, phone)
	if session.Conn != nil {
		sessionToClose = session
	}
	metrics.OnlineDevices.Set(float64(len(sm.sessions)))
	sm.mu.Unlock()

	if sessionToClose != nil {
		sessionToClose.Close()
	}
	sm.deviceLogger(phone).Info("stale session removed (failover migration)",
		zap.String("id", id),
		zap.String("last_status", status))
	return true, status
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
	// R44-FIX-2026-07-26 [P2]: 保护 logger 字段的并发读写。
	// SetLogger 在配置热重载时写入，acceptLoop/handleConn 等并发读取。
	loggerMu    sync.RWMutex
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
	ipLimit     int            // 单 IP 最大连接数（默认 100）
	ipMu        sync.Mutex     // 保护 byIP
	memGuard    *memoryGuard   // OOM 防护
	authLimiter *tokenBucket   // 鉴权限流：令牌桶 1000/s
	// FIXED-2026-07-22 [P0]: 初始认证超时（连接建立后须在此时间内完成注册+鉴权）
	initialAuthTimeout time.Duration
	// FIXED-2026-07-22 [P0]: 单 IP 连接速率限制器
	ipRateLimiter *ipConnRateLimiter
	// INDUSTRIAL-FIX-2026-07-24: 每会话消息洪水检测限流器
	msgRateLimiter *sessionMsgRateLimiter
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
	// FIXED-2026-07-22 [P0]: 从配置读取初始认证超时和单 IP 连接限制
	ipLimit := 1000 // 向后兼容默认值
	if cfg != nil && cfg.MaxConnsPerIP > 0 {
		ipLimit = cfg.MaxConnsPerIP
	}
	authTimeout := 30 * time.Second // 默认 30s
	if cfg != nil && cfg.InitialAuthTimeout > 0 {
		authTimeout = time.Duration(cfg.InitialAuthTimeout) * time.Second
	}
	connRatePerIP := 50 // 默认 50/s
	if cfg != nil && cfg.MaxConnRatePerIP > 0 {
		connRatePerIP = cfg.MaxConnRatePerIP
	}
	return &TCPServer{
		cfg:                cfg,
		logger:             logger,
		sessions:           sessions,
		protocol:           protocolHub,
		storage:            store,
		registry:           reg,
		byIP:               make(map[string]int),
		ipLimit:            ipLimit,
		memGuard:           newMemoryGuard(sessions, logger),
		authLimiter:        newTokenBucket(1000, 1000), // 容量 1000，每秒填充 1000
		initialAuthTimeout: authTimeout,
		ipRateLimiter:      newIPConnRateLimiter(connRatePerIP, time.Second),
		msgRateLimiter:     newSessionMsgRateLimiter(1000, time.Second), // 每会话每秒最多 1000 条消息
	}
}

func (s *TCPServer) SetMessageHandler(handler func(session *Session, msg *protocol.Message)) {
	s.onMessage = handler
}

// SetLogger 用于配置热重载时替换 logger
// R44-FIX-2026-07-26 [P2]: 使用 loggerMu 保护并发读写，防止数据竞争。
func (s *TCPServer) SetLogger(logger *zap.Logger) {
	s.loggerMu.Lock()
	s.logger = logger
	s.loggerMu.Unlock()
	if s.sessions != nil {
		s.sessions.SetLogger(logger)
	}
}

// getLogger 在 loggerMu 保护下读取 logger 快照（线程安全）。
func (s *TCPServer) getLogger() *zap.Logger {
	s.loggerMu.RLock()
	defer s.loggerMu.RUnlock()
	return s.logger
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

	// FIXED-2026-07-23 [P2]: 启动 IP 连接速率限制器后台清理
	if s.ipRateLimiter != nil {
		s.ipRateLimiter.StartCleanup()
	}

	s.getLogger().Info("TCP server started", zap.String("addr", addr))
	util.SafeGo(s.getLogger(), "gateway.acceptLoop", s.acceptLoop)
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
	s.getLogger().Info("TCP server stopped accepting new connections")
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
	// FIXED-2026-07-23 [P2]: 停止 IP 连接速率限制器后台清理
	if s.ipRateLimiter != nil {
		s.ipRateLimiter.StopCleanup()
	}
	// INDUSTRIAL-FIX-2026-07-24: 停止每会话消息限流器后台清理
	if s.msgRateLimiter != nil {
		s.msgRateLimiter.StopCleanup()
	}
	// R44-FIX-2026-07-26 [P1]: 使用 session.Close() 替代直接访问 session.Conn。
	// 原实现 session.Conn 直接字段访问无锁保护，与 Session.Close() 的 s.mu.Lock 下
	// s.Conn = nil 形成数据竞争（Go race detector 会报错）。
	// session.Close() 内部在 s.mu 保护下安全关闭 Conn，并停止 sendLoop，避免 goroutine 泄漏。
	for _, session := range s.sessions.List() {
		session.Close()
	}
	s.getLogger().Info("TCP server stopped")
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
			s.getLogger().Error("accept connection", zap.Error(err))
			continue
		}

		if s.sessions.OnlineCount() >= s.cfg.MaxConnections {
			s.getLogger().Warn("max connections reached, rejecting", zap.String("remote", conn.RemoteAddr().String()))
			conn.Close()
			continue
		}

		util.SafeGoWithRecover(s.getLogger(), "gateway.handleConn", func(r interface{}) {
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
		s.getLogger().Warn("内存不足，拒绝新连接", zap.String("remote", conn.RemoteAddr().String()))
		conn.Close()
		return
	}

	// 单 IP 连接数限制
	remoteIP := extractIP(conn.RemoteAddr().String())
	s.ipMu.Lock()
	if s.byIP[remoteIP] >= s.ipLimit {
		s.ipMu.Unlock()
		s.getLogger().Warn("单IP连接数超限，拒绝连接",
			zap.String("ip", remoteIP), zap.Int("limit", s.ipLimit))
		conn.Close()
		return
	}
	s.byIP[remoteIP]++
	s.ipMu.Unlock()

	// FIXED-2026-07-22 [P0]: 单 IP 连接速率限制（滑动窗口）
	if s.ipRateLimiter != nil && !s.ipRateLimiter.Allow(remoteIP) {
		s.getLogger().Warn("单IP连接速率超限，拒绝连接",
			zap.String("ip", remoteIP))
		conn.Close()
		// 连接被拒，回退 byIP 计数
		s.ipMu.Lock()
		s.byIP[remoteIP]--
		if s.byIP[remoteIP] <= 0 {
			delete(s.byIP, remoteIP)
		}
		s.ipMu.Unlock()
		return
	}

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

	// FIXED-2026-07-22 [P0]: 使用可配置的 initialAuthTimeout（默认 30s）
	// 连接建立后须在 initialAuthTimeout 内完成注册+鉴权，超时关闭连接并记录日志。
	authTimeout := s.initialAuthTimeout
	if authTimeout == 0 {
		authTimeout = 30 * time.Second
	}
	conn.SetReadDeadline(time.Now().Add(authTimeout))

	var frameBuf *protocol.FrameBuffer

	// [P2-4] 读缓冲从 8192 扩大到 65536，支持大帧（如 1078 视频帧、809 批量报文）
	buf := make([]byte, 65536)
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
					s.getLogger().Warn("连接建立超时", zap.String("id", sessionID),
						zap.String("remote", session.RemoteAddr))
				case "registered":
					s.getLogger().Warn("鉴权超时", zap.String("id", sessionID),
						zap.String("phone", session.GetPhone()))
				case "authenticated":
					// P1-3: 鉴权后 30s 无数据超时（慢连接/空闲连接防护）
					s.getLogger().Warn("鉴权后无数据超时", zap.String("id", sessionID),
						zap.String("phone", session.GetPhone()))
				}
				return
			}
			s.getLogger().Debug("connection read error", zap.String("id", sessionID), zap.Error(err))
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
					s.getLogger().Warn("cannot detect protocol", zap.String("id", sessionID))
					continue
				}
				session.SetProtocol(frameBuf.GetProtocol())
			}

			frames := frameBuf.Feed(data)
			for _, frame := range frames {
				// INDUSTRIAL-FIX-2026-07-24: 每会话消息洪水检测
				// 单连接每秒超过 1000 条消息视为洪水攻击，断开连接
				if !s.msgRateLimiter.Allow(sessionID) {
					phone := session.GetPhone()
					logger := s.logger
					if phone != "" {
						logger = logger.With(zap.String("device_id", phone), zap.String("phone", phone))
					}
					logger.Warn("message flood detected, closing connection",
						zap.String("id", sessionID))
					conn.Close()
					return
				}
				metrics.MessagesPerSession.IncWithLabels(map[string]string{"session_id": sessionID})

				pt, msg, err := s.protocol.Route(frame)
				if err != nil {
					// AUTO-FIX-2026-07-02 [可观测性]: 解析错误计数指标
					metrics.MessageParseErrorsTotal.IncWithLabels(map[string]string{
						"protocol": string(pt),
						"error":    "route_failed",
					})
					s.getLogger().Warn("protocol route failed", zap.Error(err), zap.String("id", sessionID))
					continue
				}

				if pt != "" {
					session.SetProtocol(pt)
				}

				// 慢连接检测：根据消息类型推进读超时阶段
				if msg != nil {
					switch msg.Header.MsgID {
					case 0x0100: // JT/T 808 终端注册，收到后续续期 initialAuthTimeout
						conn.SetReadDeadline(time.Now().Add(authTimeout))
					case 0x0102: // JT/T 808 终端鉴权
						// AUTO-FIX-2026-06-30 [P1-2]: 鉴权限流：令牌桶 1000/s，超限下发 0x8001 应答码 0x01 + 随机退避时间
						if !s.authLimiter.Allow() {
							backoffSec := s.authBackoffSec()
							s.getLogger().Warn("鉴权限流，下发退避时间",
								zap.String("id", sessionID),
								zap.String("phone", msg.Header.Phone),
								zap.Int("backoff_sec", backoffSec))
							resp := BuildReconnectBackoffResp(msg.Header.Phone, msg.Header.SeqNum, backoffSec)
							// R44-FIX-2026-07-26 [P1]: 使用 session.Write 替代 conn.Write。
							// 原实现直接 conn.Write(resp) 绕过 sendLoop，若前序消息的 onMessage
							// 回调已通过 session.Write 投递了下行帧到 sendLoop 队列，
							// 则 sendLoop goroutine 可能正在并发执行 conn.Write，
							// 与此处的 conn.Write 形成并发写竞争，导致数据交错/损坏。
							// session.Write 内部通过 sendLoop 串行化所有下行写入，从根本上避免并发写。
							if _, werr := session.Write(resp); werr != nil {
								s.getLogger().Warn("发送鉴权退避应答失败",
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
		s.getLogger().Debug("detected JT/T 808/1078/905/1253 protocol")
		return protocol.NewFrameBuffer(protocol.ProtocolJT808)
	case data[0] == 0x5B:
		s.getLogger().Debug("detected JT/T 809 protocol")
		return protocol.NewFrameBuffer(protocol.ProtocolJT809)
	case len(data) >= 2 && data[0] == 0x23 && data[1] == 0x23:
		s.getLogger().Debug("detected GB/T 32960 protocol")
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

// ipConnRateLimiter 单 IP 连接速率限制器（滑动窗口）。
// FIXED-2026-07-22 [P0]: 防止单 IP 在短时间内大量新建连接占满 max_connections。
// FIXED-2026-07-23 [P2]: 添加后台清理机制，防止 counters map 内存泄漏。
type ipConnRateLimiter struct {
	mu       sync.Mutex
	window   time.Duration // 时间窗口（默认 1s）
	maxRate  int           // 窗口内最大新建连接数
	counters map[string]*ipRateCounter
	stopCh   chan struct{}
	stopOnce sync.Once // R35-FIX [P2]: 幂等关闭，防止重复 close panic
}

type ipRateCounter struct {
	count    int
	windowStart time.Time
}

// newIPConnRateLimiter 创建单 IP 连接速率限制器。
func newIPConnRateLimiter(maxRate int, window time.Duration) *ipConnRateLimiter {
	return &ipConnRateLimiter{
		window:   window,
		maxRate:  maxRate,
		counters: make(map[string]*ipRateCounter),
		stopCh:   make(chan struct{}),
	}
}

// Allow 检查指定 IP 是否允许新建连接。如果允许，计数器+1；否则返回 false。
func (r *ipConnRateLimiter) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	c, ok := r.counters[ip]
	if !ok || now.Sub(c.windowStart) >= r.window {
		// 新窗口
		r.counters[ip] = &ipRateCounter{count: 1, windowStart: now}
		return true
	}
	if c.count >= r.maxRate {
		return false
	}
	c.count++
	return true
}

// Cleanup 清理过期的计数器（由后台定期调用或惰性清理）。
func (r *ipConnRateLimiter) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for ip, c := range r.counters {
		if now.Sub(c.windowStart) >= r.window {
			delete(r.counters, ip)
		}
	}
}

// StartCleanup 启动后台清理协程，每 5 分钟清理一次过期计数器。
// FIXED-2026-07-23 [P2]: 防止 counters map 无限增长导致内存泄漏。
func (r *ipConnRateLimiter) StartCleanup() {
	util.SafeGo(nil, "ipConnRateLimiter.cleanup", func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 Cleanup panic
				// 不会导致清理协程整体退出。SafeGo 的 recover 在 goroutine 级别，
				// panic 后协程退出，counters map 永不被清理，内存泄漏。
				func() {
					defer func() {
						if r := recover(); r != nil {
							// SafeGo 已有 recover，此处二级 recover 确保循环不退出
						}
					}()
					r.Cleanup()
				}()
			case <-r.stopCh:
				return
			}
		}
	})
}

// StopCleanup 停止后台清理协程（幂等，多次调用安全）。
// R35-FIX [P2]: 使用 sync.Once 防止重复 close panic。
func (r *ipConnRateLimiter) StopCleanup() {
	r.stopOnce.Do(func() { close(r.stopCh) })
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
			// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 checkSessions panic
			// 不会导致心跳检查协程退出。SafeGo 的 recover 在 goroutine 级别，
			// panic 后协程退出，心跳超时检测失效，僵尸会话不被清理，连接泄漏。
			func() {
				defer func() {
					if r := recover(); r != nil {
						// SafeGo 已有 recover，此处二级 recover 确保循环不退出
					}
				}()
				h.checkSessions()
			}()
		}
	}
}

// checkSessions 定期检查所有会话的心跳状态。
// INDUSTRIAL-FIX-2026-07-24: 同时上报会话状态分布指标和发送队列深度告警。
func (h *HeartbeatChecker) checkSessions() {
	now := time.Now()
	// INDUSTRIAL-FIX-2026-07-24: 会话状态分布指标
	stateCount := map[string]int{"connected": 0, "registered": 0, "authenticated": 0}
	for _, session := range h.sessions.List() {
		lastActive := session.GetLastActive()
		status := session.GetStatus()
		stateCount[status]++

		// INDUSTRIAL-FIX-2026-07-24: 发送队列深度告警（队列 ≥200/256 = 78%时告警）
		queueDepth := session.SendQueueDepth()
		if queueDepth >= 200 {
			phone := session.GetPhone()
			logger := h.logger
			if phone != "" {
				logger = logger.With(zap.String("device_id", phone), zap.String("phone", phone))
			}
			logger.Warn("session send queue near full, slow client detected",
				zap.String("id", session.ID),
				zap.Int("queue_depth", queueDepth),
				zap.Int("queue_capacity", 256))
		}

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

	// 上报会话状态分布指标
	for state, count := range stateCount {
		metrics.SessionStateDistribution.SetWithLabels(float64(count), map[string]string{"state": state})
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
			// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 check panic
			// 不会导致 OOM 防护协程退出。SafeGo 的 recover 在 goroutine 级别，
			// panic 后协程退出，OOM 防护失效，高负载下可能触发 OOM Kill。
			func() {
				defer func() {
					if r := recover(); r != nil {
						// SafeGo 已有 recover，此处二级 recover 确保循环不退出
					}
				}()
				m.check()
			}()
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

// sessionMsgRateLimiter 每会话消息速率限制器（滑动窗口）。
// INDUSTRIAL-FIX-2026-07-24: 防止单设备消息洪水攻击（如高频位置上报）。
// 每个会话在 window 窗口内最多发送 maxRate 条消息，超限视为洪水攻击。
type sessionMsgRateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	maxRate  int
	counters map[string]*sessionRateCounter
	stopCh   chan struct{}
	stopOnce sync.Once // R35-FIX [P2]: 幂等关闭，防止重复 close panic
}

type sessionRateCounter struct {
	count       int
	windowStart time.Time
}

// newSessionMsgRateLimiter 创建每会话消息速率限制器。
func newSessionMsgRateLimiter(maxRate int, window time.Duration) *sessionMsgRateLimiter {
	r := &sessionMsgRateLimiter{
		window:   window,
		maxRate:  maxRate,
		counters: make(map[string]*sessionRateCounter),
		stopCh:   make(chan struct{}),
	}
	// 启动后台清理协程，定期清理已关闭会话的计数器
	util.SafeGo(nil, "sessionMsgRateLimiter.cleanup", func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 Cleanup panic
				// 不会导致清理协程整体退出。SafeGo 的 recover 在 goroutine 级别，
				// panic 后协程退出，counters map 永不被清理，内存泄漏。
				func() {
					defer func() {
						if r := recover(); r != nil {
							// SafeGo 已有 recover，此处二级 recover 确保循环不退出
						}
					}()
					r.Cleanup()
				}()
			case <-r.stopCh:
				return
			}
		}
	})
	return r
}

// Allow 检查指定会话是否允许发送消息。如果允许，计数器+1；否则返回 false。
func (r *sessionMsgRateLimiter) Allow(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	c, ok := r.counters[sessionID]
	if !ok || now.Sub(c.windowStart) >= r.window {
		// 新窗口
		r.counters[sessionID] = &sessionRateCounter{count: 1, windowStart: now}
		return true
	}
	if c.count >= r.maxRate {
		return false
	}
	c.count++
	return true
}

// Cleanup 清理过期的计数器（由后台定期调用）。
func (r *sessionMsgRateLimiter) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for sid, c := range r.counters {
		if now.Sub(c.windowStart) >= r.window {
			delete(r.counters, sid)
		}
	}
}

// StopCleanup 停止后台清理协程（幂等，多次调用安全）。
// R35-FIX [P2]: 使用 sync.Once 防止重复 close panic。
func (r *sessionMsgRateLimiter) StopCleanup() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}
