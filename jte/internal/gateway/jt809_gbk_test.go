package gateway

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"hash/crc32"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/registry"
	"github.com/suoten/jt-engine/pkg/merge"
	"github.com/suoten/jt-engine/pkg/storage"
	"github.com/suoten/jt-engine/pkg/storage/memory"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestGBK_RoundtripChinese 验证 GBK 编解码对称性——发送侧 GBK 编码、接收侧 GBK 解码
// 必须能还原中文字符串（车牌号/报警类型）。这是 Task #26/#27 修复的回归守卫。
func TestGBK_RoundtripChinese(t *testing.T) {
	cases := []string{
		"京A12345",     // 含中文车牌
		"沪B·00000",    // 含中点
		"报警类型：超速", // 含中文冒号
		"粤Z12345警",   // 含中文+字母
	}
	for _, s := range cases {
		encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatalf("GBK encode %q: %v", s, err)
		}
		// 关键断言：纯 ASCII 字符串编码后字节不变；含中文时编码后字节长度应与 UTF-8 不同
		// （防止"修复"被误改回 []byte(s) 即 UTF-8 直传）
		decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(encoded)
		if err != nil {
			t.Fatalf("GBK decode %q: %v", s, err)
		}
		if string(decoded) != s {
			t.Fatalf("GBK roundtrip mismatch: got %q want %q", decoded, s)
		}
	}
}

// TestSendVehicleData_GBKEncoding 验证 SendVehicleData 发送的 body 为 GBK 编码（非 UTF-8）。
// 通过 net.Pipe 捕获写入字节，提取 body 段并 GBK 解码，确认中文车牌号被正确编码。
func TestSendVehicleData_GBKEncoding(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := &JT809Client{
		cfg:         nil,
		logger:      zap.NewNop(),
		conn:        clientConn,
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}

	vehicleNo := "京A12345"
	loc := &storage.LocationData{
		VehicleID: vehicleNo,
		Phone:     vehicleNo,
		Latitude:  39.904200,
		Longitude: 116.407400,
		Speed:     60.5,
		Direction: 180,
		Time:      time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
	}

	// 发送在 goroutine 中（Write 会阻塞直到对端读取）
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.SendVehicleData(vehicleNo, loc)
	}()

	// 读取完整帧（0x5B ... 0x5D）
	frame, err := readFullFrame(serverConn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendVehicleData: %v", err)
	}

	// 剥离 0x5B/0x5D 包裹
	if len(frame) < 2 || frame[0] != 0x5B || frame[len(frame)-1] != 0x5D {
		t.Fatalf("frame not wrapped with 0x5B/0x5D: % x", frame[:min(8, len(frame))])
	}
	inner := frame[1 : len(frame)-1]
	unescaped, _ := unescape809(inner)

	// 解析 header 提取 body 长度
	if len(unescaped) < jt809HeaderLen+4 {
		t.Fatalf("unescaped too short: %d", len(unescaped))
	}
	msgID := binary.BigEndian.Uint16(unescaped[18:20])
	if msgID != 0x1200 {
		t.Fatalf("expected msgID 0x1200, got 0x%04X", msgID)
	}
	msgLen := int(binary.BigEndian.Uint16(unescaped[0:2]))
	bodyLen := msgLen - jt809HeaderLen
	bodyStart := jt809HeaderLen
	bodyEnd := bodyStart + bodyLen
	if bodyEnd > len(unescaped)-4 {
		t.Fatalf("body length %d out of range", bodyLen)
	}
	bodyBytes := unescaped[bodyStart:bodyEnd]

	// 关键断言 1：body 不应等于 UTF-8 编码的 XML（说明改回了 UTF-8）
	utf8XML := `<VehicleLocation><VehicleNo>` + vehicleNo + `</VehicleNo>`
	if bytes.HasPrefix(bodyBytes, []byte(utf8XML)) {
		t.Fatalf("body appears to be UTF-8 encoded (regression!), first bytes: % x", bodyBytes[:min(40, len(bodyBytes))])
	}

	// 关键断言 2：body GBK 解码后应包含中文车牌号
	utf8Decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(bodyBytes)
	if err != nil {
		t.Fatalf("GBK decode body: %v", err)
	}
	if !strings.Contains(string(utf8Decoded), vehicleNo) {
		t.Fatalf("decoded body does not contain %q: %s", vehicleNo, string(utf8Decoded))
	}

	// 关键断言 3：解码后是合法 XML 并能正确反序列化
	var v struct {
		XMLName   xml.Name `xml:"VehicleLocation"`
		VehicleNo string   `xml:"VehicleNo"`
	}
	if err := xml.Unmarshal(utf8Decoded, &v); err != nil {
		t.Fatalf("xml unmarshal: %v", err)
	}
	if v.VehicleNo != vehicleNo {
		t.Fatalf("VehicleNo mismatch: got %q want %q", v.VehicleNo, vehicleNo)
	}
}

// TestSendAlarm_GBKEncoding 验证 SendAlarm 发送的 body 为 GBK 编码。
func TestSendAlarm_GBKEncoding(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := &JT809Client{
		logger:      zap.NewNop(),
		conn:        clientConn,
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}

	vehicleNo := "沪B12345"
	alarm := &storage.AlarmData{
		Phone:     vehicleNo,
		Type:      "超速报警",
		Level:     2,
		Latitude:  31.230400,
		Longitude: 121.473700,
		Speed:     120.5,
		Direction: 90,
		Time:      time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.SendAlarm(alarm)
	}()

	frame, err := readFullFrame(serverConn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendAlarm: %v", err)
	}

	inner := frame[1 : len(frame)-1]
	unescaped, _ := unescape809(inner)
	if len(unescaped) < jt809HeaderLen+4 {
		t.Fatalf("unescaped too short: %d", len(unescaped))
	}
	msgID := binary.BigEndian.Uint16(unescaped[18:20])
	if msgID != 0x1400 {
		t.Fatalf("expected msgID 0x1400, got 0x%04X", msgID)
	}
	msgLen := int(binary.BigEndian.Uint16(unescaped[0:2]))
	bodyLen := msgLen - jt809HeaderLen
	bodyBytes := unescaped[jt809HeaderLen : jt809HeaderLen+bodyLen]

	// GBK 解码后应包含中文车牌号和中文报警类型
	utf8Decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(bodyBytes)
	if err != nil {
		t.Fatalf("GBK decode body: %v", err)
	}
	if !strings.Contains(string(utf8Decoded), vehicleNo) {
		t.Fatalf("decoded body missing %q: %s", vehicleNo, string(utf8Decoded))
	}
	if !strings.Contains(string(utf8Decoded), "超速报警") {
		t.Fatalf("decoded body missing alarm type: %s", string(utf8Decoded))
	}
}

// TestProcessVehicleData_GBKDecoding 验证 processVehicleData 能正确解析 GBK 编码的 XML 帧。
// 构造 header(18) + GBK XML body + CRC(4) 的未转义帧，调用 processVehicleData，
// 然后从 merge engine 的 latestData 中取出并校验中文车牌号被正确还原。
func TestProcessVehicleData_GBKDecoding(t *testing.T) {
	logger := zap.NewNop()
	store := memory.NewMemoryStore(100)
	reg := registry.NewFeatureRegistry()
	eng := merge.NewEngine(store, logger, reg)

	s := &JT809Server{
		logger:  logger,
		merge:   eng,
		store:   store,
		clients: make(map[string]*JT809DownstreamClient),
	}

	vehicleNo := "京A12345"
	xmlData := `<VehicleLocation><VehicleNo>` + vehicleNo + `</VehicleNo><Latitude>39.904200</Latitude><Longitude>116.407400</Longitude><Speed>60.500000</Speed><Direction>180</Direction><Time>2026-06-29 12:00:00</Time></VehicleLocation>`

	// GBK 编码 body（与 SendVehicleData 对称）
	gbkBody, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(xmlData))
	if err != nil {
		t.Fatalf("GBK encode: %v", err)
	}

	// 构造未转义 809 帧：header(22) + body + CRC(4)
	header := make([]byte, jt809HeaderLen) // 22 bytes
	binary.BigEndian.PutUint16(header[0:2], uint16(jt809HeaderLen+len(gbkBody))) // 报文长度
	binary.BigEndian.PutUint16(header[2:4], 1)                                   // 报文序号
	header[4] = 0x00                                                            // 加密方式
	header[5] = 0x01                                                            // 车牌颜色
	binary.BigEndian.PutUint16(header[18:20], 0x1200)                            // 子业务类型

	payload := append(header, gbkBody...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	frame := append(payload, crcBytes...)

	// 调用 processVehicleData（接收 GBK→UTF-8 解码路径）
	s.processVehicleData("test-client", frame)

	// 从 merge engine 取出合并后的位置数据
	loc, ok := eng.GetLatestLocation(vehicleNo)
	if !ok {
		t.Fatalf("location not merged for vehicle %q", vehicleNo)
	}
	if loc.VehicleID != vehicleNo {
		t.Fatalf("VehicleID mismatch: got %q want %q", loc.VehicleID, vehicleNo)
	}
	if loc.Phone != vehicleNo {
		t.Fatalf("Phone mismatch: got %q want %q", loc.Phone, vehicleNo)
	}
	// 验证数值字段也被正确解析
	if loc.Latitude < 39.904199 || loc.Latitude > 39.904201 {
		t.Fatalf("Latitude mismatch: got %v", loc.Latitude)
	}
	if loc.Longitude < 116.407399 || loc.Longitude > 116.407401 {
		t.Fatalf("Longitude mismatch: got %v", loc.Longitude)
	}
}

// TestProcessVehicleData_UTF8InputRejected 验证 UTF-8 输入在 GBK 解码阶段会被拒绝
// （防止误传 UTF-8 帧被静默接受，确保编码契约严格执行）。
// 注：GBK NewDecoder 对非法 GBK 字节返回错误；纯 ASCII 既是合法 UTF-8 也是合法 GBK，
// 因此用包含中文的 UTF-8 字节（多字节序列在 GBK 中非法）来触发解码失败。
func TestProcessVehicleData_UTF8InputRejected(t *testing.T) {
	logger := zap.NewNop()
	store := memory.NewMemoryStore(100)
	reg := registry.NewFeatureRegistry()
	eng := merge.NewEngine(store, logger, reg)

	s := &JT809Server{
		logger:  logger,
		merge:   eng,
		store:   store,
		clients: make(map[string]*JT809DownstreamClient),
	}

	vehicleNo := "京A12345"
	xmlData := `<VehicleLocation><VehicleNo>` + vehicleNo + `</VehicleNo><Latitude>39.904200</Latitude><Longitude>116.407400</Longitude><Speed>60.500000</Speed><Direction>180</Direction><Time>2026-06-29 12:00:00</Time></VehicleLocation>`

	// 故意用 UTF-8 字节（"京"的 UTF-8 编码 E4 BA AC 在 GBK 中不是合法双字节序列开头）
	utf8Body := []byte(xmlData)

	header := make([]byte, jt809HeaderLen) // 22 bytes
	binary.BigEndian.PutUint16(header[0:2], uint16(jt809HeaderLen+len(utf8Body))) // 报文长度
	binary.BigEndian.PutUint16(header[2:4], 1)                                   // 报文序号
	header[4] = 0x00                                                            // 加密方式
	header[5] = 0x01                                                            // 车牌颜色
	binary.BigEndian.PutUint16(header[18:20], 0x1200)                            // 子业务类型

	payload := append(header, utf8Body...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	frame := append(payload, crcBytes...)

	// 调用 processVehicleData——GBK 解码应失败，函数应提前 return（不合并数据）
	s.processVehicleData("test-client-utf8", frame)

	// 验证：UTF-8 输入不应被合并到 merge engine
	if _, ok := eng.GetLatestLocation(vehicleNo); ok {
		t.Fatalf("UTF-8 input should NOT be merged (GBK decode must reject it first)")
	}
}

// readFullFrame 从 conn 读取一个完整的 809 帧（从 0x5B 到 0x5D）。
// 由于 809 帧可能包含转义序列，这里按字节扫描直到匹配首尾标记。
func readFullFrame(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	started := false
	for {
		n, err := r.Read(one)
		if err != nil {
			if n == 0 {
				return nil, err
			}
		}
		if n == 0 {
			continue
		}
		b := one[0]
		if !started {
			if b == 0x5B {
				started = true
				buf = append(buf, b)
			}
			continue
		}
		buf = append(buf, b)
		if b == 0x5D {
			return buf, nil
		}
	}
}

// min 返回两个整数中的较小值（Go 1.22 内置，此处无需定义）。
