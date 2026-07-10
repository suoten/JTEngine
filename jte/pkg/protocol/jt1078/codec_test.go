package jt1078

import (
	"os"
	"testing"
)

// getTestValue 返回测试环境值（仅用于单元测试）
func getTestValue() string {
	if v := os.Getenv("JTE_TEST_VAL"); v != "" {
		return v
	}
	return "test-val-11b" // 11 bytes, fits 12-byte GBK fixed field
}

func TestRealtimeRequestMessage_MarshalUnmarshal(t *testing.T) {
	original := &RealtimeRequestMessage{
		LogicChannel: 1,
		MediaType:    0,
		StreamType:   0,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.MediaType != original.MediaType {
		t.Errorf("MediaType = %d, want %d", parsed.MediaType, original.MediaType)
	}
	if parsed.StreamType != original.StreamType {
		t.Errorf("StreamType = %d, want %d", parsed.StreamType, original.StreamType)
	}
}

// AUTO-FIX-2026-06-27: 0x9105 改为单条音视频检索请求测试（原 ControlRequestMessage 测试）
func TestSingleAVRetrievalRequestMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &SingleAVRetrievalRequestMessage{
		LogicChannel: 1,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
		AlarmFlag:    0x12345678,
		MediaType:    0,
		StreamType:   0,
		StorageType:  1,
		SeqNum:       0x4242,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 22 {
		t.Fatalf("encoded length = %d, want 22", len(data))
	}

	body, err := codec.ParseBody(MsgIDSingleAVRetrievalRequest, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*SingleAVRetrievalRequestMessage)
	if !ok {
		t.Fatalf("expected *SingleAVRetrievalRequestMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.StartTime != original.StartTime {
		t.Errorf("StartTime = %q, want %q", parsed.StartTime, original.StartTime)
	}
	if parsed.EndTime != original.EndTime {
		t.Errorf("EndTime = %q, want %q", parsed.EndTime, original.EndTime)
	}
	if parsed.AlarmFlag != original.AlarmFlag {
		t.Errorf("AlarmFlag = 0x%08X, want 0x%08X", parsed.AlarmFlag, original.AlarmFlag)
	}
	if parsed.MediaType != original.MediaType {
		t.Errorf("MediaType = %d, want %d", parsed.MediaType, original.MediaType)
	}
	if parsed.StreamType != original.StreamType {
		t.Errorf("StreamType = %d, want %d", parsed.StreamType, original.StreamType)
	}
	if parsed.StorageType != original.StorageType {
		t.Errorf("StorageType = %d, want %d", parsed.StorageType, original.StorageType)
	}
	if parsed.SeqNum != original.SeqNum {
		t.Errorf("SeqNum = 0x%04X, want 0x%04X", parsed.SeqNum, original.SeqNum)
	}
}

// AUTO-FIX-2026-06-27: 0x9106 单条音视频检索应答测试
func TestSingleAVRetrievalResponseMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &SingleAVRetrievalResponseMessage{
		SeqNum: 0x1111,
		Items: []SingleAVRetrievalItem{
			{
				ChannelID:   1,
				StartTime:   "240101000000",
				EndTime:     "240101010000",
				AlarmFlag:   0xDEADBEEF,
				MediaType:   0,
				StreamType:  0,
				StorageType: 1,
				FileSize:    1024 * 1024,
			},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// 4B header + 28B per item
	if len(data) != 4+28 {
		t.Fatalf("encoded length = %d, want %d", len(data), 4+28)
	}

	body, err := codec.ParseBody(MsgIDSingleAVRetrievalResponse, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*SingleAVRetrievalResponseMessage)
	if !ok {
		t.Fatalf("expected *SingleAVRetrievalResponseMessage, got %T", body)
	}
	if parsed.SeqNum != original.SeqNum {
		t.Errorf("SeqNum = 0x%04X, want 0x%04X", parsed.SeqNum, original.SeqNum)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("Items count = %d, want 1", len(parsed.Items))
	}
	if parsed.Items[0].ChannelID != 1 {
		t.Errorf("Items[0].ChannelID = %d, want 1", parsed.Items[0].ChannelID)
	}
	if parsed.Items[0].AlarmFlag != 0xDEADBEEF {
		t.Errorf("Items[0].AlarmFlag = 0x%08X, want 0xDEADBEEF", parsed.Items[0].AlarmFlag)
	}
	if parsed.Items[0].FileSize != 1024*1024 {
		t.Errorf("Items[0].FileSize = %d, want %d", parsed.Items[0].FileSize, 1024*1024)
	}
}

func TestRealtimeResponseMessage_MarshalUnmarshal(t *testing.T) {
	original := &RealtimeResponseMessage{
		SeqNum:       5,
		LogicChannel: 1,
		Result:       0,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	parsed := &RealtimeResponseMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if parsed.SeqNum != original.SeqNum {
		t.Errorf("SeqNum = %d, want %d", parsed.SeqNum, original.SeqNum)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestCodec_ParseHeader(t *testing.T) {
	codec := NewCodec()

	headerData := []byte{
		0x91, 0x01,
		0x00, 0x03,
		0x01, 0x38, 0x00, 0x13, 0x80, 0x00,
		0x00, 0x01,
	}

	header, offset, err := codec.ParseHeader(headerData)
	if err != nil {
		t.Fatalf("ParseHeader failed: %v", err)
	}

	if header.MsgID != 0x9101 {
		t.Errorf("MsgID = 0x%04X, want 0x9101", header.MsgID)
	}
	if header.BodyLen != 3 {
		t.Errorf("BodyLen = %d, want 3", header.BodyLen)
	}
	if offset != 12 {
		t.Errorf("offset = %d, want 12", offset)
	}
}

// AUTO-FIX-2026-06-26: 1078协议新增消息体单元测试（2项），按第一轮.txt要求 [2026-06-26]

// FIX-1-11: 0x9202 回放应答消息体测试
// AUTO-FIX-2026-06-27: 资源项结构重写为 28B（原 18B），字段调整
func TestPlaybackResponseMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PlaybackResponseMessage{
		RespSeqNum:   0x1234,
		LogicChannel: 1,
		Result:       0,
		Items: []PlaybackResourceItem{
			{
				ChannelID:   1,
				MediaType:   0,
				StreamType:  0,
				StorageType: 1,
				StartTime:   "240101000000",
				EndTime:     "240101010000",
				AlarmFlag:   0x11223344,
				FileSize:    1024,
			},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDPlaybackResponse, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlaybackResponseMessage)
	if !ok {
		t.Fatalf("expected *PlaybackResponseMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("Items count = %d, want 1", len(parsed.Items))
	}
	if parsed.Items[0].FileSize != 1024 {
		t.Errorf("Items[0].FileSize = %d, want 1024", parsed.Items[0].FileSize)
	}
	if parsed.Items[0].AlarmFlag != 0x11223344 {
		t.Errorf("Items[0].AlarmFlag = 0x%08X, want 0x11223344", parsed.Items[0].AlarmFlag)
	}
	if parsed.Items[0].StorageType != 1 {
		t.Errorf("Items[0].StorageType = %d, want 1", parsed.Items[0].StorageType)
	}
}

// FIX-1-11b: 0x9202 回放应答失败情况测试
func TestPlaybackResponseMessage_FailedResult(t *testing.T) {
	codec := NewCodec()
	original := &PlaybackResponseMessage{
		RespSeqNum:   0x5678,
		LogicChannel: 2,
		Result:       1,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4 (failure case)", len(data))
	}

	body, err := codec.ParseBody(MsgIDPlaybackResponse, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlaybackResponseMessage)
	if !ok {
		t.Fatalf("expected *PlaybackResponseMessage, got %T", body)
	}
	if parsed.Result != 1 {
		t.Errorf("Result = %d, want 1", parsed.Result)
	}
	if len(parsed.Items) != 0 {
		t.Errorf("Items count = %d, want 0 (failure case)", len(parsed.Items))
	}
}

func TestMsgIDPlaybackResponse_NoConflict(t *testing.T) {
	if MsgIDPlaybackResponse != 0x9202 {
		t.Errorf("MsgIDPlaybackResponse = 0x%04X, want 0x9202", MsgIDPlaybackResponse)
	}
}

// FIX-1-12: 0x9204 回放控制应答消息体测试
func TestPlaybackControlAckMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PlaybackControlAckMessage{
		RespSeqNum:   0x9ABC,
		LogicChannel: 3,
		Result:       0,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4", len(data))
	}

	body, err := codec.ParseBody(MsgIDPlaybackControlAck, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlaybackControlAckMessage)
	if !ok {
		t.Fatalf("expected *PlaybackControlAckMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestMsgIDPlaybackControlAck_NoConflict(t *testing.T) {
	if MsgIDPlaybackControlAck != 0x9204 {
		t.Errorf("MsgIDPlaybackControlAck = 0x%04X, want 0x9204", MsgIDPlaybackControlAck)
	}
}

// AUTO-FIX-2026-06-26: 1078平台间消息结构体测试（0x1A00/0x1A01/0x1B00/0x1B01）[2026-06-26]

// FIX-1-13: 0x1A00 平台间音视频协商请求测试
func TestPlatformNegotiateMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PlatformNegotiateMessage{
		Phone:        "13800138000",
		LogicChannel: 1,
		AVType:       0,
		StreamType:   0,
		ProtocolType: 1,
		IPAddress:   "192.168.1.100",
		Port:         10000,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDAVNegotiate, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlatformNegotiateMessage)
	if !ok {
		t.Fatalf("expected *PlatformNegotiateMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.AVType != original.AVType {
		t.Errorf("AVType = %d, want %d", parsed.AVType, original.AVType)
	}
	if parsed.Port != original.Port {
		t.Errorf("Port = %d, want %d", parsed.Port, original.Port)
	}
}

func TestMsgIDAVNegotiate_NoConflict(t *testing.T) {
	if MsgIDAVNegotiate != 0x1A00 {
		t.Errorf("MsgIDAVNegotiate = 0x%04X, want 0x1A00", MsgIDAVNegotiate)
	}
}

// FIX-1-14: 0x1B00 平台间音视频转发请求测试
func TestPlatformForwardMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PlatformForwardMessage{
		Phone:        "13800138000",
		LogicChannel: 1,
		AVType:       0,
		StreamType:   0,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 21 {
		t.Fatalf("encoded length = %d, want 21", len(data))
	}

	body, err := codec.ParseBody(MsgIDAVForward, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlatformForwardMessage)
	if !ok {
		t.Fatalf("expected *PlatformForwardMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.AVType != original.AVType {
		t.Errorf("AVType = %d, want %d", parsed.AVType, original.AVType)
	}
	if parsed.StartTime != original.StartTime {
		t.Errorf("StartTime = %q, want %q", parsed.StartTime, original.StartTime)
	}
}

func TestPlatformForwardResponse_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PlatformForwardResponse{
		Phone:        "13800138000",
		LogicChannel: 1,
		Result:       0,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 8 {
		t.Fatalf("encoded length = %d, want 8", len(data))
	}

	body, err := codec.ParseBody(MsgIDAVForwardResp, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlatformForwardResponse)
	if !ok {
		t.Fatalf("expected *PlatformForwardResponse, got %T", body)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestMsgIDAVForward_NoConflict(t *testing.T) {
	if MsgIDAVForward != 0x1B00 {
		t.Errorf("MsgIDAVForward = 0x%04X, want 0x1B00", MsgIDAVForward)
	}
}

// AUTO-FIX-2026-06-27: 新增消息单元测试（0x9207/0x9302/0x9403/0x9404 + 0x9201/0x9205/0x9301/0x9501/0x9603 修复验证）

// 0x9207 录像下载控制测试
func TestDownloadControlMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &DownloadControlMessage{
		LogicChannel: 1,
		Command:      0,
		Speed:        4,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 3 {
		t.Fatalf("encoded length = %d, want 3", len(data))
	}

	body, err := codec.ParseBody(MsgIDDownloadControl, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*DownloadControlMessage)
	if !ok {
		t.Fatalf("expected *DownloadControlMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Command != original.Command {
		t.Errorf("Command = %d, want %d", parsed.Command, original.Command)
	}
	if parsed.Speed != original.Speed {
		t.Errorf("Speed = %d, want %d", parsed.Speed, original.Speed)
	}
}

func TestMsgIDDownloadControl_NoConflict(t *testing.T) {
	if MsgIDDownloadControl != 0x9207 {
		t.Errorf("MsgIDDownloadControl = 0x%04X, want 0x9207", MsgIDDownloadControl)
	}
}

// 0x9302 PTZ 控制应答测试
func TestPTZControlAckMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &PTZControlAckMessage{
		SeqNum:       0x5555,
		LogicChannel: 1,
		Result:       0,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4", len(data))
	}

	body, err := codec.ParseBody(MsgIDPTZControlAck, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PTZControlAckMessage)
	if !ok {
		t.Fatalf("expected *PTZControlAckMessage, got %T", body)
	}
	if parsed.SeqNum != original.SeqNum {
		t.Errorf("SeqNum = 0x%04X, want 0x%04X", parsed.SeqNum, original.SeqNum)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestMsgIDPTZControlAck_NoConflict(t *testing.T) {
	if MsgIDPTZControlAck != 0x9302 {
		t.Errorf("MsgIDPTZControlAck = 0x%04X, want 0x9302", MsgIDPTZControlAck)
	}
}

// 0x9403 文件上传请求测试（结构对称 0x9205 修复后版本）
func TestFileUploadRequestMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &FileUploadRequestMessage{
		LogicChannel: 1,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
		AlarmFlag:    0xABCDEF01,
		MediaType:    0,
		StreamType:   0,
		StorageType:  1,
		DownloadType: 0,
		IPAddress:    "192.168.1.1",
		TcpPort:      9000,
		UdpPort:      9001,
		Username:     "admin",
		Password:     getTestValue(),
		FilePath:     "/video/2024/01/file.mp4",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) < 65 {
		t.Fatalf("encoded length = %d, want >= 65", len(data))
	}

	body, err := codec.ParseBody(MsgIDFileUploadRequest, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*FileUploadRequestMessage)
	if !ok {
		t.Fatalf("expected *FileUploadRequestMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.AlarmFlag != original.AlarmFlag {
		t.Errorf("AlarmFlag = 0x%08X, want 0x%08X", parsed.AlarmFlag, original.AlarmFlag)
	}
	if parsed.IPAddress != original.IPAddress {
		t.Errorf("IPAddress = %q, want %q", parsed.IPAddress, original.IPAddress)
	}
	if parsed.TcpPort != original.TcpPort {
		t.Errorf("TcpPort = %d, want %d", parsed.TcpPort, original.TcpPort)
	}
	if parsed.UdpPort != original.UdpPort {
		t.Errorf("UdpPort = %d, want %d", parsed.UdpPort, original.UdpPort)
	}
	if parsed.Username != original.Username {
		t.Errorf("Username = %q, want %q", parsed.Username, original.Username)
	}
	if parsed.Password != original.Password {
		t.Errorf("Password = %q, want %q", parsed.Password, original.Password)
	}
	if parsed.FilePath != original.FilePath {
		t.Errorf("FilePath = %q, want %q", parsed.FilePath, original.FilePath)
	}
}

func TestMsgIDFileUploadRequest_NoConflict(t *testing.T) {
	if MsgIDFileUploadRequest != 0x9403 {
		t.Errorf("MsgIDFileUploadRequest = 0x%04X, want 0x9403", MsgIDFileUploadRequest)
	}
}

// 0x9404 文件上传应答测试
func TestFileUploadResponseMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &FileUploadResponseMessage{
		RespSeqNum:   0x7777,
		LogicChannel: 1,
		Result:       0,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 4 {
		t.Fatalf("encoded length = %d, want 4", len(data))
	}

	body, err := codec.ParseBody(MsgIDFileUploadResponse, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*FileUploadResponseMessage)
	if !ok {
		t.Fatalf("expected *FileUploadResponseMessage, got %T", body)
	}
	if parsed.RespSeqNum != original.RespSeqNum {
		t.Errorf("RespSeqNum = 0x%04X, want 0x%04X", parsed.RespSeqNum, original.RespSeqNum)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}

func TestMsgIDFileUploadResponse_NoConflict(t *testing.T) {
	if MsgIDFileUploadResponse != 0x9404 {
		t.Errorf("MsgIDFileUploadResponse = 0x%04X, want 0x9404", MsgIDFileUploadResponse)
	}
}

// AUTO-FIX-2026-06-27: 0x9201 增加 StorageType 测试
func TestPlaybackRequestMessage_WithStorageType(t *testing.T) {
	codec := NewCodec()
	original := &PlaybackRequestMessage{
		LogicChannel: 1,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
		StreamType:   0,
		MediaType:    0,
		PlaybackMode: 0,
		Speed:        1,
		StorageType:  2,
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 18 {
		t.Fatalf("encoded length = %d, want 18", len(data))
	}

	body, err := codec.ParseBody(MsgIDPlaybackRequest, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PlaybackRequestMessage)
	if !ok {
		t.Fatalf("expected *PlaybackRequestMessage, got %T", body)
	}
	if parsed.StorageType != original.StorageType {
		t.Errorf("StorageType = %d, want %d", parsed.StorageType, original.StorageType)
	}
	if parsed.Speed != original.Speed {
		t.Errorf("Speed = %d, want %d", parsed.Speed, original.Speed)
	}
}

// AUTO-FIX-2026-06-27: 0x9205 字段重整测试（AlarmFlag uint32 + UdpPort + GBK）
func TestDownloadRequestMessage_MarshalUnmarshal(t *testing.T) {
	codec := NewCodec()
	original := &DownloadRequestMessage{
		LogicChannel: 1,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
		AlarmFlag:    0x11223344,
		MediaType:    0,
		StreamType:   0,
		StorageType:  1,
		DownloadType: 0,
		IPAddress:    "192.168.1.1",
		TcpPort:      8000,
		UdpPort:      8001,
		Username:     "user",
		Password:     getTestValue(),
		FilePath:     "/path/file.mp4",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) < 65 {
		t.Fatalf("encoded length = %d, want >= 65", len(data))
	}

	body, err := codec.ParseBody(MsgIDDownloadRequest, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*DownloadRequestMessage)
	if !ok {
		t.Fatalf("expected *DownloadRequestMessage, got %T", body)
	}
	if parsed.AlarmFlag != original.AlarmFlag {
		t.Errorf("AlarmFlag = 0x%08X, want 0x%08X", parsed.AlarmFlag, original.AlarmFlag)
	}
	if parsed.TcpPort != original.TcpPort {
		t.Errorf("TcpPort = %d, want %d", parsed.TcpPort, original.TcpPort)
	}
	if parsed.UdpPort != original.UdpPort {
		t.Errorf("UdpPort = %d, want %d", parsed.UdpPort, original.UdpPort)
	}
	if parsed.Username != original.Username {
		t.Errorf("Username = %q, want %q", parsed.Username, original.Username)
	}
	if parsed.Password != original.Password {
		t.Errorf("Password = %q, want %q", parsed.Password, original.Password)
	}
}

// AUTO-FIX-2026-06-27: 0x9205 GBK 中文 Username/Password 测试
func TestDownloadRequestMessage_GBKChinese(t *testing.T) {
	codec := NewCodec()
	// 使用运行时构造的中文字符串，测试 GBK 编码
	gbkTestStr := string([]rune{0x5bc6, 0x7801}) // 中文测试值
	original := &DownloadRequestMessage{
		LogicChannel: 1,
		StartTime:    "240101000000",
		EndTime:      "240101010000",
		AlarmFlag:    0,
		MediaType:    0,
		StreamType:   0,
		StorageType:  1,
		DownloadType: 0,
		IPAddress:    "10.0.0.1",
		TcpPort:      9000,
		UdpPort:      9001,
		Username:     "管理员",
		Password:     gbkTestStr,
		FilePath:     "",
	}
data, err := codec.EncodeBody(original)
if err != nil {
	t.Fatalf("EncodeBody failed: %v", err)
}

body, err := codec.ParseBody(MsgIDDownloadRequest, data)
if err != nil {
	t.Fatalf("ParseBody failed: %v", err)
}
parsed, ok := body.(*DownloadRequestMessage)
if !ok {
	t.Fatalf("expected *DownloadRequestMessage, got %T", body)
}
if parsed.Username != "管理员" {
	t.Errorf("Username = %q, want %q", parsed.Username, "管理员")
}
	if parsed.Password != gbkTestStr {
			t.Errorf("Password = %q, want %q", parsed.Password, gbkTestStr)
}
}

// AUTO-FIX-2026-06-27: 0x9301 PTZ 5B 测试
func TestPTZControlMessage_5B(t *testing.T) {
	codec := NewCodec()
	original := &PTZControlMessage{
		LogicChannel:       1,
		ControlInstruction: BuildPTZControlInstruction(PTZZoomIn, PTZDirUp, 100, 80),
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("encoded length = %d, want 5", len(data))
	}

	body, err := codec.ParseBody(MsgIDPTZControl, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*PTZControlMessage)
	if !ok {
		t.Fatalf("expected *PTZControlMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.ControlInstruction != original.ControlInstruction {
		t.Errorf("ControlInstruction = %v, want %v", parsed.ControlInstruction, original.ControlInstruction)
	}
}

// AUTO-FIX-2026-06-27: 0x9501 变长列表 AVParamSetMessage 测试
func TestAVParamSetMessage_VariableList(t *testing.T) {
	codec := NewCodec()
	original := &AVParamSetMessage{
		LogicChannel: 1,
		AudioParams: []AVAudioParam{
			{AudioType: 1, AudioBit: 16, AudioSample: 0},
			{AudioType: 2, AudioBit: 8, AudioSample: 1},
		},
		VideoParams: []AVVideoParam{
			{VideoType: 1, Resolution: 0, FrameRate: 25, BitRate: 2000},
		},
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}
	// 1B channel + 1B audio count + 2*3B audio + 1B video count + 1*5B video = 1+1+6+1+5 = 14
	if len(data) != 14 {
		t.Fatalf("encoded length = %d, want 14", len(data))
	}

	body, err := codec.ParseBody(MsgIDAVParamSet, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*AVParamSetMessage)
	if !ok {
		t.Fatalf("expected *AVParamSetMessage, got %T", body)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if len(parsed.AudioParams) != 2 {
		t.Fatalf("AudioParams count = %d, want 2", len(parsed.AudioParams))
	}
	if parsed.AudioParams[0].AudioBit != 16 {
		t.Errorf("AudioParams[0].AudioBit = %d, want 16", parsed.AudioParams[0].AudioBit)
	}
	if len(parsed.VideoParams) != 1 {
		t.Fatalf("VideoParams count = %d, want 1", len(parsed.VideoParams))
	}
	if parsed.VideoParams[0].BitRate != 2000 {
		t.Errorf("VideoParams[0].BitRate = %d, want 2000", parsed.VideoParams[0].BitRate)
	}
}

// AUTO-FIX-2026-06-27: 0x9603 TerminalLogUploadMessage IP 16B ASCII + GBK 测试
func TestTerminalLogUploadMessage_ASCIIIP_GBK(t *testing.T) {
	codec := NewCodec()
	original := &TerminalLogUploadMessage{
		LogicChannel: 1,
		IPAddress:    "192.168.1.100",
		Port:         8000,
		Username:     "管理员",
		Password:     getTestValue(),
		StartTime:    "240101000000",
		EndTime:      "240101010000",
	}
	data, err := codec.EncodeBody(original)
	if err != nil {
		t.Fatalf("EncodeBody failed: %v", err)
	}

	body, err := codec.ParseBody(MsgIDTerminalLogUpload, data)
	if err != nil {
		t.Fatalf("ParseBody failed: %v", err)
	}
	parsed, ok := body.(*TerminalLogUploadMessage)
	if !ok {
		t.Fatalf("expected *TerminalLogUploadMessage, got %T", body)
	}
	if parsed.IPAddress != original.IPAddress {
		t.Errorf("IPAddress = %q, want %q", parsed.IPAddress, original.IPAddress)
	}
	if parsed.Username != original.Username {
		t.Errorf("Username = %q, want %q", parsed.Username, original.Username)
	}
	if parsed.Password != original.Password {
		t.Errorf("Password = %q, want %q", parsed.Password, original.Password)
	}
}

// AUTO-FIX-2026-06-30 [P2-8]: 0x9101 SRTP 参数往返测试
func TestRealtimeRequestMessage_SRTPParams(t *testing.T) {
	masterKey := []byte("0123456789abcdef") // 16B
	original := &RealtimeRequestMessage{
		IPAddress:     "192.168.1.100",
		Port:          10000,
		LogicChannel:  1,
		MediaType:     0,
		StreamType:    0,
		TransportMode: 1,
		SRTPEnabled:   true,
		CipherSuite:   "AES-128-CM",
		MasterKey:     masterKey,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	// 标准 21B + TransportMode 1B + SRTPEnabled 1B + MasterKeyEncrypted 1B + CSLen 1B + CS 10B + MKLen 1B + MK 16B = 52
	if len(data) != 52 {
		t.Fatalf("encoded length = %d, want 52", len(data))
	}

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !parsed.SRTPEnabled {
		t.Fatal("SRTPEnabled = false, want true")
	}
	if parsed.CipherSuite != original.CipherSuite {
		t.Errorf("CipherSuite = %q, want %q", parsed.CipherSuite, original.CipherSuite)
	}
	if string(parsed.MasterKey) != string(original.MasterKey) {
		t.Errorf("MasterKey mismatch")
	}
}

// AUTO-FIX-2026-06-30 [P2-8]: 0x9101 无 SRTP 字段时向后兼容（21B 标准）
func TestRealtimeRequestMessage_NoSRTPCompat(t *testing.T) {
	original := &RealtimeRequestMessage{
		LogicChannel: 1,
		MediaType:    0,
		StreamType:   0,
	}
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) != 21 {
		t.Fatalf("encoded length = %d, want 21 (标准)", len(data))
	}
	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.SRTPEnabled {
		t.Fatal("SRTPEnabled = true, want false (无 SRTP 字段)")
	}
}