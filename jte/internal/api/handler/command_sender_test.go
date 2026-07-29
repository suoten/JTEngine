package handler

import (
	"net"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/pkg/protocol"
	"github.com/suoten/jt-engine/pkg/protocol/jt808"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-30 [P0-1]: CommandSender 投递链路单元测试
//
// 本测试通过本地 TCP listener 模拟终端设备，验证 CommandSender.SendToDevice
// 经 session.Send → sendLoop 串行化写入后，下行 808 帧能真正到达对端设备，
// 且帧的 msgID / phone / seqNum / body 与预期一致。覆盖 0x8103 终端指令与
// 0x8300 文本下发两条核心下行链路。

// setupDeviceSession 建立一对本地 TCP 连接，将平台侧连接包装为已注册的
// gateway.Session，返回 session、设备侧连接与清理函数。
func setupDeviceSession(t *testing.T, phone string) (*gateway.Session, net.Conn, *gateway.SessionManager) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	deviceConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	platformConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	sm := gateway.NewSessionManager(zap.NewNop())
	session := sm.Create("cmd-sender-test", platformConn)
	// Register 会将 session 按 phone 登记到 byPhone 索引，供 GetByPhone 查找
	sm.Register(session, phone)
	return session, deviceConn, sm
}

// readFrame 从设备侧连接读取一个完整的 0x7e...0x7e 808 帧。
func readFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	for {
		_, err := conn.Read(tmp)
		if err != nil {
			t.Fatalf("device read: %v (buf so far=% x)", err, buf)
		}
		buf = append(buf, tmp[0])
		if len(buf) >= 2 && buf[0] == 0x7e && buf[len(buf)-1] == 0x7e {
			return buf
		}
		if len(buf) > 2048 {
			t.Fatalf("frame too long without closing 0x7e: % x", buf)
		}
	}
}

// decodeFrame 对一个 0x7e...0x7e 帧执行去分隔符 + 反转义 + 校验码验证 +
// 头/体解析，返回解析后的 header 与 body。
func decodeFrame(t *testing.T, frame []byte) (*protocol.MessageHeader, protocol.MessageBody) {
	t.Helper()
	// StripDelimiter 去掉首尾 0x7e
	escaped := jt808.StripDelimiter(frame)
	// 反转义得到原始 payload = header + body + checksum
	raw, err := jt808.Unescape(escaped)
	if err != nil {
		t.Fatalf("Unescape failed: %v", err)
	}
	if len(raw) < 13 { // 12B header + 至少 1B body 或仅校验码
		t.Fatalf("decoded payload too short: %d bytes (% x)", len(raw), raw)
	}
	// 校验码 = 最后 1B，等于前面所有字节的 XOR
	payload := raw[:len(raw)-1]
	wantChecksum := raw[len(raw)-1]
	gotChecksum := jt808.CalcChecksum(payload)
	if gotChecksum != wantChecksum {
		t.Fatalf("checksum mismatch: got=0x%02x want=0x%02x (payload=% x)", gotChecksum, wantChecksum, payload)
	}
	codec := jt808.NewCodec()
	header, headerLen, err := codec.ParseHeader(payload)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	bodyData := payload[headerLen:]
	if header.BodyLen != len(bodyData) {
		t.Fatalf("body length mismatch: header.BodyLen=%d actual=%d", header.BodyLen, len(bodyData))
	}
	body, err := codec.ParseBody(header.MsgID, bodyData)
	if err != nil {
		t.Fatalf("ParseBody(msgID=0x%04x): %v (data=% x)", header.MsgID, err, bodyData)
	}
	return header, body
}

// TestCommandSender_DeliversCommandToDevice 验证 0x8103 终端指令经
// session.Send 下发后能被设备侧完整接收并正确解码。
func TestCommandSender_DeliversCommandToDevice(t *testing.T) {
	// 808 BCD 电话为 12 位（11 位号码前补 0），与编解码 BCDToString 输出一致
	const phone = "013800000000"
	session, deviceConn, sm := setupDeviceSession(t, phone)
	defer sm.Remove(session.ID)
	defer session.Close()
	defer deviceConn.Close()

	cs := NewCommandSender(sm, zap.NewNop())

	// 构造 0x8103 指令：1 个参数项，参数ID=0x0029(心跳间隔)，值=0x003C(60s)
	params := map[uint32][]byte{
		0x0029: {0x00, 0x3C},
	}
	if err := cs.SendToDevice(phone, jt808.MsgIDCommand, &jt808.CommandMessage{Params: params}); err != nil {
		t.Fatalf("SendToDevice(0x8103) failed: %v", err)
	}

	frame := readFrame(t, deviceConn)
	header, body := decodeFrame(t, frame)

	if header.MsgID != jt808.MsgIDCommand {
		t.Errorf("msgID=0x%04x, want 0x%04x", header.MsgID, jt808.MsgIDCommand)
	}
	if header.Phone != phone {
		t.Errorf("phone=%q, want %q", header.Phone, phone)
	}
	if header.SeqNum == 0 {
		t.Errorf("seqNum should be non-zero, got %d", header.SeqNum)
	}

	cmd, ok := body.(*jt808.CommandMessage)
	if !ok {
		t.Fatalf("body type=%T, want *CommandMessage", body)
	}
	if len(cmd.Params) != 1 {
		t.Fatalf("params count=%d, want 1", len(cmd.Params))
	}
	val, ok := cmd.Params[0x0029]
	if !ok {
		t.Fatalf("param 0x0029 not found in decoded body")
	}
	if len(val) != 2 || val[0] != 0x00 || val[1] != 0x3C {
		t.Errorf("param 0x0029 value=% x, want 00 3C", val)
	}
}

// TestCommandSender_DeliversTextMessageToDevice 验证 0x8300 文本下发链路。
func TestCommandSender_DeliversTextMessageToDevice(t *testing.T) {
	const phone = "013900000001"
	session, deviceConn, sm := setupDeviceSession(t, phone)
	defer sm.Remove(session.ID)
	defer session.Close()
	defer deviceConn.Close()

	cs := NewCommandSender(sm, zap.NewNop())

	const text = "超速报警，请减速"
	const sign byte = 0x01
	if err := cs.SendToDevice(phone, jt808.MsgIDTextSend, &jt808.TextSendMessage{Sign: sign, Text: text}); err != nil {
		t.Fatalf("SendToDevice(0x8300) failed: %v", err)
	}

	frame := readFrame(t, deviceConn)
	header, body := decodeFrame(t, frame)

	if header.MsgID != jt808.MsgIDTextSend {
		t.Errorf("msgID=0x%04x, want 0x%04x", header.MsgID, jt808.MsgIDTextSend)
	}
	if header.Phone != phone {
		t.Errorf("phone=%q, want %q", header.Phone, phone)
	}

	txt, ok := body.(*jt808.TextSendMessage)
	if !ok {
		t.Fatalf("body type=%T, want *TextSendMessage", body)
	}
	if txt.Sign != sign {
		t.Errorf("sign=0x%02x, want 0x%02x", txt.Sign, sign)
	}
	if txt.Text != text {
		t.Errorf("text=%q, want %q", txt.Text, text)
	}
}

// TestCommandSender_DeviceOffline 验证设备未注册时 SendToDevice 返回错误，
// 不会 panic 或误发空帧。
func TestCommandSender_DeviceOffline(t *testing.T) {
	sm := gateway.NewSessionManager(zap.NewNop())
	cs := NewCommandSender(sm, zap.NewNop())

	err := cs.SendToDevice("013700000000", jt808.MsgIDCommand, &jt808.CommandMessage{Params: map[uint32][]byte{}})
	if err == nil {
		t.Error("SendToDevice on offline device should return error")
	}
}

// TestCommandSender_SendAndWaitDeliversFrame 验证 SendAndWait 通过
// session.SendWithSeq 获取序号并注册 pending 应答表。设备侧应能收到帧；
// 当终端通用应答（0x0001）到达时，HandleGeneralResp 应唤醒 pending 并返回结果。
func TestCommandSender_SendAndWaitDeliversFrame(t *testing.T) {
	const phone = "013800000002"
	session, deviceConn, sm := setupDeviceSession(t, phone)
	defer sm.Remove(session.ID)
	defer session.Close()
	defer deviceConn.Close()

	cs := NewCommandSender(sm, zap.NewNop())

	// 异步发起 SendAndWait，等待终端通用应答
	type result struct {
		res byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := cs.SendAndWait(phone, jt808.MsgIDTextSend, &jt808.TextSendMessage{Sign: 0x02, Text: "test"})
		done <- result{res, err}
	}()

	frame := readFrame(t, deviceConn)
	header, _ := decodeFrame(t, frame)

	if header.MsgID != jt808.MsgIDTextSend {
		t.Errorf("msgID=0x%04x, want 0x%04x", header.MsgID, jt808.MsgIDTextSend)
	}
	if header.Phone != phone {
		t.Errorf("phone=%q, want %q", header.Phone, phone)
	}

	// 确认 pending 表已注册该序号（SendAndWait 应在帧入队后注册）
	cs.pendingMu.RLock()
	_, registered := cs.pending[header.SeqNum]
	cs.pendingMu.RUnlock()
	if !registered {
		t.Errorf("pending[%d] not registered while SendAndWait in flight", header.SeqNum)
	}

	// 模拟终端回送 0x0001 通用应答：RespSeqNum=下行序号，RespResult=0(成功/已确认)
	cs.HandleGeneralResp(&jt808.TerminalGeneralRespMessage{
		RespSeqNum: header.SeqNum,
		RespMsgID:  jt808.MsgIDTextSend,
		RespResult: 0,
	})

	// SendAndWait 应在收到应答后立即返回，无需等待 30s 超时
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("SendAndWait returned error: %v", r.err)
		}
		if r.res != 0 {
			t.Errorf("result=0x%02x, want 0x00", r.res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendAndWait did not return within 3s after response received")
	}

	// 应答处理后 pending 表应已清理该序号
	cs.pendingMu.RLock()
	_, stillRegistered := cs.pending[header.SeqNum]
	cs.pendingMu.RUnlock()
	if stillRegistered {
		t.Errorf("pending[%d] should be cleared after response", header.SeqNum)
	}
}
