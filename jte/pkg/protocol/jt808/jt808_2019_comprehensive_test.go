package jt808

import (
	"testing"
	"time"
)

// ============================================================================
// JT/T 808-2019 全消息编解码完整性测试
// AUTO-FIX-2026-07-04: 逐消息验证 Marshal→Unmarshal 往返一致性
// ============================================================================
// NOTE: makeTestHeader 定义在 jt808_2019_fix_test.go 中，同一 package 共享。

// ---------------------------------------------------------------------------
// 注册与鉴权
// ---------------------------------------------------------------------------

func TestComprehensive_RegisterMessage_FullFields(t *testing.T) {
	orig := &RegisterMessage{
		ProvinceID:    11,
		CityID:        1101,
		Manufacturer:  "HUAWE", // 808-2019: 厂商标识为 5 字节定长
		TerminalModel: "GT-500",
		TerminalID:    "SN12345",
		PlateColor:    2,
		PlateNumber:   "京B88888",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &RegisterMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.ProvinceID != 11 || parsed.CityID != 1101 {
		t.Errorf("ProvinceID/CityID mismatch")
	}
	if parsed.Manufacturer != "HUAWE" || parsed.TerminalModel != "GT-500" || parsed.TerminalID != "SN12345" {
		t.Errorf("Terminal info mismatch: %v/%v/%v", parsed.Manufacturer, parsed.TerminalModel, parsed.TerminalID)
	}
	if parsed.PlateColor != 2 || parsed.PlateNumber != "京B88888" {
		t.Errorf("Plate mismatch: color=%d number=%q", parsed.PlateColor, parsed.PlateNumber)
	}
}

func TestComprehensive_AuthMessage_WithIMEI(t *testing.T) {
	// 808-2019 0x0102: 标准体仅鉴权码；IMEI 为 15 字节可选扩展。
	// SoftwareVersion 无法在无长度前缀的情况下可靠反向解析，故仅测试 AuthCode + IMEI 往返。
	// FIXED-2026-07-22 [P0]: 启发式 IMEI 检测默认关闭，需显式启用。
	orig := &AuthMessage{
		AuthCode: "auth_code_12345",
		IMEI:     "123456789012345",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 启用启发式 IMEI 检测（仅此测试需要）
	SetIMEIHeuristic(true)
	defer SetIMEIHeuristic(false)
	parsed := &AuthMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AuthCode != orig.AuthCode {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, orig.AuthCode)
	}
	if parsed.IMEI != orig.IMEI {
		t.Errorf("IMEI: got %q, want %q", parsed.IMEI, orig.IMEI)
	}
}

// TestP0_AuthMessage_PureDigitAuthCode_NotTruncated 验证纯数字结尾的鉴权码不会被启发式 IMEI 检测误截断。
// FIXED-2026-07-22 [P0]: 默认关闭启发式，整个 body 作为鉴权码。
func TestP0_AuthMessage_PureDigitAuthCode_NotTruncated(t *testing.T) {
	// 鉴权码全部为纯数字，长度 > 15，末尾 15 字节全为数字
	// 启发式开启时会误截断，关闭时应完整保留
	authCode := "1234567890123456789012345" // 25 字节纯数字
	orig := &AuthMessage{AuthCode: authCode}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 默认 allowIMEIHeuristic=false
	parsed := &AuthMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AuthCode != authCode {
		t.Errorf("AuthCode 被误截断: got %q (len=%d), want %q (len=%d)",
			parsed.AuthCode, len(parsed.AuthCode), authCode, len(authCode))
	}
	if parsed.IMEI != "" {
		t.Errorf("IMEI 应为空（启发式已关闭）: got %q", parsed.IMEI)
	}
}

// TestP0_AuthMessage_HeuristicDisabledByDefault 验证启发式 IMEI 检测默认关闭。
func TestP0_AuthMessage_HeuristicDisabledByDefault(t *testing.T) {
	// 构造 body = 鉴权码(15B非数字) + IMEI(15B纯数字)
	authPart := "authcode_test_1" // 15B
	imeiPart := "123456789012345"   // 15B 纯数字
	fullBody := append([]byte(authPart), []byte(imeiPart)...)

	parsed := &AuthMessage{}
	if err := parsed.Unmarshal(fullBody); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// 默认关闭时，整个 body 作为鉴权码
	if parsed.AuthCode != string(fullBody) {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, string(fullBody))
	}
	if parsed.IMEI != "" {
		t.Errorf("IMEI 应为空（默认关闭启发式）: got %q", parsed.IMEI)
	}
}

// TestP0_AuthMessage_HeuristicEnabled 验证显式启用启发式时 IMEI 可正确剥离。
func TestP0_AuthMessage_HeuristicEnabled(t *testing.T) {
	authPart := "authcode_test_1" // 15B
	imeiPart := "123456789012345"   // 15B 纯数字
	fullBody := append([]byte(authPart), []byte(imeiPart)...)

	SetIMEIHeuristic(true)
	defer SetIMEIHeuristic(false)

	parsed := &AuthMessage{}
	if err := parsed.Unmarshal(fullBody); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AuthCode != authPart {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, authPart)
	}
	if parsed.IMEI != imeiPart {
		t.Errorf("IMEI: got %q, want %q", parsed.IMEI, imeiPart)
	}
}

// TestP0_AuthMessage_EmptyBody 验证空 body 不触发 panic。
func TestP0_AuthMessage_EmptyBody(t *testing.T) {
	parsed := &AuthMessage{}
	if err := parsed.Unmarshal([]byte{}); err != nil {
		t.Fatalf("Unmarshal empty body: %v", err)
	}
	if parsed.AuthCode != "" {
		t.Errorf("AuthCode should be empty, got %q", parsed.AuthCode)
	}
	if parsed.IMEI != "" {
		t.Errorf("IMEI should be empty, got %q", parsed.IMEI)
	}
}

// TestP0_AuthMessage_ShortDigitAuthCode 验证 <=15 字节的纯数字鉴权码不受影响。
func TestP0_AuthMessage_ShortDigitAuthCode(t *testing.T) {
	authCode := "1234567890" // 10B 纯数字
	parsed := &AuthMessage{}
	if err := parsed.Unmarshal([]byte(authCode)); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AuthCode != authCode {
		t.Errorf("AuthCode: got %q, want %q", parsed.AuthCode, authCode)
	}
	if parsed.IMEI != "" {
		t.Errorf("IMEI should be empty: got %q", parsed.IMEI)
	}
}

func TestComprehensive_TerminalCancel_EmptyBody(t *testing.T) {
	orig := &TerminalCancelMessage{}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("TerminalCancel body should be empty, got %d bytes", len(data))
	}
	parsed := &TerminalCancelMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}

func TestComprehensive_TerminalCancelResponse(t *testing.T) {
	orig := &TerminalCancelResponse{Result: 0}
	data, _ := orig.Marshal()
	parsed := &TerminalCancelResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Result != 0 {
		t.Errorf("Result: got %d, want 0", parsed.Result)
	}
}

// ---------------------------------------------------------------------------
// 位置上报与批量位置
// ---------------------------------------------------------------------------

func TestComprehensive_LocationMessage_WithExtras(t *testing.T) {
	orig := &LocationMessage{
		AlarmFlag:          0x00000001,
		StatusFlag:         0x00000002,
		Latitude:           39.9042,
		Longitude:          116.4074,
		Altitude:           5000,
		Speed:              600,
		Direction:          180,
		Time:               "240704120000",
		Mileage:            123456,
		Fuel:               5000,
		Speed2:             580,
		OverspeedAlarmState: 0x01,
		AnalogValue:        0x1234,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &LocationMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Mileage != orig.Mileage {
		t.Errorf("Mileage: got %d, want %d", parsed.Mileage, orig.Mileage)
	}
	if parsed.Fuel != orig.Fuel {
		t.Errorf("Fuel: got %d, want %d", parsed.Fuel, orig.Fuel)
	}
	if parsed.Speed2 != orig.Speed2 {
		t.Errorf("Speed2: got %d, want %d", parsed.Speed2, orig.Speed2)
	}
	if parsed.OverspeedAlarmState != orig.OverspeedAlarmState {
		t.Errorf("OverspeedAlarmState: got %d, want %d", parsed.OverspeedAlarmState, orig.OverspeedAlarmState)
	}
	if parsed.AnalogValue != orig.AnalogValue {
		t.Errorf("AnalogValue: got %d, want %d", parsed.AnalogValue, orig.AnalogValue)
	}
}

func TestComprehensive_LocationBatchMessage(t *testing.T) {
	orig := &LocationBatchMessage{
		LocationType: 1,
		Count:        2,
		Locations: []*LocationMessage{
			{Latitude: 30.0, Longitude: 120.0, Altitude: 100, Speed: 500, Direction: 90, Time: "240704120000"},
			{Latitude: 30.1, Longitude: 120.1, Altitude: 110, Speed: 550, Direction: 95, Time: "240704120010"},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &LocationBatchMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.LocationType != 1 || parsed.Count != 2 || len(parsed.Locations) != 2 {
		t.Errorf("Batch location header mismatch: type=%d count=%d locations=%d",
			parsed.LocationType, parsed.Count, len(parsed.Locations))
	}
}

// ---------------------------------------------------------------------------
// 位置查询与控制
// ---------------------------------------------------------------------------

func TestComprehensive_LocationQueryMessage_Empty(t *testing.T) {
	orig := &LocationQueryMessage{}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("LocationQuery body should be empty, got %d bytes", len(data))
	}
}

func TestComprehensive_LocationQueryResponse(t *testing.T) {
	orig := &LocationQueryResponse{
		Location: LocationMessage{
			Latitude:  39.9042,
			Longitude: 116.4074,
			Altitude:  5000,
			Speed:     600,
			Direction: 180,
			Time:      "240704120000",
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &LocationQueryResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Location.Latitude < 39.9041 || parsed.Location.Latitude > 39.9043 {
		t.Errorf("Latitude mismatch: %f", parsed.Location.Latitude)
	}
}

func TestComprehensive_TempLocationTrackMessage(t *testing.T) {
	orig := &TempLocationTrackMessage{Interval: 10, Validity: 300}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("data length: got %d, want 4", len(data))
	}
	parsed := &TempLocationTrackMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Interval != 10 || parsed.Validity != 300 {
		t.Errorf("Interval/Validity: got %d/%d, want 10/300", parsed.Interval, parsed.Validity)
	}
}

func TestComprehensive_TempLocationTrackRespMessage(t *testing.T) {
	orig := &TempLocationTrackRespMessage{Result: 0x00}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &TempLocationTrackRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Result != 0 {
		t.Errorf("Result: got %d, want 0", parsed.Result)
	}
}

// ---------------------------------------------------------------------------
// 参数设置与查询
// ---------------------------------------------------------------------------

func TestComprehensive_CommandMessage_MultipleParams(t *testing.T) {
	orig := &CommandMessage{
		Params: map[uint32][]byte{
			0x0001: {0x3C, 0x00}, // 心跳间隔
			0x0010: {0x01},       // TCP
			0x0011: {0x01},       // UDP
			0x0012: []byte("cmnet"), // APN
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
	if len(parsed.Params) != 4 {
		t.Fatalf("Params count: got %d, want 4", len(parsed.Params))
	}
	if hb, ok := parsed.Params[0x0001]; !ok || hb[0] != 0x3C {
		t.Errorf("Heartbeat param mismatch")
	}
}

func TestComprehensive_CommandRespMessage(t *testing.T) {
	orig := &CommandRespMessage{
		RespSeqNum: 100,
		RespCount:  2,
		Params: map[uint32][]byte{
			0x0001: {0x3C, 0x00},
			0x0010: {0x01},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &CommandRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != 100 {
		t.Errorf("RespSeqNum: got %d, want 100", parsed.RespSeqNum)
	}
	if len(parsed.Params) != 2 {
		t.Errorf("Params count: got %d, want 2", len(parsed.Params))
	}
}

func TestComprehensive_ParamRespMessage(t *testing.T) {
	orig := &ParamRespMessage{
		SeqNum: 200,
		Params: map[uint32][]byte{
			0x0001: {0x3C, 0x00},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &ParamRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SeqNum != 200 {
		t.Errorf("SeqNum: got %d, want 200", parsed.SeqNum)
	}
	if len(parsed.Params) != 1 {
		t.Errorf("Params count: got %d, want 1", len(parsed.Params))
	}
}

// ---------------------------------------------------------------------------
// 终端控制
// ---------------------------------------------------------------------------

func TestComprehensive_TerminalCtrlMessage(t *testing.T) {
	orig := &TerminalCtrlMessage{CtrlType: 0x03, Param: []byte{0x01, 0x02}}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &TerminalCtrlMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.CtrlType != 0x03 {
		t.Errorf("CtrlType: got %d, want 3", parsed.CtrlType)
	}
	if len(parsed.Param) != 2 || parsed.Param[0] != 0x01 || parsed.Param[1] != 0x02 {
		t.Errorf("Param mismatch: %v", parsed.Param)
	}
}

func TestComprehensive_VehicleControlMessage(t *testing.T) {
	orig := &VehicleControlMessage{ControlType: 0x01}
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
	if parsed.ControlType != 0x01 {
		t.Errorf("ControlType: got %d, want 1", parsed.ControlType)
	}
}

func TestComprehensive_TerminalUpgradeMessage(t *testing.T) {
	orig := &TerminalUpgradeMessage{
		UpgradeType:  0x01,
		Manufacturer: "HUAWEI",
		Version:      "v2.0",
		URL:          "http://192.168.1.1/upgrade.bin",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &TerminalUpgradeMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.UpgradeType != 0x01 {
		t.Errorf("UpgradeType: got %d, want 1", parsed.UpgradeType)
	}
	if parsed.Version != "v2.0" {
		t.Errorf("Version: got %q, want %q", parsed.Version, "v2.0")
	}
}

func TestComprehensive_TerminalUpgradeRespMessage(t *testing.T) {
	orig := &TerminalUpgradeRespMessage{
		UpgradeType: 0x01,
		CompileLen:  1024,
		ProvinceID:  11,
		CityID:      1101,
		Manufacturer: "HUAWEI",
		Version:     "v2.0",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &TerminalUpgradeRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.UpgradeType != 0x01 || parsed.CompileLen != 1024 {
		t.Errorf("UpgradeType/CompileLen mismatch")
	}
	if parsed.Manufacturer != "HUAWEI" || parsed.Version != "v2.0" {
		t.Errorf("Manufacturer/Version mismatch: %q/%q", parsed.Manufacturer, parsed.Version)
	}
}

func TestComprehensive_TerminalPropQueryMessage_Empty(t *testing.T) {
	orig := &TerminalPropQueryMessage{}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("TerminalPropQuery body should be empty")
	}
}

// ---------------------------------------------------------------------------
// 多媒体
// ---------------------------------------------------------------------------

func TestComprehensive_MultimediaMessage(t *testing.T) {
	orig := &MultimediaMessage{
		MultimediaID:   0x12345678,
		MultimediaType: 0x00,
		MultimediaFmt:  0x01,
		EventItem:      0x01,
		ChannelID:      0x01,
		Location: LocationMessage{
			Latitude:  39.9042,
			Longitude: 116.4074,
			Altitude:  5000,
			Speed:     600,
			Direction: 180,
			Time:      "240704120000",
		},
		MediaLen: 1024,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) < 40 {
		t.Fatalf("data too short: %d", len(data))
	}
	parsed := &MultimediaMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.MultimediaID != 0x12345678 {
		t.Errorf("MultimediaID: got 0x%08X, want 0x12345678", parsed.MultimediaID)
	}
	if parsed.MediaLen != 1024 {
		t.Errorf("MediaLen: got %d, want 1024", parsed.MediaLen)
	}
}

func TestComprehensive_MultimediaUploadMessage(t *testing.T) {
	orig := &MultimediaUploadMessage{
		MultimediaID: 0x12345678,
		PacketIndex:  1,
		PacketTotal:  3,
		MediaData:    []byte{0x01, 0x02, 0x03, 0x04, 0x05},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &MultimediaUploadMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.MultimediaID != 0x12345678 || parsed.PacketIndex != 1 || parsed.PacketTotal != 3 {
		t.Errorf("Header fields mismatch: id=0x%08X idx=%d total=%d",
			parsed.MultimediaID, parsed.PacketIndex, parsed.PacketTotal)
	}
	if len(parsed.MediaData) != 5 {
		t.Errorf("MediaData length: got %d, want 5", len(parsed.MediaData))
	}
}

func TestComprehensive_PhotoCommandMessage(t *testing.T) {
	orig := &PhotoCommandMessage{
		ChannelID:  1,
		Cmd:        0x01,
		Time:       10,
		SaveFlag:   0x01,
		Resolution: 0x05,
		Quality:    0x0A,
		Brightness: 0x40,
		Contrast:   0x40,
		Saturation: 0x40,
		Chroma:     0x40,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &PhotoCommandMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.ChannelID != 1 || parsed.Cmd != 0x01 {
		t.Errorf("ChannelID/Cmd mismatch")
	}
	if parsed.Resolution != 0x05 || parsed.Quality != 0x0A {
		t.Errorf("Resolution/Quality mismatch")
	}
}

func TestComprehensive_MultimediaUploadCmdMessage(t *testing.T) {
	orig := &MultimediaUploadCmdMessage{
		MultimediaID: 0x12345678,
		ChannelID:    1,
		MediaType:    0x00,
		StartTime:    "240704000000",
		EndTime:      "240704120000",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 18 {
		t.Fatalf("data length: got %d, want 18", len(data))
	}
	parsed := &MultimediaUploadCmdMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.MultimediaID != 0x12345678 {
		t.Errorf("MultimediaID mismatch")
	}
}

func TestComprehensive_FileUploadCmdMessage(t *testing.T) {
	orig := &FileUploadCmdMessage{
		MultimediaID: 0x12345678,
		Cmd:          0x01,
		Time:         10,
		SaveFlag:     0x01,
		AudioSample:  0x03,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 9 {
		t.Fatalf("data length: got %d, want 9", len(data))
	}
	parsed := &FileUploadCmdMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.MultimediaID != 0x12345678 || parsed.Cmd != 0x01 {
		t.Errorf("Fields mismatch")
	}
}

func TestComprehensive_AudioRecordCmdMessage(t *testing.T) {
	orig := &AudioRecordCmdMessage{
		RecordTime:  30,
		RecordCmd:   0x01,
		SaveFlag:    0x01,
		AudioSample: 0x03,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("data length: got %d, want 5", len(data))
	}
	parsed := &AudioRecordCmdMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RecordTime != 30 || parsed.RecordCmd != 0x01 {
		t.Errorf("Fields mismatch")
	}
}

// ---------------------------------------------------------------------------
// 区域设置
// ---------------------------------------------------------------------------

func TestComprehensive_CircularAreaSetMessage(t *testing.T) {
	orig := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{AreaID: 1, CenterLat: 39.9, CenterLon: 116.4, Radius: 1000, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 60},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &CircularAreaSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Areas) != 1 || parsed.Areas[0].AreaID != 1 {
		t.Errorf("Area mismatch")
	}
}

func TestComprehensive_CircularAreaDelMessage(t *testing.T) {
	orig := &CircularAreaDelMessage{AreaIDs: []uint32{1, 2, 3}}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &CircularAreaDelMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.AreaIDs) != 3 {
		t.Errorf("AreaIDs count: got %d, want 3", len(parsed.AreaIDs))
	}
}

func TestComprehensive_RectAreaSetMessage(t *testing.T) {
	orig := &RectAreaSetMessage{
		SetType: 0x01,
		Areas: []RectArea{
			{AreaID: 1, TopLat: 40.0, TopLon: 116.5, BottomLat: 39.5, BottomLon: 116.0, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 60},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &RectAreaSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Areas) != 1 {
		t.Fatalf("Areas count: got %d, want 1", len(parsed.Areas))
	}
	if parsed.Areas[0].TopLat < 39.99 || parsed.Areas[0].TopLat > 40.01 {
		t.Errorf("TopLat mismatch: %f", parsed.Areas[0].TopLat)
	}
}

func TestComprehensive_PolygonAreaSetMessage(t *testing.T) {
	orig := &PolygonAreaSetMessage{
		AreaID: 1, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 60,
		Points: []PolygonPoint{
			{Latitude: 39.9, Longitude: 116.4},
			{Latitude: 39.8, Longitude: 116.4},
			{Latitude: 39.8, Longitude: 116.3},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &PolygonAreaSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Points) != 3 {
		t.Errorf("Points count: got %d, want 3", len(parsed.Points))
	}
}

func TestComprehensive_RouteSetMessage(t *testing.T) {
	orig := &RouteSetMessage{
		RouteID: 1, RouteName: "Route1", DepartTime: 600, DrivingTime: 3600,
		Points: []RoutePoint{
			{PointID: 1, RouteID: 1, Latitude: 39.9, Longitude: 116.4, Width: 100, Attr: 0x01, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 60},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &RouteSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RouteName != "Route1" {
		t.Errorf("RouteName: got %q, want %q", parsed.RouteName, "Route1")
	}
	if len(parsed.Points) != 1 {
		t.Fatalf("Points count: got %d, want 1", len(parsed.Points))
	}
}

func TestComprehensive_FireAreaSetMessage(t *testing.T) {
	orig := &FireAreaSetMessage{
		SetType: 0x01,
		Areas: []FireArea{
			{AreaID: 1, CenterLat: 39.9, CenterLon: 116.4, Radius: 500, SpeedLimit: 40, Duration: 20, MaxSpeed: 60, NightMaxSpeed: 40},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &FireAreaSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Areas) != 1 {
		t.Fatalf("Areas count: got %d, want 1", len(parsed.Areas))
	}
}

// ---------------------------------------------------------------------------
// 信息类
// ---------------------------------------------------------------------------

func TestComprehensive_TextSendMessage(t *testing.T) {
	orig := &TextSendMessage{Sign: 0x01, Text: "Hello World"}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &TextSendMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Sign != 0x01 || parsed.Text != "Hello World" {
		t.Errorf("Sign/Text mismatch: %d/%q", parsed.Sign, parsed.Text)
	}
}

func TestComprehensive_PhoneCallbackMessage(t *testing.T) {
	orig := &PhoneCallbackMessage{CallbackType: 0x01, PhoneNumber: "13800138000"}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &PhoneCallbackMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.CallbackType != 0x01 || parsed.PhoneNumber != "13800138000" {
		t.Errorf("Fields mismatch")
	}
}

func TestComprehensive_EventSetMessage(t *testing.T) {
	orig := &EventSetMessage{
		SetType: 0x01,
		Events: []EventItem{
			{EventID: 1, Content: "超速报警"},
			{EventID: 2, Content: "疲劳驾驶"},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &EventSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Events) != 2 {
		t.Fatalf("Events count: got %d, want 2", len(parsed.Events))
	}
	if parsed.Events[0].Content != "超速报警" {
		t.Errorf("Event1 content: got %q", parsed.Events[0].Content)
	}
}

func TestComprehensive_QuestionDownMessage(t *testing.T) {
	orig := &QuestionDownMessage{
		Sign: 0x01,
		Question: "是否继续行驶？",
		Options: []QuestionOption{
			{OptionID: 1, Content: "是"},
			{OptionID: 2, Content: "否"},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &QuestionDownMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Question != "是否继续行驶？" {
		t.Errorf("Question: got %q", parsed.Question)
	}
	if len(parsed.Options) != 2 {
		t.Fatalf("Options count: got %d, want 2", len(parsed.Options))
	}
}

func TestComprehensive_EventRespMessage(t *testing.T) {
	orig := &EventRespMessage{EventID: 0x0001}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("data length: got %d, want 2", len(data))
	}
	parsed := &EventRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.EventID != 0x0001 {
		t.Errorf("EventID: got 0x%04X, want 0x0001", parsed.EventID)
	}
}

func TestComprehensive_QuestionRespMessage(t *testing.T) {
	orig := &QuestionRespMessage{RespSeqNum: 100, AnswerID: 1}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("data length: got %d, want 4", len(data))
	}
	parsed := &QuestionRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != 100 || parsed.AnswerID != 1 {
		t.Errorf("Fields mismatch: seq=%d answer=%d", parsed.RespSeqNum, parsed.AnswerID)
	}
}

// ---------------------------------------------------------------------------
// 电子运单
// ---------------------------------------------------------------------------

func TestComprehensive_ElectronicWaybillMessage(t *testing.T) {
	orig := &ElectronicWaybillMessage{
		Content: "运单编号:YD20240704001;货物:钢材;重量:30吨",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &ElectronicWaybillMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Content != orig.Content {
		t.Errorf("Content: got %q, want %q", parsed.Content, orig.Content)
	}
}

func TestComprehensive_EWaybillSetMessage(t *testing.T) {
	orig := &EWaybillSetMessage{WaybillData: []byte("test waybill data")}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &EWaybillSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(parsed.WaybillData) != "test waybill data" {
		t.Errorf("WaybillData mismatch")
	}
}

func TestComprehensive_EWaybillDelMessage(t *testing.T) {
	orig := &EWaybillDelMessage{DelType: 1, IDs: []string{"YD001", "YD002"}}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &EWaybillDelMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.DelType != 1 || len(parsed.IDs) != 2 {
		t.Errorf("Fields mismatch: type=%d ids=%d", parsed.DelType, len(parsed.IDs))
	}
}

func TestComprehensive_EWaybillRespMessage(t *testing.T) {
	orig := &EWaybillRespMessage{SeqNum: 100, Result: 0}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &EWaybillRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SeqNum != 100 || parsed.Result != 0 {
		t.Errorf("Fields mismatch")
	}
}

// ---------------------------------------------------------------------------
// 驾驶类
// ---------------------------------------------------------------------------

func TestComprehensive_DriverIDMessage(t *testing.T) {
	orig := &DriverIDMessage{Status: 0x01, Time: "240704120000", DriverID: "D001"}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &DriverIDMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Status != 0x01 || parsed.DriverID != "D001" {
		t.Errorf("Fields mismatch: status=%d driverID=%q", parsed.Status, parsed.DriverID)
	}
}

func TestComprehensive_CanDataMessage(t *testing.T) {
	orig := &CanDataMessage{
		ReceiveTime: "240704120000",
		CanCount:    2,
		CanItems: []CanItem{
			{CANID: 0x0001, Data: []byte{0x01, 0x02}},
			{CANID: 0x0002, Data: []byte{0x03, 0x04, 0x05}},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &CanDataMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.CanCount != 2 || len(parsed.CanItems) != 2 {
		t.Fatalf("CanCount/Items mismatch: %d/%d", parsed.CanCount, len(parsed.CanItems))
	}
	if parsed.CanItems[0].CANID != 0x0001 || len(parsed.CanItems[0].Data) != 2 {
		t.Errorf("CanItem0 mismatch")
	}
}

func TestComprehensive_BillOperateMessage(t *testing.T) {
	orig := &BillOperateMessage{OperateType: 0x01, OperateData: []byte{0x01, 0x02}}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &BillOperateMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.OperateType != 0x01 || len(parsed.OperateData) != 2 {
		t.Errorf("Fields mismatch")
	}
}

// ---------------------------------------------------------------------------
// 报警
// ---------------------------------------------------------------------------

func TestComprehensive_AlarmMessage(t *testing.T) {
	orig := &AlarmMessage{
		AlarmFlag:  0x00000001,
		AlarmCount: 2,
		AlarmItems: []AlarmItem{
			{SeqNum: 1, AlarmType: 0x00000001},
			{SeqNum: 2, AlarmType: 0x00000002},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &AlarmMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AlarmCount != 2 || len(parsed.AlarmItems) != 2 {
		t.Fatalf("AlarmCount/Items mismatch")
	}
	if parsed.AlarmItems[0].AlarmType != 0x00000001 {
		t.Errorf("AlarmType0 mismatch: 0x%08X", parsed.AlarmItems[0].AlarmType)
	}
}

func TestComprehensive_AlarmAckMessage(t *testing.T) {
	orig := &AlarmAckMessage{RespSeqNum: 100, AlarmType: 0x00000001, AlarmID: 1}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 8 {
		t.Fatalf("data length: got %d, want 8", len(data))
	}
	parsed := &AlarmAckMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != 100 || parsed.AlarmType != 0x00000001 || parsed.AlarmID != 1 {
		t.Errorf("Fields mismatch")
	}
}

func TestComprehensive_AlarmAttachmentMessage(t *testing.T) {
	orig := &AlarmAttachmentMessage{
		AlarmID: 0x12345678,
		Attachments: []AlarmAttachmentItem{
			{Type: 0, Size: 4, Data: []byte{0x01, 0x02, 0x03, 0x04}},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &AlarmAttachmentMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AlarmID != 0x12345678 {
		t.Errorf("AlarmID mismatch: 0x%08X", parsed.AlarmID)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("Attachments count: got %d, want 1", len(parsed.Attachments))
	}
}

func TestComprehensive_OverspeedAlarmMessage(t *testing.T) {
	orig := &OverspeedAlarmMessage{
		AlarmFlag: 0x00000001, StatusFlag: 0x00000002,
		Latitude: 39.9, Longitude: 116.4, Altitude: 5000, Speed: 1200, Direction: 180,
		Time: "240704120000", AlarmAttach: []byte{0x01},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &OverspeedAlarmMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Speed != 1200 {
		t.Errorf("Speed: got %d, want 1200", parsed.Speed)
	}
}

// ---------------------------------------------------------------------------
// 校验与转义
// ---------------------------------------------------------------------------

func TestComprehensive_Checksum_RoundTrip(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	checksum := CalcChecksum(data)
	full := append(data, checksum)
	codec := &JT808Codec{}
	if !codec.VerifyChecksum(full) {
		t.Error("VerifyChecksum failed for correct checksum")
	}
	// 翻转一位
	wrong := append([]byte{}, full...)
	wrong[len(wrong)-1] ^= 0x01
	if codec.VerifyChecksum(wrong) {
		t.Error("VerifyChecksum should fail for wrong checksum")
	}
}

func TestComprehensive_EscapeRoundTrip(t *testing.T) {
	// 包含 0x7E 和 0x7D 的数据
	original := []byte{0x01, 0x7E, 0x02, 0x7D, 0x03}
	escaped := Escape(original)
	// 0x7E → 0x7D 0x02, 0x7D → 0x7D 0x01
	// 期望: 0x01 0x7D 0x02 0x02 0x7D 0x01 0x03
	if len(escaped) != 7 {
		t.Fatalf("escaped length: got %d, want 7", len(escaped))
	}
	unescaped, err := Unescape(escaped)
	if err != nil {
		t.Fatalf("Unescape error: %v", err)
	}
	if len(unescaped) != len(original) {
		t.Fatalf("unescaped length: got %d, want %d", len(unescaped), len(original))
	}
	for i := range original {
		if unescaped[i] != original[i] {
			t.Errorf("byte %d: got 0x%02X, want 0x%02X", i, unescaped[i], original[i])
		}
	}
}

func TestComprehensive_WrapAndStripDelimiter(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	wrapped := WrapWithDelimiter(data)
	if len(wrapped) != 5 || wrapped[0] != 0x7E || wrapped[4] != 0x7E {
		t.Errorf("WrapWithDelimiter result: %v", wrapped)
	}
	stripped := StripDelimiter(wrapped)
	if len(stripped) != 3 {
		t.Errorf("StripDelimiter length: got %d, want 3", len(stripped))
	}
}

// ---------------------------------------------------------------------------
// 分包重组器完整验证
// ---------------------------------------------------------------------------

func TestComprehensive_Reassembler_ThreeFragments(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13800138000"

	h1 := makeTestHeader(0x0704, phone, 1, true, 3, 0)
	h2 := makeTestHeader(0x0704, phone, 1, true, 3, 1)
	h3 := makeTestHeader(0x0704, phone, 1, true, 3, 2)

	// 乱序投递
	r.Feed(h3, []byte{0x05, 0x06})
	_, ready, _ := r.Feed(h1, []byte{0x01, 0x02})
	if ready {
		t.Error("should not be ready after 2 of 3 fragments")
	}
	complete, ready, err := r.Feed(h2, []byte{0x03, 0x04})
	if err != nil {
		t.Fatalf("Feed 3 failed: %v", err)
	}
	if !ready {
		t.Fatal("should be ready after all 3 fragments")
	}
	expected := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	if len(complete) != len(expected) {
		t.Fatalf("complete length: got %d, want %d", len(complete), len(expected))
	}
	for i, b := range expected {
		if complete[i] != b {
			t.Errorf("byte %d: got 0x%02X, want 0x%02X", i, complete[i], b)
		}
	}
}

func TestComprehensive_Reassembler_DifferentDevices(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)

	// 两个不同设备的分片不应互相干扰
	h1a := makeTestHeader(0x0704, "13800138001", 1, true, 2, 0)
	h1b := makeTestHeader(0x0704, "13800138001", 1, true, 2, 1)
	h2a := makeTestHeader(0x0704, "13800138002", 1, true, 2, 0)

	r.Feed(h1a, []byte{0xAA})
	r.Feed(h2a, []byte{0xBB})

	complete1, ready, _ := r.Feed(h1b, []byte{0xCC})
	if !ready {
		t.Fatal("device 1 should be ready")
	}
	if complete1[0] != 0xAA || complete1[1] != 0xCC {
		t.Errorf("device 1 data mismatch: %v", complete1)
	}

	// device 2 still pending
	if r.PendingCount() != 1 {
		t.Errorf("PendingCount: got %d, want 1", r.PendingCount())
	}
}

// ---------------------------------------------------------------------------
// PhoneBookSet / InfoService / AreaRouteAlarm
// ---------------------------------------------------------------------------

func TestComprehensive_PhoneBookSetMessage(t *testing.T) {
	orig := &PhoneBookSetMessage{
		Contacts: []PhoneBookContact{
			{Name: "张三", PhoneNumber: "13800138000", CallType: 0x00},
			{Name: "李四", PhoneNumber: "13900139000", CallType: 0x01},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &PhoneBookSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.Contacts) != 2 {
		t.Fatalf("Contacts count: got %d, want 2", len(parsed.Contacts))
	}
	if parsed.Contacts[0].Name != "张三" {
		t.Errorf("Contact0 name: got %q", parsed.Contacts[0].Name)
	}
}

func TestComprehensive_InfoServiceMessage(t *testing.T) {
	orig := &InfoServiceMessage{InfoType: 0x01, Content: "service info"}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &InfoServiceMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.InfoType != 0x01 || parsed.Content != "service info" {
		t.Errorf("Fields mismatch")
	}
}

func TestComprehensive_AreaRouteAlarmSetMessage(t *testing.T) {
	orig := &AreaRouteAlarmSetMessage{
		AreaID: 1, AreaAttr: 0x0001,
		StartTime: "240704000000", EndTime: "240704235959",
		AlarmFlag: 0x0001, CenterLat: 39.9, CenterLon: 116.4, Radius: 1000,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed := &AreaRouteAlarmSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AreaID != 1 || parsed.Radius != 1000 {
		t.Errorf("Fields mismatch")
	}
}

// ---------------------------------------------------------------------------
// 0x0805 摄像头立即拍摄命令应答 (PhotoCommandRespMessage)
// ---------------------------------------------------------------------------

func TestComprehensive_PhotoCommandRespMessage_WithRetransmit(t *testing.T) {
	orig := &PhotoCommandRespMessage{
		RespSeqNum:    0x1234,
		Result:        0x00,
		MultimediaID:  0xABCDEF01,
		RetransmitIDs: []uint16{0x0001, 0x0003, 0x0005},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 2(RespSeqNum) + 1(Result) + 4(MultimediaID) + 2(count) + 3*2(IDs) = 15
	if len(data) != 15 {
		t.Fatalf("data length: got %d, want 15", len(data))
	}
	parsed := &PhotoCommandRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != 0x1234 {
		t.Errorf("RespSeqNum: got 0x%04X, want 0x1234", parsed.RespSeqNum)
	}
	if parsed.Result != 0x00 {
		t.Errorf("Result: got %d, want 0", parsed.Result)
	}
	if parsed.MultimediaID != 0xABCDEF01 {
		t.Errorf("MultimediaID: got 0x%08X, want 0xABCDEF01", parsed.MultimediaID)
	}
	if len(parsed.RetransmitIDs) != 3 {
		t.Fatalf("RetransmitIDs count: got %d, want 3", len(parsed.RetransmitIDs))
	}
	if parsed.RetransmitIDs[0] != 0x0001 || parsed.RetransmitIDs[1] != 0x0003 || parsed.RetransmitIDs[2] != 0x0005 {
		t.Errorf("RetransmitIDs: got %v, want [1, 3, 5]", parsed.RetransmitIDs)
	}
}

func TestComprehensive_PhotoCommandRespMessage_NoRetransmit(t *testing.T) {
	orig := &PhotoCommandRespMessage{
		RespSeqNum:    0x5678,
		Result:        0x01, // 失败
		MultimediaID:  0,
		RetransmitIDs: nil,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 9 {
		t.Fatalf("data length: got %d, want 9", len(data))
	}
	parsed := &PhotoCommandRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.Result != 0x01 {
		t.Errorf("Result: got %d, want 1", parsed.Result)
	}
	if len(parsed.RetransmitIDs) != 0 {
		t.Errorf("RetransmitIDs count: got %d, want 0", len(parsed.RetransmitIDs))
	}
}

func TestComprehensive_PhotoCommandRespMessage_ParseBodyDispatch(t *testing.T) {
	codec := NewCodec()
	orig := &PhotoCommandRespMessage{
		RespSeqNum:    0x9999,
		Result:        0x00,
		MultimediaID:  0xDEADBEEF,
		RetransmitIDs: []uint16{0x000A},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body, err := codec.ParseBody(MsgIDPhotoCommandResp, data)
	if err != nil {
		t.Fatalf("ParseBody: %v", err)
	}
	parsed, ok := body.(*PhotoCommandRespMessage)
	if !ok {
		t.Fatalf("expected *PhotoCommandRespMessage, got %T", body)
	}
	if parsed.MultimediaID != 0xDEADBEEF {
		t.Errorf("MultimediaID: got 0x%08X, want 0xDEADBEEF", parsed.MultimediaID)
	}
}

// ---------------------------------------------------------------------------
// RSA 公钥交换
// ---------------------------------------------------------------------------

func TestComprehensive_RSAPublicKeyMessage(t *testing.T) {
	euler := make([]byte, 128)
	for i := range euler {
		euler[i] = byte(i)
	}
	orig := &RSAPublicKeyMessage{
		Euler:          euler,
		PublicExponent: 65537,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != 132 {
		t.Fatalf("data length: got %d, want 132", len(data))
	}
	parsed := &RSAPublicKeyMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.PublicExponent != 65537 {
		t.Errorf("PublicExponent: got %d, want 65537", parsed.PublicExponent)
	}
	if len(parsed.Euler) != 128 {
		t.Fatalf("Euler length: got %d, want 128", len(parsed.Euler))
	}
}
