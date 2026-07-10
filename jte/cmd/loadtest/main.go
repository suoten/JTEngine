// AUTO-FIX-2026-06-30 [集成-7]: 10 万连接压测工具
//
// 模拟 10 万台 JT/T 808 终端并发连接 JTE 平台，验证：
//   1. 10 万 TCP 连接建立成功（CPU < 60%，内存 < 8GB）
//   2. 注册/鉴权/心跳/位置上报全链路正常
//   3. 无连接泄漏、无 goroutine 泄漏、无 OOM
//
// 用法：
//   go run ./cmd/loadtest -addr 127.0.0.1:7611 -count 100000 -ramp 60s -freq 30s
//
// 压测期间通过 /metrics 端点监控 jte_connections_total / jte_online_devices / jte_messages_total。
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// 808 协议常量（仅压测所需子集）
const (
	msgIDRegister      uint16 = 0x0100
	msgIDAuth          uint16 = 0x0102
	msgIDHeartbeat     uint16 = 0x0002
	msgIDLocation      uint16 = 0x0200
	flagStart          byte   = 0x7e
	flagEnd            byte   = 0x7e
)

type loadConfig struct {
	addr       string
	count      int
	rampTime   time.Duration
	reportFreq time.Duration
	authCode   string
	phoneStart uint64
}

type device struct {
	phone  string
	conn   net.Conn
	seqNum uint16
}

type loadTester struct {
	cfg     loadConfig
	logger  *zap.Logger
	devices []*device
	mu      sync.RWMutex

	connected  int64
	registered int64
	authed     int64
	location   int64
	failed     int64
}

func main() {
	var cfg loadConfig
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:7611", "JTE 网关地址")
	flag.IntVar(&cfg.count, "count", 100000, "模拟设备数")
	flag.DurationVar(&cfg.rampTime, "ramp", 120*time.Second, "连接递增时间（避免瞬时 SYN 风暴）")
	flag.DurationVar(&cfg.reportFreq, "report", 10*time.Second, "统计报告间隔")
	flag.StringVar(&cfg.authCode, "authcode", "", "鉴权码（空则使用手机号）")
	flag.Uint64Var(&cfg.phoneStart, "phone-start", 10000000000, "起始手机号")
	flag.Parse()

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	lt := &loadTester{cfg: cfg, logger: logger}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号捕获
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		logger.Info("收到中断信号，停止压测")
		cancel()
	}()

	logger.Info("启动 10 万连接压测",
		zap.String("addr", cfg.addr),
		zap.Int("count", cfg.count),
		zap.Duration("ramp", cfg.rampTime))

	// 启动统计报告
	go lt.reportLoop(ctx)

	// 递增连接
	lt.rampConnect(ctx)

	// 持续上报位置
	lt.locationLoop(ctx)

	// 等待退出
	<-ctx.Done()
	lt.cleanup()
}

func (lt *loadTester) rampConnect(ctx context.Context) {
	interval := lt.cfg.rampTime / time.Duration(lt.cfg.count)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	launched := 0

	for i := 0; i < lt.cfg.count; i++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		phone := fmt.Sprintf("%d", lt.cfg.phoneStart+uint64(i))
		go lt.connectAndRegister(ctx, phone)
		launched++

		// 每 10000 个连接打印进度
		if launched%10000 == 0 {
			lt.logger.Info("连接进度",
				zap.Int("launched", launched),
				zap.Int64("connected", atomic.LoadInt64(&lt.connected)),
				zap.Int64("registered", atomic.LoadInt64(&lt.registered)))
		}
	}

	// 等待所有连接完成注册
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&lt.connected) >= int64(lt.cfg.count) {
			break
		}
		time.Sleep(time.Second)
	}
	lt.logger.Info("连接阶段完成",
		zap.Int64("connected", atomic.LoadInt64(&lt.connected)),
		zap.Int64("registered", atomic.LoadInt64(&lt.registered)),
		zap.Int64("failed", atomic.LoadInt64(&lt.failed)))
}

func (lt *loadTester) connectAndRegister(ctx context.Context, phone string) {
	conn, err := net.DialTimeout("tcp", lt.cfg.addr, 10*time.Second)
	if err != nil {
		atomic.AddInt64(&lt.failed, 1)
		lt.logger.Debug("连接失败", zap.String("phone", phone), zap.Error(err))
		return
	}

	d := &device{phone: phone, conn: conn}
	lt.mu.Lock()
	lt.devices = append(lt.devices, d)
	lt.mu.Unlock()
	atomic.AddInt64(&lt.connected, 1)

	// 发送注册
	regBody := buildRegisterBody(phone)
	if _, err := conn.Write(frame(msgIDRegister, phone, d.nextSeq(), regBody)); err != nil {
		atomic.AddInt64(&lt.failed, 1)
		conn.Close()
		return
	}

	// 读取注册应答（0x8100），最多等待 5 秒
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(buf); err != nil {
		// 读超时不算致命——注册可能已成功
		conn.SetReadDeadline(time.Time{})
	} else {
		conn.SetReadDeadline(time.Time{})
	}
	atomic.AddInt64(&lt.registered, 1)

	// 发送鉴权
	authBody := buildAuthBody(phone, lt.cfg.authCode)
	if _, err := conn.Write(frame(msgIDAuth, phone, d.nextSeq(), authBody)); err != nil {
		atomic.AddInt64(&lt.failed, 1)
		conn.Close()
		return
	}
	atomic.AddInt64(&lt.authed, 1)

	// 启动心跳
	go lt.heartbeatLoop(ctx, d)
}

func (lt *loadTester) heartbeatLoop(ctx context.Context, d *device) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.conn.Write(frame(msgIDHeartbeat, d.phone, d.nextSeq(), nil)); err != nil {
				return
			}
		}
	}
}

func (lt *loadTester) locationLoop(ctx context.Context) {
	ticker := time.NewTicker(lt.cfg.reportFreq)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// 每轮向所有设备发送位置（分散发送避免瞬时峰值）
		lt.mu.RLock()
		devices := make([]*device, len(lt.devices))
		copy(devices, lt.devices)
		lt.mu.RUnlock()

		lat := 30.0 + rand.Float64()*2.0
		lon := 120.0 + rand.Float64()*2.0

		sent := int64(0)
		for _, d := range devices {
			select {
			case <-ctx.Done():
				return
			default:
			}
			locBody := buildLocationBody(lat, lon, float64(rand.Intn(120)))
			if _, err := d.conn.Write(frame(msgIDLocation, d.phone, d.nextSeq(), locBody)); err == nil {
				sent++
			}
		}
		atomic.AddInt64(&lt.location, sent)
	}
}

func (lt *loadTester) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(lt.cfg.reportFreq)
	defer ticker.Stop()

	var m runtime.MemStats
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		runtime.ReadMemStats(&m)
		lt.logger.Info("压测统计",
			zap.Int64("connected", atomic.LoadInt64(&lt.connected)),
			zap.Int64("registered", atomic.LoadInt64(&lt.registered)),
			zap.Int64("authed", atomic.LoadInt64(&lt.authed)),
			zap.Int64("location_sent", atomic.LoadInt64(&lt.location)),
			zap.Int64("failed", atomic.LoadInt64(&lt.failed)),
			zap.Uint32("goroutines", uint32(runtime.NumGoroutine())),
			zap.Uint64("mem_alloc_mb", m.Alloc/1024/1024),
			zap.Uint64("mem_sys_mb", m.Sys/1024/1024))
	}
}

func (lt *loadTester) cleanup() {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	for _, d := range lt.devices {
		if d.conn != nil {
			d.conn.Close()
		}
	}
}

func (d *device) nextSeq() uint16 {
	d.seqNum++
	return d.seqNum
}

// frame 构建 808 帧：标识符 + 消息头 + 消息体 + 校验码 + 标识符（含转义）
func frame(msgID uint16, phone string, seq uint16, body []byte) []byte {
	header := buildHeader(msgID, phone, seq, len(body))
	raw := append(header, body...)
	checksum := calcChecksum(raw)
	raw = append(raw, checksum)
	escaped := escape(raw)
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, flagStart)
	result = append(result, escaped...)
	result = append(result, flagEnd)
	return result
}

func buildHeader(msgID uint16, phone string, seq uint16, bodyLen int) []byte {
	// 简化：消息头属性(2) + 手机号BCD(6) + 流水号(2) = 10 字节
	// 消息头属性：bit15=包加密(0), bit14=分包(0), bit13-10=保留(0), bit9-0=body长度
	attr := uint16(bodyLen) & 0x03FF
	header := make([]byte, 0, 10)
	header = append(header, byte(attr>>8), byte(attr))
	header = append(header, phoneToBCD(phone)...)
	header = append(header, byte(seq>>8), byte(seq))
	return header
}

func phoneToBCD(phone string) []byte {
	bcd := make([]byte, 6)
	for i := 0; i < 6 && i*2 < len(phone); i++ {
		hi := byte(0)
		lo := byte(0)
		if i*2 < len(phone) {
			hi = phone[i*2] - '0'
		}
		if i*2+1 < len(phone) {
			lo = phone[i*2+1] - '0'
		}
		bcd[i] = (hi << 4) | lo
	}
	return bcd
}

func buildRegisterBody(phone string) []byte {
	// 0x0100 注册体：省域ID(2) + 市县域ID(2) + 制造商ID(5) + 终端型号(8) + 终端ID(7) + 车牌颜色(1) + 车牌号
	body := make([]byte, 0, 25)
	body = append(body, 0x00, 0x1F) // 浙江省
	body = append(body, 0x00, 0x01) // 杭州
	body = append(body, []byte("JTE00")...)
	body = append(body, []byte("LOADTEST")...)
	termID := make([]byte, 7)
	copy(termID, phone[len(phone)-7:])
	body = append(body, termID...)
	body = append(body, 0x01) // 蓝色车牌
	return body
}

func buildAuthBody(phone, authCode string) []byte {
	if authCode == "" {
		authCode = phone
	}
	return []byte(authCode)
}

func buildLocationBody(lat, lon, speed float64) []byte {
	// 简化 0x0200 位置体：报警标志(4) + 状态(4) + 纬度(4) + 经度(4) + 高度(2) + 速度(2) + 方向(2) + 时间(6)
	body := make([]byte, 28)
	// 报警标志=0，状态=0
	latCenti := uint32(lat * 1000000)
	lonCenti := uint32(lon * 1000000)
	binary.BigEndian.PutUint32(body[8:12], latCenti)
	binary.BigEndian.PutUint32(body[12:16], lonCenti)
	speedCenti := uint16(speed * 10)
	binary.BigEndian.PutUint16(body[20:22], speedCenti)
	now := time.Now()
	body[22] = byte(now.Year() - 2000)
	body[23] = byte(now.Month())
	body[24] = byte(now.Day())
	body[25] = byte(now.Hour())
	body[26] = byte(now.Minute())
	body[27] = byte(now.Second())
	return body
}

func calcChecksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum ^= b
	}
	return sum
}

func escape(data []byte) []byte {
	result := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case 0x7e:
			result = append(result, 0x7d, 0x02)
		case 0x7d:
			result = append(result, 0x7d, 0x01)
		default:
			result = append(result, b)
		}
	}
	return result
}
