package simulator

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Simulator808Config struct {
	ServerAddr string
	Phone      string
	Count      int
	Freq       time.Duration
	LatMin     float64
	LatMax     float64
	LonMin     float64
	LonMax     float64
	AuthCode   string
	ProvinceID uint16
	CityID     uint16
}

type Simulator808 struct {
	config  *Simulator808Config
	logger  *zap.Logger
	conns   []net.Conn
	mu      sync.Mutex
	running bool
}

func NewSimulator808(cfg *Simulator808Config, logger *zap.Logger) *Simulator808 {
	if cfg.Freq == 0 {
		cfg.Freq = 5 * time.Second
	}
	if cfg.Count == 0 {
		cfg.Count = 1
	}
	if cfg.LatMin == 0 {
		cfg.LatMin = 30.0
		cfg.LatMax = 32.0
		cfg.LonMin = 120.0
		cfg.LonMax = 122.0
	}
	if cfg.ProvinceID == 0 {
		cfg.ProvinceID = 31
	}
	if cfg.CityID == 0 {
		cfg.CityID = 1
	}
	return &Simulator808{
		config: cfg,
		logger: logger,
	}
}

func (s *Simulator808) Start() error {
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

	s.logger.Info("simulator 808 started",
		zap.Int("count", s.config.Count),
		zap.String("server", s.config.ServerAddr))
	return nil
}

func (s *Simulator808) Stop() {
	s.mu.Lock()
	s.running = false
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
	s.mu.Unlock()
}

func (s *Simulator808) runTerminal(phone string) {
	conn, err := net.DialTimeout("tcp", s.config.ServerAddr, 10*time.Second)
	if err != nil {
		s.logger.Error("simulator connect failed", zap.String("phone", phone), zap.Error(err))
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()

	s.logger.Info("simulator connected", zap.String("phone", phone))

	if err := s.sendRegister(conn, phone); err != nil {
		s.logger.Error("register failed", zap.String("phone", phone), zap.Error(err))
		return
	}

	if err := s.sendAuth(conn, phone); err != nil {
		s.logger.Error("auth failed", zap.String("phone", phone), zap.Error(err))
		return
	}

	go s.readLoop(conn, phone)

	lat := s.config.LatMin + rand.Float64()*(s.config.LatMax-s.config.LatMin)
	lon := s.config.LonMin + rand.Float64()*(s.config.LonMax-s.config.LonMin)

	ticker := time.NewTicker(s.config.Freq)
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
			lat += (rand.Float64() - 0.5) * 0.001
			lon += (rand.Float64() - 0.5) * 0.001
			speed := rand.Float64() * 80 + 20
			direction := rand.Intn(360)

			if err := s.sendLocation(conn, phone, lat, lon, speed, direction); err != nil {
				s.logger.Error("send location failed", zap.String("phone", phone), zap.Error(err))
				return
			}
		}
	}
}

func (s *Simulator808) sendRegister(conn net.Conn, phone string) error {
	msg := s.build808Message(0x0100, phone, s.buildRegisterBody(phone))
	_, err := conn.Write(msg)
	return err
}

func (s *Simulator808) sendAuth(conn net.Conn, phone string) error {
	authCode := s.config.AuthCode
	if authCode == "" {
		authCode = "jte_sim"
	}
	msg := s.build808Message(0x0102, phone, []byte(authCode))
	_, err := conn.Write(msg)
	return err
}

func (s *Simulator808) sendLocation(conn net.Conn, phone string, lat, lon, speed float64, direction int) error {
	body := s.buildLocationBody(lat, lon, speed, direction)
	msg := s.build808Message(0x0200, phone, body)
	_, err := conn.Write(msg)
	return err
}

func (s *Simulator808) buildRegisterBody(phone string) []byte {
	provinceID := s.config.ProvinceID
	cityID := s.config.CityID

	manufacturer := make([]byte, 5)
	copy(manufacturer, []byte("JTE"))

	model := make([]byte, 20)
	copy(model, []byte("JTE-SIM-2019"))

	terminalID := make([]byte, 7)
	phoneSuffix := phone
	if len(phoneSuffix) > 7 {
		phoneSuffix = phoneSuffix[len(phoneSuffix)-7:]
	}
	copy(terminalID, []byte(phoneSuffix))

	plateColor := byte(1)

	body := make([]byte, 0, 36)
	body = append(body, uint16ToBytes(provinceID)...)
	body = append(body, uint16ToBytes(cityID)...)
	body = append(body, manufacturer...)
	body = append(body, model...)
	body = append(body, terminalID...)
	body = append(body, plateColor)

	return body
}

func (s *Simulator808) buildLocationBody(lat, lon, speed float64, direction int) []byte {
	body := make([]byte, 28)

	alarmFlag := uint32(0)
	binary.BigEndian.PutUint32(body[0:4], alarmFlag)

	status := uint32(1)
	binary.BigEndian.PutUint32(body[4:8], status)

	latInt := uint32(lat * 1000000)
	binary.BigEndian.PutUint32(body[8:12], latInt)

	lonInt := uint32(lon * 1000000)
	binary.BigEndian.PutUint32(body[12:16], lonInt)

	altitude := uint16(100)
	binary.BigEndian.PutUint16(body[16:18], altitude)

	speedInt := uint16(speed * 10)
	binary.BigEndian.PutUint16(body[18:20], speedInt)

	dirInt := uint16(direction)
	binary.BigEndian.PutUint16(body[20:22], dirInt)

	now := time.Now()
	timeStr := now.Format("060102150405")
	copy(body[22:28], []byte(timeStr))

	return body
}

func (s *Simulator808) build808Message(msgID uint16, phone string, body []byte) []byte {
	phoneBCD := phoneToBCD(phone)
	_ = 12 + len(phoneBCD)

	header := make([]byte, 0)
	header = append(header, 0x30)
	header = append(header, uint16ToBytes(msgID)...)
	header = append(header, uint16ToBytes(uint16(len(body)))...)
	header = append(header, phoneBCD...)
	header = append(header, uint16ToBytes(uint16(time.Now().Unix()%65536))...)

	checksum := byte(0)
	for _, b := range header {
		checksum ^= b
	}
	for _, b := range body {
		checksum ^= b
	}

	msg := make([]byte, 0)
	msg = append(msg, 0x7E)
	msg = append(msg, header...)
	msg = append(msg, body...)
	msg = append(msg, checksum)
	msg = append(msg, 0x7E)

	return msg
}

func (s *Simulator808) readLoop(conn net.Conn, phone string) {
	buf := make([]byte, 4096)
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
			s.logger.Debug("simulator read", zap.String("phone", phone), zap.Error(err))
			return
		}

		if n > 0 {
			s.logger.Debug("simulator received", zap.String("phone", phone), zap.Int("bytes", n))
			if n >= 5 && buf[0] == 0x7E {
				respMsgID := binary.BigEndian.Uint16(buf[2:4])
				if respMsgID == 0x8001 {
					s.logger.Debug("universal response received", zap.String("phone", phone))
				} else if respMsgID == 0x8100 {
					s.logger.Info("register response received", zap.String("phone", phone))
				}
			}
		}
	}
}

func phoneToBCD(phone string) []byte {
	bcd := make([]byte, 6)
	for i := 0; i < 12 && i*2 < len(phone); i++ {
		high := byte(phone[i*2] - '0')
		var low byte
		if i*2+1 < len(phone) {
			low = byte(phone[i*2+1] - '0')
		}
		bcd[i] = (high << 4) | low
	}
	return bcd
}

func uint16ToBytes(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}