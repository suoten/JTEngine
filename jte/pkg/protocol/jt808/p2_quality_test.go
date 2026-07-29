package jt808

// ===================================================================
// FIXED-2026-07-23 [P2]: jt808 代码质量修复测试
// P2-1: FireAreaAlarmMessage 坐标类型修正
// P2-3: splitNullFields 字段数量上限
// ===================================================================

import (
	"testing"
)

// TestP2_FireAreaAlarmMessage_RoundTrip 验证坐标 float64 往返正确
func TestP2_FireAreaAlarmMessage_RoundTrip(t *testing.T) {
	orig := &FireAreaAlarmMessage{
		AreaType: 0x01,
		AreaID:   12345,
		Dir:      0x02,
		Lat:      39.9042,
		Lng:      116.4074,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 14 {
		t.Fatalf("encoded length: got %d, want 14", len(data))
	}

	parsed := &FireAreaAlarmMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AreaType != orig.AreaType {
		t.Errorf("AreaType: got 0x%02X, want 0x%02X", parsed.AreaType, orig.AreaType)
	}
	if parsed.AreaID != orig.AreaID {
		t.Errorf("AreaID: got %d, want %d", parsed.AreaID, orig.AreaID)
	}
	if parsed.Dir != orig.Dir {
		t.Errorf("Dir: got 0x%02X, want 0x%02X", parsed.Dir, orig.Dir)
	}
	// float64 比较需容忍精度误差
	if abs(parsed.Lat-orig.Lat) > 0.0001 {
		t.Errorf("Lat: got %.6f, want %.6f", parsed.Lat, orig.Lat)
	}
	if abs(parsed.Lng-orig.Lng) > 0.0001 {
		t.Errorf("Lng: got %.6f, want %.6f", parsed.Lng, orig.Lng)
	}
}

// TestP2_FireAreaAlarmMessage_RangeValidation 验证坐标范围校验
func TestP2_FireAreaAlarmMessage_RangeValidation(t *testing.T) {
	// 纬度超 90
	msg := &FireAreaAlarmMessage{
		Lat: 95.0,
		Lng: 116.0,
	}
	_, err := msg.Marshal()
	if err == nil {
		t.Error("Marshal should fail for latitude > 90")
	}

	// 经度超 180
	msg2 := &FireAreaAlarmMessage{
		Lat: 39.0,
		Lng: 200.0,
	}
	_, err = msg2.Marshal()
	if err == nil {
		t.Error("Marshal should fail for longitude > 180")
	}
}

// TestP2_FireAreaAlarmMessage_NegativeCoords 验证负坐标（南纬/西经）
func TestP2_FireAreaAlarmMessage_NegativeCoords(t *testing.T) {
	orig := &FireAreaAlarmMessage{
		AreaType: 0x01,
		AreaID:   1,
		Dir:      0x00,
		Lat:      -33.8688, // 南纬
		Lng:      -151.2093, // 西经
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal with negative coords: %v", err)
	}

	parsed := &FireAreaAlarmMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// 注意：JT/T 808 坐标为 uint32，负坐标取绝对值编码，解出来是正值
	if abs(parsed.Lat-abs(orig.Lat)) > 0.0001 {
		t.Errorf("Lat: got %.6f, want %.6f (abs of orig)", parsed.Lat, abs(orig.Lat))
	}
	if abs(parsed.Lng-abs(orig.Lng)) > 0.0001 {
		t.Errorf("Lng: got %.6f, want %.6f (abs of orig)", parsed.Lng, abs(orig.Lng))
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestP2_splitNullFields_MaxFields 验证字段数量上限 20
func TestP2_splitNullFields_MaxFields(t *testing.T) {
	// 构造 25 个空字节分隔的字段
	data := make([]byte, 0, 50)
	for i := 0; i < 25; i++ {
		data = append(data, byte('A'+i%26))
		data = append(data, 0x00)
	}
	fields := splitNullFields(data)
	if len(fields) > 20 {
		t.Errorf("fields count should be <= 20, got %d", len(fields))
	}
	if len(fields) != 20 {
		t.Errorf("fields count should be exactly 20 (capped), got %d", len(fields))
	}
}

// TestP2_splitNullFields_Normal 验证正常分割不受影响
func TestP2_splitNullFields_Normal(t *testing.T) {
	data := []byte("hello\x00world\x00test")
	fields := splitNullFields(data)
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}
	if fields[0] != "hello" {
		t.Errorf("field 0: got %q, want %q", fields[0], "hello")
	}
	if fields[1] != "world" {
		t.Errorf("field 1: got %q, want %q", fields[1], "world")
	}
	if fields[2] != "test" {
		t.Errorf("field 2: got %q, want %q", fields[2], "test")
	}
}

// TestP2_TerminalPropRespMessage_TooManyFields 验证字段数超过 8 返回 error
func TestP2_TerminalPropRespMessage_TooManyFields(t *testing.T) {
	// 构造超过 8 个字段的恶意数据
	data := []byte{0x01} // PropType
	for i := 0; i < 10; i++ {
		data = append(data, byte('A'+i))
		data = append(data, 0x00)
	}

	msg := &TerminalPropRespMessage{}
	err := msg.Unmarshal(data)
	if err == nil {
		t.Error("Unmarshal should return error for > 8 fields")
	}
}

// TestP2_TerminalPropRespMessage_Normal 验证正常数据不受影响
func TestP2_TerminalPropRespMessage_Normal(t *testing.T) {
	orig := &TerminalPropRespMessage{
		PropType:     0x01,
		Manufacturer: "MFR",
		Model:        "MDL",
		ID:           "ID001",
		ICCID:        "8986001",
		HardwareVer:  "HW1.0",
		FirmwareVer:  "FW2.0",
		GNSSProp:     0x01,
		CommProp:     0x02,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed := &TerminalPropRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Manufacturer != orig.Manufacturer {
		t.Errorf("Manufacturer: got %q, want %q", parsed.Manufacturer, orig.Manufacturer)
	}
	if parsed.Model != orig.Model {
		t.Errorf("Model: got %q, want %q", parsed.Model, orig.Model)
	}
	if parsed.GNSSProp != orig.GNSSProp {
		t.Errorf("GNSSProp: got 0x%02X, want 0x%02X", parsed.GNSSProp, orig.GNSSProp)
	}
}
