package simulator

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	jt808 "github.com/suoten/jt-engine/pkg/protocol/jt808"
	"go.uber.org/zap"
)

type Simulator1078Config struct {
	ServerAddr string
	Phone      string
	Count      int
}

type Simulator1078 struct {
	config    *Simulator1078Config
	logger    *zap.Logger
	conns     []net.Conn
	mu        sync.Mutex
	running   bool
	streaming map[string]bool
	streamMu  sync.Mutex
	rtpSeq    uint32
	rtpTS     uint32
	rtpMu     sync.Mutex
}

func NewSimulator1078(cfg *Simulator1078Config, logger *zap.Logger) *Simulator1078 {
	if cfg.Count == 0 {
		cfg.Count = 1
	}
	return &Simulator1078{
		config:    cfg,
		logger:    logger,
		streaming: make(map[string]bool),
	}
}

func (s *Simulator1078) Start() error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	for i := 0; i < s.config.Count; i++ {
		phone := s.config.Phone
		if s.config.Count > 1 {
			phone = fmt.Sprintf("%s%02d", s.config.Phone, i)
		}
		go s.runTerminal(phone)
	}

	s.logger.Info("simulator 1078 started",
		zap.Int("count", s.config.Count),
		zap.String("server", s.config.ServerAddr))
	return nil
}

func (s *Simulator1078) Stop() {
	s.mu.Lock()
	s.running = false
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
	s.mu.Unlock()

	s.streamMu.Lock()
	s.streaming = make(map[string]bool)
	s.streamMu.Unlock()
}

func (s *Simulator1078) runTerminal(phone string) {
	conn, err := net.DialTimeout("tcp", s.config.ServerAddr, 10*time.Second)
	if err != nil {
		s.logger.Error("1078 simulator connect failed", zap.String("phone", phone), zap.Error(err))
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()

	s.logger.Info("1078 simulator connected", zap.String("phone", phone))

	if err := s.sendRegister(conn, phone); err != nil {
		s.logger.Error("1078 register failed", zap.String("phone", phone), zap.Error(err))
		return
	}

	go s.readLoop(conn, phone)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			return
		}

		select {
		case <-ticker.C:
			if err := s.sendHeartbeat(conn, phone); err != nil {
				s.logger.Error("1078 heartbeat failed", zap.String("phone", phone), zap.Error(err))
				return
			}
		}
	}
}

func (s *Simulator1078) sendRegister(conn net.Conn, phone string) error {
	msg := s.build1078Message(0x0100, phone, s.buildRegisterBody(phone))
	_, err := conn.Write(msg)
	return err
}

func (s *Simulator1078) sendHeartbeat(conn net.Conn, phone string) error {
	msg := s.build1078Message(0x0002, phone, nil)
	_, err := conn.Write(msg)
	return err
}

func (s *Simulator1078) buildRegisterBody(phone string) []byte {
	body := make([]byte, 0)
	body = append(body, uint16ToBytes(31)...)
	body = append(body, uint16ToBytes(1)...)
	manufacturer := []byte("JTE")
	for len(manufacturer) < 5 {
		manufacturer = append(manufacturer, 0)
	}
	body = append(body, manufacturer...)
	model := []byte("SIM1078")
	for len(model) < 20 {
		model = append(model, 0)
	}
	body = append(body, model...)
	id := []byte(phone)
	for len(id) < 7 {
		id = append(id, 0)
	}
	body = append(body, id...)
	body = append(body, 1)
	return body
}

// build1078Message builds a JT/T 808/1078 frame with proper escape encoding.
// The erroneous 0x30 prefix has been removed and escape encoding is applied
// so that 0x7E/0x7D bytes in the body do not corrupt the frame.
func (s *Simulator1078) build1078Message(msgID uint16, phone string, body []byte) []byte {
	phoneBCD := phoneToBCD(phone)

	header := make([]byte, 0, 12)
	header = append(header, uint16ToBytes(msgID)...)
	header = append(header, uint16ToBytes(uint16(len(body)))...)
	header = append(header, phoneBCD...)
	header = append(header, uint16ToBytes(uint16(time.Now().Unix()%65536))...)

	raw := make([]byte, 0, len(header)+len(body)+1)
	raw = append(raw, header...)
	raw = append(raw, body...)
	checksum := jt808.CalcChecksum(raw)
	raw = append(raw, checksum)

	escaped := jt808.Escape(raw)
	frame := jt808.WrapWithDelimiter(escaped)
	return frame
}

func (s *Simulator1078) readLoop(conn net.Conn, phone string) {
	buf := make([]byte, 8192)
	var frameBuf []byte
	for {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			return
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		if n > 0 {
			frameBuf = append(frameBuf, buf[:n]...)
			frameBuf = s.parseFrames(conn, phone, frameBuf)
		}
	}
}

// parseFrames extracts complete 0x7E-delimited frames from the buffer and
// returns the remaining unparsed bytes.
func (s *Simulator1078) parseFrames(conn net.Conn, phone string, buf []byte) []byte {
	for {
		start := -1
		for i, b := range buf {
			if b == 0x7E {
				start = i
				break
			}
		}
		if start < 0 {
			return nil
		}
		if start > 0 {
			buf = buf[start:]
		}
		if len(buf) < 2 {
			return buf
		}
		end := -1
		for i := 1; i < len(buf); i++ {
			if buf[i] == 0x7E {
				end = i
				break
			}
		}
		if end < 0 {
			return buf
		}

		frame := buf[:end+1]
		buf = buf[end+1:]

		s.handleServerFrame(conn, phone, frame)
	}
}

func (s *Simulator1078) handleServerFrame(conn net.Conn, phone string, frame []byte) {
	if len(frame) < 5 {
		return
	}

	content := frame[1 : len(frame)-1]
	unescaped := jt808.Unescape(content)
	if len(unescaped) < 5 {
		return
	}

	msgID := binary.BigEndian.Uint16(unescaped[0:2])
	bodyProps := binary.BigEndian.Uint16(unescaped[2:4])
	hasPack := (bodyProps & 0x2000) != 0

	headerLen := 12
	if hasPack {
		headerLen = 16
	}

	if len(unescaped) < headerLen+1 {
		return
	}

	body := unescaped[headerLen : len(unescaped)-1]

	switch msgID {
	case 0x9101: // realtime A/V request (platform -> terminal)
		if len(body) < 1 {
			return
		}
		channel := body[0]
		s.logger.Info("1078 simulator received realtime request",
			zap.String("phone", phone),
			zap.Uint8("channel", channel))

		respBody := []byte{channel, 0x00}
		resp := s.build1078Message(0x9102, phone, respBody)
		if _, err := conn.Write(resp); err != nil {
			s.logger.Error("1078 simulator send response failed", zap.Error(err))
		}

		s.startStreaming(conn, phone, channel)

	case 0x9105: // A/V control request (platform -> terminal)
		if len(body) < 2 {
			return
		}
		channel := body[0]
		cmd := body[1]
		s.logger.Info("1078 simulator received control request",
			zap.String("phone", phone),
			zap.Uint8("channel", channel),
			zap.Uint8("command", cmd))
		if cmd == 0 {
			s.stopStreaming(phone, channel)
		}

	case 0x8001:
		s.logger.Debug("1078 simulator received universal response", zap.String("phone", phone))
	case 0x8100:
		s.logger.Info("1078 simulator register response received", zap.String("phone", phone))
	}
}

func (s *Simulator1078) startStreaming(conn net.Conn, phone string, channel byte) {
	key := fmt.Sprintf("%s_%d", phone, channel)
	s.streamMu.Lock()
	if s.streaming[key] {
		s.streamMu.Unlock()
		return
	}
	s.streaming[key] = true
	s.streamMu.Unlock()

	s.logger.Info("1078 simulator starting RTP stream",
		zap.String("phone", phone),
		zap.Uint8("channel", channel))

	go s.streamRTP(conn, phone, channel)
}

func (s *Simulator1078) stopStreaming(phone string, channel byte) {
	key := fmt.Sprintf("%s_%d", phone, channel)
	s.streamMu.Lock()
	delete(s.streaming, key)
	s.streamMu.Unlock()
	s.logger.Info("1078 simulator stopping RTP stream",
		zap.String("phone", phone),
		zap.Uint8("channel", channel))
}

func (s *Simulator1078) streamRTP(conn net.Conn, phone string, channel byte) {
	key := fmt.Sprintf("%s_%d", phone, channel)
	ticker := time.NewTicker(40 * time.Millisecond) // ~25 fps
	defer ticker.Stop()

	for {
		s.streamMu.Lock()
		streaming := s.streaming[key]
		s.streamMu.Unlock()
		if !streaming {
			return
		}

		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			return
		}

		select {
		case <-ticker.C:
			if err := s.sendRTPData(conn, phone, channel); err != nil {
				s.logger.Debug("1078 simulator RTP send failed",
					zap.String("phone", phone),
					zap.Error(err))
				return
			}
		}
	}
}

// sendRTPData sends a 0x1200 RTP data message with a minimal H.264 test payload.
func (s *Simulator1078) sendRTPData(conn net.Conn, phone string, channel byte) error {
	s.rtpMu.Lock()
	s.rtpSeq++
	s.rtpTS += 3600 // 90kHz clock, 25fps
	seq := s.rtpSeq
	ts := s.rtpTS
	s.rtpMu.Unlock()

	// Build RTP header (12 bytes, RFC 3550)
	rtpHeader := make([]byte, 12)
	rtpHeader[0] = 0x80 // V=2, P=0, X=0, CC=0
	rtpHeader[1] = 0xE0 // M=1, PT=96 (dynamic, H.264)
	binary.BigEndian.PutUint16(rtpHeader[2:4], uint16(seq))
	binary.BigEndian.PutUint32(rtpHeader[4:8], ts)
	binary.BigEndian.PutUint32(rtpHeader[8:12], 0x12345678) // SSRC

	// Minimal H.264 SPS NAL unit (Baseline profile) as test payload.
	rtpPayload := []byte{
		0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2,
	}

	// 0x1200 body: LogicChannel(1) + DataType(1) + RTPHeaderLen(2) + RTPHeader + RTPPayload
	body := make([]byte, 0, 4+len(rtpHeader)+len(rtpPayload))
	body = append(body, channel)
	body = append(body, 0x00) // DataType: 0 = video I-frame
	body = append(body, byte(len(rtpHeader)>>8), byte(len(rtpHeader)))
	body = append(body, rtpHeader...)
	body = append(body, rtpPayload...)

	msg := s.build1078Message(0x1200, phone, body)
	_, err := conn.Write(msg)
	return err
}
