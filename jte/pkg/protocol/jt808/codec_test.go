package jt808

import (
	"testing"
)

func TestBCDToString(t *testing.T) {
	tests := []struct {
		input    []byte
		expected string
	}{
		// AUTO-FIX-2026-06-29 [P0]: SIM/Phone 是 12 位定长 BCD，必须保留前导零
		{[]byte{0x01, 0x38, 0x00, 0x13, 0x80, 0x00}, "013800138000"},
		{[]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}, "000000000001"},
		// 常见 SIM 卡号 013800000000（前导零必须保留，否则视频/指令路由失败）
		{[]byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00}, "013800000000"},

	}

	for _, tt := range tests {
		got := BCDToString(tt.input)
		if got != tt.expected {
			t.Errorf("BCDToString(%v) = %s, want %s", tt.input, got, tt.expected)
		}
	}
}

func TestStringToBCD(t *testing.T) {
	tests := []struct {
		input    string
		expected []byte
	}{
		{"13800138000", []byte{0x01, 0x38, 0x00, 0x13, 0x80, 0x00}},
		{"1", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}},
	}

	for _, tt := range tests {
		got := StringToBCD(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("StringToBCD(%s) length = %d, want %d", tt.input, len(got), len(tt.expected))
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("StringToBCD(%s)[%d] = 0x%02X, want 0x%02X", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestEscapeUnescape(t *testing.T) {
	tests := [][]byte{
		{0x01, 0x7E, 0x02, 0x7D, 0x03},
		{0x7E, 0x01, 0x7D, 0x02},
		{0x7E, 0x7D},
		{0x01, 0x02, 0x03},
	}

	for _, original := range tests {
		escaped := Escape(original)
		unescaped := Unescape(escaped)
		if len(unescaped) != len(original) {
			t.Errorf("Unescape length mismatch: got %d, want %d", len(unescaped), len(original))
			continue
		}
		for i := range original {
			if unescaped[i] != original[i] {
				t.Errorf("Unescape at %d: got 0x%02X, want 0x%02X", i, unescaped[i], original[i])
			}
		}
	}
}

func TestSplitByDelimiter(t *testing.T) {
	data := []byte{0x7E, 0x01, 0x02, 0x7E, 0x7E, 0x03, 0x04, 0x7E}
	msgs := SplitByDelimiter(data)
	if len(msgs) != 2 {
		t.Fatalf("SplitByDelimiter returned %d messages, want 2", len(msgs))
	}
	if msgs[0][0] != 0x7E || msgs[0][len(msgs[0])-1] != 0x7E {
		t.Error("first message not properly delimited")
	}
	if msgs[1][0] != 0x7E || msgs[1][len(msgs[1])-1] != 0x7E {
		t.Error("second message not properly delimited")
	}
}

func TestCalcChecksum(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	checksum := CalcChecksum(data)
	expected := byte(0x01 ^ 0x02 ^ 0x03)
	if checksum != expected {
		t.Errorf("CalcChecksum = 0x%02X, want 0x%02X", checksum, expected)
	}
}

func TestCodec_ParseHeader(t *testing.T) {
	codec := NewCodec()

	headerData := []byte{
		0x01, 0x00,
		0x00, 0x06,
		0x01, 0x38, 0x00, 0x13, 0x80, 0x00,
		0x00, 0x01,
	}

	header, offset, err := codec.ParseHeader(headerData)
	if err != nil {
		t.Fatalf("ParseHeader failed: %v", err)
	}

	if header.MsgID != 0x0100 {
		t.Errorf("MsgID = 0x%04X, want 0x0100", header.MsgID)
	}
	// AUTO-FIX-2026-06-29 [P0]: SIM/Phone 是 12 位定长 BCD，必须保留前导零。
	// BCD bytes 0x01,0x38,0x00,0x13,0x80,0x00 → "013800138000"（原期望 "13800138000" 会丢前导零）
	if header.Phone != "013800138000" {
		t.Errorf("Phone = %s, want 013800138000", header.Phone)
	}
	if header.SeqNum != 1 {
		t.Errorf("SeqNum = %d, want 1", header.SeqNum)
	}
	if offset != 12 {
		t.Errorf("offset = %d, want 12", offset)
	}
}

func TestCodec_ParseHeaderWithPack(t *testing.T) {
	codec := NewCodec()

	headerData := []byte{
		0x01, 0x00,
		0x20, 0x06,
		0x01, 0x38, 0x00, 0x13, 0x80, 0x00,
		0x00, 0x01,
		0x00, 0x03,
		0x00, 0x01,
	}

	header, offset, err := codec.ParseHeader(headerData)
	if err != nil {
		t.Fatalf("ParseHeader failed: %v", err)
	}

	if !header.HasPack {
		t.Error("expected HasPack to be true")
	}
	if header.PackTotal != 3 {
		t.Errorf("PackTotal = %d, want 3", header.PackTotal)
	}
	if header.PackIndex != 1 {
		t.Errorf("PackIndex = %d, want 1", header.PackIndex)
	}
	if offset != 16 {
		t.Errorf("offset = %d, want 16", offset)
	}
}

func TestCodec_VerifyChecksum(t *testing.T) {
	codec := NewCodec()

	data := []byte{0x01, 0x02, 0x03}
	checksum := CalcChecksum(data)
	fullData := append(data, checksum)

	if !codec.VerifyChecksum(fullData) {
		t.Error("VerifyChecksum should return true for valid checksum")
	}

	fullData[len(fullData)-1] = 0xFF
	if codec.VerifyChecksum(fullData) {
		t.Error("VerifyChecksum should return false for invalid checksum")
	}
}

func TestRegisterMessage_Unmarshal(t *testing.T) {
	codec := NewCodec()

	bodyData := make([]byte, 37)
	bodyData[0] = 0x00
	bodyData[1] = 0x01
	bodyData[2] = 0x00
	bodyData[3] = 0x01
	copy(bodyData[4:9], []byte("MFR01"))
	copy(bodyData[9:29], []byte("MODEL001"))
	copy(bodyData[29:36], []byte("TID0001"))
	bodyData[36] = 0x01

	body, err := codec.ParseBody(MsgIDRegister, bodyData)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}

	reg, ok := body.(*RegisterMessage)
	if !ok {
		t.Fatal("expected *RegisterMessage")
	}

	if reg.ProvinceID != 1 {
		t.Errorf("ProvinceID = %d, want 1", reg.ProvinceID)
	}
	if reg.PlateColor != 1 {
		t.Errorf("PlateColor = %d, want 1", reg.PlateColor)
	}
}

func TestLocationMessage_Unmarshal(t *testing.T) {
	codec := NewCodec()

	bodyData := make([]byte, 28)

	bodyData[0] = 0x00
	bodyData[1] = 0x00
	bodyData[2] = 0x00
	bodyData[3] = 0x00

	bodyData[4] = 0x00
	bodyData[5] = 0x00
	bodyData[6] = 0x00
	bodyData[7] = 0x01

	lat := uint32(39.9042 * 1000000)
	bodyData[8] = byte(lat >> 24)
	bodyData[9] = byte(lat >> 16)
	bodyData[10] = byte(lat >> 8)
	bodyData[11] = byte(lat)

	lon := uint32(116.4074 * 1000000)
	bodyData[12] = byte(lon >> 24)
	bodyData[13] = byte(lon >> 16)
	bodyData[14] = byte(lon >> 8)
	bodyData[15] = byte(lon)

	bodyData[16] = 0x00
	bodyData[17] = 0x32
	bodyData[18] = 0x00
	bodyData[19] = 0x3C
	bodyData[20] = 0x00
	bodyData[21] = 0xB4

	copy(bodyData[22:28], []byte{0x20, 0x26, 0x06, 0x20, 0x15, 0x30})

	body, err := codec.ParseBody(MsgIDLocation, bodyData)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}

	loc, ok := body.(*LocationMessage)
	if !ok {
		t.Fatal("expected *LocationMessage")
	}

	if loc.Latitude < 39.9 || loc.Latitude > 39.91 {
		t.Errorf("Latitude = %f, want ~39.9042", loc.Latitude)
	}
	if loc.Longitude < 116.4 || loc.Longitude > 116.41 {
		t.Errorf("Longitude = %f, want ~116.4074", loc.Longitude)
	}
}

func TestGeneralResponse_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()

	original := &GeneralResponse{
		RespSeqNum: 1,
		RespMsgID:  0x0100,
		Result:     0,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDGeneralResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}

	resp, ok := body.(*GeneralResponse)
	if !ok {
		t.Fatal("expected *GeneralResponse")
	}

	if resp.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = %d, want %d", resp.RespSeqNum, original.RespSeqNum)
	}
	if resp.RespMsgID != original.RespMsgID {
		t.Errorf("RespMsgID = 0x%04X, want 0x%04X", resp.RespMsgID, original.RespMsgID)
	}
	if resp.Result != original.Result {
		t.Errorf("Result = %d, want %d", resp.Result, original.Result)
	}
}

func TestMsgIDDriverID_NoConflict(t *testing.T) {
	if MsgIDDriverID != 0x0702 {
		t.Errorf("MsgIDDriverID = 0x%04X, want 0x0702", MsgIDDriverID)
	}
}

func TestMsgIDLocationBatch_NoConflict(t *testing.T) {
	if MsgIDLocationBatch != 0x0704 {
		t.Errorf("MsgIDLocationBatch = 0x%04X, want 0x0704", MsgIDLocationBatch)
	}
}

func TestTempLocationTrackRespMessage_MarshalUnmarshal(t *testing.T) {
	original := &TempLocationTrackRespMessage{Result: 0x00}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed := &TempLocationTrackRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestOverspeedSetMessage_MarshalUnmarshal(t *testing.T) {
	original := &OverspeedSetMessage{
		ID:         1,
		SpeedLimit: 120,
		Duration:   10,
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed := &OverspeedSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.ID != original.ID {
		t.Errorf("ID = %d, want %d", parsed.ID, original.ID)
	}
	if parsed.SpeedLimit != original.SpeedLimit {
		t.Errorf("SpeedLimit = %d, want %d", parsed.SpeedLimit, original.SpeedLimit)
	}
}

func TestFatigueDriveSetMessage_MarshalUnmarshal(t *testing.T) {
	original := &FatigueDriveSetMessage{
		ID:         2,
		Threshold:  480,
		Duration:   30,
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed := &FatigueDriveSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.ID != original.ID {
		t.Errorf("ID = %d, want %d", parsed.ID, original.ID)
	}
	if parsed.Threshold != original.Threshold {
		t.Errorf("Threshold = %d, want %d", parsed.Threshold, original.Threshold)
	}
}

// AUTO-FIX-2026-06-26: 为新增 0x8500 车辆控制消息添加编解码测试
func TestVehicleControlMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()

	original := &VehicleControlMessage{ControlType: 0x03}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 1 || data[0] != 0x03 {
		t.Fatalf("encoded data = %v, want [0x03]", data)
	}

	body, err := codec.ParseBody(MsgIDVehicleControl, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}

	parsed, ok := body.(*VehicleControlMessage)
	if !ok {
		t.Fatalf("expected *VehicleControlMessage, got %T", body)
	}
	if parsed.ControlType != original.ControlType {
		t.Errorf("ControlType = 0x%02X, want 0x%02X", parsed.ControlType, original.ControlType)
	}
}

func TestMsgIDVehicleControl_NoConflict(t *testing.T) {
	if MsgIDVehicleControl != 0x8500 {
		t.Errorf("MsgIDVehicleControl = 0x%04X, want 0x8500", MsgIDVehicleControl)
	}
}

// FIX-7-1: 0x9101 视频请求消息体测试 [2026-06-26]
func TestVideoRequestMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()

	original := &VideoRequestMessage{
		ServerIP:   "192.168.1.100",
		ServerPort: 10000,
		PlayType:   0,
		Channel:    1,
		DataType:   0,
		StreamType: 0,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 10 {
		t.Fatalf("encoded data length = %d, want 10", len(data))
	}

	body, err := codec.ParseBody(MsgIDVideoRequest, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}

	parsed, ok := body.(*VideoRequestMessage)
	if !ok {
		t.Fatalf("expected *VideoRequestMessage, got %T", body)
	}
	if parsed.ServerIP != original.ServerIP {
		t.Errorf("ServerIP = %s, want %s", parsed.ServerIP, original.ServerIP)
	}
	if parsed.ServerPort != original.ServerPort {
		t.Errorf("ServerPort = %d, want %d", parsed.ServerPort, original.ServerPort)
	}
	if parsed.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", parsed.Channel, original.Channel)
	}
	if parsed.StreamType != original.StreamType {
		t.Errorf("StreamType = %d, want %d", parsed.StreamType, original.StreamType)
	}
}

func TestMsgIDVideoRequest_NoConflict(t *testing.T) {
	if MsgIDVideoRequest != 0x9101 {
		t.Errorf("MsgIDVideoRequest = 0x%04X, want 0x9101", MsgIDVideoRequest)
	}
}

// AUTO-FIX-2026-06-26: 808协议新增消息体单元测试（10项），按第一轮.txt要求 [2026-06-26]

// FIX-1-1: 0x8108 终端升级消息体测试
func TestTerminalUpgradeMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &TerminalUpgradeMessage{
		UpgradeType:  0x01,
		Manufacturer: "MFR01",
		Version:      "v1.0.0",
		URL:          "http://upgrade.example.com/v1.0.0",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDTerminalUpgrade, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*TerminalUpgradeMessage)
	if !ok {
		t.Fatalf("expected *TerminalUpgradeMessage, got %T", body)
	}
	if parsed.UpgradeType != original.UpgradeType {
		t.Errorf("UpgradeType = 0x%02X, want 0x%02X", parsed.UpgradeType, original.UpgradeType)
	}
	if parsed.Manufacturer != original.Manufacturer {
		t.Errorf("Manufacturer = %q, want %q", parsed.Manufacturer, original.Manufacturer)
	}
	if parsed.Version != original.Version {
		t.Errorf("Version = %q, want %q", parsed.Version, original.Version)
	}
	if parsed.URL != original.URL {
		t.Errorf("URL = %q, want %q", parsed.URL, original.URL)
	}
}

func TestMsgIDTerminalUpgrade_NoConflict(t *testing.T) {
	if MsgIDTerminalUpgrade != 0x8108 {
		t.Errorf("MsgIDTerminalUpgrade = 0x%04X, want 0x8108", MsgIDTerminalUpgrade)
	}
}

// FIX-1-2: 0x8107 终端属性查询消息体测试（空体）
func TestTerminalPropQueryMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &TerminalPropQueryMessage{}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty body, got %d bytes", len(data))
	}

	body, err := codec.ParseBody(MsgIDTerminalPropQuery, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	if _, ok := body.(*TerminalPropQueryMessage); !ok {
		t.Fatalf("expected *TerminalPropQueryMessage, got %T", body)
	}
}

func TestMsgIDTerminalPropQuery_NoConflict(t *testing.T) {
	if MsgIDTerminalPropQuery != 0x8107 {
		t.Errorf("MsgIDTerminalPropQuery = 0x%04X, want 0x8107", MsgIDTerminalPropQuery)
	}
}

// AUTO-FIX-2026-06-27: 0x8802 多媒体上传控制消息体重构测试（字段已改为 多媒体ID+通道号+媒体类型+起止时间）
func TestMultimediaUploadCmdMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &MultimediaUploadCmdMessage{
		MultimediaID: 0x12345678,
		ChannelID:    1,
		MediaType:     0,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// AUTO-FIX-2026-06-27: 实际体长 = 4(多媒体ID)+1(通道号)+1(媒体类型)+6(起时间BCD)+6(止时间BCD) = 18B
	if len(data) != 18 {
		t.Fatalf("encoded length = %d, want 18", len(data))
	}

	body, err := codec.ParseBody(MsgIDMultimediaUploadCmd, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*MultimediaUploadCmdMessage)
	if !ok {
		t.Fatalf("expected *MultimediaUploadCmdMessage, got %T", body)
	}
	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if parsed.ChannelID != original.ChannelID {
		t.Errorf("ChannelID = %d, want %d", parsed.ChannelID, original.ChannelID)
	}
	if parsed.MediaType != original.MediaType {
		t.Errorf("MediaType = %d, want %d", parsed.MediaType, original.MediaType)
	}
	if parsed.StartTime != original.StartTime {
		t.Errorf("StartTime = %s, want %s", parsed.StartTime, original.StartTime)
	}
	if parsed.EndTime != original.EndTime {
		t.Errorf("EndTime = %s, want %s", parsed.EndTime, original.EndTime)
	}
}

func TestMsgIDMultimediaUploadCmd_NoConflict(t *testing.T) {
	if MsgIDMultimediaUploadCmd != 0x8802 {
		t.Errorf("MsgIDMultimediaUploadCmd = 0x%04X, want 0x8802", MsgIDMultimediaUploadCmd)
	}
}

// FIX-1-4: 0x8803 多媒体传输消息体测试
func TestFileUploadCmdMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &FileUploadCmdMessage{
		MultimediaID: 0xABCDEF12,
		Cmd:          0x01,
		Time:         60,
		SaveFlag:     1,
		AudioSample:  0x03,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 9 {
		t.Fatalf("encoded length = %d, want 9", len(data))
	}

	body, err := codec.ParseBody(MsgIDFileUploadCmd, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*FileUploadCmdMessage)
	if !ok {
		t.Fatalf("expected *FileUploadCmdMessage, got %T", body)
	}
	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if parsed.Cmd != original.Cmd {
		t.Errorf("Cmd = 0x%02X, want 0x%02X", parsed.Cmd, original.Cmd)
	}
	if parsed.Time != original.Time {
		t.Errorf("Time = %d, want %d", parsed.Time, original.Time)
	}
	if parsed.AudioSample != original.AudioSample {
		t.Errorf("AudioSample = 0x%02X, want 0x%02X", parsed.AudioSample, original.AudioSample)
	}
}

func TestMsgIDFileUploadCmd_NoConflict(t *testing.T) {
	if MsgIDFileUploadCmd != 0x8803 {
		t.Errorf("MsgIDFileUploadCmd = 0x%04X, want 0x8803", MsgIDFileUploadCmd)
	}
}

// AUTO-FIX-2026-06-27: 0x8804 录音上传指令case注册验证（新增 RecordCmd 字段，体改为5B）
func TestAudioRecordCmdMessage_ParseBody(t *testing.T) {
	codec := NewCodec()
	original := &AudioRecordCmdMessage{
		RecordTime:  30,
		RecordCmd:   0x01,
		SaveFlag:    1,
		AudioSample: 0x03,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("encoded length = %d, want 5", len(data))
	}

	body, err := codec.ParseBody(MsgIDAudioRecordCmd, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*AudioRecordCmdMessage)
	if !ok {
		t.Fatalf("expected *AudioRecordCmdMessage, got %T", body)
	}
	if parsed.RecordTime != original.RecordTime {
		t.Errorf("RecordTime = %d, want %d", parsed.RecordTime, original.RecordTime)
	}
	if parsed.RecordCmd != original.RecordCmd {
		t.Errorf("RecordCmd = 0x%02X, want 0x%02X", parsed.RecordCmd, original.RecordCmd)
	}
	if parsed.AudioSample != original.AudioSample {
		t.Errorf("AudioSample = 0x%02X, want 0x%02X", parsed.AudioSample, original.AudioSample)
	}
}

func TestMsgIDAudioRecordCmd_NoConflict(t *testing.T) {
	if MsgIDAudioRecordCmd != 0x8804 {
		t.Errorf("MsgIDAudioRecordCmd = 0x%04X, want 0x8804", MsgIDAudioRecordCmd)
	}
}

// FIX-1-6: 0x0805 摄像头立即拍摄命令应答消息体测试（AUTO-FIX-2026-07-04 修正）
// 原 MultimediaSearchMessage 结构体已不再经 ParseBody 自动分发（0x0805 按 808-2019 标准为 PhotoCommandRespMessage），
// 此测试验证 MultimediaSearchMessage 的 Marshal/Unmarshal 往返仍可用（供旧调用方显式构造/解码）。
func TestMultimediaSearchMessage_MarshalUnmarshal(t *testing.T) {
	original := &MultimediaSearchMessage{
		MultimediaID: 0x11223344,
		Items: []MultimediaSearchItem{
			{ChannelID: 1, MediaType: 0, StartTime: "240101000000", EndTime: "240101010000", Size: 1024},
		},
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &MultimediaSearchMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("Items count = %d, want 1", len(parsed.Items))
	}
	if parsed.Items[0].ChannelID != 1 {
		t.Errorf("ChannelID = %d, want 1", parsed.Items[0].ChannelID)
	}
	if parsed.Items[0].Size != 1024 {
		t.Errorf("Size = %d, want 1024", parsed.Items[0].Size)
	}
}

// AUTO-FIX-2026-07-04: 0x0805 按 JT/T 808-2019 标准为"摄像头立即拍摄命令应答"（PhotoCommandRespMessage）
func TestMsgIDPhotoCommandResp_Value(t *testing.T) {
	if MsgIDPhotoCommandResp != 0x0805 {
		t.Errorf("MsgIDPhotoCommandResp = 0x%04X, want 0x0805", MsgIDPhotoCommandResp)
	}
	// 向后兼容：MsgIDMultimediaSearchResp 仍为 0x0805
	if MsgIDMultimediaSearchResp != 0x0805 {
		t.Errorf("MsgIDMultimediaSearchResp = 0x%04X, want 0x0805", MsgIDMultimediaSearchResp)
	}
}

// AUTO-FIX-2026-07-04: 0x0805 PhotoCommandRespMessage 编解码 + ParseBody 分发测试
func TestPhotoCommandRespMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PhotoCommandRespMessage{
		RespSeqNum:    0x1234,
		Result:        0x00,
		MultimediaID:  0xABCDEF01,
		RetransmitIDs: []uint16{0x0001, 0x0003, 0x0005},
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	// 最小 9B + 3×2B = 15B
	if len(data) != 15 {
		t.Fatalf("data length = %d, want 15", len(data))
	}

	// ParseBody 分发验证
	body, err := codec.ParseBody(MsgIDPhotoCommandResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PhotoCommandRespMessage)
	if !ok {
		t.Fatalf("expected *PhotoCommandRespMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = 0x%02X, want 0x%02X", parsed.Result, original.Result)
	}
	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if len(parsed.RetransmitIDs) != 3 {
		t.Fatalf("RetransmitIDs count = %d, want 3", len(parsed.RetransmitIDs))
	}
	if parsed.RetransmitIDs[0] != 0x0001 || parsed.RetransmitIDs[1] != 0x0003 || parsed.RetransmitIDs[2] != 0x0005 {
		t.Errorf("RetransmitIDs = %v, want [1, 3, 5]", parsed.RetransmitIDs)
	}
}

// AUTO-FIX-2026-07-04: 0x0805 PhotoCommandRespMessage 无重传包场景
func TestPhotoCommandRespMessage_NoRetransmit(t *testing.T) {
	codec := NewCodec()
	original := &PhotoCommandRespMessage{
		RespSeqNum:    0x5678,
		Result:        0x01, // 失败
		MultimediaID:  0,
		RetransmitIDs: nil,
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) != 9 {
		t.Fatalf("data length = %d, want 9", len(data))
	}

	body, err := codec.ParseBody(MsgIDPhotoCommandResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PhotoCommandRespMessage)
	if !ok {
		t.Fatalf("expected *PhotoCommandRespMessage, got %T", body)
	}
	if parsed.Result != 0x01 {
		t.Errorf("Result = 0x%02X, want 0x01", parsed.Result)
	}
	if len(parsed.RetransmitIDs) != 0 {
		t.Errorf("RetransmitIDs count = %d, want 0", len(parsed.RetransmitIDs))
	}
}

// FIX-1-7: 0x8702 电话回拨消息体测试
func TestPhoneCallbackMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PhoneCallbackMessage{
		CallbackType: 0x01,
		PhoneNumber: "13800138000",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDPhoneCallback, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PhoneCallbackMessage)
	if !ok {
		t.Fatalf("expected *PhoneCallbackMessage, got %T", body)
	}
	if parsed.CallbackType != original.CallbackType {
		t.Errorf("CallbackType = 0x%02X, want 0x%02X", parsed.CallbackType, original.CallbackType)
	}
	if parsed.PhoneNumber != original.PhoneNumber {
		t.Errorf("PhoneNumber = %q, want %q", parsed.PhoneNumber, original.PhoneNumber)
	}
}

func TestMsgIDPhoneCallback_NoConflict(t *testing.T) {
	if MsgIDPhoneCallback != 0x8702 {
		t.Errorf("MsgIDPhoneCallback = 0x%04X, want 0x8702", MsgIDPhoneCallback)
	}
}

// FIX-1-8: 0x8703 信息服务/短信转发消息体测试
func TestSMSForwardMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &SMSForwardMessage{
		InfoType: 0x01,
		Content:  "Hello, JTE!",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDSMSForward, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*SMSForwardMessage)
	if !ok {
		t.Fatalf("expected *SMSForwardMessage, got %T", body)
	}
	if parsed.InfoType != original.InfoType {
		t.Errorf("InfoType = 0x%02X, want 0x%02X", parsed.InfoType, original.InfoType)
	}
	if parsed.Content != original.Content {
		t.Errorf("Content = %q, want %q", parsed.Content, original.Content)
	}
}

func TestMsgIDSMSForward_NoConflict(t *testing.T) {
	if MsgIDSMSForward != 0x8703 {
		t.Errorf("MsgIDSMSForward = 0x%04X, want 0x8703", MsgIDSMSForward)
	}
}

// FIX-1-9: 0x8301 事件设置消息体测试
func TestEventSetMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &EventSetMessage{
		SetType: 0x01,
		Events: []EventItem{
			{EventID: 1, Content: "疲劳驾驶"},
			{EventID: 2, Content: "超速"},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDEventSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*EventSetMessage)
	if !ok {
		t.Fatalf("expected *EventSetMessage, got %T", body)
	}
	if parsed.SetType != original.SetType {
		t.Errorf("SetType = 0x%02X, want 0x%02X", parsed.SetType, original.SetType)
	}
	if len(parsed.Events) != 2 {
		t.Fatalf("Events count = %d, want 2", len(parsed.Events))
	}
	if parsed.Events[0].EventID != 1 || parsed.Events[0].Content != "疲劳驾驶" {
		t.Errorf("Event[0] = {%d, %q}, want {1, 疲劳驾驶}", parsed.Events[0].EventID, parsed.Events[0].Content)
	}
}

func TestEventSetMessage_DeleteAll(t *testing.T) {
	codec := NewCodec()
	original := &EventSetMessage{SetType: 0x00}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 1 {
		t.Errorf("encoded length = %d, want 1 (delete all)", len(data))
	}

	body, err := codec.ParseBody(MsgIDEventSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*EventSetMessage)
	if !ok {
		t.Fatalf("expected *EventSetMessage, got %T", body)
	}
	if parsed.SetType != 0x00 {
		t.Errorf("SetType = 0x%02X, want 0x00", parsed.SetType)
	}
	if len(parsed.Events) != 0 {
		t.Errorf("Events count = %d, want 0", len(parsed.Events))
	}
}

func TestMsgIDEventSet_NoConflict(t *testing.T) {
	if MsgIDEventSet != 0x8301 {
		t.Errorf("MsgIDEventSet = 0x%04X, want 0x8301", MsgIDEventSet)
	}
}

// FIX-1-10: 0x8303 信息点设置消息体测试
func TestInfoDistributeMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &InfoDistributeMessage{
		SetType:  0x01,
		InfoID:   0x00000001,
		InfoName: "高速服务区",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDInfoDistribute, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*InfoDistributeMessage)
	if !ok {
		t.Fatalf("expected *InfoDistributeMessage, got %T", body)
	}
	if parsed.SetType != original.SetType {
		t.Errorf("SetType = 0x%02X, want 0x%02X", parsed.SetType, original.SetType)
	}
	if parsed.InfoID != original.InfoID {
		t.Errorf("InfoID = 0x%08X, want 0x%08X", parsed.InfoID, original.InfoID)
	}
	if parsed.InfoName != original.InfoName {
		t.Errorf("InfoName = %q, want %q", parsed.InfoName, original.InfoName)
	}
}

func TestMsgIDInfoDistribute_NoConflict(t *testing.T) {
	if MsgIDInfoDistribute != 0x8303 {
		t.Errorf("MsgIDInfoDistribute = 0x%04X, want 0x8303", MsgIDInfoDistribute)
	}
}

// AUTO-FIX-2026-06-27: 上轮未注册4项消息 round-trip 测试 [2026-06-27]

// FIX-2-1: 0x0400 超速报警消息 round-trip
func TestOverspeedAlarmMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &OverspeedAlarmMessage{
		AlarmFlag:   0x00000001,
		StatusFlag:  0x00000002,
		Latitude:    39.9042,
		Longitude:   116.4074,
		Altitude:    50,
		Speed:       60,
		Direction:   180,
		Time:        "240627120000",
		AlarmAttach: []byte{0xAA, 0xBB},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 30 {
		t.Fatalf("encoded length = %d, want 30", len(data))
	}
	body, err := codec.ParseBody(MsgIDOverspeedAlarm, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*OverspeedAlarmMessage)
	if !ok {
		t.Fatalf("expected *OverspeedAlarmMessage, got %T", body)
	}
	if parsed.AlarmFlag != original.AlarmFlag {
		t.Errorf("AlarmFlag = 0x%08X, want 0x%08X", parsed.AlarmFlag, original.AlarmFlag)
	}
	if parsed.Latitude < 39.9 || parsed.Latitude > 39.91 {
		t.Errorf("Latitude = %f, want ~39.9042", parsed.Latitude)
	}
	if parsed.Time != original.Time {
		t.Errorf("Time = %s, want %s", parsed.Time, original.Time)
	}
	if len(parsed.AlarmAttach) != 2 || parsed.AlarmAttach[0] != 0xAA {
		t.Errorf("AlarmAttach = %v, want [0xAA, 0xBB]", parsed.AlarmAttach)
	}
}

// FIX-2-2: 0x0401 疲劳驾驶报警消息 round-trip
func TestFatigueDriveAlarmMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &FatigueDriveAlarmMessage{
		AlarmFlag:   0x00000004,
		StatusFlag:  0x00000008,
		Latitude:    31.2304,
		Longitude:   121.4737,
		Altitude:    10,
		Speed:       80,
		Direction:   90,
		Time:        "240627150000",
		AlarmAttach: []byte{0xCC},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	body, err := codec.ParseBody(MsgIDFatigueDriveAlarm, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*FatigueDriveAlarmMessage)
	if !ok {
		t.Fatalf("expected *FatigueDriveAlarmMessage, got %T", body)
	}
	if parsed.AlarmFlag != original.AlarmFlag {
		t.Errorf("AlarmFlag = 0x%08X, want 0x%08X", parsed.AlarmFlag, original.AlarmFlag)
	}
	if parsed.Longitude < 121.47 || parsed.Longitude > 121.48 {
		t.Errorf("Longitude = %f, want ~121.4737", parsed.Longitude)
	}
	if parsed.Time != original.Time {
		t.Errorf("Time = %s, want %s", parsed.Time, original.Time)
	}
	if len(parsed.AlarmAttach) != 1 || parsed.AlarmAttach[0] != 0xCC {
		t.Errorf("AlarmAttach = %v, want [0xCC]", parsed.AlarmAttach)
	}
}

// FIX-2-3: 0x8701 信息点推送消息 round-trip
func TestInfoPushMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &InfoPushMessage{
		InfoID:    0x00000001,
		InfoName:  "服务区A",
		InfoType:  0x02,
		Longitude: 116.4074,
		Latitude:  39.9042,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	body, err := codec.ParseBody(MsgIDInfoPush, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*InfoPushMessage)
	if !ok {
		t.Fatalf("expected *InfoPushMessage, got %T", body)
	}
	if parsed.InfoID != original.InfoID {
		t.Errorf("InfoID = 0x%08X, want 0x%08X", parsed.InfoID, original.InfoID)
	}
	if parsed.InfoName != original.InfoName {
		t.Errorf("InfoName = %q, want %q", parsed.InfoName, original.InfoName)
	}
	if parsed.InfoType != original.InfoType {
		t.Errorf("InfoType = 0x%02X, want 0x%02X", parsed.InfoType, original.InfoType)
	}
	if parsed.Longitude < 116.4 || parsed.Longitude > 116.41 {
		t.Errorf("Longitude = %f, want ~116.4074", parsed.Longitude)
	}
}

// FIX-2-4: 0x9102 实时音视频控制消息 round-trip
func TestVideoControlMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &VideoControlMessage{
		Channel:      1,
		ControlCmd:   0x01,
		SwitchAV:     0x00,
		Reset:        0x00,
		CloseStream:  0x00,
		SwitchStream: 0x00,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 6 {
		t.Fatalf("encoded length = %d, want 6", len(data))
	}
	body, err := codec.ParseBody(MsgIDVideoControl, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*VideoControlMessage)
	if !ok {
		t.Fatalf("expected *VideoControlMessage, got %T", body)
	}
	if parsed.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", parsed.Channel, original.Channel)
	}
	if parsed.ControlCmd != original.ControlCmd {
		t.Errorf("ControlCmd = 0x%02X, want 0x%02X", parsed.ControlCmd, original.ControlCmd)
	}
	if parsed.SwitchStream != original.SwitchStream {
		t.Errorf("SwitchStream = 0x%02X, want 0x%02X", parsed.SwitchStream, original.SwitchStream)
	}
}

// AUTO-FIX-2026-06-27: 字段错位修复 round-trip 测试 [2026-06-27]

// FIX-3-1: 0x0103 CommandRespMessage 字段错位修复（删除 RespMsgID 字段）
func TestCommandRespMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &CommandRespMessage{
		RespSeqNum: 0x0100,
		RespCount:  1,
		Params: map[uint32][]byte{
			0x00010001: {0x01, 0x02},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// AUTO-FIX-2026-06-27: 体长度 = 2 (RespSeqNum) + 1 (RespCount) + 7 (param: 4B ID + 1B len + 2B value) = 10B
	if len(data) != 10 {
		t.Fatalf("encoded length = %d, want 10", len(data))
	}
	body, err := codec.ParseBody(MsgIDCommandResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*CommandRespMessage)
	if !ok {
		t.Fatalf("expected *CommandRespMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.RespCount != original.RespCount {
		t.Errorf("RespCount = %d, want %d", parsed.RespCount, original.RespCount)
	}
	if len(parsed.Params) != 1 {
		t.Fatalf("Params count = %d, want 1", len(parsed.Params))
	}
}

// FIX-3-2: 0x0301 EventRespMessage.EventID uint32 → uint16 修复
func TestEventRespMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &EventRespMessage{EventID: 0x0102}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("encoded length = %d, want 2", len(data))
	}
	body, err := codec.ParseBody(MsgIDEventResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*EventRespMessage)
	if !ok {
		t.Fatalf("expected *EventRespMessage, got %T", body)
	}
	if parsed.EventID != original.EventID {
		t.Errorf("EventID = 0x%04X, want 0x%04X", parsed.EventID, original.EventID)
	}
}

// FIX-3-3: 0x0302 QuestionRespMessage 新增 RespSeqNum 字段
func TestQuestionRespMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &QuestionRespMessage{
		RespSeqNum: 0x0001,
		AnswerID:   0x0002,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4", len(data))
	}
	body, err := codec.ParseBody(MsgIDQuestionResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*QuestionRespMessage)
	if !ok {
		t.Fatalf("expected *QuestionRespMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.AnswerID != original.AnswerID {
		t.Errorf("AnswerID = 0x%04X, want 0x%04X", parsed.AnswerID, original.AnswerID)
	}
}

// FIX-3-4: 0x0802 MultimediaUploadMessage 移除 DataType 字段
func TestMultimediaUploadMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &MultimediaUploadMessage{
		MultimediaID: 0xDEADBEEF,
		PacketIndex:  1,
		PacketTotal:  10,
		MediaData:    []byte{0x01, 0x02, 0x03},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// AUTO-FIX-2026-06-27: 体长度 = 4 (ID) + 2 (PacketIndex) + 2 (PacketTotal) + 3 (data) = 11B
	if len(data) != 11 {
		t.Fatalf("encoded length = %d, want 11", len(data))
	}
	body, err := codec.ParseBody(MsgIDMultimediaUpload, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*MultimediaUploadMessage)
	if !ok {
		t.Fatalf("expected *MultimediaUploadMessage, got %T", body)
	}
	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if parsed.PacketIndex != original.PacketIndex {
		t.Errorf("PacketIndex = %d, want %d", parsed.PacketIndex, original.PacketIndex)
	}
	if parsed.PacketTotal != original.PacketTotal {
		t.Errorf("PacketTotal = %d, want %d", parsed.PacketTotal, original.PacketTotal)
	}
	if len(parsed.MediaData) != 3 || parsed.MediaData[0] != 0x01 {
		t.Errorf("MediaData = %v, want [0x01, 0x02, 0x03]", parsed.MediaData)
	}
}

// FIX-3-5: 0x8106 ParamSetMessage 移除 SeqNum，参数项改为仅ID
func TestParamSetMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &ParamSetMessage{
		ParamIDs: []uint32{0x00010001, 0x00010002, 0x00010003},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// AUTO-FIX-2026-06-27: 体长度 = 1 (count) + 3*4 (paramIDs) = 13B
	if len(data) != 13 {
		t.Fatalf("encoded length = %d, want 13", len(data))
	}
	body, err := codec.ParseBody(MsgIDParamSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*ParamSetMessage)
	if !ok {
		t.Fatalf("expected *ParamSetMessage, got %T", body)
	}
	if len(parsed.ParamIDs) != 3 {
		t.Fatalf("ParamIDs count = %d, want 3", len(parsed.ParamIDs))
	}
	if parsed.ParamIDs[0] != 0x00010001 {
		t.Errorf("ParamIDs[0] = 0x%08X, want 0x00010001", parsed.ParamIDs[0])
	}
}

// FIX-3-6: 0x8700 InfoMenuSetMessage 菜单总数 2B→1B，InfoID 4B→2B
func TestInfoMenuSetMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &InfoMenuSetMessage{
		SetType: 0x01,
		Items: []InfoMenuItem{
			{InfoID: 0x0001, InfoName: "菜单1"},
			{InfoID: 0x0002, InfoName: "菜单2"},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	body, err := codec.ParseBody(MsgIDInfoMenuSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*InfoMenuSetMessage)
	if !ok {
		t.Fatalf("expected *InfoMenuSetMessage, got %T", body)
	}
	if parsed.SetType != original.SetType {
		t.Errorf("SetType = 0x%02X, want 0x%02X", parsed.SetType, original.SetType)
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("Items count = %d, want 2", len(parsed.Items))
	}
	if parsed.Items[0].InfoID != 0x0001 {
		t.Errorf("Items[0].InfoID = 0x%04X, want 0x0001", parsed.Items[0].InfoID)
	}
	if parsed.Items[1].InfoName != "菜单2" {
		t.Errorf("Items[1].InfoName = %q, want %q", parsed.Items[1].InfoName, "菜单2")
	}
}

// AUTO-FIX-2026-06-27: 缺失消息 round-trip 测试 [2026-06-27]

// FIX-4-1: 0x8204 PhoneBookSetMessage round-trip
func TestPhoneBookSetMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PhoneBookSetMessage{
		Contacts: []PhoneBookContact{
			{Name: "张三", PhoneNumber: "13800138000", CallType: 0x01},
			{Name: "李四", PhoneNumber: "13900139000", CallType: 0x02},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	body, err := codec.ParseBody(MsgIDPhoneBookSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PhoneBookSetMessage)
	if !ok {
		t.Fatalf("expected *PhoneBookSetMessage, got %T", body)
	}
	if len(parsed.Contacts) != 2 {
		t.Fatalf("Contacts count = %d, want 2", len(parsed.Contacts))
	}
	if parsed.Contacts[0].Name != "张三" {
		t.Errorf("Contacts[0].Name = %q, want %q", parsed.Contacts[0].Name, "张三")
	}
	if parsed.Contacts[0].PhoneNumber != "13800138000" {
		t.Errorf("Contacts[0].PhoneNumber = %q, want %q", parsed.Contacts[0].PhoneNumber, "13800138000")
	}
	if parsed.Contacts[1].CallType != 0x02 {
		t.Errorf("Contacts[1].CallType = 0x%02X, want 0x02", parsed.Contacts[1].CallType)
	}
}

func TestMsgIDPhoneBookSet_NoConflict(t *testing.T) {
	if MsgIDPhoneBookSet != 0x8204 {
		t.Errorf("MsgIDPhoneBookSet = 0x%04X, want 0x8204", MsgIDPhoneBookSet)
	}
}

// FIX-4-2: 0x8304 InfoServiceMessage round-trip
func TestInfoServiceMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &InfoServiceMessage{
		InfoType: 0x01,
		Content:  "天气：晴",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	body, err := codec.ParseBody(MsgIDInfoService, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*InfoServiceMessage)
	if !ok {
		t.Fatalf("expected *InfoServiceMessage, got %T", body)
	}
	if parsed.InfoType != original.InfoType {
		t.Errorf("InfoType = 0x%02X, want 0x%02X", parsed.InfoType, original.InfoType)
	}
	if parsed.Content != original.Content {
		t.Errorf("Content = %q, want %q", parsed.Content, original.Content)
	}
}

func TestMsgIDInfoService_NoConflict(t *testing.T) {
	if MsgIDInfoService != 0x8304 {
		t.Errorf("MsgIDInfoService = 0x%04X, want 0x8304", MsgIDInfoService)
	}
}

// FIX-4-3: 0x8402 AreaRouteAlarmSetMessage round-trip
func TestAreaRouteAlarmSetMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &AreaRouteAlarmSetMessage{
		AreaID:    0x00000001,
		AreaAttr:  0x0002,
		StartTime: "240101000000",
		EndTime:   "240102000000",
		AlarmFlag: 0x0001,
		CenterLat: 39.9042,
		CenterLon: 116.4074,
		Radius:    1000,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 32 {
		t.Fatalf("encoded length = %d, want 32", len(data))
	}
	body, err := codec.ParseBody(MsgIDAreaRouteAlarmSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*AreaRouteAlarmSetMessage)
	if !ok {
		t.Fatalf("expected *AreaRouteAlarmSetMessage, got %T", body)
	}
	if parsed.AreaID != original.AreaID {
		t.Errorf("AreaID = 0x%08X, want 0x%08X", parsed.AreaID, original.AreaID)
	}
	if parsed.Radius != original.Radius {
		t.Errorf("Radius = %d, want %d", parsed.Radius, original.Radius)
	}
	if parsed.StartTime != original.StartTime {
		t.Errorf("StartTime = %s, want %s", parsed.StartTime, original.StartTime)
	}
}

func TestMsgIDAreaRouteAlarmSet_NoConflict(t *testing.T) {
	if MsgIDAreaRouteAlarmSet != 0x8402 {
		t.Errorf("MsgIDAreaRouteAlarmSet = 0x%04X, want 0x8402", MsgIDAreaRouteAlarmSet)
	}
}

// FIX-4-4: 0x8403 AreaRouteAlarmDelMessage round-trip
func TestAreaRouteAlarmDelMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &AreaRouteAlarmDelMessage{AreaID: 0x12345678}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4", len(data))
	}
	body, err := codec.ParseBody(MsgIDAreaRouteAlarmDel, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*AreaRouteAlarmDelMessage)
	if !ok {
		t.Fatalf("expected *AreaRouteAlarmDelMessage, got %T", body)
	}
	if parsed.AreaID != original.AreaID {
		t.Errorf("AreaID = 0x%08X, want 0x%08X", parsed.AreaID, original.AreaID)
	}
}

func TestMsgIDAreaRouteAlarmDel_NoConflict(t *testing.T) {
	if MsgIDAreaRouteAlarmDel != 0x8403 {
		t.Errorf("MsgIDAreaRouteAlarmDel = 0x%04X, want 0x8403", MsgIDAreaRouteAlarmDel)
	}
}

// AUTO-FIX-2026-06-28: 0x8404-0x8407 电子运单类消息具体结构体验证（替换原 RawMessage 占位）
func TestEWaybillMessages(t *testing.T) {
	codec := NewCodec()

	// 0x8404 电子运单设置：4B长度 + 内容
	setData := append([]byte{0x00, 0x00, 0x00, 0x03}, []byte{0x01, 0x02, 0x03}...)
	body, err := codec.ParseBody(MsgIDEWaybillSet, setData)
	if err != nil {
		t.Fatalf("ParseBody(0x8404) failed: %v", err)
	}
	setMsg, ok := body.(*EWaybillSetMessage)
	if !ok {
		t.Fatalf("expected *EWaybillSetMessage, got %T", body)
	}
	if len(setMsg.WaybillData) != 3 {
		t.Errorf("EWaybillSetMessage.WaybillData length = %d, want 3", len(setMsg.WaybillData))
	}

	// 0x8405 电子运单删除：类型=0 全部删除
	delAllData := []byte{0x00}
	body, err = codec.ParseBody(MsgIDEWaybillDel, delAllData)
	if err != nil {
		t.Fatalf("ParseBody(0x8405 type=0) failed: %v", err)
	}
	delMsg, ok := body.(*EWaybillDelMessage)
	if !ok {
		t.Fatalf("expected *EWaybillDelMessage, got %T", body)
	}
	if delMsg.DelType != 0 {
		t.Errorf("EWaybillDelMessage.DelType = %d, want 0", delMsg.DelType)
	}

	// 0x8405 电子运单删除：类型=1 + 数量=1 + ID长度=3 + "ABC"
	delSpecData := []byte{0x01, 0x00, 0x01, 0x03, 'A', 'B', 'C'}
	body, err = codec.ParseBody(MsgIDEWaybillDel, delSpecData)
	if err != nil {
		t.Fatalf("ParseBody(0x8405 type=1) failed: %v", err)
	}
	delMsg, ok = body.(*EWaybillDelMessage)
	if !ok {
		t.Fatalf("expected *EWaybillDelMessage, got %T", body)
	}
	if delMsg.DelType != 1 || len(delMsg.IDs) != 1 || delMsg.IDs[0] != "ABC" {
		t.Errorf("EWaybillDelMessage unexpected: type=%d ids=%v", delMsg.DelType, delMsg.IDs)
	}

	// 0x8406 电子运单上传：无消息体
	body, err = codec.ParseBody(MsgIDEWaybillUpload, nil)
	if err != nil {
		t.Fatalf("ParseBody(0x8406) failed: %v", err)
	}
	if _, ok := body.(*EWaybillUploadMessage); !ok {
		t.Fatalf("expected *EWaybillUploadMessage, got %T", body)
	}

	// 0x8407 电子运单应答：流水号(2B) + 结果(1B)
	respData := []byte{0x12, 0x34, 0x00}
	body, err = codec.ParseBody(MsgIDEWaybillResp, respData)
	if err != nil {
		t.Fatalf("ParseBody(0x8407) failed: %v", err)
	}
	respMsg, ok := body.(*EWaybillRespMessage)
	if !ok {
		t.Fatalf("expected *EWaybillRespMessage, got %T", body)
	}
	if respMsg.SeqNum != 0x1234 || respMsg.Result != 0 {
		t.Errorf("EWaybillRespMessage = {SeqNum:0x%04X Result:%d}, want {0x1234, 0}", respMsg.SeqNum, respMsg.Result)
	}
}

// AUTO-FIX-2026-06-27: 常量重命名测试 [2026-06-27]
func TestMsgIDManualAlarmConfirm_NoConflict(t *testing.T) {
	if MsgIDManualAlarmConfirm != 0x8203 {
		t.Errorf("MsgIDManualAlarmConfirm = 0x%04X, want 0x8203", MsgIDManualAlarmConfirm)
	}
}

// AUTO-FIX-2026-06-27: 9xxx/8Axx/12xx/13xx 系列常量验证
func TestJT1078JT1045Constants(t *testing.T) {
	if MsgIDRealtimeAVReq1078 != 0x9101 {
		t.Errorf("MsgIDRealtimeAVReq1078 = 0x%04X, want 0x9101", MsgIDRealtimeAVReq1078)
	}
	if MsgIDRTPData1078 != 0x1200 {
		t.Errorf("MsgIDRTPData1078 = 0x%04X, want 0x1200", MsgIDRTPData1078)
	}
	if MsgIDADASAlarm != 0x8A00 {
		t.Errorf("MsgIDADASAlarm = 0x%04X, want 0x8A00", MsgIDADASAlarm)
	}
	if MsgIDDSMAlarm != 0x1205 {
		t.Errorf("MsgIDDSMAlarm = 0x%04X, want 0x1205", MsgIDDSMAlarm)
	}
	if MsgIDADASExtended != 0x1304 {
		t.Errorf("MsgIDADASExtended = 0x%04X, want 0x1304", MsgIDADASExtended)
	}
}

// AUTO-FIX-2026-07-02 [P3]: 0x0A00 / 0x8A00 常量别名冲突修复验证
// 验证点：
//  1. RSA 常量值正确且与旧别名同值（向后兼容）；
//  2. ParseBody 按 808-2019 标准将 0x0A00 分发至 RSAPublicKeyMessage（非 PassengerCountMessage），
//     0x8A00 分发至 RSADistributeMessage；
//  3. PassengerCountMessage Marshal/Unmarshal 往返仍可用（旧调用方显式解码兼容）。
//
// 核查背景：module-protocol-1045 拥有独立 codec（ADAS 报警=0x0901），不复用 jt808.ParseBody，
// 故 0x0A00 在 808 链路无运行时冲突。本测试固化该结论与分发行为，防止回归。
func TestRSAAliasResolution(t *testing.T) {
	// —— 1. 常量值与别名兼容性 ——
	if MsgIDRSAPublicKey != 0x0A00 {
		t.Errorf("MsgIDRSAPublicKey = 0x%04X, want 0x0A00", MsgIDRSAPublicKey)
	}
	if MsgIDRSADistribute != 0x8A00 {
		t.Errorf("MsgIDRSADistribute = 0x%04X, want 0x8A00", MsgIDRSADistribute)
	}
	// 旧别名保留同值（向后兼容）：调用方使用旧常量名仍指向同一 MsgID
	if MsgIDPassengerCount != MsgIDRSAPublicKey {
		t.Errorf("MsgIDPassengerCount = 0x%04X, MsgIDRSAPublicKey = 0x%04X, 应同值",
			MsgIDPassengerCount, MsgIDRSAPublicKey)
	}
	if MsgIDADASAlarm != MsgIDRSADistribute {
		t.Errorf("MsgIDADASAlarm = 0x%04X, MsgIDRSADistribute = 0x%04X, 应同值",
			MsgIDADASAlarm, MsgIDRSADistribute)
	}

	// —— 2. ParseBody 分发行为（808-2019 标准语义）——
	codec := NewCodec()

	// 0x0A00 → RSAPublicKeyMessage（132 字节：128 模数 + 4 指数）
	rsaData := make([]byte, 132)
	copy(rsaData[128:], []byte{0x00, 0x01, 0x00, 0x01}) // e = 65537
	body, err := codec.ParseBody(MsgIDRSAPublicKey, rsaData)
	if err != nil {
		t.Fatalf("ParseBody(0x0A00) error: %v", err)
	}
	if _, ok := body.(*RSAPublicKeyMessage); !ok {
		t.Fatalf("ParseBody(0x0A00) = %T, want *RSAPublicKeyMessage（不应误解析为 PassengerCountMessage）", body)
	}
	// 明确断言：0x0A00 不应分发至 PassengerCountMessage
	if _, ok := body.(*PassengerCountMessage); ok {
		t.Fatal("ParseBody(0x0A00) 误解析为 *PassengerCountMessage，应按 808-2019 标准分发至 *RSAPublicKeyMessage")
	}

	// 0x8A00 → RSADistributeMessage（≥128 字节）
	distData := make([]byte, 132)
	body2, err := codec.ParseBody(MsgIDRSADistribute, distData)
	if err != nil {
		t.Fatalf("ParseBody(0x8A00) error: %v", err)
	}
	if _, ok := body2.(*RSADistributeMessage); !ok {
		t.Fatalf("ParseBody(0x8A00) = %T, want *RSADistributeMessage", body2)
	}

	// —— 3. PassengerCountMessage 往返兼容（旧调用方显式构造/解码路径）——
	orig := &PassengerCountMessage{
		CountType: 0x01,
		CountData: []PassengerCountItem{
			{DoorID: 1, UpCount: 5, DownCount: 3},
			{DoorID: 2, UpCount: 8, DownCount: 6},
		},
	}
	buf, err := orig.Marshal()
	if err != nil {
		t.Fatalf("PassengerCountMessage.Marshal error: %v", err)
	}
	var decoded PassengerCountMessage
	if err := decoded.Unmarshal(buf); err != nil {
		t.Fatalf("PassengerCountMessage.Unmarshal error: %v", err)
	}
	if decoded.CountType != orig.CountType || len(decoded.CountData) != len(orig.CountData) {
		t.Fatalf("PassengerCount 往返不一致: got %+v, want %+v", decoded, orig)
	}
	if decoded.CountData[1].UpCount != orig.CountData[1].UpCount {
		t.Fatalf("PassengerCount 第二门 UpCount 不一致: got %d, want %d",
			decoded.CountData[1].UpCount, orig.CountData[1].UpCount)
	}
}