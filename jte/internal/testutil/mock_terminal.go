package testutil

import (

	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jte-engine/jte/pkg/protocol"
	"github.com/jte-engine/jte/pkg/protocol/jt808"
)

type MockTerminal struct {
	Phone      string
	Conn       net.Conn
	SeqNum     uint16
	Registered bool
	Authed     bool
	t          *testing.T
	mu         sync.Mutex
}

func NewMockTerminal(t *testing.T, addr, phone string) *MockTerminal {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to %s: %v", addr, err)
	}

	return &MockTerminal{
		Phone:  phone,
		Conn:   conn,
		SeqNum: 1,
		t:      t,
	}
}

func (m *MockTerminal) Close() {
	if m.Conn != nil {
		m.Conn.Close()
	}
}

func (m *MockTerminal) nextSeq() uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	seq := m.SeqNum
	m.SeqNum++
	return seq
}

func (m *MockTerminal) Register(provinceID, cityID int, manufacturer, model, terminalID string, plateColor byte) error {
	reg := &jt808.RegisterMessage{
		ProvinceID:    provinceID,
		CityID:        cityID,
		Manufacturer:  manufacturer,
		TerminalModel: model,
		TerminalID:    terminalID,
		PlateColor:    plateColor,
	}

	body, err := reg.Marshal()
	if err != nil {
		return err
	}

	msg, err := m.buildMessage(jt808.MsgIDRegister, body)
	if err != nil {
		return err
	}

	if _, err := m.Conn.Write(msg); err != nil {
		return err
	}

	resp, err := m.readResponse()
	if err != nil {
		return err
	}

	if resp.Header.MsgID == jt808.MsgIDRegisterResp {
		m.Registered = true
	}

	return nil
}

func (m *MockTerminal) Auth(authCode string) error {
	auth := &jt808.AuthMessage{AuthCode: authCode}
	body, err := auth.Marshal()
	if err != nil {
		return err
	}

	msg, err := m.buildMessage(jt808.MsgIDAuth, body)
	if err != nil {
		return err
	}

	if _, err := m.Conn.Write(msg); err != nil {
		return err
	}

	resp, err := m.readResponse()
	if err != nil {
		return err
	}

	if resp.Header.MsgID == jt808.MsgIDGeneralResp {
		m.Authed = true
	}

	return nil
}

func (m *MockTerminal) Heartbeat() error {
	msg, err := m.buildMessage(jt808.MsgIDHeartbeat, nil)
	if err != nil {
		return err
	}

	_, err = m.Conn.Write(msg)
	return err
}

func (m *MockTerminal) Location(lat, lon float64, speed float64, direction int) error {
	loc := &jt808.LocationMessage{
		Latitude:  lat,
		Longitude: lon,
		Speed:     uint16(speed * 10),
		Direction: uint16(direction),
		Time:      time.Now().Format("20060102150405"),
	}

	body, err := loc.Marshal()
	if err != nil {
		return err
	}

	msg, err := m.buildMessage(jt808.MsgIDLocation, body)
	if err != nil {
		return err
	}

	_, err = m.Conn.Write(msg)
	return err
}

func (m *MockTerminal) buildMessage(msgID uint16, body []byte) ([]byte, error) {
	header := &protocol.MessageHeader{
		MsgID:   msgID,
		Phone:   m.Phone,
		SeqNum:  m.nextSeq(),
		BodyLen: len(body),
	}

	codec := &jt808.JT808Codec{}
	headerBytes, err := codec.EncodeHeader(header)
	if err != nil {
		return nil, err
	}

	payload := append(headerBytes, body...)
	checksum := jt808.CalcChecksum(payload)
	payload = append(payload, checksum)

	escaped := jt808.Escape(payload)
	return jt808.WrapWithDelimiter(escaped), nil
}

func (m *MockTerminal) readResponse() (*jt808Message, error) {
	buf := make([]byte, 4096)
	m.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := m.Conn.Read(buf)
	if err != nil {
		return nil, err
	}

	data := buf[:n]
	if len(data) < 2 || data[0] != 0x7E || data[len(data)-1] != 0x7E {
		return nil, fmt.Errorf("invalid response format")
	}

	inner := data[1 : len(data)-1]
	unescaped := jt808.Unescape(inner)

	codec := &jt808.JT808Codec{}
	header, offset, err := codec.ParseHeader(unescaped)
	if err != nil {
		return nil, err
	}

	return &jt808Message{Header: header, Body: unescaped[offset:]}, nil
}

type jt808Message struct {
	Header *protocol.MessageHeader
	Body   []byte
}

type ConcurrentTestRunner struct {
	t       *testing.T
	addr    string
	count   int
	results chan error
}

func NewConcurrentTestRunner(t *testing.T, addr string, count int) *ConcurrentTestRunner {
	return &ConcurrentTestRunner{
		t:       t,
		addr:    addr,
		count:   count,
		results: make(chan error, count),
	}
}

func (r *ConcurrentTestRunner) Run(fn func(*testing.T, string, int)) {
	var started atomic.Int32
	var completed atomic.Int32

	for i := 0; i < r.count; i++ {
		go func(idx int) {
			started.Add(1)
			fn(r.t, r.addr, idx)
			completed.Add(1)
		}(i)
	}

	timeout := time.After(30 * time.Second)
	for {
		select {
		case <-timeout:
			r.t.Fatalf("timeout: started %d, completed %d of %d", started.Load(), completed.Load(), r.count)
			return
		default:
			if int(completed.Load()) >= r.count {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}