// videodemo — JT/T 1078 视频链路端到端演示工具（复用 JTE 开源协议库）
//
// 两个子命令：
//
//	videodemo platform -listen 127.0.0.1:7611 -out out.h264 -duration 30s
//	  平台侧：监听 TCP，解析 JT808 帧，响应终端注册(0x0100→0x8100)，
//	  下发实时音视频请求(0x9101)，接收 0x1200 RTP 数据并还原为 H.264 Annex-B。
//
//	videodemo terminal -server 127.0.0.1:7611 -phone 13800000001 -file source.h264 -fps 25
//	  终端侧：模拟 JT/T 1078 车载终端，注册后按平台 0x9101 指令开始推流，
//	  将本地 H.264 Annex-B 文件按 RFC 3984 (Single NAL / FU-A) 打包为
//	  RTP，再封装为 JT1078 0x1200 消息经 JT808 帧格式（转义/校验/0x7E）发送。
//
// 协议编解码全部复用 JTE 开源库 github.com/suoten/jt-engine/pkg/protocol/jt808。
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	jt808 "github.com/suoten/jt-engine/pkg/protocol/jt808"
	"go.uber.org/zap"
)

// =====================================================================
// JT808 帧构建（终端侧）与解析（平台侧）
// =====================================================================

const delimiter = 0x7E

const (
	msgIDHeartbeat        uint16 = 0x0002
	msgIDRegister         uint16 = 0x0100
	msgIDRegisterResp     uint16 = 0x8100
	msgIDRealtimeRequest  uint16 = 0x9101
	msgIDRealtimeResponse uint16 = 0x9102
	msgIDRTPData          uint16 = 0x1200
	msgIDAVControl        uint16 = 0x9105
)

type frame struct {
	msgID uint16
	props uint16
	phone string
	seq   uint16
	body  []byte
}

func buildPhoneBCD(phone string) []byte {
	bcd, _ := jt808.StringToBCD(phone, 6)
	return bcd
}

// buildMessage 组装一条 JT808 帧（含转义与 0x7E 定界符）。
func buildMessage(msgID uint16, phone string, msgSeq uint16, body []byte) []byte {
	header := make([]byte, 0, 12)
	header = append(header, byte(msgID>>8), byte(msgID))
	header = append(header, byte(len(body)>>8), byte(len(body)))
	header = append(header, buildPhoneBCD(phone)...)
	header = append(header, byte(msgSeq>>8), byte(msgSeq))

	raw := append(append([]byte{}, header...), body...)
	raw = append(raw, jt808.CalcChecksum(raw))
	return jt808.WrapWithDelimiter(jt808.Escape(raw))
}

// parseFrame 从已去转义的完整帧内容（不含定界符）解析消息。
func parseFrame(unescaped []byte) (*frame, error) {
	if len(unescaped) < 16 { // 2+2+6+2+1(校验)
		return nil, fmt.Errorf("frame too short: %d", len(unescaped))
	}
	payload := unescaped[:len(unescaped)-1]
	if jt808.CalcChecksum(payload) != unescaped[len(unescaped)-1] {
		return nil, fmt.Errorf("checksum mismatch")
	}
	f := &frame{}
	f.msgID = binary.BigEndian.Uint16(payload[0:2])
	f.props = binary.BigEndian.Uint16(payload[2:4])
	phone, err := jt808.BCDToString(payload[4:10])
	if err != nil {
		return nil, fmt.Errorf("bad phone bcd: %w", err)
	}
	f.phone = phone
	f.seq = binary.BigEndian.Uint16(payload[10:12])
	bodyLen := int(f.props & 0x03FF)
	if f.props&0x2000 != 0 { // 分包标志（演示数据不分包，跳过包头处理）
		if len(payload) < 16 {
			return nil, fmt.Errorf("subpackage header missing")
		}
		if len(payload) < 16+bodyLen {
			return nil, fmt.Errorf("subpackage body truncated")
		}
		f.body = payload[16 : 16+bodyLen]
		return f, nil
	}
	if len(payload) < 12+bodyLen {
		return nil, fmt.Errorf("body truncated: have %d want %d", len(payload)-12, bodyLen)
	}
	f.body = payload[12 : 12+bodyLen]
	return f, nil
}

// readFrames 从读取的数据中按 0x7E 分帧（含去转义与校验）。
func readFrames(buf []byte, data []byte) ([]*frame, []byte) {
	buf = append(buf, data...)
	var frames []*frame
	for {
		start := -1
		for i, b := range buf {
			if b == delimiter {
				start = i
				break
			}
		}
		if start < 0 {
			return frames, nil
		}
		if start > 0 {
			buf = buf[start:]
		}
		end := -1
		for i := 1; i < len(buf); i++ {
			if buf[i] == delimiter {
				end = i
				break
			}
		}
		if end < 0 {
			return frames, buf
		}
		content := buf[1:end]
		buf = buf[end+1:]
		if len(content) == 0 {
			continue
		}
		unescaped, err := jt808.Unescape(content)
		if err != nil {
			continue
		}
		f, err := parseFrame(unescaped)
		if err != nil {
			continue
		}
		frames = append(frames, f)
	}
}

// =====================================================================
// H.264 RTP 收包重组（RFC 3984: Single NAL / STAP-A / FU-A）
// =====================================================================

type nalAssembler struct {
	fuBuf []byte // FU-A 重组中的 NAL
}

// depacketize 将一个 RTP payload 还原为 0 或多个完整 NAL。
// 返回 NAL 列表与“是否完成一个 NAL”（用于清空状态）。
func (a *nalAssembler) depacketize(payload []byte) ([][]byte, bool) {
	var out [][]byte
	if len(payload) == 0 {
		return out, false
	}
	nalType := payload[0] & 0x1F
	switch {
	case nalType >= 1 && nalType <= 23: // Single NAL unit
		out = append(out, append([]byte{}, payload...))
		return out, true
	case nalType == 24: // STAP-A
		p := payload[1:]
		for len(p) >= 2 {
			size := int(binary.BigEndian.Uint16(p[0:2]))
			if len(p) < 2+size {
				break
			}
			out = append(out, append([]byte{}, p[2:2+size]...))
			p = p[2+size:]
		}
		return out, true
	case nalType == 28: // FU-A
		if len(payload) < 2 {
			return out, false
		}
		fuHdr := payload[1]
		start := fuHdr&0x80 != 0
		end := fuHdr&0x40 != 0
		if start {
			a.fuBuf = []byte{(payload[0] & 0xE0) | (fuHdr & 0x1F)}
			a.fuBuf = append(a.fuBuf, payload[2:]...)
			return out, false
		}
		if a.fuBuf == nil {
			return out, false // 丢起始分片，丢弃
		}
		a.fuBuf = append(a.fuBuf, payload[2:]...)
		if end {
			out = append(out, a.fuBuf)
			a.fuBuf = nil
			return out, true
		}
		return out, false
	}
	// 其他类型（如 FU-B/FEC）不处理
	return out, false
}

// =====================================================================
// H.264 Annex-B 解析
// =====================================================================

type accessUnit struct {
	nals [][]byte
}

// splitAnnexB 按起始码切分 NAL（去掉起始码）。
func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte
	n := len(data)
	i := 0
	for i < n {
		start := -1
		for j := i; j+2 < n; j++ {
			if data[j] == 0 && data[j+1] == 0 && data[j+2] == 1 {
				start = j
				break
			}
		}
		if start < 0 {
			break
		}
		p := start + 3
		end := n
		for j := p; j+2 < n; j++ {
			if data[j] == 0 && data[j+1] == 0 && data[j+2] == 1 {
				end = j
				break
			}
		}
		e := end
		for e > p && data[e-1] == 0 { // 去掉结尾属于下一个起始码前导的零字节
			e--
		}
		if e > p {
			nals = append(nals, data[p:e])
		}
		i = end
	}
	return nals
}

// groupAccessUnits 将 NAL 分组为访问单元（帧）。
// 演示源视频由 x264 单 slice 模式编码，每个 VCL NAL 即一帧；
// 前导 SPS/PPS/SEI 等非 VCL NAL 归入其后的第一个访问单元。
func groupAccessUnits(nals [][]byte) []accessUnit {
	var units []accessUnit
	isVCL := func(nal []byte) bool {
		t := nal[0] & 0x1F
		return t >= 1 && t <= 5
	}
	var cur *accessUnit
	for _, nal := range nals {
		if isVCL(nal) {
			if cur != nil {
				units = append(units, *cur)
			}
			cur = &accessUnit{nals: [][]byte{nal}}
		} else if cur != nil {
			cur.nals = append(cur.nals, nal)
		} else {
			cur = &accessUnit{nals: [][]byte{nal}}
		}
	}
	if cur != nil {
		units = append(units, *cur)
	}
	return units
}

// =====================================================================
// 平台侧（platform）
// =====================================================================

var msgSeq uint16

func nextSeq() uint16 {
	msgSeq++
	return msgSeq
}

func runPlatform(listen, outPath string, duration time.Duration, logger *zap.Logger) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	defer ln.Close()
	logger.Info("platform listening", zap.String("addr", listen))

	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	logger.Info("terminal connected", zap.String("remote", conn.RemoteAddr().String()))

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	startCode := []byte{0, 0, 0, 1}
	var totalNAL, totalPackets, totalBytes uint64
	sent9101 := false
	streamStart := time.Time{}
	asm := &nalAssembler{}

	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 32*1024)
	var lastSeq uint16

	done := false
	for !done {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, rerr := conn.Read(chunk)
		if n > 0 {
			frames, rest := readFrames(buf, chunk[:n])
			buf = rest
			for _, f := range frames {
				switch f.msgID {
				case msgIDRegister:
					logger.Info("terminal registered", zap.String("phone", f.phone))
					// 0x8100 应答：应答流水号(2) + 结果(1) + 鉴权码
					respBody := []byte{byte(f.seq >> 8), byte(f.seq), 0x00}
					respBody = append(respBody, []byte("JTE1078")...)
					if _, werr := conn.Write(buildMessage(msgIDRegisterResp, f.phone, nextSeq(), respBody)); werr != nil {
						logger.Error("send register resp failed", zap.Error(werr))
					}
					// 0x9101 实时音视频请求：媒体类型+码流类型+通道号+IP(4)+TCP端口(2)+UDP端口(2)+实时回放(1)+传输模式(1)
					req := make([]byte, 0, 14)
					req = append(req, 0x00) // 视频
					req = append(req, 0x00) // 主码流
					req = append(req, 0x01) // 通道 1
					req = append(req, 127, 0, 0, 1)
					req = append(req, 0x1D, 0xC4) // TCP 7620
					req = append(req, 0x00, 0x00)
					req = append(req, 0x00) // 实时
					req = append(req, 0x01) // TCP 传输
					if _, werr := conn.Write(buildMessage(msgIDRealtimeRequest, f.phone, nextSeq(), req)); werr != nil {
						logger.Error("send 9101 failed", zap.Error(werr))
					} else {
						sent9101 = true
						logger.Info("sent 0x9101 realtime request (ch1 video main stream)")
					}
				case msgIDRealtimeResponse:
					logger.Info("terminal accepted 0x9101", zap.String("phone", f.phone))
				case msgIDRTPData:
					if streamStart.IsZero() {
						streamStart = time.Now()
						logger.Info("receiving 0x1200 RTP video data")
					}
					if len(f.body) < 4 {
						continue
					}
					// 0x1200 body: 通道(1) + 数据类型(1) + RTP头长度(2) + RTP整包(头+载荷)
					rtpHeaderLen := int(binary.BigEndian.Uint16(f.body[2:4]))
					if rtpHeaderLen < 12 || len(f.body) < 4+rtpHeaderLen {
						continue
					}
					rtp := f.body[4:] // 完整 RTP 包
					payload := rtp[rtpHeaderLen:]
					seq := binary.BigEndian.Uint16(rtp[2:4])

					if totalPackets < 3 {
						logger.Info("DEBUG rtp packet",
							zap.Int("bodyLen", len(f.body)),
							zap.Int("rtpHeaderLen", rtpHeaderLen),
							zap.Int("payloadLen", len(payload)))
					}
					if len(payload) == 0 {
						continue
					}

					if totalPackets > 0 && seq != (lastSeq+1)&0xFFFF {
						logger.Warn("rtp seq jump", zap.Uint16("expect", lastSeq+1), zap.Uint16("got", seq))
					}
					lastSeq = seq

					nals, _ := asm.depacketize(payload)
					for _, nal := range nals {
						out.Write(startCode)
						out.Write(nal)
						totalNAL++
					}
					totalPackets++
					totalBytes += uint64(len(f.body))
				}
			}
		}
		if rerr != nil {
			if ne, ok := rerr.(net.Error); !ok || !ne.Timeout() {
				logger.Info("connection closed", zap.Error(rerr))
				done = true
			}
		}
		if !streamStart.IsZero() && time.Since(streamStart) > duration {
			done = true
		}
		if !sent9101 && time.Since(startTime) > 20*time.Second {
			done = true
		}
	}
	out.Sync()
	dur := 0.0
	if !streamStart.IsZero() {
		dur = time.Since(streamStart).Seconds()
	}
	logger.Info("platform capture finished",
		zap.Uint64("rtp_packets", totalPackets),
		zap.Uint64("nals", totalNAL),
		zap.Uint64("bytes", totalBytes),
		zap.Float64("stream_seconds", dur))
	if totalNAL == 0 {
		return fmt.Errorf("no video data captured")
	}
	return nil
}

var startTime = time.Now()

// =====================================================================
// 终端侧（terminal）
// =====================================================================

func runTerminal(server, phone, h264Path string, fps int, logger *zap.Logger) error {
	data, err := os.ReadFile(h264Path)
	if err != nil {
		return fmt.Errorf("read h264: %w", err)
	}
	accessUnits := groupAccessUnits(splitAnnexB(data))
	if len(accessUnits) == 0 {
		return fmt.Errorf("no frames found in %s", h264Path)
	}
	logger.Info("h264 loaded",
		zap.Int("frames", len(accessUnits)),
		zap.Int("bytes", len(data)))

	conn, err := net.DialTimeout("tcp", server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect %s: %w", server, err)
	}
	defer conn.Close()
	logger.Info("connected to platform", zap.String("server", server))

	regBody := buildRegisterBody(phone)
	if _, err := conn.Write(buildMessage(msgIDRegister, phone, nextSeq(), regBody)); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	// 读循环：处理 0x8100 / 0x9101 / 0x9105
	streamCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 0, 16*1024)
		chunk := make([]byte, 8192)
		for {
			conn.SetReadDeadline(time.Now().Add(120 * time.Second))
			n, err := conn.Read(chunk)
			if n > 0 {
				frames, rest := readFrames(buf, chunk[:n])
				buf = rest
				for _, f := range frames {
					switch f.msgID {
					case msgIDRegisterResp:
						logger.Info("register accepted by platform")
					case msgIDRealtimeRequest:
						if len(f.body) >= 3 {
							logger.Info("received 0x9101, starting stream",
								zap.Uint8("channel", f.body[2]))
							resp := []byte{f.body[2], 0x00}
							if _, werr := conn.Write(buildMessage(msgIDRealtimeResponse, phone, nextSeq(), resp)); werr != nil {
								logger.Error("send 9102 failed", zap.Error(werr))
							}
							select {
							case streamCh <- struct{}{}:
							default:
							}
						}
					case msgIDAVControl:
						if len(f.body) >= 2 && f.body[1] == 0 {
							logger.Info("received 0x9105 stop")
							select {
							case stopCh <- struct{}{}:
							default:
							}
							return
						}
					}
				}
			}
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
		}
	}()

	select {
	case <-streamCh:
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timeout waiting for 0x9101 realtime request")
	}

	if err := streamH264(conn, phone, accessUnits, fps, stopCh, logger); err != nil {
		return err
	}
	logger.Info("stream finished")
	return nil
}

func buildRegisterBody(phone string) []byte {
	body := make([]byte, 0, 40)
	body = append(body, 0x00, 0x1F) // 省域 ID
	body = append(body, 0x00, 0x01) // 市域 ID
	mfr := []byte("JTE")
	for len(mfr) < 5 {
		mfr = append(mfr, 0)
	}
	body = append(body, mfr...)
	model := []byte("VIDEODEMO")
	for len(model) < 20 {
		model = append(model, 0)
	}
	body = append(body, model...)
	id := []byte(phone)
	for len(id) < 7 {
		id = append(id, 0)
	}
	body = append(body, id[:7]...)
	body = append(body, 0x00) // 车牌颜色
	return body
}

// streamH264 按目标帧率实时推送 RTP。
func streamH264(conn net.Conn, phone string, units []accessUnit, fps int, stopCh chan struct{}, logger *zap.Logger) error {
	interval := time.Second / time.Duration(fps)
	tsInc := uint32(90000 / fps)
	var rtpSeq uint16
	var rtpTS uint32
	const ssrc = 0x12345678
	const maxPayload = 900

	start := time.Now()
	for i, au := range units {
		select {
		case <-stopCh:
			return nil
		default:
		}
		target := start.Add(time.Duration(float64(i) * float64(interval)))
		if wait := time.Until(target); wait > 0 {
			time.Sleep(wait)
		}

		containsIDR := false
		for _, nal := range au.nals {
			if t := nal[0] & 0x1F; t == 5 {
				containsIDR = true
			}
			// Single NAL 或 FU-A 分片
			var chunks [][]byte
			if len(nal) <= maxPayload {
				chunks = [][]byte{nal}
			} else {
				fuIndicator := (nal[0] & 0xE0) | 28
				rest := nal[1:]
				first := true
				for len(rest) > 0 {
					n := maxPayload - 2
					if n > len(rest) {
						n = len(rest)
					}
					fuHdr := nal[0] & 0x1F
					if first {
						fuHdr |= 0x80
					}
					if len(rest)-n == 0 {
						fuHdr |= 0x40
					}
					chunk := []byte{fuIndicator, fuHdr}
					chunk = append(chunk, rest[:n]...)
					chunks = append(chunks, chunk)
					rest = rest[n:]
					first = false
				}
			}
			for ci, c := range chunks {
				rtpSeq++
				hdr := make([]byte, 12)
				hdr[0] = 0x80
				hdr[1] = 96
				if ci == len(chunks)-1 {
					hdr[1] |= 0x80 // marker: 帧末包
				}
				binary.BigEndian.PutUint16(hdr[2:4], rtpSeq)
				binary.BigEndian.PutUint32(hdr[4:8], rtpTS)
				binary.BigEndian.PutUint32(hdr[8:12], ssrc)
				body := make([]byte, 0, 4+12+len(c))
				if containsIDR {
					body = append(body, 0x01, 0x00) // 通道1 视频I帧
				} else {
					body = append(body, 0x01, 0x01) // 通道1 视频P帧
				}
				body = append(body, 0x00, 12)
				body = append(body, hdr...)
				body = append(body, c...)
				if _, err := conn.Write(buildMessage(msgIDRTPData, phone, nextSeq(), body)); err != nil {
					return fmt.Errorf("send rtp: %w", err)
				}
			}
		}
		rtpTS += tsInc
		if (i+1)%50 == 0 {
			logger.Info("streaming progress", zap.Int("frame", i+1), zap.Int("total", len(units)))
		}
	}
	logger.Info("all frames sent",
		zap.Int("frames", len(units)),
		zap.Float64("seconds", time.Since(start).Seconds()))
	time.Sleep(time.Second) // 留时间让对端收尾
	return nil
}

// =====================================================================
// main
// =====================================================================

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "platform":
		fs := flag.NewFlagSet("platform", flag.ExitOnError)
		listen := fs.String("listen", "127.0.0.1:7611", "TCP listen address")
		out := fs.String("out", "out.h264", "output H.264 Annex-B file")
		dur := fs.Duration("duration", 15*time.Second, "max capture duration")
		_ = fs.Parse(os.Args[2:])
		if err := runPlatform(*listen, *out, *dur, logger); err != nil {
			logger.Fatal("platform failed", zap.Error(err))
		}
	case "terminal":
		fs := flag.NewFlagSet("terminal", flag.ExitOnError)
		server := fs.String("server", "127.0.0.1:7611", "platform address")
		phone := fs.String("phone", "13800000001", "terminal phone (BCD)")
		file := fs.String("file", "source.h264", "input H.264 Annex-B file")
		fps := fs.Int("fps", 25, "frame rate")
		_ = fs.Parse(os.Args[2:])
		if err := runTerminal(*server, *phone, *file, *fps, logger); err != nil {
			logger.Fatal("terminal failed", zap.Error(err))
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
用法:
  videodemo platform -listen 127.0.0.1:7611 -out out.h264 -duration 15s
  videodemo terminal -server 127.0.0.1:7611 -phone 13800000001 -file source.h264 -fps 25`))
	os.Exit(2)
}
