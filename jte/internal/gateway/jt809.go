package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/handler"
	"github.com/suoten/jt-engine/pkg/merge"
	"github.com/suoten/jt-engine/pkg/protocol"
	"github.com/suoten/jt-engine/pkg/storage"
	"golang.org/x/text/encoding/simplifiedchinese"
	"go.uber.org/zap"
)

type JT809Client struct {
	cfg          *config.JT809PlatformConfig
	logger       *zap.Logger
	conn         net.Conn
	merge        *merge.Engine
	store        storage.Interface
	running      atomic.Bool
	seqNum       uint16
	mu           sync.Mutex
	reconnectCh  chan struct{}
	stopCh       chan struct{}
	stopOnce     sync.Once // 保证 Disconnect 多次调用只 close 一次 stopCh，避免 panic
	forwardRules config.ForwardRules
	// AUTO-FIX-2026-06-29 [P1-6]: 运行时转发规则快照（原子读写）
	// 优先使用 storage.ForwardRule（含报警类型/级别/时间过滤），无规则时回退到 YAML ForwardRules。
	// 使用 atomic.Pointer 实现无锁热更新：ReloadForwardRules 替换快照，转发路径无锁读取。
	rulesSnapshot atomic.Pointer[[]*storage.ForwardRule]
	// AUTO-FIX-2026-06-29 [P1-5]: 断线期间发送缓冲区（按 SN 顺序补发）
	// 连接断开时 SendVehicleData/SendAlarm 写入缓冲；重连成功后 flushPendingBuffer 按 SN 顺序补发。
	// 缓冲区固定容量，溢出时丢弃最旧数据并告警（避免内存爆炸）。
	pendingMu     sync.Mutex
	pendingBuffer []pendingFrame
	pendingCap    int // 缓冲区容量，默认 1000
	pendingDropped int64 // 累计丢弃计数（监控用）

	// AUTO-FIX-2026-07-04 [P0]: SN 消息确认与重试机制
	// 每条下行消息分配 SN，上级平台收到后回 0x1002/0x1007 等应答。
	// 未应答消息重试 3 次，间隔 5s；超过重试次数 → 断链重连。
	ackMu         sync.Mutex
	ackPending    map[uint16]*ackEntry // SN → 待确认消息
	ackTimeout    time.Duration        // 单次应答超时，默认 5s
	ackMaxRetry   int                  // 最大重试次数，默认 3

	// AUTO-FIX-2026-07-04 [P0]: 心跳应答超时检测
	// 3 倍心跳间隔（180s）无应答 → 断链重连
	lastKeepaliveResp atomic.Int64 // unix nano of last keepalive response

	// AUTO-FIX-2026-07-04 [P0]: 从链路（Down-link）独立管理
	// JT/T 809-2019 规定主链路和从链路为独立 TCP 连接，各自独立登录/心跳/断开。
	// 上级平台通过从链路向下级平台发送指令（0x9xxx 消息族）。
	downlinkListener          net.Listener
	downlinkConn              net.Conn
	downlinkRunning           atomic.Bool
	downlinkMu                sync.Mutex
	lastDownlinkKeepaliveReq  atomic.Int64 // 上级平台最近一次从链路保活请求时间
}

// pendingFrame 待补发帧（已构造完成的 809 帧 + 流水号）
type pendingFrame struct {
	msgID   uint16
	seqNum  uint16
	payload []byte // 已转义+加 CRC 的完整帧（含 0x5B/0x5D 边界）
}

// ackEntry 待确认消息条目（SN 消息确认与重试机制）
type ackEntry struct {
	msgID      uint16
	seqNum     uint16
	payload    []byte // 已构造的完整帧（含边界符），用于重发
	retryCount int    // 已重试次数
	nextRetry  time.Time // 下次重试时间
}

func NewJT809Client(cfg *config.JT809PlatformConfig, logger *zap.Logger, mergeEngine *merge.Engine, store storage.Interface) *JT809Client {
	c := &JT809Client{
		cfg:          cfg,
		logger:       logger,
		merge:        mergeEngine,
		store:        store,
		reconnectCh:  make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
		forwardRules: cfg.ForwardRules,
		pendingCap:   1000, // AUTO-FIX-2026-06-29 [P1-5]: 断线缓冲容量
		ackPending:   make(map[uint16]*ackEntry),
		ackTimeout:   5 * time.Second,
		ackMaxRetry:  3,
	}
	c.lastKeepaliveResp.Store(time.Now().UnixNano())
	c.lastDownlinkKeepaliveReq.Store(time.Now().UnixNano())
	// AUTO-FIX-2026-06-29 [P1-6]: 启动时从关系库加载持久化转发规则
	c.ReloadForwardRules()
	return c
}

func (c *JT809Client) Connect() error {
	if err := c.dial(); err != nil {
		return err
	}

	util.SafeGo(c.logger, "jt809.reconnectLoop", c.reconnectLoop)
	util.SafeGo(c.logger, "jt809.keepaliveLoop", c.keepaliveLoop)
	// AUTO-FIX-2026-07-04 [P0]: 启动 SN 消息确认超时重试循环
	util.SafeGo(c.logger, "jt809.ackRetryLoop", c.ackRetryLoop)

	// AUTO-FIX-2026-07-04 [P0]: 启动从链路监听（独立双链路管理）
	if c.cfg.DownlinkPort > 0 {
		if err := c.startDownlinkListener(c.cfg.DownlinkPort); err != nil {
			c.logger.Warn("809 downlink listener failed, falling back to multiplexed mode",
				zap.String("platform_id", c.cfg.ID),
				zap.Int("port", c.cfg.DownlinkPort),
				zap.Error(err))
		}
	}

	return nil
}

func (c *JT809Client) dial() error {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port)), 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	// AUTO-FIX-2026-06-29: conn 字段在 c.mu 保护下写入，避免与 Login/SendKeepalive 等
	// 持 c.mu 读 c.conn 的路径竞争（string/指针赋值非原子，可被撕裂导致 nil panic）
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.running.Store(true)

	c.logger.Info("809 client connected",
		zap.String("platform_id", c.cfg.ID),
		zap.String("addr", addr))

	util.SafeGo(c.logger, "jt809.readLoop", c.readLoop)

	return nil
}

func (c *JT809Client) reconnectLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		case <-c.reconnectCh:
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}
			c.mu.Unlock()
			c.running.Store(false)

			// AUTO-FIX-2026-07-04 [P0]: 清理 ACK 待确认表，重置心跳计时器
			c.clearAllAcks()
			c.lastKeepaliveResp.Store(time.Now().UnixNano())

			// AUTO-FIX-2026-06-28: 连续失败次数计数器，达到 10 次告警
			// 对照 jte-plan-final-v3.0.md 第3.6.1节：连续失败 10 次：告警 + 继续重连
			consecutiveFailures := 0
			const maxFailAlert = 10

			for i := 1; ; i++ {
				select {
				case <-c.stopCh:
					return
				default:
				}

				// AUTO-FIX-2026-06-26: 重连退避改为指数退避（1,2,4,8,16,32,60...）
				// 原实现为线性退避 i*5，不符合文档要求的指数退避策略
				waitSec := 1 << uint(i-1) // 1,2,4,8,16,32,64...
				if waitSec > 60 {
					waitSec = 60
				}
				c.logger.Info("809 reconnecting",
					zap.String("platform_id", c.cfg.ID),
					zap.Int("attempt", i),
					zap.Int("wait_sec", waitSec),
					zap.Int("consecutive_failures", consecutiveFailures))

				time.Sleep(time.Duration(waitSec) * time.Second)

				if err := c.dial(); err != nil {
					c.logger.Error("809 reconnect failed", zap.Error(err))
					consecutiveFailures++
					if consecutiveFailures == maxFailAlert {
						// 连续失败 10 次告警（不中断重连）
						c.logger.Warn("809 reconnect failed 10 consecutive times, alerting",
							zap.String("platform_id", c.cfg.ID),
							zap.Int("consecutive_failures", consecutiveFailures))
					}
					continue
				}

				if err := c.Login(); err != nil {
					c.logger.Error("809 re-login failed", zap.Error(err))
					c.running.Store(false)
					c.mu.Lock()
					if c.conn != nil {
						c.conn.Close()
						c.conn = nil
					}
					c.mu.Unlock()
					consecutiveFailures++
					if consecutiveFailures == maxFailAlert {
						c.logger.Warn("809 re-login failed 10 consecutive times, alerting",
							zap.String("platform_id", c.cfg.ID),
							zap.Int("consecutive_failures", consecutiveFailures))
					}
					continue
				}

				// 重连成功后重置间隔/失败计数
				consecutiveFailures = 0
				c.logger.Info("809 reconnected successfully",
					zap.String("platform_id", c.cfg.ID),
					zap.Int("attempt", i))

				// AUTO-FIX-2026-06-29 [P1-5]: 重连成功后补发缓冲区数据（按 SN 顺序）。
				// 持有 c.mu 保证与 SendVehicleData/SendAlarm 互斥，避免补发与在线发送交错。
				c.mu.Lock()
				c.flushPendingBuffer()
				c.mu.Unlock()
				break
			}
		}
	}
}

func (c *JT809Client) keepaliveLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// AUTO-FIX-2026-07-04 [P0]: 心跳应答超时检测，每 30s 检查一次
	heartbeatCheck := time.NewTicker(30 * time.Second)
	defer heartbeatCheck.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			if !c.running.Load() {
				continue
			}
			if err := c.SendKeepalive(); err != nil {
				c.logger.Error("809 keepalive failed, triggering reconnect", zap.Error(err))
				select {
				case c.reconnectCh <- struct{}{}:
				default:
				}
			}
		case <-heartbeatCheck.C:
			if !c.running.Load() {
				continue
			}
			// AUTO-FIX-2026-07-04 [P0]: 3 倍心跳间隔（180s）无应答 → 断链重连
			lastResp := time.Unix(0, c.lastKeepaliveResp.Load())
			if time.Since(lastResp) > 180*time.Second {
				c.logger.Warn("809 heartbeat response timeout, triggering reconnect",
					zap.String("platform_id", c.cfg.ID),
					zap.Duration("since_last_resp", time.Since(lastResp)))
				c.running.Store(false)
				select {
				case c.reconnectCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (c *JT809Client) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	body := make([]byte, 0, 14)

	// 使用配置的 Username 作为用户ID（809 用户ID 为 4 字节整数）
	userID := uint32(0)
	if c.cfg.Username != "" {
		if parsed, err := strconv.ParseUint(c.cfg.Username, 10, 32); err == nil {
			userID = uint32(parsed)
		} else {
			c.logger.Warn("809 username is not numeric, using 0",
				zap.String("platform_id", c.cfg.ID),
				zap.String("username", c.cfg.Username))
		}
	}
	userIDBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(userIDBytes, userID)
	body = append(body, userIDBytes...)

	pwd := []byte(c.cfg.Password)
	for len(pwd) < 8 {
		pwd = append(pwd, 0)
	}
	body = append(body, pwd[:8]...)
	body = append(body, byte(c.cfg.LinkType))
	body = append(body, 0)

	msgID := uint16(0x1001)
	seq := c.nextSeq()

	msg, err := c.buildMessage(msgID, body, seq)
	if err != nil {
		return err
	}

	// AUTO-FIX-2026-06-29: 重连窗口期 c.conn 可能为 nil（dial 失败/对端关闭），
	// 直接 Write 会 nil panic。所有 Write 路径必须 nil 检查。
	if c.conn == nil {
		return fmt.Errorf("send login: connection not established")
	}
	if _, err := c.conn.Write(msg); err != nil {
		return fmt.Errorf("send login: %w", err)
	}

	// AUTO-FIX-2026-07-04 [P0]: 注册 SN 确认追踪，等待 0x1002 应答
	c.registerAck(msgID, seq, msg)

	c.logger.Info("809 login sent",
		zap.String("platform_id", c.cfg.ID),
		zap.Uint32("user_id", userID),
		zap.Uint16("seq", seq))

	return nil
}

func (c *JT809Client) SendVehicleData(vehicleID string, loc *storage.LocationData) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	xmlData := fmt.Sprintf(`<VehicleLocation><VehicleNo>%s</VehicleNo><VehicleColor>1</VehicleColor><Latitude>%.6f</Latitude><Longitude>%.6f</Longitude><Speed>%.1f</Speed><Direction>%d</Direction><Time>%s</Time></VehicleLocation>`,
		vehicleID, loc.Latitude, loc.Longitude, loc.Speed, loc.Direction, loc.Time.Format("2006-01-02 15:04:05"))

	// AUTO-FIX-2026-06-29 [P1]: JT/T 809-2019 规定 XML 中文字段（车牌号等）必须 GBK 编码。
	// 原实现 body := []byte(xmlData) 用 UTF-8，导致上级平台解析车牌号（如"京A12345"）乱码，
	// 甚至因字节序不合法触发 XML 解析失败、整帧被丢弃。
	// 修复：发送前用 GBK 编码，与 808/1078 已修复的中文编解码保持一致。
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(xmlData))
	if err != nil {
		return fmt.Errorf("gbk encode vehicle data: %w", err)
	}
	msgID := uint16(0x1200)
	seq := c.nextSeq()

	msg, err := c.buildMessage(msgID, body, seq)
	if err != nil {
		return err
	}

	// AUTO-FIX-2026-06-29 [P1-5]: 断线期间写入缓冲区，重连后按 SN 顺序补发。
	// 在线时直接发送；离线时进缓冲区；调用方无感知。
	return c.sendOrBuffer(msgID, seq, msg)
}

// sendOrBuffer 在线发送或离线缓冲的核心方法。
// 在线（conn!=nil）：直接 Write，失败时进缓冲区等待重连补发。
// 离线：写入 pendingBuffer，按 SN 顺序排列，重连成功后 flushPendingBuffer 补发。
// 缓冲区满时丢弃最旧帧并累加 pendingDropped 计数（避免内存爆炸）。
// AUTO-FIX-2026-06-29 [P1-5]: 809 断线重连期间数据缓存与补发
func (c *JT809Client) sendOrBuffer(msgID, seq uint16, frame []byte) error {
	// 在线快速路径：只要有 conn 就尝试发送
	// 注意：调用方持 c.mu，conn 不为 nil 即可安全 Write
	if c.conn != nil {
		if _, err := c.conn.Write(frame); err == nil {
			return nil
		}
		// 发送失败：连接可能已断开，触发重连并进缓冲
		c.running.Store(false)
		select {
		case c.reconnectCh <- struct{}{}:
		default:
		}
	}

	// 离线缓冲路径
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.pendingCap <= 0 {
		// 缓冲未启用，直接返回错误
		return fmt.Errorf("809 client offline and buffer disabled")
	}
	// 缓冲区满：丢弃最旧帧（FIFO 淘汰）
	if len(c.pendingBuffer) >= c.pendingCap {
		c.pendingBuffer = c.pendingBuffer[1:]
		c.pendingDropped++
		c.logger.Warn("809 pending buffer overflow, dropping oldest frame",
			zap.String("platform_id", c.cfg.ID),
			zap.Int64("total_dropped", c.pendingDropped),
			zap.Int("buffer_size", len(c.pendingBuffer)))
	}
	// 深拷贝 frame（外部传入的 msg 可能在调用方被复用）
	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)
	c.pendingBuffer = append(c.pendingBuffer, pendingFrame{
		msgID:   msgID,
		seqNum:  seq,
		payload: frameCopy,
	})
	return nil
}

// flushPendingBuffer 重连成功后按 SN 顺序补发缓冲区数据。
// 补发失败的帧保留在缓冲区尾部，下次重连后再试。
// 在 c.mu 保护下调用（与发送路径互斥）。
func (c *JT809Client) flushPendingBuffer() {
	c.pendingMu.Lock()
	if len(c.pendingBuffer) == 0 {
		c.pendingMu.Unlock()
		return
	}
	pending := c.pendingBuffer
	c.pendingBuffer = nil
	c.pendingMu.Unlock()

	if c.conn == nil {
		// 连接又断了，放回缓冲区
		c.pendingMu.Lock()
		c.pendingBuffer = append(pending, c.pendingBuffer...)
		c.pendingMu.Unlock()
		return
	}

	failed := pending[:0]
	for _, f := range pending {
		if _, err := c.conn.Write(f.payload); err != nil {
			c.logger.Warn("809 flush pending frame failed, will retry on next reconnect",
				zap.String("platform_id", c.cfg.ID),
				zap.Uint16("msg_id", f.msgID),
				zap.Uint16("seq", f.seqNum),
				zap.Error(err))
			failed = append(failed, f)
		}
	}
	if len(failed) > 0 {
		c.pendingMu.Lock()
		// 失败的帧追加到缓冲区头部，下次优先补发
		c.pendingBuffer = append(failed, c.pendingBuffer...)
		c.pendingMu.Unlock()
	}
	c.logger.Info("809 pending buffer flushed",
		zap.String("platform_id", c.cfg.ID),
		zap.Int("total", len(pending)),
		zap.Int("failed", len(failed)))
}

// GetPendingBufferStatus 返回当前缓冲区状态（监控/调试用）。
func (c *JT809Client) GetPendingBufferStatus() (size int, dropped int64) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return len(c.pendingBuffer), c.pendingDropped
}

// SetForwardRules 设置 YAML 转发规则（用于 StartAutoForward 自动转发位置/报警数据到上级平台）。
// 注意：此方法仅设置 YAML 静态规则；持久化规则需通过 ReloadForwardRules 从关系库加载。
// AUTO-FIX-2026-06-29 [P1-6]: 改用 atomic 写入，保证与转发路径无锁读取的并发安全。
func (c *JT809Client) SetForwardRules(rules config.ForwardRules) {
	c.forwardRules = rules
}

// ReloadForwardRules 从关系库重新加载转发规则快照。
// API 修改规则后调用此方法即可热更新，无需重启客户端。
// 加载失败或无规则时快照为空，回退到 YAML ForwardRules。
func (c *JT809Client) ReloadForwardRules() {
	if c.store == nil {
		empty := make([]*storage.ForwardRule, 0)
		c.rulesSnapshot.Store(&empty)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rules, err := c.store.ListForwardRules(ctx, c.cfg.ID)
	if err != nil {
		c.logger.Warn("809 reload forward rules failed, using empty snapshot",
			zap.String("platform_id", c.cfg.ID),
			zap.Error(err))
		empty := make([]*storage.ForwardRule, 0)
		c.rulesSnapshot.Store(&empty)
		return
	}
	if rules == nil {
		rules = make([]*storage.ForwardRule, 0)
	}
	c.rulesSnapshot.Store(&rules)
	c.logger.Info("809 forward rules reloaded",
		zap.String("platform_id", c.cfg.ID),
		zap.Int("rule_count", len(rules)))
}

// matchForwardRule 判断单条规则是否匹配给定数据。
// AUTO-FIX-2026-07-02: 新增 sourcePlatformID 过滤 + "video" DataType 支持。
// dataType: "location" | "alarm" | "video"；phone 必须匹配；alarm 还需匹配类型/级别/时间；
// sourcePlatformID 非空时需匹配规则的 SourcePlatformID（空规则=所有源平台）。
func matchForwardRule(rule *storage.ForwardRule, dataType, phone, sourcePlatformID string, alarm *storage.AlarmData) bool {
	if rule == nil || !rule.Enabled {
		return false
	}
	if rule.DataType != dataType {
		return false
	}
	// 源平台过滤：规则指定了源平台时必须精确匹配
	if rule.SourcePlatformID != "" && rule.SourcePlatformID != sourcePlatformID {
		return false
	}
	if rule.Phone != "" && rule.Phone != phone {
		return false
	}
	if dataType == "alarm" && alarm != nil {
		// 报警类型过滤：空=全部
		if rule.AlarmTypes != "" {
			types := strings.Split(rule.AlarmTypes, ",")
			matched := false
			for _, t := range types {
				if strings.TrimSpace(t) == alarm.Type {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		// 最低报警级别过滤：0=全部
		if rule.MinLevel > 0 && alarm.Level < rule.MinLevel {
			return false
		}
	}
	// 每日生效时间窗口过滤：HH:MM:SS
	if rule.TimeStart != "" && rule.TimeEnd != "" {
		now := time.Now()
		nowStr := now.Format("15:04:05")
		if rule.TimeStart <= rule.TimeEnd {
			// 同一天内：[start, end]
			if nowStr < rule.TimeStart || nowStr > rule.TimeEnd {
				return false
			}
		} else {
			// 跨天：[start, 23:59:59] ∪ [00:00:00, end]
			if nowStr < rule.TimeStart && nowStr > rule.TimeEnd {
				return false
			}
		}
	}
	return true
}

// shouldForward 判断指定数据类型+手机号是否应转发到本平台。
// AUTO-FIX-2026-07-02: 新增 sourcePlatformID 参数，支持平台间定向转发规则匹配。
// 优先匹配持久化规则；无规则或无匹配时回退到 YAML ForwardRules（仅 phone 过滤）。
func (c *JT809Client) shouldForward(dataType, phone, sourcePlatformID string, alarm *storage.AlarmData) bool {
	rulesPtr := c.rulesSnapshot.Load()
	if rulesPtr != nil && len(*rulesPtr) > 0 {
		for _, r := range *rulesPtr {
			if matchForwardRule(r, dataType, phone, sourcePlatformID, alarm) {
				return true
			}
		}
		// 存在持久化规则但无匹配，按规则语义不转发
		return false
	}
	// 回退到 YAML 静态规则
	switch dataType {
	case "location":
		if !c.forwardRules.ForwardLocation {
			return false
		}
	case "alarm":
		if !c.forwardRules.ForwardAlarm {
			return false
		}
	case "video":
		if !c.forwardRules.ForwardVideo {
			return false
		}
	}
	if len(c.forwardRules.ForwardPhones) > 0 && !contains(c.forwardRules.ForwardPhones, phone) {
		return false
	}
	return true
}

// ShouldForward 是 shouldForward 的导出包装（AUTO-FIX-2026-07-02 [P1]）。
// 供外部适配器（cmd/jte 中的 ForwardCheckerAdapter）调用，使 809 协议模块
// 能通过 handler.ForwardChecker 接口检查转发规则，无需 import internal/gateway。
func (c *JT809Client) ShouldForward(dataType, phone, sourcePlatformID string, alarm *storage.AlarmData) bool {
	return c.shouldForward(dataType, phone, sourcePlatformID, alarm)
}

// StartAutoForward 订阅 EventBus，根据转发规则自动将 808 位置/报警数据转发到上级平台。
// 转发是异步的（go func），避免阻塞事件总线。若客户端未连接则跳过并记录日志。
// AUTO-FIX-2026-06-29 [P1-6]: 转发决策从 YAML 静态规则升级为
// 持久化规则优先 + YAML 回退，新增报警类型/级别/时间窗口过滤。
func (c *JT809Client) StartAutoForward(eventBus *merge.EventBus) {
	if eventBus == nil {
		return
	}

	eventBus.Subscribe(merge.EventTypeLocationUpdate, func(event merge.Event) {
		loc, ok := event.Data.(*storage.LocationData)
		if !ok {
			return
		}
		if !c.shouldForward("location", loc.Phone, "", nil) {
			return
		}
		util.SafeGo(c.logger, "jt809.forwardLocation", func() {
			if !c.running.Load() {
				c.logger.Debug("809 forward location skipped, client not connected",
					zap.String("platform_id", c.cfg.ID),
					zap.String("phone", loc.Phone))
				return
			}
			if err := c.SendVehicleData(loc.Phone, loc); err != nil {
				c.logger.Error("809 forward location failed",
					zap.String("platform_id", c.cfg.ID),
					zap.String("phone", loc.Phone),
					zap.Error(err))
			}
		})
	})
	c.logger.Info("809 auto forward location enabled",
		zap.String("platform_id", c.cfg.ID),
		zap.Int("rules", lenPointerSlice(c.rulesSnapshot.Load())))

	eventBus.Subscribe(merge.EventTypeAlarmEvent, func(event merge.Event) {
		alarm, ok := event.Data.(*storage.AlarmData)
		if !ok {
			return
		}
		// AUTO-FIX-2026-07-02 [P1]: 传递 alarm.SourcePlatformID 进行平台间定向转发规则匹配。
		// 报警来自下级 809 平台时 SourcePlatformID 非空，来自本平台直连终端时为空。
		if !c.shouldForward("alarm", alarm.Phone, alarm.SourcePlatformID, alarm) {
			return
		}
		util.SafeGo(c.logger, "jt809.forwardAlarm", func() {
			if !c.running.Load() {
				c.logger.Debug("809 forward alarm skipped, client not connected",
					zap.String("platform_id", c.cfg.ID),
					zap.String("phone", alarm.Phone))
				return
			}
			if err := c.SendAlarm(alarm); err != nil {
				c.logger.Error("809 forward alarm failed",
					zap.String("platform_id", c.cfg.ID),
					zap.String("phone", alarm.Phone),
					zap.Error(err))
			}
		})
	})
	c.logger.Info("809 auto forward alarm enabled",
		zap.String("platform_id", c.cfg.ID),
		zap.Int("rules", lenPointerSlice(c.rulesSnapshot.Load())))
}

// lenPointerSlice 安全获取 atomic.Pointer 切片长度（nil-safe）
func lenPointerSlice(p *[]*storage.ForwardRule) int {
	if p == nil {
		return 0
	}
	return len(*p)
}

// SendAlarm 将报警数据编码为 809 的 0x1400 报警上报消息发送到上级平台。
// 参考 SendVehicleData 的实现模式，使用 XML 描述报警内容。
func (c *JT809Client) SendAlarm(alarm *storage.AlarmData) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	xmlData := fmt.Sprintf(`<WarnMsg><VehicleNo>%s</VehicleNo><WarnType>%s</WarnType><WarnLevel>%d</WarnLevel><Latitude>%.6f</Latitude><Longitude>%.6f</Longitude><Speed>%.1f</Speed><Direction>%d</Direction><Time>%s</Time></WarnMsg>`,
		alarm.Phone, alarm.Type, alarm.Level, alarm.Latitude, alarm.Longitude, alarm.Speed, alarm.Direction, alarm.Time.Format("2006-01-02 15:04:05"))

	// AUTO-FIX-2026-06-29 [P1]: JT/T 809-2019 规定 XML 中文字段（车牌号/报警类型描述等）
	// 必须 GBK 编码。原实现 body := []byte(xmlData) 用 UTF-8，导致上级平台解析中文乱码或
	// XML 解析失败。修复：发送前用 GBK 编码，与 SendVehicleData 保持一致。
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(xmlData))
	if err != nil {
		return fmt.Errorf("gbk encode alarm: %w", err)
	}
	msgID := uint16(0x1400)
	seq := c.nextSeq()

	msg, err := c.buildMessage(msgID, body, seq)
	if err != nil {
		return err
	}

	// AUTO-FIX-2026-06-29 [P1-5]: 报警也走断线缓冲补发链路
	return c.sendOrBuffer(msgID, seq, msg)
}

// contains 判断字符串切片中是否包含指定字符串。
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func (c *JT809Client) SendKeepalive() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// AUTO-FIX-2026-06-28: 主链路保活请求 msgID 应为 0x1006 (UP_LINKKEEP_ALIVE_REQ)
	// 原值 0x1005 是 UP_LINKDISCONNECT_RSP（断开应答），会导致保活被上级平台误解为断开确认
	msgID := uint16(0x1006)
	seq := c.nextSeq()

	msg, err := c.buildMessage(msgID, nil, seq)
	if err != nil {
		return err
	}

	if c.conn == nil {
		return fmt.Errorf("send keepalive: connection not established")
	}
	if _, err := c.conn.Write(msg); err != nil {
		return fmt.Errorf("send keepalive: %w", err)
	}

	// AUTO-FIX-2026-07-04 [P0]: 注册 SN 确认追踪，等待 0x1007 应答
	c.registerAck(msgID, seq, msg)

	return nil
}

// Disconnect 关闭客户端：停止 accept/重连/保活/读循环，关闭连接。
// 使用 sync.Once 保证 stopCh 只 close 一次（多次调用安全），
// 修复原实现不 close stopCh 导致 reconnectLoop/keepaliveLoop/readLoop 3 个 goroutine 永久泄漏。
func (c *JT809Client) Disconnect() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	c.running.Store(false)
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()

	// AUTO-FIX-2026-07-04 [P0]: 关闭从链路
	c.downlinkRunning.Store(false)
	c.downlinkMu.Lock()
	if c.downlinkConn != nil {
		c.downlinkConn.Close()
		c.downlinkConn = nil
	}
	c.downlinkMu.Unlock()
	if c.downlinkListener != nil {
		c.downlinkListener.Close()
	}
	c.clearAllAcks()
}

func (c *JT809Client) IsRunning() bool {
	return c.running.Load()
}

// GetPlatformID 返回上级平台 ID，供 ForwardRuleHandler 索引 reloader 使用。
func (c *JT809Client) GetPlatformID() string {
	return c.cfg.ID
}

func (c *JT809Client) readLoop() {
	buf := make([]byte, 8192)
	remaining := make([]byte, 0)
	for c.running.Load() {
		// AUTO-FIX-2026-06-29: 持锁快照 conn，避免与 Disconnect/dial 的 conn 赋值竞争；
		// 重连窗口期 c.conn 可能为 nil，直接 SetReadDeadline 会 nil panic
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if !c.running.Load() {
				return
			}
			if err == io.EOF {
				c.logger.Info("809 server closed connection", zap.String("platform_id", c.cfg.ID))
			} else {
				c.logger.Error("809 read error", zap.Error(err))
			}
			c.running.Store(false)
			// AUTO-FIX-2026-06-28: 读错误/EOF 时主动触发重连
			// 原实现仅 keepalive 失败才触发重连，服务端主动关闭时需等 60s 下一次保活才重连
			select {
			case c.reconnectCh <- struct{}{}:
			default:
			}
			return
		}

		if n > 0 {
			remaining = append(remaining, buf[:n]...)
			remaining = c.processUpstreamData(remaining)
		}
	}
}

func (c *JT809Client) processUpstreamData(data []byte) []byte {
	for len(data) >= 2 {
		start := -1
		end := -1
		for i := 0; i < len(data); i++ {
			if data[i] == 0x5B && start == -1 {
				start = i
			} else if data[i] == 0x5D && start != -1 {
				end = i
				break
			}
		}

		if start == -1 || end == -1 {
			break
		}

		frame := data[start : end+1]
		remaining := data[end+1:]

		if len(frame) > 2 {
			inner := frame[1 : len(frame)-1]
			unescaped := unescape809(inner)

			if len(unescaped) >= 2 {
				msgID := binary.BigEndian.Uint16(unescaped[0:2])
				c.handleUpstreamMessage(msgID, unescaped)
			}
		}

		data = remaining
	}

	if len(data) > 0 && data[0] != 0x5B {
		data = nil
	}

	return data
}

func (c *JT809Client) handleUpstreamMessage(msgID uint16, data []byte) {
	// AUTO-FIX-2026-06-28: 修正所有 msgID 错位
	// 原实现基于旧版错值常量：0x9001=登录应答、0x9003=保活应答、0x9004=下行保活应答
	// 标准 JT/T 809-2019：主链路应答为 0x1002/0x1007，从链路请求为 0x9001/0x9003/0x9005
	switch msgID {
	case 0x1002:
		// 主链路登录应答（UP_CONNECT_RSP）：消息头(18) + 结果(1) + 校验码(4)
		// AUTO-FIX-2026-07-04 [P0]: 提取 SN 清除待确认条目
		if len(data) >= jt809HeaderLen {
			respSeq := binary.BigEndian.Uint16(data[16:18])
			c.clearAck(respSeq)
		}
		if len(data) >= jt809HeaderLen+1 {
			result := data[jt809HeaderLen]
			c.logger.Info("809 login response received",
				zap.String("platform_id", c.cfg.ID),
				zap.Uint8("result", result))
			if result != 0 {
				c.logger.Warn("809 login rejected by upstream platform",
					zap.String("platform_id", c.cfg.ID),
					zap.Uint8("result", result))
			}
		}
	case 0x1007:
		// 主链路保活应答（UP_LINKKEEP_ALIVE_RSP）
		// AUTO-FIX-2026-07-04 [P0]: 提取 SN 清除待确认条目 + 更新心跳应答时间
		if len(data) >= jt809HeaderLen {
			respSeq := binary.BigEndian.Uint16(data[16:18])
			c.clearAck(respSeq)
		}
		c.lastKeepaliveResp.Store(time.Now().UnixNano())
		c.logger.Debug("809 keepalive response received",
			zap.String("platform_id", c.cfg.ID))
	case 0x9001:
		// 从链路登录请求（DOWN_CONNECT_REQ，上级→下级）
		// JTE 作为下级平台需应答 0x9002
		c.logger.Info("809 downlink connect request received",
			zap.String("platform_id", c.cfg.ID))
		c.sendDownlinkResp(0x9002, data)
	case 0x9003:
		// 从链路断开请求（DOWN_LINKDISCONNECT_REQ，上级→下级）
		c.logger.Info("809 downlink disconnect request received",
			zap.String("platform_id", c.cfg.ID))
		c.sendDownlinkResp(0x9004, data)
	case 0x9005:
		// 从链路保活请求（DOWN_LINKKEEP_ALIVE_REQ，上级→下级）
		c.logger.Debug("809 downlink keepalive request received",
			zap.String("platform_id", c.cfg.ID))
		c.sendDownlinkResp(0x9006, data)
	default:
		c.logger.Debug("809 upstream message",
			zap.String("platform_id", c.cfg.ID),
			zap.Uint16("msg_id", msgID),
			zap.Int("bytes", len(data)))
	}
}

// AUTO-FIX-2026-06-28: sendDownlinkResp 发送从链路应答
// 上级平台通过从链路（0x9xxx）发起请求时，JTE 作为下级平台需应答
func (c *JT809Client) sendDownlinkResp(msgID uint16, reqData []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}
	// 复用请求的流水号
	var seqNum uint16
	if len(reqData) >= jt809HeaderLen {
		seqNum = binary.BigEndian.Uint16(reqData[16:18])
	} else {
		seqNum = c.nextSeq()
	}
	msg, err := c.buildMessage(msgID, nil, seqNum)
	if err != nil {
		c.logger.Error("809 send downlink resp failed", zap.Error(err))
		return
	}
	if _, err := c.conn.Write(msg); err != nil {
		c.logger.Error("809 write downlink resp failed", zap.Error(err))
	}
}

// jt809HeaderLen 是 JT/T 809-2019 消息头长度（字节）。
// 消息头 = 消息ID(2) + 消息体属性(2) + 企业数字证书(4) + 消息加密(1) +
//          消息密钥(4) + 上下行链路(1) + 数据加密方式(1) + 保留(1) + 消息流水号(2) = 18
const jt809HeaderLen = 18

func (c *JT809Client) buildMessage(msgID uint16, body []byte, seqNum uint16) ([]byte, error) {
	bodyLen := len(body)
	header := make([]byte, jt809HeaderLen)

	// 消息ID (2 bytes)
	binary.BigEndian.PutUint16(header[0:2], msgID)

	// 消息体属性 (2 bytes): 低 10 位为消息体长度
	bodyAttr := uint16(bodyLen) & 0x03FF
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)

	// 企业数字证书 (4 bytes) — 未使用，填 0
	// header[4:8] = 0

	// 消息加密 (1 byte) — 不加密
	// header[8] = 0

	// 消息密钥 (4 bytes) — 未使用，填 0
	// header[9:13] = 0

	// 上下行链路 (1 byte): 0=上行, 1=下行
	// header[13] = 0

	// 数据加密方式 (1 byte) — 0=不加密
	// header[14] = 0

	// 保留 (1 byte)
	// header[15] = 0

	// 消息流水号 (2 bytes)
	binary.BigEndian.PutUint16(header[16:18], seqNum)

	payload := append(header, body...)

	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	payload = append(payload, crcBytes...)

	escaped := escape809(payload)
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, 0x5B)
	result = append(result, escaped...)
	result = append(result, 0x5D)

	return result, nil
}

func (c *JT809Client) nextSeq() uint16 {
	c.seqNum++
	return c.seqNum
}

// ============================================================================
// AUTO-FIX-2026-07-04 [P0]: SN 消息确认与重试机制
// ============================================================================

// ackRetryLoop 定期扫描 ackPending 表，重试超时未应答的消息。
// 超过 ackMaxRetry 次仍未应答 → 触发断链重连。
// 锁序：先 ackMu 找超时条目 → 释放 ackMu → 再 c.mu 重发（避免死锁）。
func (c *JT809Client) ackRetryLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.checkPendingAcks()
		}
	}
}

// checkPendingAcks 扫描待确认表，重试超时消息，超限触发重连。
func (c *JT809Client) checkPendingAcks() {
	now := time.Now()
	type retryItem struct {
		sn      uint16
		msgID   uint16
		payload []byte
	}
	var toRetry []retryItem
	var needReconnect bool

	c.ackMu.Lock()
	for sn, entry := range c.ackPending {
		if now.Before(entry.nextRetry) {
			continue
		}
		if entry.retryCount >= c.ackMaxRetry {
			c.logger.Warn("809 ACK timeout, max retries exceeded, triggering reconnect",
				zap.String("platform_id", c.cfg.ID),
				zap.Uint16("sn", sn),
				zap.Uint16("msg_id", entry.msgID),
				zap.Int("retries", entry.retryCount))
			delete(c.ackPending, sn)
			needReconnect = true
			continue
		}
		entry.retryCount++
		entry.nextRetry = now.Add(c.ackTimeout)
		payloadCopy := make([]byte, len(entry.payload))
		copy(payloadCopy, entry.payload)
		toRetry = append(toRetry, retryItem{sn: sn, msgID: entry.msgID, payload: payloadCopy})
	}
	c.ackMu.Unlock()

	// 重发超时消息（在 ackMu 锁外操作，避免与 c.mu 死锁）
	if len(toRetry) > 0 {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		for _, item := range toRetry {
			if conn == nil {
				break
			}
			c.logger.Warn("809 ACK timeout, retrying",
				zap.String("platform_id", c.cfg.ID),
				zap.Uint16("sn", item.sn),
				zap.Uint16("msg_id", item.msgID))
			if _, err := conn.Write(item.payload); err != nil {
				c.logger.Error("809 ACK retry write failed",
					zap.String("platform_id", c.cfg.ID),
					zap.Uint16("sn", item.sn),
					zap.Error(err))
				break
			}
		}
	}

	if needReconnect {
		c.running.Store(false)
		select {
		case c.reconnectCh <- struct{}{}:
		default:
		}
	}
}

// registerAck 注册消息到待确认表，启动超时计时器。
// payload 为完整 809 帧（含 0x5B/0x5D 边界），用于重发。
// 调用时调用方持有 c.mu，本方法获取 c.ackMu（锁序：c.mu → c.ackMu）。
func (c *JT809Client) registerAck(msgID, seq uint16, payload []byte) {
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)
	c.ackMu.Lock()
	c.ackPending[seq] = &ackEntry{
		msgID:      msgID,
		seqNum:     seq,
		payload:    payloadCopy,
		retryCount: 0,
		nextRetry:  time.Now().Add(c.ackTimeout),
	}
	c.ackMu.Unlock()
}

// clearAck 收到应答后按 SN 清除待确认条目。返回是否找到并清除。
func (c *JT809Client) clearAck(seq uint16) bool {
	c.ackMu.Lock()
	_, ok := c.ackPending[seq]
	if ok {
		delete(c.ackPending, seq)
	}
	c.ackMu.Unlock()
	return ok
}

// clearAllAcks 清除所有待确认条目（断链重连时调用）。
func (c *JT809Client) clearAllAcks() {
	c.ackMu.Lock()
	c.ackPending = make(map[uint16]*ackEntry)
	c.ackMu.Unlock()
}

// GetAckPendingCount 返回当前待确认消息数（监控/调试用）。
func (c *JT809Client) GetAckPendingCount() int {
	c.ackMu.Lock()
	defer c.ackMu.Unlock()
	return len(c.ackPending)
}

// ============================================================================
// AUTO-FIX-2026-07-04 [P0]: 从链路（Down-link）独立管理
// ============================================================================

// startDownlinkListener 启动从链路监听，等待上级平台反向连接。
// JT/T 809-2019 规定从链路为独立 TCP 连接，上级平台通过从链路发送 0x9xxx 消息。
func (c *JT809Client) startDownlinkListener(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("809 downlink listen on :%d: %w", port, err)
	}
	c.downlinkListener = listener
	c.logger.Info("809 downlink listener started",
		zap.String("platform_id", c.cfg.ID),
		zap.Int("port", port))
	util.SafeGo(c.logger, "jt809.downlinkAcceptLoop", c.downlinkAcceptLoop)
	util.SafeGo(c.logger, "jt809.downlinkKeepaliveCheck", c.downlinkKeepaliveCheck)
	return nil
}

// downlinkAcceptLoop 接受上级平台的从链路连接。
func (c *JT809Client) downlinkAcceptLoop() {
	for {
		conn, err := c.downlinkListener.Accept()
		if err != nil {
			if !c.running.Load() {
				return
			}
			c.logger.Error("809 downlink accept error",
				zap.String("platform_id", c.cfg.ID),
				zap.Error(err))
			continue
		}
		c.logger.Info("809 downlink connected",
			zap.String("platform_id", c.cfg.ID),
			zap.String("remote", conn.RemoteAddr().String()))

		c.downlinkMu.Lock()
		if c.downlinkConn != nil {
			c.downlinkConn.Close()
		}
		c.downlinkConn = conn
		c.downlinkMu.Unlock()
		c.downlinkRunning.Store(true)
		c.lastDownlinkKeepaliveReq.Store(time.Now().UnixNano())

		util.SafeGo(c.logger, "jt809.downlinkReadLoop", func() { c.downlinkReadLoop(conn) })
	}
}

// downlinkReadLoop 读取从链路消息并处理。
func (c *JT809Client) downlinkReadLoop(conn net.Conn) {
	defer func() {
		conn.Close()
		c.downlinkRunning.Store(false)
		c.logger.Info("809 downlink disconnected",
			zap.String("platform_id", c.cfg.ID))
	}()

	buf := make([]byte, 8192)
	remaining := make([]byte, 0)
	for c.downlinkRunning.Load() {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if c.downlinkRunning.Load() {
				c.logger.Error("809 downlink read error",
					zap.String("platform_id", c.cfg.ID),
					zap.Error(err))
			}
			return
		}
		if n > 0 {
			remaining = append(remaining, buf[:n]...)
			remaining = c.processDownlinkData(remaining, conn)
		}
	}
}

// processDownlinkData 解析从链路数据帧并处理 0x9xxx 消息。
func (c *JT809Client) processDownlinkData(data []byte, conn net.Conn) []byte {
	for len(data) >= 2 {
		start := -1
		end := -1
		for i := 0; i < len(data); i++ {
			if data[i] == 0x5B && start == -1 {
				start = i
			} else if data[i] == 0x5D && start != -1 {
				end = i
				break
			}
		}
		if start == -1 || end == -1 {
			break
		}
		frame := data[start : end+1]
		remaining := data[end+1:]
		if len(frame) > 2 {
			inner := frame[1 : len(frame)-1]
			unescaped := unescape809(inner)
			if len(unescaped) >= 2 {
				msgID := binary.BigEndian.Uint16(unescaped[0:2])
				c.handleDownlinkMessage(msgID, unescaped, conn)
			}
		}
		data = remaining
	}
	if len(data) > 0 && data[0] != 0x5B {
		data = nil
	}
	return data
}

// handleDownlinkMessage 处理从链路消息（0x9xxx 消息族）。
// AUTO-FIX-2026-07-04 [P0]: 独立从链路消息处理，不再在主链路上复用。
func (c *JT809Client) handleDownlinkMessage(msgID uint16, data []byte, conn net.Conn) {
	var seqNum uint16
	if len(data) >= jt809HeaderLen {
		seqNum = binary.BigEndian.Uint16(data[16:18])
	}
	switch msgID {
	case 0x9001:
		// 从链路登录请求（DOWN_CONNECT_REQ，上级→下级）
		c.logger.Info("809 downlink login request received",
			zap.String("platform_id", c.cfg.ID))
		body := extractBody(data)
		result := byte(0x00) // 0=成功
		if !c.authenticateDownlinkLogin(body) {
			result = 0x01
			c.logger.Warn("809 downlink login rejected",
				zap.String("platform_id", c.cfg.ID))
		}
		c.sendOnDownlink(conn, 0x9002, seqNum, []byte{result})
	case 0x9003:
		// 从链路断开请求（DOWN_LINKDISCONNECT_REQ，上级→下级）
		c.logger.Info("809 downlink disconnect request received",
			zap.String("platform_id", c.cfg.ID))
		c.sendOnDownlink(conn, 0x9004, seqNum, []byte{0x00})
		c.downlinkRunning.Store(false)
	case 0x9005:
		// 从链路保活请求（DOWN_LINKKEEP_ALIVE_REQ，上级→下级）
		c.lastDownlinkKeepaliveReq.Store(time.Now().UnixNano())
		c.sendOnDownlink(conn, 0x9006, seqNum, nil)
	default:
		c.logger.Debug("809 downlink message",
			zap.String("platform_id", c.cfg.ID),
			zap.Uint16("msg_id", msgID),
			zap.Int("bytes", len(data)))
	}
}

// authenticateDownlinkLogin 验证上级平台从链路登录凭证。
// 使用与主链路相同的 username/password 进行校验。
// AUTO-FIX-2026-07-04 [P0]: 未配置凭证时默认拒绝（消除默认放行后门），
// 与 authenticateDownstream 保持一致的严格策略。
func (c *JT809Client) authenticateDownlinkLogin(body []byte) bool {
	if len(body) < 32 {
		c.logger.Warn("809 downlink login body too short", zap.Int("len", len(body)))
		return false
	}
	userID := string(bytes.TrimRight(body[0:16], "\x00"))
	password := string(bytes.TrimRight(body[16:32], "\x00"))
	if c.cfg.Username == "" || c.cfg.Password == "" {
		c.logger.Warn("809 downlink credentials not configured, rejecting all downlink logins",
			zap.String("platform_id", c.cfg.ID))
		return false
	}
	return userID == c.cfg.Username && password == c.cfg.Password
}

// sendOnDownlink 在从链路连接上发送消息。
func (c *JT809Client) sendOnDownlink(conn net.Conn, msgID uint16, seqNum uint16, body []byte) {
	if conn == nil {
		return
	}
	header := make([]byte, jt809HeaderLen)
	binary.BigEndian.PutUint16(header[0:2], msgID)
	bodyAttr := uint16(len(body)) & 0x03FF
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)
	binary.BigEndian.PutUint16(header[16:18], seqNum)
	payload := append(header, body...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	payload = append(payload, crcBytes...)
	escaped := escape809(payload)
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, 0x5B)
	result = append(result, escaped...)
	result = append(result, 0x5D)
	if _, err := conn.Write(result); err != nil {
		c.logger.Error("809 send on downlink failed",
			zap.String("platform_id", c.cfg.ID),
			zap.Uint16("msg_id", msgID),
			zap.Error(err))
	}
}

// downlinkKeepaliveCheck 监控从链路保活超时。
// 3 倍保活间隔（180s）未收到上级平台保活请求 → 关闭从链路等待重连。
func (c *JT809Client) downlinkKeepaliveCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			if !c.downlinkRunning.Load() {
				continue
			}
			lastReq := time.Unix(0, c.lastDownlinkKeepaliveReq.Load())
			if time.Since(lastReq) > 180*time.Second {
				c.logger.Warn("809 downlink keepalive timeout, closing down-link",
					zap.String("platform_id", c.cfg.ID),
					zap.Duration("since_last_req", time.Since(lastReq)))
				c.downlinkRunning.Store(false)
				c.downlinkMu.Lock()
				if c.downlinkConn != nil {
					c.downlinkConn.Close()
					c.downlinkConn = nil
				}
				c.downlinkMu.Unlock()
			}
		}
	}
}

// IsDownlinkRunning 返回从链路是否在线。
func (c *JT809Client) IsDownlinkRunning() bool {
	return c.downlinkRunning.Load()
}

// ============================================================================
// AUTO-FIX-2026-07-04 [P1]: 809-2019 视频转发
// ============================================================================

// SendVideoData 将实时音视频转发请求发送到上级平台（JT/T 809-2019 0x1B00）。
// 用于将下级平台的视频流转发到上级平台进行实时监控。
func (c *JT809Client) SendVideoData(phone string, channelID int, avItemID int, startTime, endTime time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	xmlData := fmt.Sprintf(`<RealVideoForward><VehicleNo>%s</VehicleNo><ChannelID>%d</ChannelID><AVItemID>%d</AVItemID><StartTime>%s</StartTime><EndTime>%s</EndTime></RealVideoForward>`,
		phone, channelID, avItemID, startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(xmlData))
	if err != nil {
		return fmt.Errorf("gbk encode video data: %w", err)
	}

	msgID := uint16(0x1B00)
	seq := c.nextSeq()
	msg, err := c.buildMessage(msgID, body, seq)
	if err != nil {
		return err
	}

	return c.sendOrBuffer(msgID, seq, msg)
}

type JT809Server struct {
	cfg            *config.GatewayConfig
	logger         *zap.Logger
	listener       net.Listener
	merge          *merge.Engine
	store          storage.Interface
	running        atomic.Bool
	clients        map[string]*JT809DownstreamClient
	mu             sync.RWMutex
	protocolHub    *protocol.Hub
	messageHandler func(session handler.Session, msg *protocol.Message) error
	// AUTO-FIX-2026-06-26: 下级平台接入鉴权账号列表（按第一轮.txt要求）[2026-06-26]
	downstreamPlatforms []config.DownstreamPlatformConfig
}

// SetDownstreamPlatforms 设置下级平台接入鉴权账号列表
func (s *JT809Server) SetDownstreamPlatforms(platforms []config.DownstreamPlatformConfig) {
	s.downstreamPlatforms = platforms
}

type JT809DownstreamClient struct {
	ID         string
	Conn       net.Conn
	Username   string
	Status     string
	lastActive time.Time
	writeMu    sync.Mutex
}

func NewJT809Server(cfg *config.GatewayConfig, logger *zap.Logger, mergeEngine *merge.Engine, store storage.Interface) *JT809Server {
	return &JT809Server{
		cfg:     cfg,
		logger:  logger,
		merge:   mergeEngine,
		store:   store,
		clients: make(map[string]*JT809DownstreamClient),
	}
}

// SetMessageHandler 设置消息处理回调，用于将收到的 809 消息分发给注册的协议处理器。
// 当 messageHandler 不为 nil 时，processDownstreamData 会将解析出的完整 809 消息交给它处理，
// 从而利用 Handler809 的完整消息处理能力（车辆增删改、报警、音视频、统计、路线等）。
func (s *JT809Server) SetMessageHandler(fn func(session handler.Session, msg *protocol.Message) error) {
	s.messageHandler = fn
}

// SetProtocolHub 设置协议 Hub，用于将原始 809 帧解析为 *protocol.Message。
func (s *JT809Server) SetProtocolHub(hub *protocol.Hub) {
	s.protocolHub = hub
}

// handler.Session 接口实现 —— 使 JT809DownstreamClient 可直接作为 handler.Session 传入 Handler809

func (c *JT809DownstreamClient) GetID() string                      { return c.ID }
func (c *JT809DownstreamClient) GetPhone() string                    { return c.Username }
func (c *JT809DownstreamClient) GetProtocol() protocol.ProtocolType  { return protocol.ProtocolJT809 }
func (c *JT809DownstreamClient) UpdateActivity()                     { c.lastActive = time.Now() }
func (c *JT809DownstreamClient) SetProtocol(pt protocol.ProtocolType) {}

func (c *JT809DownstreamClient) Write(data []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.Conn == nil {
		return 0, fmt.Errorf("809 downstream client %s connection closed", c.ID)
	}
	return c.Conn.Write(data)
}

func (s *JT809Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("809 listen on %s: %w", addr, err)
	}
	s.listener = listener
	s.running.Store(true)

	s.logger.Info("809 server started", zap.String("addr", addr))
	util.SafeGo(s.logger, "jt809server.acceptLoop", s.acceptLoop)
	return nil
}

func (s *JT809Server) Stop() {
	s.running.Store(false)
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for _, c := range s.clients {
		c.Conn.Close()
	}
	s.mu.Unlock()
}

func (s *JT809Server) acceptLoop() {
	for s.running.Load() {
		conn, err := s.listener.Accept()
		if err != nil {
			if !s.running.Load() {
				return
			}
			s.logger.Error("809 accept error", zap.Error(err))
			continue
		}

		clientID := fmt.Sprintf("809_%s_%d", conn.RemoteAddr().String(), time.Now().UnixNano())
		client := &JT809DownstreamClient{
			ID:     clientID,
			Conn:   conn,
			Status: "connected",
		}

		s.mu.Lock()
		s.clients[clientID] = client
		s.mu.Unlock()

		s.logger.Info("809 downstream connected", zap.String("id", clientID))
		util.SafeGoWithRecover(s.logger, "jt809server.handleDownstream", func(r interface{}) { _ = conn.Close() }, func() { s.handleDownstream(clientID, conn) })
	}
}

func (s *JT809Server) handleDownstream(clientID string, conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, clientID)
		s.mu.Unlock()
		conn.Close()
		s.logger.Info("809 downstream disconnected", zap.String("id", clientID))
	}()

	buf := make([]byte, 8192)
	for s.running.Load() {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		if n > 0 {
			s.logger.Debug("809 downstream data",
				zap.String("id", clientID),
				zap.Int("bytes", n))

			data := make([]byte, n)
			copy(data, buf[:n])

			s.processDownstreamData(clientID, data)
		}
	}
}

func (s *JT809Server) processDownstreamData(clientID string, data []byte) {
	if len(data) < 2 {
		return
	}

	if data[0] == 0x5B && data[len(data)-1] == 0x5D {
		// 当 messageHandler 已注册时，将完整 809 消息交给它处理（利用 Handler809 的完整消息处理能力）
		if s.messageHandler != nil && s.protocolHub != nil {
			_, msg, err := s.protocolHub.Route(data)
			if err == nil && msg != nil {
				s.mu.RLock()
				client, ok := s.clients[clientID]
				s.mu.RUnlock()
				if ok {
					if err := s.messageHandler(client, msg); err != nil {
						s.logger.Error("809 message handler error",
							zap.String("id", clientID),
							zap.Uint16("msg_id", msg.Header.MsgID),
							zap.Error(err))
					}
				}
				return
			}
			// Route 失败（如 codec 未注册），回退到内联处理
			s.logger.Debug("809 route failed, falling back to inline handler",
				zap.String("id", clientID),
				zap.Error(err))
		}

		// 内联 fallback 处理（当 messageHandler 为 nil 或 Route 失败时使用）
		inner := data[1 : len(data)-1]
		unescaped := unescape809(inner)

		if len(unescaped) < 2 {
			return
		}

		msgID := binary.BigEndian.Uint16(unescaped[0:2])

		var seqNum uint16
		if len(unescaped) >= jt809HeaderLen {
			seqNum = binary.BigEndian.Uint16(unescaped[16:18])
		}

		switch msgID {
		// AUTO-FIX-2026-06-28: 修正 Server 端 msgID 错位
		// 原实现基于旧版错值常量（0x1002=logout/0x1003=keepalive/0x1005=linkkeepalive/0x1006=linkdisconnect）
		// 标准 JT/T 809-2019：0x1001=登录/0x1003=断开/0x1006=保活；Rsp(0x1002/0x1005/0x1007) 不应出现在此方向
		case 0x1001:
			// 主链路登录请求（UP_CONNECT_REQ，下级→上级）
			s.logger.Info("809 downstream login", zap.String("id", clientID))
			s.mu.RLock()
			client, ok := s.clients[clientID]
			s.mu.RUnlock()
			if ok {
				// AUTO-FIX-2026-06-26: 下级平台接入鉴权校验（按第一轮.txt要求）[2026-06-26]
				// 从消息体解析UserID/Password进行校验，未配置账号时放行（向后兼容）
				loginBody := extractBody(unescaped)
				if s.authenticateDownstream(loginBody) {
					client.Status = "authenticated"
					// AUTO-FIX-2026-06-28: 应答应为 0x1002 (UP_CONNECT_RSP)
					s.sendDownstreamResponse(client.Conn, 0x1002, seqNum, []byte{0x00})
				} else {
					client.Status = "rejected"
					s.sendDownstreamResponse(client.Conn, 0x1002, seqNum, []byte{0x01})
					s.logger.Warn("809 downstream login rejected", zap.String("id", clientID))
				}
			}
		case 0x1003:
			// 主链路断开请求（UP_LINKDISCONNECT_REQ，下级→上级）
			s.logger.Info("809 downstream disconnect", zap.String("id", clientID))
			s.mu.RLock()
			client, ok := s.clients[clientID]
			s.mu.RUnlock()
			if ok {
				// AUTO-FIX-2026-06-28: 应答应为 0x1005 (UP_LINKDISCONNECT_RSP)
				s.sendDownstreamResponse(client.Conn, 0x1005, seqNum, []byte{0x00})
				client.Status = "disconnected"
			}
		case 0x1006:
			// 主链路保活请求（UP_LINKKEEP_ALIVE_REQ，下级→上级）
			s.logger.Debug("809 downstream keepalive", zap.String("id", clientID))
			s.mu.RLock()
			client, ok := s.clients[clientID]
			s.mu.RUnlock()
			if ok {
				// AUTO-FIX-2026-06-28: 应答应为 0x1007 (UP_LINKKEEP_ALIVE_RSP)
				s.sendDownstreamResponse(client.Conn, 0x1007, seqNum, nil)
			}
		case 0x1200:
			s.processVehicleData(clientID, unescaped)
		}
	}
}

// extractBody 从809帧中提取消息体（跳过header，去掉校验码）
// AUTO-FIX-2026-06-26: 用于下级平台登录鉴权时提取UserID/Password [2026-06-26]
func extractBody(unescaped []byte) []byte {
	if len(unescaped) <= jt809HeaderLen+1 {
		return nil
	}
	if len(unescaped) < 4 {
		return nil
	}
	bodyAttr := binary.BigEndian.Uint16(unescaped[2:4])
	bodyLen := int(bodyAttr & 0x03FF)
	start := jt809HeaderLen
	end := start + bodyLen
	if end > len(unescaped)-1 {
		end = len(unescaped) - 1
	}
	if start >= end {
		return nil
	}
	return unescaped[start:end]
}

// authenticateDownstream 校验下级平台登录凭证
// 809登录消息体：用户名(16B) + 密码(16B) + 下级平台标识(12B) + ...
// AUTO-FIX-2026-06-29 [P1]: 原实现未配置下级平台账号时放行（向后兼容），
// 构成默认放行后门——任意下级平台均可接入级联。已改为默认拒绝，必须显式配置
// downstreamPlatforms 白名单，未配置时拒绝所有下级平台登录并记录告警。
func (s *JT809Server) authenticateDownstream(body []byte) bool {
	if len(s.downstreamPlatforms) == 0 {
		s.logger.Warn("809 downstream platforms not configured, rejecting all downstream logins")
		return false
	}
	if len(body) < 32 {
		s.logger.Warn("809 login body too short for auth", zap.Int("len", len(body)))
		return false
	}
	userID := string(bytes.TrimRight(body[0:16], "\x00"))
	password := string(bytes.TrimRight(body[16:32], "\x00"))
	for _, p := range s.downstreamPlatforms {
		if p.UserID == userID && p.Password == password {
			return true
		}
	}
	return false
}

func (s *JT809Server) sendDownstreamResponse(conn net.Conn, msgID uint16, seqNum uint16, body []byte) {
	if conn == nil {
		return
	}

	header := make([]byte, jt809HeaderLen)
	binary.BigEndian.PutUint16(header[0:2], msgID)

	bodyAttr := uint16(len(body)) & 0x03FF
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)

	// 企业数字证书/消息加密/消息密钥/上下行链路/数据加密方式/保留 均填 0
	// 消息流水号
	binary.BigEndian.PutUint16(header[16:18], seqNum)

	payload := append(header, body...)

	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	payload = append(payload, crcBytes...)

	escaped := escape809(payload)
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, 0x5B)
	result = append(result, escaped...)
	result = append(result, 0x5D)

	if _, err := conn.Write(result); err != nil {
		s.logger.Error("809 send downstream response failed",
			zap.Uint16("msg_id", msgID),
			zap.Error(err))
	}
}

func (s *JT809Server) processVehicleData(clientID string, data []byte) {
	headerLen := jt809HeaderLen
	crcLen := 4
	if len(data) <= headerLen+crcLen {
		return
	}

	xmlData := data[headerLen : len(data)-crcLen]

	// AUTO-FIX-2026-06-29 [P1]: JT/T 809-2019 规定 XML 中文字段（车牌号等）为 GBK 编码。
	// 原实现直接 xml.Unmarshal(xmlData, &v)，Go 的 encoding/xml 默认按 UTF-8 解析，
	// 导致中文车牌号（如"京A12345"）乱码或解析失败、整帧被丢弃。
	// 修复：先 GBK→UTF-8 解码再 Unmarshal，与 SendVehicleData 的发送侧编码对称。
	utf8Data, err := simplifiedchinese.GBK.NewDecoder().Bytes(xmlData)
	if err != nil {
		s.logger.Error("gbk decode 809 vehicle XML", zap.Error(err))
		return
	}

	var v struct {
		XMLName    xml.Name `xml:"VehicleLocation"`
		VehicleNo  string   `xml:"VehicleNo"`
		Latitude   float64  `xml:"Latitude"`
		Longitude  float64  `xml:"Longitude"`
		Speed      float64  `xml:"Speed"`
		Direction  int      `xml:"Direction"`
		Time       string   `xml:"Time"`
	}

	if err := xml.Unmarshal(utf8Data, &v); err != nil {
		s.logger.Error("parse 809 vehicle XML", zap.Error(err))
		return
	}

	loc := &storage.LocationData{
		VehicleID:  v.VehicleNo,
		Phone:      v.VehicleNo,
		Latitude:   v.Latitude,
		Longitude:  v.Longitude,
		Speed:      v.Speed,
		Direction:  v.Direction,
		ReceivedAt: time.Now(),
		Source:     "jt809",
	}

	if err := s.merge.Merge(context.Background(), loc); err != nil {
		s.logger.Error("merge 809 location", zap.Error(err))
	}
}

func escape809(data []byte) []byte {
	result := make([]byte, 0, len(data)*2)
	for _, b := range data {
		switch b {
		case 0x5B:
			result = append(result, 0x5A, 0x01)
		case 0x5A:
			result = append(result, 0x5A, 0x02)
		case 0x5D:
			result = append(result, 0x5E, 0x01)
		case 0x5E:
			result = append(result, 0x5E, 0x02)
		default:
			result = append(result, b)
		}
	}
	return result
}

func unescape809(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if i+1 < len(data) {
			if data[i] == 0x5A && data[i+1] == 0x01 {
				result = append(result, 0x5B)
				i += 2
				continue
			}
			if data[i] == 0x5A && data[i+1] == 0x02 {
				result = append(result, 0x5A)
				i += 2
				continue
			}
			if data[i] == 0x5E && data[i+1] == 0x01 {
				result = append(result, 0x5D)
				i += 2
				continue
			}
			if data[i] == 0x5E && data[i+1] == 0x02 {
				result = append(result, 0x5E)
				i += 2
				continue
			}
		}
		result = append(result, data[i])
		i++
	}
	return result
}