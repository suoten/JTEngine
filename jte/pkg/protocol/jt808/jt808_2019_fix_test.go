package jt808

import (
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/protocol"
)

// ============================================================================
// JT/T 808-2019 修复项单元测试 — jt808 包内编解码验证
// ============================================================================

// ---------------------------------------------------------------------------
// 修复项1: RegisterMessage 0x0100 车牌号字段 (PlateNumber)
// ---------------------------------------------------------------------------

func TestFix_RegisterMessage_WithPlateNumber(t *testing.T) {
	reg := &RegisterMessage{
		ProvinceID:    11,
		CityID:        1101,
		Manufacturer:  "TEST",
		TerminalModel: "MODEL-X",
		TerminalID:    "TID001",
		PlateColor:    1, // 蓝色车牌
		PlateNumber:   "京A12345",
	}
	data, err := reg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) < 37 {
		t.Fatalf("data too short: %d", len(data))
	}

	parsed := &RegisterMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.ProvinceID != reg.ProvinceID {
		t.Errorf("ProvinceID: got %d, want %d", parsed.ProvinceID, reg.ProvinceID)
	}
	if parsed.CityID != reg.CityID {
		t.Errorf("CityID: got %d, want %d", parsed.CityID, reg.CityID)
	}
	if parsed.Manufacturer != reg.Manufacturer {
		t.Errorf("Manufacturer: got %q, want %q", parsed.Manufacturer, reg.Manufacturer)
	}
	if parsed.TerminalModel != reg.TerminalModel {
		t.Errorf("TerminalModel: got %q, want %q", parsed.TerminalModel, reg.TerminalModel)
	}
	if parsed.TerminalID != reg.TerminalID {
		t.Errorf("TerminalID: got %q, want %q", parsed.TerminalID, reg.TerminalID)
	}
	if parsed.PlateColor != reg.PlateColor {
		t.Errorf("PlateColor: got %d, want %d", parsed.PlateColor, reg.PlateColor)
	}
	if parsed.PlateNumber != reg.PlateNumber {
		t.Errorf("PlateNumber: got %q, want %q", parsed.PlateNumber, reg.PlateNumber)
	}
}

func TestFix_RegisterMessage_WithoutPlateNumber(t *testing.T) {
	reg := &RegisterMessage{
		ProvinceID:    11,
		CityID:        0,
		Manufacturer:  "TEST",
		TerminalModel: "MODEL-X",
		TerminalID:    "TID002",
		PlateColor:    0,
	}
	data, err := reg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) != 37 {
		t.Fatalf("data length: got %d, want 37", len(data))
	}

	parsed := &RegisterMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.PlateColor != 0 {
		t.Errorf("PlateColor: got %d, want 0", parsed.PlateColor)
	}
	if parsed.PlateNumber != "" {
		t.Errorf("PlateNumber: got %q, want empty", parsed.PlateNumber)
	}
}

func TestFix_RegisterMessage_BackwardCompat36Bytes(t *testing.T) {
	// 验证旧终端仅发送 36 字节（无 PlateColor）的兼容性
	data := make([]byte, 36)
	data[0] = 0x00
	data[1] = 0x0B
	data[2] = 0x04
	data[3] = 0x4D
	copy(data[4:9], "TEST\x00")
	copy(data[9:29], "MODEL-X\x00")
	copy(data[29:36], "TID003\x00")

	parsed := &RegisterMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal 36 bytes failed: %v", err)
	}
	if parsed.ProvinceID != 11 {
		t.Errorf("ProvinceID: got %d, want 11", parsed.ProvinceID)
	}
	if parsed.PlateColor != 0 {
		t.Errorf("PlateColor should be 0 for 36-byte data")
	}
}

// ---------------------------------------------------------------------------
// 分包重组器验证
// ---------------------------------------------------------------------------

func TestFix_PacketReassembler_BasicReassembly(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)

	header1 := makeTestHeader(0x0704, "13800138000", 1, true, 2, 0)
	header2 := makeTestHeader(0x0704, "13800138000", 1, true, 2, 1)

	_, ready, err := r.Feed(header1, []byte{0x01, 0x02})
	if err != nil {
		t.Fatalf("Feed 1 failed: %v", err)
	}
	if ready {
		t.Error("should not be ready after first fragment")
	}

	complete, ready, err := r.Feed(header2, []byte{0x03, 0x04})
	if err != nil {
		t.Fatalf("Feed 2 failed: %v", err)
	}
	if !ready {
		t.Error("should be ready after all fragments")
	}
	if len(complete) != 4 {
		t.Errorf("complete length: got %d, want 4", len(complete))
	}
	if complete[0] != 0x01 || complete[1] != 0x02 || complete[2] != 0x03 || complete[3] != 0x04 {
		t.Errorf("complete data mismatch: %v", complete)
	}
}

func TestFix_PacketReassembler_OutOfOrder(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)

	h2 := makeTestHeader(0x0704, "13800138001", 2, true, 2, 1)
	h1 := makeTestHeader(0x0704, "13800138001", 2, true, 2, 0)

	_, ready, _ := r.Feed(h2, []byte{0xCC})
	if ready {
		t.Error("should not be ready after out-of-order first feed")
	}

	complete, ready, _ := r.Feed(h1, []byte{0xAA})
	if !ready {
		t.Error("should be ready after all fragments (out of order)")
	}
	if complete[0] != 0xAA || complete[1] != 0xCC {
		t.Errorf("complete data out of order: %v", complete)
	}
}

func TestFix_PacketReassembler_DuplicateFragment(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	h1 := makeTestHeader(0x0704, "13800138002", 3, true, 2, 0)

	r.Feed(h1, []byte{0x01})
	_, ready, _ := r.Feed(h1, []byte{0x01})
	if ready {
		t.Error("duplicate fragment should not trigger ready")
	}
}

func TestFix_PacketReassembler_CleanupExpired(t *testing.T) {
	r := NewPacketReassembler(1 * time.Millisecond)
	h := makeTestHeader(0x0704, "13800138003", 4, true, 3, 0)
	r.Feed(h, []byte{0x01})

	time.Sleep(50 * time.Millisecond)
	removed := r.Cleanup()
	if removed == 0 {
		t.Error("should have removed expired groups")
	}
	if r.PendingCount() != 0 {
		t.Errorf("PendingCount after cleanup: got %d, want 0", r.PendingCount())
	}
}

func TestFix_PacketReassembler_NonFragment(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	h := makeTestHeader(0x0200, "13800138004", 5, false, 0, 0)
	body := []byte{0x01, 0x02, 0x03}
	complete, ready, err := r.Feed(h, body)
	if err != nil {
		t.Fatalf("Feed non-fragment failed: %v", err)
	}
	if !ready {
		t.Error("non-fragment should be ready immediately")
	}
	if len(complete) != 3 {
		t.Errorf("complete length: got %d, want 3", len(complete))
	}
}

// ---------------------------------------------------------------------------
// 关键消息编解码验证（Marshal → Unmarshal 往返测试）
// ---------------------------------------------------------------------------

func TestFix_GeneralResponse_RoundTrip(t *testing.T) {
	orig := &GeneralResponse{
		RespSeqNum: 1234,
		RespMsgID:  0x0200,
		Result:     0x00,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("data length: got %d, want 5", len(data))
	}
	parsed := &GeneralResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != orig.RespSeqNum {
		t.Errorf("RespSeqNum: got %d, want %d", parsed.RespSeqNum, orig.RespSeqNum)
	}
	if parsed.RespMsgID != orig.RespMsgID {
		t.Errorf("RespMsgID: got 0x%04X, want 0x%04X", parsed.RespMsgID, orig.RespMsgID)
	}
	if parsed.Result != orig.Result {
		t.Errorf("Result: got %d, want %d", parsed.Result, orig.Result)
	}
}

func TestFix_RegisterResponse_RoundTrip(t *testing.T) {
	orig := &RegisterResponse{
		RespSeqNum: 5678,
		Result:     0x00,
		AuthCode:   "test_auth_code_12345",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &RegisterResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != orig.RespSeqNum {
		t.Errorf("RespSeqNum: got %d, want %d", parsed.RespSeqNum, orig.RespSeqNum)
	}
	if parsed.Result != orig.Result {
		t.Errorf("Result: got %d, want %d", parsed.Result, orig.Result)
	}
	if parsed.AuthCode != orig.AuthCode {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, orig.AuthCode)
	}
}

func TestFix_LocationMessage_RoundTrip(t *testing.T) {
	orig := &LocationMessage{
		AlarmFlag:  0x00000001,
		StatusFlag: 0x00000002,
		Latitude:   39.9042,
		Longitude:  116.4074,
		Altitude:   5000,
		Speed:      600,
		Direction:  180,
		Time:       "240703120000",
		Mileage:    123456,
		Fuel:       5000,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &LocationMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Latitude < 39.9041 || parsed.Latitude > 39.9043 {
		t.Errorf("Latitude: got %f, want ~39.9042", parsed.Latitude)
	}
	if parsed.Longitude < 116.4073 || parsed.Longitude > 116.4075 {
		t.Errorf("Longitude: got %f, want ~116.4074", parsed.Longitude)
	}
	if parsed.Mileage != orig.Mileage {
		t.Errorf("Mileage: got %d, want %d", parsed.Mileage, orig.Mileage)
	}
	if parsed.Fuel != orig.Fuel {
		t.Errorf("Fuel: got %d, want %d", parsed.Fuel, orig.Fuel)
	}
	if parsed.Time != orig.Time {
		t.Errorf("Time: got %q, want %q", parsed.Time, orig.Time)
	}
}

func TestFix_TerminalCancelResponse_RoundTrip(t *testing.T) {
	orig := &TerminalCancelResponse{Result: 0x00}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("data length: got %d, want 1", len(data))
	}
	parsed := &TerminalCancelResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Result != orig.Result {
		t.Errorf("Result: got %d, want %d", parsed.Result, orig.Result)
	}
}

func TestFix_VehicleControlMessage_RoundTrip(t *testing.T) {
	orig := &VehicleControlMessage{ControlType: 0x03}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("data length: got %d, want 1", len(data))
	}
	parsed := &VehicleControlMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.ControlType != orig.ControlType {
		t.Errorf("ControlType: got 0x%02X, want 0x%02X", parsed.ControlType, orig.ControlType)
	}
}

func TestFix_CommandMessage_RoundTrip(t *testing.T) {
	orig := &CommandMessage{
		Params: map[uint32][]byte{
			0x0001: {0x3C, 0x00},
			0x0010: {0x01},
			0x0011: {0x01},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &CommandMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Params) != 3 {
		t.Fatalf("Params count: got %d, want 3", len(parsed.Params))
	}
	heartbeat, ok := parsed.Params[0x0001]
	if !ok {
		t.Fatal("param 0x0001 not found")
	}
	if heartbeat[0] != 0x3C || heartbeat[1] != 0x00 {
		t.Errorf("heartbeat param: got %v, want [0x3C 0x00]", heartbeat)
	}
}

func TestFix_AuthMessage_RoundTrip(t *testing.T) {
	orig := &AuthMessage{AuthCode: "my_auth_code_xyz"}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &AuthMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AuthCode != orig.AuthCode {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, orig.AuthCode)
	}
}

func TestFix_HeartbeatMessage_Empty(t *testing.T) {
	orig := &HeartbeatMessage{}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("Heartbeat body should be empty, got %d bytes", len(data))
	}
	parsed := &HeartbeatMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}

func TestFix_LocationBatchMessage_RoundTrip(t *testing.T) {
	loc1 := &LocationMessage{
		Latitude:  30.0,
		Longitude: 120.0,
		Altitude:  100,
		Speed:     500,
		Direction: 90,
		Time:      "240703120000",
	}
	loc2 := &LocationMessage{
		Latitude:  30.1,
		Longitude: 120.1,
		Altitude:  110,
		Speed:     550,
		Direction: 95,
		Time:      "240703120010",
	}
	orig := &LocationBatchMessage{
		LocationType: 0,
		Count:        2,
		Locations:    []*LocationMessage{loc1, loc2},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &LocationBatchMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.LocationType != orig.LocationType {
		t.Errorf("LocationType: got %d, want %d", parsed.LocationType, orig.LocationType)
	}
	if parsed.Count != orig.Count {
		t.Errorf("Count: got %d, want %d", parsed.Count, orig.Count)
	}
	if len(parsed.Locations) != 2 {
		t.Fatalf("Locations count: got %d, want 2", len(parsed.Locations))
	}
	if parsed.Locations[0].Latitude < 29.99 || parsed.Locations[0].Latitude > 30.01 {
		t.Errorf("Loc1 Latitude: got %f, want ~30.0", parsed.Locations[0].Latitude)
	}
}

// ---------------------------------------------------------------------------
// 校验码验证
// ---------------------------------------------------------------------------

func TestFix_Checksum_CalcAndVerify(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	checksum := CalcChecksum(data)

	full := append(data, checksum)
	codec := &JT808Codec{}
	if !codec.VerifyChecksum(full) {
		t.Error("VerifyChecksum should return true for correct checksum")
	}

	wrong := append(data, checksum^0xFF)
	if codec.VerifyChecksum(wrong) {
		t.Error("VerifyChecksum should return false for wrong checksum")
	}
}

func TestFix_Checksum_SingleByte(t *testing.T) {
	data := []byte{0xAB}
	checksum := CalcChecksum(data)
	if checksum != 0xAB {
		t.Errorf("checksum of single byte 0xAB: got 0x%02X, want 0xAB", checksum)
	}
}

func TestFix_Checksum_EmptyData(t *testing.T) {
	data := []byte{}
	checksum := CalcChecksum(data)
	if checksum != 0x00 {
		t.Errorf("checksum of empty data: got 0x%02X, want 0x00", checksum)
	}
}

// ---------------------------------------------------------------------------
// BCD 编码验证
// ---------------------------------------------------------------------------

func TestFix_BCDToString_NoLeadingZeroLoss(t *testing.T) {
	bcd := []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00}
	result := BCDToString(bcd)
	if result != "013800000000" {
		t.Errorf("BCDToString: got %q, want %q", result, "013800000000")
	}
}

func TestFix_BCDToStringFixed_TimeFormat(t *testing.T) {
	bcd := []byte{0x24, 0x07, 0x03, 0x12, 0x00, 0x00}
	result := BCDToStringFixed(bcd)
	if result != "240703120000" {
		t.Errorf("BCDToStringFixed: got %q, want %q", result, "240703120000")
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func makeTestHeader(msgID uint16, phone string, seq uint16, hasPack bool, total, index uint16) *protocol.MessageHeader {
	return &protocol.MessageHeader{
		MsgID:     msgID,
		Phone:     phone,
		SeqNum:    seq,
		HasPack:   hasPack,
		PackTotal: total,
		PackIndex: index,
	}
}
