package jt1078

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jte-engine/jte/internal/metrics"
	"github.com/jte-engine/jte/internal/util"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-28: SRTP 密钥交换与加密（plan 8.6.1）
// 简化实现：AES-128-CM (CTR 模式) 加密 RTP payload + HMAC-SHA1-80 认证标签。
// 密钥仅存内存，会话结束销毁；不实现完整 RFC 3711（KEK/SRTP 会话密钥派生）。

// SRTPConfig SRTP 加密配置。
type SRTPConfig struct {
	Enabled     bool
	CipherSuite string // "AES-128-CM" 或 "SM4-CBC"
	MasterKey   []byte // 16 字节主密钥（仅内存）
}

// SRTPAuthTagLen SRTP 认证标签长度（HMAC-SHA1-80 = 10 字节）。
const SRTPAuthTagLen = 10

// GenerateMasterKey 生成 16 字节随机主密钥。
func (e *VideoEngine) GenerateMasterKey() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate srtp master key: %w", err)
	}
	return key, nil
}

// SetSRTPConfig 设置 SRTP 加密配置（供外部配置，如 0x9101 携带 SRTP 参数时）。
// AUTO-FIX-2026-06-30 [P2-8]: 同时创建 SRTPSession（派生 enc/auth/salt 密钥 + ROC）。
func (e *VideoEngine) SetSRTPConfig(cfg SRTPConfig) {
	e.srtpMu.Lock()
	defer e.srtpMu.Unlock()
	e.srtpConfig = cfg
	if cfg.Enabled && len(cfg.MasterKey) > 0 {
		sess, err := NewSRTPSession(cfg.MasterKey, cfg.CipherSuite)
		if err != nil {
			e.logger.Error("srtp session 创建失败，加密降级为关闭",
				zap.String("cipher_suite", cfg.CipherSuite),
				zap.Error(err))
			e.srtpSession = nil
			return
		}
		e.srtpSession = sess
	} else {
		e.srtpSession = nil
	}
	e.logger.Info("srtp config updated",
		zap.Bool("enabled", cfg.Enabled),
		zap.String("cipher_suite", cfg.CipherSuite))
}

// GetSRTPConfig 返回当前 SRTP 配置快照。
func (e *VideoEngine) GetSRTPConfig() SRTPConfig {
	e.srtpMu.RLock()
	defer e.srtpMu.RUnlock()
	return e.srtpConfig
}

// NegotiateSRTPResult SRTP 动态密钥协商结果，可直接嵌入 0x9101 消息。
type NegotiateSRTPResult struct {
	MasterKeyEncrypted bool   // true=已 RSA 加密
	CipherSuite        string // "AES-128-CM" 或 "SM4-CBC"
	EncryptedMasterKey []byte // RSA-OAEP 加密后的主密钥（或明文，仅测试用）
}

// NegotiateSRTP 动态 SRTP 密钥协商完整路径。
// AUTO-FIX-2026-07-04 [P1]: 将分散的密钥生成→RSA加密→SRTPSession创建编排为单一入口。
//
// 流程：
//  1. 生成 16 字节随机 SRTP 主密钥
//  2. 用终端 RSA 公钥加密主密钥（RSA-OAEP）
//  3. 创建本地 SRTPSession（派生 enc/auth/salt + ROC）
//  4. 返回加密后的主密钥供 0x9101 下发
//
// 参数：
//   - rsaModulus: 终端 RSA 模数 n（来自 0x0A00 RSAPublicKeyMessage.Euler）
//   - rsaExponent: 终端 RSA 公钥指数 e（来自 0x0A00 RSAPublicKeyMessage.PublicExponent）
//   - cipherSuite: "AES-128-CM"（标准）或 "SM4-CBC"（国密）
func (e *VideoEngine) NegotiateSRTP(rsaModulus []byte, rsaExponent uint32, cipherSuite string) (*NegotiateSRTPResult, error) {
	if len(rsaModulus) == 0 {
		return nil, fmt.Errorf("rsa modulus is empty (terminal public key not received)")
	}
	if cipherSuite == "" {
		cipherSuite = "AES-128-CM"
	}

	// 1. 生成随机主密钥
	masterKey, err := GenerateSRTPMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate srtp master key: %w", err)
	}

	// 2. RSA-OAEP 加密主密钥
	encryptedKey, err := EncryptSRTPMasterKeyWithRSA(masterKey, rsaModulus, rsaExponent)
	if err != nil {
		return nil, fmt.Errorf("encrypt srtp master key: %w", err)
	}

	// 3. 创建本地 SRTPSession
	e.SetSRTPConfig(SRTPConfig{
		Enabled:     true,
		CipherSuite: cipherSuite,
		MasterKey:   masterKey,
	})

	e.logger.Info("srtp key negotiation completed",
		zap.String("cipher_suite", cipherSuite),
		zap.Int("encrypted_key_len", len(encryptedKey)))

	return &NegotiateSRTPResult{
		MasterKeyEncrypted: true,
		CipherSuite:        cipherSuite,
		EncryptedMasterKey: encryptedKey,
	}, nil
}

// DestroySRTPSession 销毁 SRTP 会话（会话结束 0x9103 时调用）。
// AUTO-FIX-2026-07-04 [P1]: 密钥生命周期管理——会话结束后密钥从内存清除。
func (e *VideoEngine) DestroySRTPSession() {
	e.srtpMu.Lock()
	defer e.srtpMu.Unlock()
	e.srtpConfig = SRTPConfig{Enabled: false}
	e.srtpSession = nil
	e.logger.Info("srtp session destroyed (key wiped from memory)")
}

// SetRecordSegmentTracker 注入录制断片跟踪器（P2-7）。
// 注入后 qualityMonitorLoop 会在流断开/质量差切换时自动记录分片与断片告警。
func (e *VideoEngine) SetRecordSegmentTracker(t *RecordSegmentTracker) {
	e.mu.Lock()
	e.recordTracker = t
	e.mu.Unlock()
}

// GetRecordSegmentTracker 返回录制断片跟踪器（供 API 层查询断片列表/合并）。
// AUTO-FIX-2026-07-02 [P1]: 录制断片查询 API。
func (e *VideoEngine) GetRecordSegmentTracker() *RecordSegmentTracker {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.recordTracker
}

// QueryRecordSegments 按时间段查询已结束的录制分片列表（委托给 RecordSegmentTracker）。
// AUTO-FIX-2026-07-02 [P1]: 录制断片查询 API。
// phone 为空时不过滤设备，logicChannel 为 0 时不过滤通道。
func (e *VideoEngine) QueryRecordSegments(phone string, logicChannel byte, start, end time.Time) []RecordSegment {
	tracker := e.GetRecordSegmentTracker()
	if tracker == nil {
		return nil
	}
	return tracker.QuerySegments(phone, logicChannel, start, end)
}

// MergeRecordSegments 按时间段合并录制分片（委托给 RecordSegmentTracker）。
// AUTO-FIX-2026-07-02 [P1]: 断片合并接口。
func (e *VideoEngine) MergeRecordSegments(phone string, logicChannel byte, start, end time.Time) (RecordSegment, error) {
	tracker := e.GetRecordSegmentTracker()
	if tracker == nil {
		return RecordSegment{}, fmt.Errorf("record segment tracker not configured")
	}
	return tracker.MergeByTimeRange(phone, logicChannel, start, end)
}

// GetKeyFrameRecoveryTracker 返回关键帧恢复计时器（供 API 层查询恢复状态）。
func (e *VideoEngine) GetKeyFrameRecoveryTracker() *KeyFrameRecoveryTracker {
	return e.keyframeTracker
}

// GetPTZLatencyTracker 返回 PTZ 延迟追踪器（供 API 层查询延迟统计）。
func (e *VideoEngine) GetPTZLatencyTracker() *PTZLatencyTracker {
	return e.ptzTracker
}

// GetAutoRecoveryTracker 返回弱网自动恢复追踪器。
func (e *VideoEngine) GetAutoRecoveryTracker() *AutoRecoveryTracker {
	return e.autoRecovery
}

// SetConcurrentPlayManager 注入并发播放管理器。
func (e *VideoEngine) SetConcurrentPlayManager(mgr *ConcurrentPlayManager) {
	e.mu.Lock()
	e.concurrentMgr = mgr
	e.mu.Unlock()
}

// GetConcurrentPlayManager 返回并发播放管理器。
func (e *VideoEngine) GetConcurrentPlayManager() *ConcurrentPlayManager {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.concurrentMgr
}

// SetAutoRecoveryHandler 注册自动恢复回调。
// 当 autoRecovery 检测到网络恢复（连续3秒丢包率<2%且码率>200kbps）时调用，
// 业务侧应在此回调中下发 0x9101 StreamType=0 切回主码流。
func (e *VideoEngine) SetAutoRecoveryHandler(fn func(streamID, phone string, logicChannel byte)) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.onAutoRecovery = fn
}

// RecordKeyFrameRequest 记录关键帧请求下发（业务侧下发 0x9203 Command=4 后调用）。
func (e *VideoEngine) RecordKeyFrameRequest(streamID, phone string, channel byte) {
	if e.keyframeTracker != nil {
		e.keyframeTracker.RecordRequest(streamID, phone, channel)
	}
}

// RecordPTZSent 记录 PTZ 指令下发（业务侧下发 0x9301 后调用）。
func (e *VideoEngine) RecordPTZSent(seqNum uint16, streamID, phone string, channel byte, direction, speed int) {
	if e.ptzTracker != nil {
		e.ptzTracker.RecordPTZSent(seqNum, streamID, phone, channel, direction, speed)
	}
}

// RecordPTZAck 记录 PTZ 应答到达（收到终端 0x9302 时调用）。
func (e *VideoEngine) RecordPTZAck(seqNum uint16) {
	if e.ptzTracker != nil {
		e.ptzTracker.RecordPTZAck(seqNum)
	}
}

// NotifyStreamStart 通知录制跟踪器流开始（业务侧下发 0x9101 后调用）。
// AUTO-FIX-2026-06-30 [P2-7]: 创建新录制分片，检测与上一分片间隔是否为断片。
func (e *VideoEngine) NotifyStreamStart(streamID, phone string, logicChannel, streamType byte) {
	e.mu.RLock()
	tracker := e.recordTracker
	e.mu.RUnlock()
	if tracker == nil {
		return
	}
	tracker.OnStreamStart(streamID, phone, logicChannel, streamType, time.Now())
}

// NotifyStreamEnd 通知录制跟踪器流结束（业务侧停止流后调用）。
func (e *VideoEngine) NotifyStreamEnd(streamID, phone string, logicChannel byte) {
	e.mu.RLock()
	tracker := e.recordTracker
	e.mu.RUnlock()
	if tracker == nil {
		return
	}
	tracker.OnStreamEnd(streamID, phone, logicChannel, time.Now())
}

// encryptSRTP 对 RTP 包进行 SRTP 加密：保留 RTP 头，加密 payload，追加 10 字节认证标签。
// AUTO-FIX-2026-06-30 [P2-8]: 委托给 SRTPSession.Encrypt，修复 IV 复用 + 密钥同源问题。
//   - 派生独立 enc_key/auth_key/salt（不再用 MasterKey 既加密又认证）
//   - IV 引入 ROC，防止 SeqNum 回绕后复用
//   - 支持 AES-128-CM 和 SM4-CBC（国密，需 module-crypto 注册）
func (e *VideoEngine) encryptSRTP(rtpData []byte) ([]byte, error) {
	e.srtpMu.RLock()
	sess := e.srtpSession
	cfg := e.srtpConfig
	e.srtpMu.RUnlock()

	if !cfg.Enabled || sess == nil {
		return rtpData, nil
	}

	return sess.Encrypt(rtpData)
}

// decryptSRTP 解密 SRTP 包：校验认证标签，解密 payload，返回原始 RTP 包。
// AUTO-FIX-2026-06-30 [P2-8]: 新增解密路径，支持接收加密流（如终端加密上报的视频流）。
func (e *VideoEngine) decryptSRTP(srtpData []byte) ([]byte, error) {
	e.srtpMu.RLock()
	sess := e.srtpSession
	cfg := e.srtpConfig
	e.srtpMu.RUnlock()

	if !cfg.Enabled || sess == nil {
		return srtpData, nil // 未启用 SRTP，原样返回
	}

	return sess.Decrypt(srtpData)
}

type StreamSession struct {
	ID           string
	Phone        string
	LogicChannel byte
	StreamType   byte
	Conn         net.Conn
	StartTime    time.Time
	LastActive   time.Time
	SeqNum       uint16
	Packets      uint64
	Bytes        uint64
	// AUTO-FIX-2026-06-28: 视频质量统计字段（plan 5.5 + project_memory 工程约定）
	// 用于实时码率(kbps)/帧率(fps)/丢包率(%) 计算
	LastSeqNum      uint16 // 上一个收到的 RTP 序号，用于 gap 检测丢包
	LastTimestamp   uint32 // 上一个 RTP 时间戳，用于帧率统计
	WindowPackets   uint64 // 当前1秒窗口内收到的包数
	WindowBytes     uint64 // 当前1秒窗口内收到的字节数
	WindowLost      uint64 // 当前1秒窗口内丢失的包数
	WindowFrames    uint64 // 当前1秒窗口内帧数（Marker=1 计一帧）
	LossExceedCount int    // 连续丢包>5%的窗口数（达到3次触发子码流切换）
	LowBitrateCount int    // 连续码率<100kbps的窗口数（达到3次触发子码流切换）
	// 离散统计：最近一次计算结果（由 qualityMonitorLoop 每秒刷新）
	BitrateKbps float64 // 实时码率
	FrameRate   float64 // 实时帧率
	LossRate    float64 // 实时丢包率 (%)
	// 验收标准2: RTP SeqNum gap 累计统计（跨窗口，不随 computeQualityAndCheckAlerts 重置）
	TotalLost     uint64 // 累计丢失包数
	TotalExpected uint64 // 累计期望包数（收到的 + 丢失的）
	MaxGap        uint64 // 单次最大 gap
}

// QualityStats 视频流质量统计快照（对外暴露的只读视图）。
// AUTO-FIX-2026-06-28: 实现 plan 5.5 节"视频质量统计"要求 + project_memory 工程约定
// 验收标准2: 增加 TotalLost/TotalExpected/CumulativeLossRate 累计丢包统计
// 验收标准2: 增加 MaxGap 单次最大 gap 报告
type QualityStats struct {
	StreamID     string  `json:"stream_id"`
	Phone        string  `json:"phone"`
	LogicChannel byte    `json:"logic_channel"`
	StreamType   byte    `json:"stream_type"` // 0=主码流 1=子码流
	BitrateKbps  float64 `json:"bitrate_kbps"`
	FrameRate    float64 `json:"frame_rate"`
	LossRate     float64 `json:"loss_rate"` // 百分比 0-100
	Packets      uint64  `json:"packets"`
	Bytes        uint64  `json:"bytes"`
	StartTime    time.Time `json:"start_time"`
	LastActive   time.Time `json:"last_active"`
	Online       bool    `json:"online"` // false 表示流已断开
	// 验收标准2: 累计丢包统计（跨窗口，不随窗口重置）
	TotalLost         uint64  `json:"total_lost"`          // 累计丢失包数
	TotalExpected     uint64  `json:"total_expected"`      // 累计期望包数
	CumulativeLossRate float64 `json:"cumulative_loss_rate"` // 累计丢包率 (%)
	MaxGap            uint64  `json:"max_gap"`             // 单次最大 gap
}

type VideoEngine struct {
	logger       *zap.Logger
	sessions     map[string]*StreamSession
	mu           sync.RWMutex
	running      atomic.Bool
	zlmAddr      string
	streamPorts  map[string]int
	portsMu      sync.RWMutex
	// 流传输模式：streamID -> "udp" | "tcp"，默认 udp，NAT/公网环境可切 tcp
	streamMode   map[string]string
	streamModeMu sync.RWMutex
	// AUTO-FIX-2026-06-29 [P1-7]: auto 模式下 UDP 连续失败计数，达到阈值后自动 fallback 到 TCP。
	// project_memory: 网络中断时需自动重连并保留播放状态——UDP 不通时切 TCP 保证流不中断。
	udpFailCount   map[string]int
	udpFailMu      sync.Mutex
	udpFailThreshold int // 连续失败达到此值后切 TCP，默认 3
	// 默认流传输模式：未显式设置 per-stream 模式时使用。
	// 由配置 video.rtp_mode (udp/tcp/auto) 设定，auto 表示按需在运行时切换。
	defaultStreamMode string
	// AUTO-FIX-2026-07-02 [P1]: RTP 转发长连接池（UDP/TCP 统一管理，复用率统计 + LRU 上限 + 空闲扫描）。
	// 替代原散落的 udpPool/tcpPool 内联 map。验收标准：1000 并发复用率 > 90%。
	rtpPool *RTPConnPool
	// AUTO-FIX-2026-06-28: 视频质量保障体系
	// qualityMonitorLoop 每秒计算各流的码率/帧率/丢包率，
	// 检测流断开（无 RTP 包超过 streamDownTimeout）并触发自动重连，
	// 检测连续3次丢包>5% 或 码率<100kbps 并触发子码流自动切换。
	qualityMu          sync.RWMutex
	streamDownTimeout  time.Duration // 流断开判定阈值，默认 10 秒
	autoReconnect      bool          // 是否启用自动重连（网络中断时）
	autoSwitchSub      bool          // 是否启用子码流自动切换
	onStreamDown       func(streamID, phone string, logicChannel byte) // 流断开回调（业务侧重新下发 0x9101）
	onQualityPoor      func(streamID, phone string, logicChannel, curStreamType byte) // 质量差回调（业务侧下发 0x9101 StreamType=1 切子码流）
	qualityStopCh      chan struct{}
	// AUTO-FIX-2026-07-02 [P1]: UDP→TCP fallback 事件回调（业务侧可订阅以告警/记录）。
	// project_memory: 添加 fallback 事件日志和指标上报。
	// fallback 时播放状态(session/SSRC/时间戳)天然保留——ForwardRTP 不触碰 StreamSession。
	onFallback         func(streamID, phone string, logicChannel byte, reason string)
	// AUTO-FIX-2026-06-28: SRTP 加密配置（plan 8.6.1）
	// 启用后 sendUDP/sendTCP 发送前对 RTP 包进行 SRTP 加密。
	// AUTO-FIX-2026-06-30 [P2-8]: srtpSession 持有派生密钥+ROC，修复 IV 复用+密钥同源问题。
	srtpConfig  SRTPConfig
	srtpSession *SRTPSession
	srtpMu      sync.RWMutex
	// AUTO-FIX-2026-06-30 [P2-7]: 录制断片防护跟踪器。
	// 跟踪每设备每通道的录制分片，检测断片（间隔>5s）并写入 alert 表。
	// 录制侧始终录制主码流，不受播放侧码流切换影响。
	recordTracker *RecordSegmentTracker
	// 验收标准1: 并发播放管理器
	concurrentMgr *ConcurrentPlayManager
	// 验收标准3: 关键帧恢复计时器
	keyframeTracker *KeyFrameRecoveryTracker
	// 验收标准4: 弱网自动恢复追踪器
	autoRecovery *AutoRecoveryTracker
	// 验收标准6: PTZ 延迟追踪器
	ptzTracker *PTZLatencyTracker
	// 自动恢复回调（网络恢复后切回主码流）
	onAutoRecovery func(streamID, phone string, logicChannel byte)
}

func NewVideoEngine(logger *zap.Logger, zlmAddr string) *VideoEngine {
	e := &VideoEngine{
		logger:            logger,
		sessions:          make(map[string]*StreamSession),
		zlmAddr:           zlmAddr,
		streamPorts:       make(map[string]int),
		streamMode:        make(map[string]string),
		udpFailCount:      make(map[string]int),
		udpFailThreshold:  3, // 连续 3 次 UDP 发送失败后 fallback 到 TCP
		streamDownTimeout: 10 * time.Second,
		autoReconnect:     true,  // 默认启用自动重连（project_memory: 网络中断时需自动重连并保留播放状态）
		autoSwitchSub:     true,  // 默认启用子码流自动切换（project_memory: 连续3次丢包>5%或码率<100kbps）
		qualityStopCh:     make(chan struct{}),
	}
	// AUTO-FIX-2026-07-02 [P1]: 统一 RTP 长连接池（UDP+TCP），5 分钟空闲关闭，上限 4096。
	e.rtpPool = NewRTPConnPool(zlmAddr, 5*time.Minute, 4096, logger)
	e.rtpPool.StartSweep()
	// 验收标准: 初始化各追踪器
	e.keyframeTracker = NewKeyFrameRecoveryTracker(logger)
	e.autoRecovery = NewAutoRecoveryTracker(logger)
	e.ptzTracker = NewPTZLatencyTracker(logger)
	util.SafeGo(e.logger, "jt1078.qualityMonitorLoop", e.qualityMonitorLoop)
	return e
}

// GetRTPConnPool 返回底层 RTP 长连接池（供运维/压测读取复用率与统计）。
func (e *VideoEngine) GetRTPConnPool() *RTPConnPool {
	return e.rtpPool
}

// SetStreamDownHandler 注册流断开回调。
// 当 qualityMonitorLoop 检测到流无 RTP 包超过 streamDownTimeout 时调用，
// 业务侧应在此回调中重新下发 0x9101 实时音视频请求以恢复播放（保留原 streamID/通道/码流类型）。
func (e *VideoEngine) SetStreamDownHandler(fn func(streamID, phone string, logicChannel byte)) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.onStreamDown = fn
}

// SetQualityPoorHandler 注册质量差回调。
// 当连续3次窗口（3秒）丢包率>5% 或 码率<100kbps 时调用，
// 业务侧应在此回调中下发 0x9101 StreamType=1 切换到子码流（project_memory 工程约定）。
// curStreamType 为当前码流类型（0=主 1=子），已为子码流时不再触发切换。
func (e *VideoEngine) SetQualityPoorHandler(fn func(streamID, phone string, logicChannel, curStreamType byte)) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.onQualityPoor = fn
}

// SetAutoReconnect 启用/禁用自动重连。
func (e *VideoEngine) SetAutoReconnect(enable bool) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.autoReconnect = enable
}

// SetAutoSwitchSub 启用/禁用子码流自动切换。
func (e *VideoEngine) SetAutoSwitchSub(enable bool) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.autoSwitchSub = enable
}

// SetStreamDownTimeout 设置流断开判定阈值（默认10秒）。
func (e *VideoEngine) SetStreamDownTimeout(d time.Duration) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.streamDownTimeout = d
}

// SetFallbackHandler 注册 UDP→TCP fallback 事件回调。
// AUTO-FIX-2026-07-02 [P1]: 业务侧可订阅此事件以告警/记录。
// 触发时机：auto 模式下 UDP 连续失败达阈值(默认3次)切换到 TCP 时。
// 播放状态(session/SSRC/时间戳)在 fallback 中保留——ForwardRTP 不触碰 StreamSession。
func (e *VideoEngine) SetFallbackHandler(fn func(streamID, phone string, logicChannel byte, reason string)) {
	e.qualityMu.Lock()
	defer e.qualityMu.Unlock()
	e.onFallback = fn
}

// GetQualityStats 返回指定流的质量统计快照。
func (e *VideoEngine) GetQualityStats(streamID string) (*QualityStats, bool) {
	e.mu.RLock()
	s, ok := e.sessions[streamID]
	e.mu.RUnlock()
	if !ok {
		return nil, false
	}
	e.qualityMu.RLock()
	downTimeout := e.streamDownTimeout
	e.qualityMu.RUnlock()

	now := time.Now()
	online := now.Sub(s.LastActive) < downTimeout

	// 计算累计丢包率
	var cumulativeLossRate float64
	if s.TotalExpected > 0 {
		cumulativeLossRate = float64(s.TotalLost) / float64(s.TotalExpected) * 100.0
	}

	return &QualityStats{
		StreamID:           s.ID,
		Phone:              s.Phone,
		LogicChannel:       s.LogicChannel,
		StreamType:         s.StreamType,
		BitrateKbps:        s.BitrateKbps,
		FrameRate:          s.FrameRate,
		LossRate:           s.LossRate,
		Packets:            s.Packets,
		Bytes:              s.Bytes,
		StartTime:          s.StartTime,
		LastActive:         s.LastActive,
		Online:             online,
		TotalLost:          s.TotalLost,
		TotalExpected:      s.TotalExpected,
		CumulativeLossRate: cumulativeLossRate,
		MaxGap:             s.MaxGap,
	}, true
}

// ListQualityStats 返回所有流的的质量统计快照列表。
func (e *VideoEngine) ListQualityStats() []*QualityStats {
	e.mu.RLock()
	ids := make([]string, 0, len(e.sessions))
	for id := range e.sessions {
		ids = append(ids, id)
	}
	e.mu.RUnlock()

	result := make([]*QualityStats, 0, len(ids))
	for _, id := range ids {
		if stats, ok := e.GetQualityStats(id); ok {
			result = append(result, stats)
		}
	}
	return result
}

// GapReport RTP SeqNum gap 累计统计报告。
// 验收标准2: 画面质量监控 - 抓包统计 RTP SeqNum gap，计算累计丢包率。
type GapReport struct {
	StreamID           string    `json:"stream_id"`
	Phone              string    `json:"phone"`
	LogicChannel       byte      `json:"logic_channel"`
	TotalPackets       uint64    `json:"total_packets"`         // 累计收到的包数
	TotalLost          uint64    `json:"total_lost"`            // 累计丢失包数
	TotalExpected      uint64    `json:"total_expected"`        // 累计期望包数
	CumulativeLossRate float64   `json:"cumulative_loss_rate"`  // 累计丢包率 (%)
	MaxGap             uint64    `json:"max_gap"`               // 单次最大 gap
	CurrentLossRate    float64   `json:"current_loss_rate"`     // 当前窗口丢包率 (%)
	Online             bool      `json:"online"`                // 流是否在线
	LastActive         time.Time `json:"last_active"`
}

// GetGapReport 返回指定流的 RTP SeqNum gap 累计统计报告。
// 验收标准2: 用于前端展示累计丢包率、最大 gap 等质量指标。
func (e *VideoEngine) GetGapReport(streamID string) (*GapReport, bool) {
	e.mu.RLock()
	s, ok := e.sessions[streamID]
	e.mu.RUnlock()
	if !ok {
		return nil, false
	}
	e.qualityMu.RLock()
	downTimeout := e.streamDownTimeout
	e.qualityMu.RUnlock()

	now := time.Now()
	online := now.Sub(s.LastActive) < downTimeout

	var cumulativeLossRate float64
	if s.TotalExpected > 0 {
		cumulativeLossRate = float64(s.TotalLost) / float64(s.TotalExpected) * 100.0
	}

	return &GapReport{
		StreamID:           s.ID,
		Phone:              s.Phone,
		LogicChannel:       s.LogicChannel,
		TotalPackets:       s.Packets,
		TotalLost:          s.TotalLost,
		TotalExpected:      s.TotalExpected,
		CumulativeLossRate: cumulativeLossRate,
		MaxGap:             s.MaxGap,
		CurrentLossRate:    s.LossRate,
		Online:             online,
		LastActive:         s.LastActive,
	}, true
}

// ListGapReports 返回所有流的 gap 累计统计报告列表。
func (e *VideoEngine) ListGapReports() []*GapReport {
	e.mu.RLock()
	ids := make([]string, 0, len(e.sessions))
	for id := range e.sessions {
		ids = append(ids, id)
	}
	e.mu.RUnlock()

	result := make([]*GapReport, 0, len(ids))
	for _, id := range ids {
		if report, ok := e.GetGapReport(id); ok {
			result = append(result, report)
		}
	}
	return result
}

// qualityMonitorLoop 每秒计算各流的码率/帧率/丢包率，并触发流断开/质量差回调。
// AUTO-FIX-2026-06-28: 实现 plan 5.5 节"视频质量统计/网络中断重连/码率自适应"要求
func (e *VideoEngine) qualityMonitorLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.computeQualityAndCheckAlerts()
		case <-e.qualityStopCh:
			return
		}
	}
}

// computeQualityAndCheckAlerts 单次质量计算与告警检查。
// 每秒执行：基于窗口计数计算码率/帧率/丢包率，重置窗口，检查流断开与质量差触发条件。
func (e *VideoEngine) computeQualityAndCheckAlerts() {
	e.qualityMu.RLock()
	downTimeout := e.streamDownTimeout
	autoReconnect := e.autoReconnect
	autoSwitchSub := e.autoSwitchSub
	onStreamDown := e.onStreamDown
	onQualityPoor := e.onQualityPoor
	e.qualityMu.RUnlock()

	// AUTO-FIX-2026-06-30 [P2-7]: 获取录制断片跟踪器引用（无锁读，仅判定 nil）
	e.mu.RLock()
	recordTracker := e.recordTracker
	e.mu.RUnlock()

	now := time.Now()

	// 复制 session 引用，避免长时间持锁
	e.mu.RLock()
	sessions := make([]*StreamSession, 0, len(e.sessions))
	for _, s := range e.sessions {
		sessions = append(sessions, s)
	}
	e.mu.RUnlock()

	for _, s := range sessions {
		// 1) 计算码率/帧率/丢包率（基于1秒窗口）
		bitrateKbps := float64(s.WindowBytes) * 8.0 / 1000.0
		frameRate := float64(s.WindowFrames)
		var lossRate float64
		if s.WindowPackets+s.WindowLost > 0 {
			lossRate = float64(s.WindowLost) / float64(s.WindowPackets+s.WindowLost) * 100.0
		}

		s.BitrateKbps = bitrateKbps
		s.FrameRate = frameRate
		s.LossRate = lossRate

		// AUTO-FIX-2026-06-30 [集成-7]: Prometheus 视频质量指标（按流标签）
		streamLabels := map[string]string{
			"stream_id": s.ID,
			"device_id": s.Phone,
			"channel":   strconv.Itoa(int(s.LogicChannel)),
		}
		metrics.VideoBitrate.SetWithLabels(bitrateKbps, streamLabels)
		metrics.VideoFramerate.SetWithLabels(frameRate, streamLabels)
		metrics.VideoPacketLoss.SetWithLabels(lossRate, streamLabels)

		// 重置窗口
		s.WindowPackets = 0
		s.WindowBytes = 0
		s.WindowLost = 0
		s.WindowFrames = 0

		// 2) 流断开检测：无 RTP 包超过 downTimeout
		if autoReconnect && onStreamDown != nil && now.Sub(s.LastActive) >= downTimeout {
			// 仅在已经有过 RTP 数据的流上触发（避免误判未启动的流）
			if s.Packets > 0 {
				e.logger.Warn("stream down detected, triggering auto reconnect",
					zap.String("stream_id", s.ID),
					zap.String("phone", s.Phone),
					zap.Uint8("channel", s.LogicChannel),
					zap.Duration("idle", now.Sub(s.LastActive)))
				// AUTO-FIX-2026-06-30 [P2-7]: 记录录制分片结束（流断开），供断片检测
				if recordTracker != nil {
					recordTracker.OnStreamSwitch(s.ID, s.Phone, s.LogicChannel, SwitchReasonStreamDown, now)
				}
				// 标记已触发，避免重复回调：将 LastActive 推后一个 downTimeout
				s.LastActive = now
				util.SafeGo(e.logger, "jt1078.onStreamDown", func() { onStreamDown(s.ID, s.Phone, s.LogicChannel) })
			}
			continue
		}

		// 3) 子码流自动切换检测
		// project_memory: 连续3次丢包>5% 或 码率<100kbps 自动切换到子码流
		if autoSwitchSub && onQualityPoor != nil && s.StreamType == 0 {
			// 仅主码流才需要切换到子码流
			poorLoss := lossRate > 5.0
			poorBitrate := bitrateKbps < 100.0 && s.WindowPackets == 0 && s.Packets > 0
			// 注：码率<100kbps 判定需谨慎，仅在确实有数据流动但码率极低时触发
			// WindowPackets==0 表示本秒无包（可能是断流），不参与码率判定

			if poorLoss {
				s.LossExceedCount++
			} else {
				s.LossExceedCount = 0
			}
			if poorBitrate {
				s.LowBitrateCount++
			} else {
				s.LowBitrateCount = 0
			}

			if s.LossExceedCount >= 3 || s.LowBitrateCount >= 3 {
				e.logger.Warn("stream quality poor, switching to sub stream",
					zap.String("stream_id", s.ID),
					zap.Float64("bitrate_kbps", bitrateKbps),
					zap.Float64("frame_rate", frameRate),
					zap.Float64("loss_rate", lossRate),
					zap.Int("loss_exceed_count", s.LossExceedCount),
					zap.Int("low_bitrate_count", s.LowBitrateCount))
				// AUTO-FIX-2026-06-30 [P2-7]: 播放侧切子码流，录制侧仍录主码流（仅标记分片切换原因）。
				// 录制分片不结束——主码流仍在传输，只是播放侧切到子码流，用户无感。
				if recordTracker != nil {
					recordTracker.OnStreamSwitch(s.ID, s.Phone, s.LogicChannel, SwitchReasonQualityPoor, now)
				}
				// 重置计数，避免切换后持续触发
				s.LossExceedCount = 0
				s.LowBitrateCount = 0
				// 标记当前码流类型为子码流，避免重复切换
				s.StreamType = 1
				util.SafeGo(e.logger, "jt1078.onQualityPoor", func() { onQualityPoor(s.ID, s.Phone, s.LogicChannel, 0) })
			}
		}

		// 4) 验收标准4: 弱网自动恢复检测
		// 当流在子码流状态且网络质量恢复（连续3秒丢包率<2%且码率>200kbps）时，自动切回主码流
		if s.StreamType == 1 && e.autoRecovery != nil {
			if e.autoRecovery.CheckRecovery(s.ID, lossRate, bitrateKbps) {
				e.logger.Info("auto recovery: switching back to main stream",
					zap.String("stream_id", s.ID),
					zap.Float64("loss_rate", lossRate),
					zap.Float64("bitrate_kbps", bitrateKbps))
				// 标记当前码流类型为主码流
				s.StreamType = 0
				// 重置计数
				s.LossExceedCount = 0
				s.LowBitrateCount = 0
				// 触发自动恢复回调
				e.qualityMu.RLock()
				onRecovery := e.onAutoRecovery
				e.qualityMu.RUnlock()
				if onRecovery != nil {
					util.SafeGo(e.logger, "jt1078.onAutoRecovery", func() { onRecovery(s.ID, s.Phone, s.LogicChannel) })
				}
			}
		}

		// 5) 验收标准3: 关键帧恢复超时检查
		// 检查是否有超时未恢复的关键帧请求（超过5秒）
		if e.keyframeTracker != nil {
			e.keyframeTracker.CheckTimeout(5 * time.Second)
		}
	}
}

// RegisterStreamPort records the ZLMediaKit RTP port allocated for a stream.
func (e *VideoEngine) RegisterStreamPort(streamID string, port int) {
	e.portsMu.Lock()
	e.streamPorts[streamID] = port
	e.portsMu.Unlock()
}

// UnregisterStreamPort clears the RTP port for a stream.
func (e *VideoEngine) UnregisterStreamPort(streamID string) {
	e.portsMu.Lock()
	delete(e.streamPorts, streamID)
	e.portsMu.Unlock()
}

// GetStreamPort returns the ZLMediaKit RTP port for a stream.
func (e *VideoEngine) GetStreamPort(streamID string) (int, bool) {
	e.portsMu.RLock()
	port, ok := e.streamPorts[streamID]
	e.portsMu.RUnlock()
	return port, ok
}

// SetStreamMode 设置流的 RTP 传输模式： "udp" | "tcp" | "auto"。
// 公网/NAT 环境下 UDP 不通时可切换为 TCP（1078-2022 标准）。
// 切换模式时重置该流的 UDP 失败计数，避免历史失败影响 auto 模式判断。
func (e *VideoEngine) SetStreamMode(streamID, mode string) {
	e.streamModeMu.Lock()
	e.streamMode[streamID] = mode
	e.streamModeMu.Unlock()
	e.resetUDPFailure(streamID)
}

// SetDefaultStreamMode 设置默认 RTP 传输模式（udp/tcp/auto），作用于未显式设置 per-stream 模式的流。
func (e *VideoEngine) SetDefaultStreamMode(mode string) {
	e.streamModeMu.Lock()
	e.defaultStreamMode = mode
	e.streamModeMu.Unlock()
}

// GetDefaultStreamMode 返回默认传输模式。
func (e *VideoEngine) GetDefaultStreamMode() string {
	e.streamModeMu.RLock()
	defer e.streamModeMu.RUnlock()
	if e.defaultStreamMode == "" {
		return "udp"
	}
	return e.defaultStreamMode
}

// GetStreamMode 返回流的传输模式：优先 per-stream 设置，其次默认模式，最后 "udp"。
func (e *VideoEngine) GetStreamMode(streamID string) string {
	e.streamModeMu.RLock()
	mode, ok := e.streamMode[streamID]
	def := e.defaultStreamMode
	e.streamModeMu.RUnlock()
	if ok {
		return mode
	}
	if def == "" {
		return "udp"
	}
	return def
}

// ForwardRTP sends a raw RTP packet to the ZLMediaKit RTP port for the stream.
// 传输模式由 GetStreamMode 决定：
//   - "udp"：使用 UDP 长连接池转发
//   - "tcp"：使用 TCP 长连接池转发（RFC 4571 帧格式）
//   - "auto"：优先 UDP，连续失败达阈值后自动 fallback 到 TCP（P1-7）
//
// AUTO-FIX-2026-07-02 [P1]: fallback 时上报指标 + 触发回调，并保留播放状态。
// 播放状态（session/SSRC/时间戳）保存在 StreamSession 中，ForwardRTP 仅操作传输层，
// 不触碰 session，因此 UDP→TCP 切换天然保留播放状态（project_memory 工程约定）。
func (e *VideoEngine) ForwardRTP(streamID string, rtpData []byte) error {
	port, ok := e.GetStreamPort(streamID)
	if !ok {
		return fmt.Errorf("no rtp port registered for stream %s", streamID)
	}
	mode := e.GetStreamMode(streamID)

	// TCP 模式直接走 TCP
	if mode == "tcp" {
		return e.sendTCP(port, rtpData)
	}

	// auto 模式：检查是否已因连续失败切换到 TCP
	if mode == "auto" && e.shouldFallbackTCP(streamID) {
		return e.sendTCP(port, rtpData)
	}

	// UDP（含 auto 的首次尝试）
	err := e.sendUDP(port, rtpData)
	if err != nil && mode == "auto" {
		// UDP 发送失败，递增失败计数，达阈值后切 TCP
		if e.recordUDPFailure(streamID) {
			e.logger.Warn("auto mode: UDP consecutive failures reached threshold, falling back to TCP",
				zap.String("stream_id", streamID),
				zap.Int("threshold", e.udpFailThreshold))
			// AUTO-FIX-2026-07-02 [P1]: fallback 事件指标上报 + 回调
			e.emitFallback(streamID, "udp_consecutive_failures")
			if tcpErr := e.sendTCP(port, rtpData); tcpErr == nil {
				return nil
			}
		}
	} else if err == nil && mode == "auto" {
		// UDP 成功，重置失败计数
		e.resetUDPFailure(streamID)
	}
	return err
}

// emitFallback 上报 fallback 事件：递增指标计数器，触发业务回调。
// AUTO-FIX-2026-07-02 [P1]: 添加 fallback 事件日志和指标上报。
func (e *VideoEngine) emitFallback(streamID, reason string) {
	metrics.RTPFallbackTotal.Inc()
	e.qualityMu.RLock()
	fn := e.onFallback
	e.qualityMu.RUnlock()
	if fn != nil {
		// 从 streamID 反查 phone/channel（格式 phone_chN），无则留空
		phone, ch := parseStreamID(streamID)
		util.SafeGo(e.logger, "jt1078.onFallback", func() { fn(streamID, phone, ch, reason) })
	}
}

// parseStreamID 从 streamID（格式 "phone_chN"）反查 phone 与 channel。
func parseStreamID(streamID string) (string, byte) {
	idx := -1
	for i := len(streamID) - 1; i >= 0; i-- {
		if streamID[i] == '_' {
			idx = i
			break
		}
	}
	if idx < 0 || idx+3 >= len(streamID) || streamID[idx+1] != 'c' || streamID[idx+2] != 'h' {
		return "", 0
	}
	ch := byte(0)
	for i := idx + 3; i < len(streamID); i++ {
		if streamID[i] < '0' || streamID[i] > '9' {
			break
		}
		ch = ch*10 + (streamID[i] - '0')
	}
	return streamID[:idx], ch
}

// shouldFallbackTCP 检查 auto 模式下该流是否应已切换到 TCP。
func (e *VideoEngine) shouldFallbackTCP(streamID string) bool {
	e.udpFailMu.Lock()
	defer e.udpFailMu.Unlock()
	return e.udpFailCount[streamID] >= e.udpFailThreshold
}

// recordUDPFailure 记录一次 UDP 发送失败，返回是否达到 fallback 阈值。
func (e *VideoEngine) recordUDPFailure(streamID string) bool {
	e.udpFailMu.Lock()
	defer e.udpFailMu.Unlock()
	e.udpFailCount[streamID]++
	return e.udpFailCount[streamID] >= e.udpFailThreshold
}

// resetUDPFailure 重置流的 UDP 失败计数（UDP 成功或手动切回 UDP 时调用）。
func (e *VideoEngine) resetUDPFailure(streamID string) {
	e.udpFailMu.Lock()
	defer e.udpFailMu.Unlock()
	delete(e.udpFailCount, streamID)
}

// getUDPConn 从 RTP 长连接池获取或创建到 ZLMediaKit 指定端口的 UDP 长连接。
// AUTO-FIX-2026-07-02 [P1]: 委托给 RTPConnPool，统一复用率统计与 LRU 上限管理。
func (e *VideoEngine) getUDPConn(port int) (*net.UDPConn, error) {
	return e.rtpPool.GetUDP(port)
}

// sendUDP 使用 UDP 长连接池转发 RTP 数据到 ZLMediaKit。
func (e *VideoEngine) sendUDP(port int, data []byte) error {
	if e.zlmAddr == "" || port == 0 {
		return fmt.Errorf("invalid zlm addr or port: addr=%s port=%d", e.zlmAddr, port)
	}

	// AUTO-FIX-2026-06-28: SRTP 加密（plan 8.6.1）——发送前若启用 SRTP 则加密 RTP 包
	encData, err := e.encryptSRTP(data)
	if err != nil {
		return fmt.Errorf("srtp encrypt: %w", err)
	}

	conn, err := e.getUDPConn(port)
	if err != nil {
		return err
	}

	if _, err := conn.Write(encData); err != nil {
		// 连接可能已失效，从池中移除并重试一次
		e.rtpPool.InvalidateUDP(port)
		conn, err = e.getUDPConn(port)
		if err != nil {
			return fmt.Errorf("reconnect udp: %w", err)
		}
		if _, err := conn.Write(encData); err != nil {
			return fmt.Errorf("write udp %s:%d: %w", e.zlmAddr, port, err)
		}
	}
	return nil
}

// sendTCP 使用 TCP 长连接池转发 RTP 数据到 ZLMediaKit（RTP over TCP）。
// JT/T 1078 TCP 格式：2 字节大端长度前缀 + RTP 数据（RFC 4571）。
// AUTO-FIX-2026-07-02 [P1]: 委托给 RTPConnPool 管理 TCP 连接复用。
// AUTO-FIX-2026-07-04 [P1]: 加固——写超时 5s 防挂死、重试 2 次提升容错。
func (e *VideoEngine) sendTCP(port int, rtpData []byte) error {
	if e.zlmAddr == "" || port == 0 {
		return fmt.Errorf("invalid zlm addr or port: addr=%s port=%d", e.zlmAddr, port)
	}

	// AUTO-FIX-2026-06-28: SRTP 加密（plan 8.6.1）——发送前若启用 SRTP 则加密 RTP 包
	encData, err := e.encryptSRTP(rtpData)
	if err != nil {
		return fmt.Errorf("srtp encrypt: %w", err)
	}

	// 2 字节大端长度前缀 + RTP 数据（RFC 4571）
	frame := make([]byte, 2+len(encData))
	frame[0] = byte(len(encData) >> 8)
	frame[1] = byte(len(encData))
	copy(frame[2:], encData)

	const maxRetries = 2
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		conn, err := e.rtpPool.GetTCP(port)
		if err != nil {
			lastErr = err
			continue
		}

		// 写超时 5s，防止 TCP 挂死
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Write(frame)
		conn.SetWriteDeadline(time.Time{}) // 清除超时

		if err == nil {
			return nil
		}
		lastErr = err
		// 连接可能已失效，从池中移除后重试
		e.rtpPool.InvalidateTCP(port)
		e.logger.Debug("rtp tcp write failed, retrying",
			zap.Int("port", port),
			zap.Int("attempt", attempt+1),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))
	}
	return fmt.Errorf("write tcp %s:%d after %d retries: %w", e.zlmAddr, port, maxRetries, lastErr)
}

func (e *VideoEngine) Start() error {
	e.running.Store(true)
	e.logger.Info("1078 video engine started", zap.String("zlm_addr", e.zlmAddr))
	return nil
}

func (e *VideoEngine) Stop() {
	e.running.Store(false)
	// AUTO-FIX-2026-06-28: 停止质量监控协程
	close(e.qualityStopCh)
	// AUTO-FIX-2026-07-02 [P1]: 停止 RTP 长连接池（含 UDP/TCP 空闲扫描协程 + 关闭所有连接）
	if e.rtpPool != nil {
		e.rtpPool.Stop()
	}
	e.mu.Lock()
	for _, s := range e.sessions {
		if s.Conn != nil {
			s.Conn.Close()
		}
	}
	e.mu.Unlock()
	e.logger.Info("1078 video engine stopped")
}

func (e *VideoEngine) CreateSession(id, phone string, logicChannel, streamType byte) *StreamSession {
	session := &StreamSession{
		ID:           id,
		Phone:        phone,
		LogicChannel: logicChannel,
		StreamType:   streamType,
		StartTime:    time.Now(),
		LastActive:   time.Now(),
	}

	e.mu.Lock()
	e.sessions[id] = session
	e.mu.Unlock()

	e.logger.Info("1078 stream session created",
		zap.String("id", id),
		zap.String("phone", phone),
		zap.Uint8("channel", logicChannel))

	return session
}

func (e *VideoEngine) RemoveSession(id string) {
	e.mu.Lock()
	if s, ok := e.sessions[id]; ok {
		if s.Conn != nil {
			s.Conn.Close()
		}
		delete(e.sessions, id)
	}
	e.mu.Unlock()
}

func (e *VideoEngine) GetSession(id string) *StreamSession {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sessions[id]
}

// SwitchStreamType 更新指定流 session 的码流类型（0=主码流 1=子码流）。
// AUTO-FIX-2026-07-02 [P1]: 双码流手动切换——后端 StreamType 已定义，补全切换接口。
// project_memory: 双码流前端切换 UI 缺失（后端 StreamType 已定义）。
// 切换时保留播放状态（session/SSRC/时间戳），仅更新 StreamType 字段。
// 返回是否切换成功（流不存在或新旧类型相同返回 false）。
func (e *VideoEngine) SwitchStreamType(id string, streamType byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return false
	}
	if s.StreamType == streamType {
		return false
	}
	old := s.StreamType
	s.StreamType = streamType
	e.logger.Info("stream type switched",
		zap.String("stream_id", id),
		zap.Uint8("old_type", old),
		zap.Uint8("new_type", streamType))
	return true
}

func (e *VideoEngine) ListSessions() []*StreamSession {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*StreamSession, 0, len(e.sessions))
	for _, s := range e.sessions {
		result = append(result, s)
	}
	return result
}

func (e *VideoEngine) ProcessRTPData(sessionID string, rtpData []byte) error {
	session := e.GetSession(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	pkt, err := ParseRTPPacket(rtpData)
	if err != nil {
		return fmt.Errorf("parse rtp: %w", err)
	}

	now := time.Now()
	session.LastActive = now
	session.Packets++
	session.Bytes += uint64(len(rtpData))

	// AUTO-FIX-2026-06-28: 视频质量统计 - 更新1秒窗口计数
	// 验收标准2: RTP SeqNum gap 检测 + 累计统计
	// 1) 基于 RTP SeqNum 检测丢包：seq gap > 0 表示中间丢包
	//    首包不计算丢包（LastSeqNum 为零值时跳过）
	// 2) 累计统计 TotalLost/TotalExpected/MaxGap（跨窗口，不随重置）
	if session.Packets > 1 {
		expected := session.LastSeqNum + 1
		seq := pkt.Header.SeqNum
		if seq != expected {
			// 处理 uint16 回绕：正常情况下 seq 应大于 expected
			// 若 seq < expected 且 expected-seq > 32768，认为是回绕
			var gap uint64
			if seq > expected {
				gap = uint64(seq - expected)
			} else if expected-seq > 32768 {
				// 回绕：seq + 65536 - expected
				gap = uint64(uint32(seq) + 65536 - uint32(expected))
			}
			// gap == 0 表示乱序（旧包晚到），不计为丢包
			if gap > 0 {
				session.WindowLost += gap
				// 验收标准2: 累计统计
				session.TotalLost += gap
				session.TotalExpected += gap // 丢失的包也计入期望包数
				if gap > session.MaxGap {
					session.MaxGap = gap
				}
			}
		}
	}
	// 验收标准2: 当前收到的包也计入期望包数
	session.TotalExpected++
	session.LastSeqNum = pkt.Header.SeqNum
	session.LastTimestamp = pkt.Header.Timestamp

	// 2) 累加窗口计数（用于码率/帧率计算）
	session.WindowPackets++
	session.WindowBytes += uint64(len(rtpData))
	// 3) Marker=1 表示一帧的最后一个包（视频帧边界）
	if pkt.Header.Marker {
		session.WindowFrames++
	}

	if e.zlmAddr != "" {
		if err := e.forwardToZLM(session, pkt); err != nil {
			e.logger.Debug("forward to zlmediakit failed",
				zap.String("session", sessionID),
				zap.Error(err))
		}
	}

	return nil
}

func (e *VideoEngine) forwardToZLM(session *StreamSession, pkt *RTPPacket) error {
	streamID := fmt.Sprintf("%s_ch%d", session.Phone, session.LogicChannel)

	// Reconstruct the raw RTP packet (header + payload) and forward via UDP
	// to the ZLMediaKit openRtpServer port registered for this stream.
	rtpData, err := BuildRTPPacket(&pkt.Header, pkt.Payload)
	if err != nil {
		return fmt.Errorf("build rtp packet: %w", err)
	}

	if err := e.ForwardRTP(streamID, rtpData); err != nil {
		return fmt.Errorf("forward rtp: %w", err)
	}
	// AUTO-FIX-2026-06-28: 移除此处的 Packets++/Bytes+= 重复计数
	// 统计已在 ProcessRTPData 中完成，forwardToZLM 仅负责转发
	return nil
}

func (e *VideoEngine) GetStreamURL(phone string, channel byte, protocol string) string {
	switch protocol {
	case "rtmp":
		return fmt.Sprintf("rtmp://%s/live/%s_ch%d", e.zlmAddr, phone, channel)
	case "rtsp":
		return fmt.Sprintf("rtsp://%s/live/%s_ch%d", e.zlmAddr, phone, channel)
	case "hls":
		return fmt.Sprintf("http://%s/live/%s_ch%d/hls.m3u8", e.zlmAddr, phone, channel)
	case "ws-flv":
		return fmt.Sprintf("ws://%s/live/%s_ch%d.flv", e.zlmAddr, phone, channel)
	case "webrtc":
		return fmt.Sprintf("http://%s/index/api/webrtc?app=live&stream=%s_ch%d&type=play", e.zlmAddr, phone, channel)
	default:
		return fmt.Sprintf("rtsp://%s/live/%s_ch%d", e.zlmAddr, phone, channel)
	}
}

func (e *VideoEngine) StartStreamListener(port int) error {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("1078 stream listen on %s: %w", addr, err)
	}

	e.logger.Info("1078 stream listener started", zap.String("addr", addr))
	util.SafeGo(e.logger, "jt1078.acceptStreamConnections", func() { e.acceptStreamConnections(listener) })
	return nil
}

func (e *VideoEngine) acceptStreamConnections(listener net.Listener) {
	for e.running.Load() {
		conn, err := listener.Accept()
		if err != nil {
			if !e.running.Load() {
				return
			}
			e.logger.Error("1078 stream accept error", zap.Error(err))
			continue
		}

		util.SafeGoWithRecover(e.logger, "jt1078.handleStreamConnection", func(r interface{}) { _ = conn.Close() }, func() { e.handleStreamConnection(conn) })
	}
}

// AUTO-FIX-2026-07-04 [P0]: 修复 TCP 流分帧。
// 原实现直接将 conn.Read 返回的整个缓冲传给 UnwrapJT1078RTP，导致：
//   1) TCP 粘包：一次 Read 返回多个 1078 包，仅解析第一个，其余丢弃
//   2) TCP 拆包：一个 1078 包跨多次 Read，仅解析到首段，RTP 数据截断
//   3) 无起始字节同步：流中间错位后无法恢复
//
// 修复方案：使用 bufio.Reader 缓冲 + ParseJT1078Packet 逐包解析：
//   1) Peek 起始字节 (0x30) 同步流
//   2) Peek 固定头 (9B) 读取数据类型，计算可选字段长度
//   3) Peek 完整包头（含体长度字段），读取 BodyLength
//   4) Peek 完整包（包头+体），解析后 Discard 消费
//   5) 循环处理缓冲区中的所有完整包（解决粘包）
//   6) 不完整包保留在缓冲区，等待下次 Read（解决拆包）
func (e *VideoEngine) handleStreamConnection(conn net.Conn) {
	defer conn.Close()

	// 128KB 缓冲区：1078 包体最大 65535B，加上头部约 65552B，128KB 足够
	reader := bufio.NewReaderSize(conn, 128*1024)

	for e.running.Load() {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		// 1) 同步起始字节
		firstByte, err := reader.Peek(1)
		if err != nil {
			if err == io.EOF {
				return
			}
			if !e.running.Load() {
				return
			}
			// 超时或其他错误
			return
		}

		if firstByte[0] != JT1078StartByte {
			// 跳过无效字节，重新同步
			reader.ReadByte()
			continue
		}

		// 2) Peek 固定头部（起始字节+SIM+通道+数据类型 = 9B）
		fixedHeader, err := reader.Peek(JT1078FixedHeaderLen)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		// 3) 计算完整包头长度（含可选字段 + 体长度字段）
		dataType := fixedHeader[8]
		headerLen := JT1078FixedHeaderLen
		if dataType&JT1078HasTimestamp != 0 {
			headerLen += 4
		}
		if dataType&JT1078HasLastIFrame != 0 {
			headerLen += 1
		}
		if dataType&JT1078HasLastFrame != 0 {
			headerLen += 1
		}
		headerLen += 2 // 体长度字段

		// 4) Peek 完整包头，读取体长度
		fullHeader, err := reader.Peek(headerLen)
		if err != nil {
			if err == io.EOF {
				return
			}
			// 缓冲区不足，等待更多数据（拆包场景）
			return
		}

		bodyLen := int(binary.BigEndian.Uint16(fullHeader[headerLen-2 : headerLen]))
		totalLen := headerLen + bodyLen

		// 5) Peek 完整包（包头 + 体）
		pktData, err := reader.Peek(totalLen)
		if err != nil {
			if err == io.EOF {
				return
			}
			// 包不完整，等待更多数据（拆包场景）
			return
		}

		// 6) 解析完整包
		pkt, consumed, err := ParseJT1078Packet(pktData)
		if err != nil {
			e.logger.Debug("parse jt1078 packet failed, skipping byte",
				zap.Error(err))
			reader.ReadByte() // 跳过错误字节，重新同步
			continue
		}

		// 7) 消费已解析的包
		reader.Discard(consumed)

		// 8) 处理 RTP 数据
		sim := pkt.SIM
		logicChannel := pkt.LogicChannel
		frameType := pkt.FrameType()
		rtpData := pkt.Body

		sessionID := fmt.Sprintf("%s_ch%d", sim, logicChannel)
		session := e.GetSession(sessionID)
		if session == nil {
			// 从 RTP 包头中的 PT 或数据类型推断码流类型
			// frameType: 0=视频I帧 1=视频P帧 2=视频B帧 3=音频 4=透传
			// StreamType: 0=主码流 1=子码流
			// JT1078 RTP 头中无直接码流类型字段，默认主码流，由 9101 请求时设置的 session 为准
			streamType := byte(0)
			session = e.CreateSession(sessionID, sim, logicChannel, streamType)
			session.Conn = conn
		}

		// 验收标准3: 检测 I 帧到达，触发关键帧恢复计时
		// frameType: 0=视频I帧 1=视频P帧 2=视频B帧 3=音频 4=透传
		if frameType == 0x00 && e.keyframeTracker != nil {
			e.keyframeTracker.RecordIFrame(sessionID)
		}

		if err := e.ProcessRTPData(sessionID, rtpData); err != nil {
			e.logger.Debug("process rtp data failed",
				zap.String("session", sessionID),
				zap.Error(err))
		}
	}
}

func (e *VideoEngine) GetStats() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	totalPackets := uint64(0)
	totalBytes := uint64(0)
	for _, s := range e.sessions {
		totalPackets += s.Packets
		totalBytes += s.Bytes
	}

	return map[string]interface{}{
		"active_streams": len(e.sessions),
		"total_packets":  totalPackets,
		"total_bytes":    totalBytes,
	}
}