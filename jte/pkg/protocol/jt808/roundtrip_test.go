package jt808

// ====================================================================
// 工业级编解码往返测试矩阵
// 覆盖所有已注册消息类型，验证 Marshal → Unmarshal → 字段一致
// ====================================================================

import (
	"testing"
)

// === 0x0100 终端注册 ===

func TestRoundTrip_Register(t *testing.T) {
	tests := []struct {
		name string
		msg  RegisterMessage
	}{
		{"正常", RegisterMessage{ProvinceID: 11, CityID: 22, Manufacturer: "SUOTEN", TerminalModel: "JTE-V3", TerminalID: "T001", PlateColor: 1, PlateNumber: "京A12345"}},
		{"无车牌", RegisterMessage{ProvinceID: 0, CityID: 0, Manufacturer: "M", TerminalModel: "T", TerminalID: "X", PlateColor: 0}},
		{"超长制造商", RegisterMessage{ProvinceID: 65535, CityID: 65535, Manufacturer: "VERYLONGMANUFACTURER", TerminalModel: "MODEL", TerminalID: "ID123", PlateColor: 2, PlateNumber: "沪B99999"}},
		{"空字段", RegisterMessage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &RegisterMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.ProvinceID != tt.msg.ProvinceID {
				t.Errorf("ProvinceID: got %d, want %d", decoded.ProvinceID, tt.msg.ProvinceID)
			}
			if decoded.CityID != tt.msg.CityID {
				t.Errorf("CityID: got %d, want %d", decoded.CityID, tt.msg.CityID)
			}
			if decoded.PlateColor != tt.msg.PlateColor {
				t.Errorf("PlateColor: got %d, want %d", decoded.PlateColor, tt.msg.PlateColor)
			}
		})
	}
}

// === 0x0102 终端鉴权 ===

func TestRoundTrip_Auth(t *testing.T) {
	tests := []struct {
		name string
		msg  AuthMessage
	}{
		{"正常", AuthMessage{AuthCode: "ABC123456789"}},
		{"空", AuthMessage{AuthCode: ""}},
		{"超长", AuthMessage{AuthCode: "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &AuthMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.AuthCode != tt.msg.AuthCode {
				t.Errorf("AuthCode: got %q, want %q", decoded.AuthCode, tt.msg.AuthCode)
			}
		})
	}
}

// === 0x0200 位置上报（核心消息） ===

func TestRoundTrip_Location(t *testing.T) {
	tests := []struct {
		name string
		msg  LocationMessage
	}{
		{"正常北纬东经", LocationMessage{
			AlarmFlag: 0x01, StatusFlag: 0x01,
			Latitude: 39.9, Longitude: 116.4,
			Altitude: 500, Speed: 60, Direction: 180,
			Time: "240721120000",
		}},
		{"南纬西经", LocationMessage{
			AlarmFlag: 0, StatusFlag: 0,
			Latitude: -33.8, Longitude: -70.5,
			Altitude: 0, Speed: 0, Direction: 0,
			Time: "240721120000",
		}},
		{"零坐标", LocationMessage{
			Latitude: 0, Longitude: 0, Time: "240721120000",
		}},
		{"最大值", LocationMessage{
			AlarmFlag: 0xFFFFFFFF, StatusFlag: 0xFFFFFFFF,
			Latitude: 90.0, Longitude: 180.0,
			Altitude: 65535, Speed: 65535, Direction: 65535,
			Time: "991231235959",
		}},
		{"带扩展数据", LocationMessage{
			Latitude: 22.5, Longitude: 114.0, Time: "240721120000",
			ExtraData: []byte{0xFF, 0xFF, 0xFF, 0xFF},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &LocationMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.AlarmFlag != tt.msg.AlarmFlag {
				t.Errorf("AlarmFlag: got 0x%X, want 0x%X", decoded.AlarmFlag, tt.msg.AlarmFlag)
			}
			if decoded.StatusFlag != tt.msg.StatusFlag {
				t.Errorf("StatusFlag: got 0x%X, want 0x%X", decoded.StatusFlag, tt.msg.StatusFlag)
			}
			if decoded.Altitude != tt.msg.Altitude {
				t.Errorf("Altitude: got %d, want %d", decoded.Altitude, tt.msg.Altitude)
			}
			if decoded.Speed != tt.msg.Speed {
				t.Errorf("Speed: got %d, want %d", decoded.Speed, tt.msg.Speed)
			}
			if decoded.Direction != tt.msg.Direction {
				t.Errorf("Direction: got %d, want %d", decoded.Direction, tt.msg.Direction)
			}
		})
	}
}

// === 0x0002 心跳 ===

func TestRoundTrip_Heartbeat(t *testing.T) {
	msg := &HeartbeatMessage{}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("Heartbeat should marshal to empty, got %d bytes", len(data))
	}
	decoded := &HeartbeatMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
}

// === 0x8001 通用应答 ===

func TestRoundTrip_GeneralResponse(t *testing.T) {
	tests := []struct {
		name string
		msg  GeneralResponse
	}{
		{"正常", GeneralResponse{RespSeqNum: 1, RespMsgID: 0x0200, Result: 0}},
		{"最大值", GeneralResponse{RespSeqNum: 65535, RespMsgID: 0xFFFF, Result: 255}},
		{"零值", GeneralResponse{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &GeneralResponse{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.RespSeqNum != tt.msg.RespSeqNum {
				t.Errorf("RespSeqNum: got %d, want %d", decoded.RespSeqNum, tt.msg.RespSeqNum)
			}
			if decoded.RespMsgID != tt.msg.RespMsgID {
				t.Errorf("RespMsgID: got 0x%X, want 0x%X", decoded.RespMsgID, tt.msg.RespMsgID)
			}
			if decoded.Result != tt.msg.Result {
				t.Errorf("Result: got %d, want %d", decoded.Result, tt.msg.Result)
			}
		})
	}
}

// === 0x8100 注册应答 ===

func TestRoundTrip_RegisterResponse(t *testing.T) {
	tests := []struct {
		name string
		msg  RegisterResponse
	}{
		{"正常", RegisterResponse{RespSeqNum: 1, Result: 0, AuthCode: "AUTH123"}},
		{"无授权码", RegisterResponse{RespSeqNum: 100, Result: 1}},
		{"超长授权码", RegisterResponse{RespSeqNum: 65535, Result: 255, AuthCode: "0123456789012345678901234567890123456789"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &RegisterResponse{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.RespSeqNum != tt.msg.RespSeqNum {
				t.Errorf("RespSeqNum: got %d, want %d", decoded.RespSeqNum, tt.msg.RespSeqNum)
			}
			if decoded.Result != tt.msg.Result {
				t.Errorf("Result: got %d, want %d", decoded.Result, tt.msg.Result)
			}
			if decoded.AuthCode != tt.msg.AuthCode {
				t.Errorf("AuthCode: got %q, want %q", decoded.AuthCode, tt.msg.AuthCode)
			}
		})
	}
}

// === 0x8103 指令下发 ===

func TestRoundTrip_Command(t *testing.T) {
	tests := []struct {
		name string
		msg  CommandMessage
	}{
		{"单参数", CommandMessage{Params: map[uint32][]byte{1: {0x01, 0x02}}}},
		{"多参数", CommandMessage{Params: map[uint32][]byte{
			0x0001: {0x01},
			0x0002: {0x02, 0x03},
			0x0100: {0xFF, 0xFF},
		}}},
		{"空参数", CommandMessage{Params: map[uint32][]byte{}}},
		{"最大ID", CommandMessage{Params: map[uint32][]byte{0xFFFFFFFF: {0x00}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data1, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			// 确定性：编码两次结果相同
			data2, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("second Marshal failed: %v", err)
			}
			if len(data1) != len(data2) {
				t.Fatalf("non-deterministic: length %d vs %d", len(data1), len(data2))
			}
			for i := range data1 {
				if data1[i] != data2[i] {
					t.Fatalf("non-deterministic: byte %d mismatch", i)
				}
			}
			// 往返
			decoded := &CommandMessage{}
			if err := decoded.Unmarshal(data1); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if len(decoded.Params) != len(tt.msg.Params) {
				t.Errorf("Params count: got %d, want %d", len(decoded.Params), len(tt.msg.Params))
			}
			for id, val := range tt.msg.Params {
				got, ok := decoded.Params[id]
				if !ok {
					t.Errorf("Param 0x%X missing", id)
					continue
				}
				if len(got) != len(val) {
					t.Errorf("Param 0x%X length: got %d, want %d", id, len(got), len(val))
					continue
				}
				for i := range val {
					if got[i] != val[i] {
						t.Errorf("Param 0x%X byte %d: got 0x%02X, want 0x%02X", id, i, got[i], val[i])
					}
				}
			}
		})
	}
}

// === 0x8105 终端控制 ===

func TestRoundTrip_TerminalCtrl(t *testing.T) {
	tests := []struct {
		name string
		msg  TerminalCtrlMessage
	}{
		{"无线重启", TerminalCtrlMessage{CtrlType: 0x01}},
		{"远程关机", TerminalCtrlMessage{CtrlType: 0x02, Param: []byte{0x01}}},
		{"带参数", TerminalCtrlMessage{CtrlType: 0x03, Param: []byte{0xAA, 0xBB, 0xCC, 0xDD}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &TerminalCtrlMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.CtrlType != tt.msg.CtrlType {
				t.Errorf("CtrlType: got %d, want %d", decoded.CtrlType, tt.msg.CtrlType)
			}
			if len(decoded.Param) != len(tt.msg.Param) {
				t.Errorf("Param length: got %d, want %d", len(decoded.Param), len(tt.msg.Param))
			}
		})
	}
}

// === 0x8500 车辆控制 ===

func TestRoundTrip_VehicleControl(t *testing.T) {
	tests := []struct {
		name string
		msg  VehicleControlMessage
	}{
		{"车辆锁定", VehicleControlMessage{ControlType: 0x01}},
		{"车辆解锁", VehicleControlMessage{ControlType: 0x02}},
		{"断油断电", VehicleControlMessage{ControlType: 0x03}},
		{"恢复油电", VehicleControlMessage{ControlType: 0x04}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &VehicleControlMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.ControlType != tt.msg.ControlType {
				t.Errorf("ControlType: got %d, want %d", decoded.ControlType, tt.msg.ControlType)
			}
		})
	}
}

// === 0x8300 文本下发 ===

func TestRoundTrip_TextSend(t *testing.T) {
	tests := []struct {
		name string
		msg  TextSendMessage
	}{
		{"短文本", TextSendMessage{Sign: 0x01, Text: "Hello"}},
		{"空文本", TextSendMessage{Sign: 0x00, Text: ""}},
		{"中文文本", TextSendMessage{Sign: 0x01, Text: "请减速慢行"}},
		{"超长文本", TextSendMessage{Sign: 0x01, Text: string(make([]byte, 500))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			decoded := &TextSendMessage{}
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.Sign != tt.msg.Sign {
				t.Errorf("Sign: got %d, want %d", decoded.Sign, tt.msg.Sign)
			}
			if decoded.Text != tt.msg.Text {
				t.Errorf("Text: got %q, want %q", decoded.Text, tt.msg.Text)
			}
		})
	}
}

// === 0x0702 驾驶员身份 ===

func TestRoundTrip_DriverID(t *testing.T) {
	msg := &DriverIDMessage{
		Status:  0x01,
		Time:    "240721120000",
		DriverID: "110101199001011234",
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &DriverIDMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Status != msg.Status {
		t.Errorf("Status: got %d, want %d", decoded.Status, msg.Status)
	}
}

// === 0x0801 多媒体事件 ===

func TestRoundTrip_Multimedia(t *testing.T) {
	msg := &MultimediaMessage{
		MultimediaID: 12345,
		MultimediaType: 0,  // 图像
		MultimediaFmt: 1,   // JPEG
		EventItem: 0,       // 平台下发
		ChannelID: 1,
		Location: LocationMessage{
			Latitude: 39.9, Longitude: 116.4,
			Altitude: 500, Speed: 60, Direction: 180,
			Time: "240721120000",
		},
		MediaLen: 1024,
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &MultimediaMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.MultimediaID != msg.MultimediaID {
		t.Errorf("MultimediaID: got %d, want %d", decoded.MultimediaID, msg.MultimediaID)
	}
	if decoded.MultimediaType != msg.MultimediaType {
		t.Errorf("MultimediaType: got %d, want %d", decoded.MultimediaType, msg.MultimediaType)
	}
	if decoded.ChannelID != msg.ChannelID {
		t.Errorf("ChannelID: got %d, want %d", decoded.ChannelID, msg.ChannelID)
	}
	if decoded.MediaLen != msg.MediaLen {
		t.Errorf("MediaLen: got %d, want %d", decoded.MediaLen, msg.MediaLen)
	}
}

// === 0x8600 圆形区域设置 ===

func TestRoundTrip_CircularAreaSet(t *testing.T) {
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{AreaID: 1, CenterLat: -33.8, CenterLon: -70.5, Radius: 500, SpeedLimit: 80, Duration: 30, MaxSpeed: 120, NightMaxSpeed: 60},
			{AreaID: 2, CenterLat: 39.9, CenterLon: 116.4, Radius: 1000, SpeedLimit: 60, Duration: 60, MaxSpeed: 80, NightMaxSpeed: 40},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &CircularAreaSetMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(decoded.Areas) != len(msg.Areas) {
		t.Fatalf("Areas count: got %d, want %d", len(decoded.Areas), len(msg.Areas))
	}
	for i, area := range msg.Areas {
		if decoded.Areas[i].AreaID != area.AreaID {
			t.Errorf("Area[%d].AreaID: got %d, want %d", i, decoded.Areas[i].AreaID, area.AreaID)
		}
		if decoded.Areas[i].Radius != area.Radius {
			t.Errorf("Area[%d].Radius: got %d, want %d", i, decoded.Areas[i].Radius, area.Radius)
		}
	}
}

// === 0x8602 矩形区域设置 ===

func TestRoundTrip_RectAreaSet(t *testing.T) {
	msg := &RectAreaSetMessage{
		SetType: 0x01,
		Areas: []RectArea{
			{AreaID: 1, TopLat: 40.0, TopLon: 116.0, BottomLat: 39.0, BottomLon: 117.0, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 40},
			{AreaID: 2, TopLat: -10.0, TopLon: -20.0, BottomLat: -30.0, BottomLon: -40.0, SpeedLimit: 40, Duration: 20, MaxSpeed: 60, NightMaxSpeed: 30},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &RectAreaSetMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(decoded.Areas) != len(msg.Areas) {
		t.Fatalf("Areas count: got %d, want %d", len(decoded.Areas), len(msg.Areas))
	}
	for i, area := range msg.Areas {
		if decoded.Areas[i].AreaID != area.AreaID {
			t.Errorf("Area[%d].AreaID: got %d, want %d", i, decoded.Areas[i].AreaID, area.AreaID)
		}
	}
}

// === 0x8604 多边形区域设置 ===

func TestRoundTrip_PolygonAreaSet(t *testing.T) {
	msg := &PolygonAreaSetMessage{
		AreaID:        1,
		SpeedLimit:    60,
		Duration:      30,
		MaxSpeed:      80,
		NightMaxSpeed: 40,
		Points: []PolygonPoint{
			{Latitude: 39.9, Longitude: 116.4},
			{Latitude: -33.8, Longitude: -70.5},
			{Latitude: 0, Longitude: 0},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &PolygonAreaSetMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.AreaID != msg.AreaID {
		t.Errorf("AreaID: got %d, want %d", decoded.AreaID, msg.AreaID)
	}
	if len(decoded.Points) != len(msg.Points) {
		t.Fatalf("Points count: got %d, want %d", len(decoded.Points), len(msg.Points))
	}
}

// === 0x8608 火灾区域设置 ===

func TestRoundTrip_FireAreaSet(t *testing.T) {
	msg := &FireAreaSetMessage{
		SetType: 0x01,
		Areas: []FireArea{
			{AreaID: 1, CenterLat: -15.5, CenterLon: -25.5, Radius: 300, SpeedLimit: 40, Duration: 20, MaxSpeed: 60, NightMaxSpeed: 30},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &FireAreaSetMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(decoded.Areas) != len(msg.Areas) {
		t.Fatalf("Areas count: got %d, want %d", len(decoded.Areas), len(msg.Areas))
	}
}

// === 0x0704 批量位置上报 ===

func TestRoundTrip_LocationBatch(t *testing.T) {
	msg := &LocationBatchMessage{
		LocationType: 0x01,
		Locations: []*LocationMessage{
			{Latitude: 39.9, Longitude: 116.4, Altitude: 500, Speed: 60, Direction: 180, Time: "240721120000"},
			{Latitude: -33.8, Longitude: -70.5, Altitude: 0, Speed: 0, Direction: 0, Time: "240721120100"},
		},
	}
	msg.Count = uint16(len(msg.Locations))
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &LocationBatchMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.LocationType != msg.LocationType {
		t.Errorf("LocationType: got %d, want %d", decoded.LocationType, msg.LocationType)
	}
	if len(decoded.Locations) != len(msg.Locations) {
		t.Errorf("Locations count: got %d, want %d", len(decoded.Locations), len(msg.Locations))
	}
}

// === 0x8201 位置查询 ===

func TestRoundTrip_LocationQuery(t *testing.T) {
	msg := &LocationQueryMessage{}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &LocationQueryMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
}

// === 0x0201 位置查询应答 ===

func TestRoundTrip_LocationQueryResp(t *testing.T) {
	msg := &LocationQueryResponse{
		Location: LocationMessage{
			Latitude: 39.9, Longitude: 116.4,
			Altitude: 500, Speed: 60, Direction: 180,
			Time: "240721120000",
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &LocationQueryResponse{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Location.Altitude != msg.Location.Altitude {
		t.Errorf("Altitude: got %d, want %d", decoded.Location.Altitude, msg.Location.Altitude)
	}
}

// === 0x8801 摄像头立即拍摄命令 ===

func TestRoundTrip_PhotoCommand(t *testing.T) {
	msg := &PhotoCommandMessage{
		ChannelID:  1,
		Cmd:        0, // 立即拍摄
		Time:       10,
		SaveFlag:   1,
		Resolution: 0x01,
		Quality:    80,
		Brightness: 0,
		Contrast:   0,
		Saturation: 0,
		Chroma:     0,
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &PhotoCommandMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.ChannelID != msg.ChannelID {
		t.Errorf("ChannelID: got %d, want %d", decoded.ChannelID, msg.ChannelID)
	}
	if decoded.Quality != msg.Quality {
		t.Errorf("Quality: got %d, want %d", decoded.Quality, msg.Quality)
	}
}

// === 0x0003 终端注销 ===

func TestRoundTrip_TerminalCancel(t *testing.T) {
	msg := &TerminalCancelMessage{}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &TerminalCancelMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
}

// === 0x0701 电子运单 ===

func TestRoundTrip_ElectronicWaybill(t *testing.T) {
	msg := &ElectronicWaybillMessage{
		Content: "EWAYBILL_DATA_12345",
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	decoded := &ElectronicWaybillMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Content != msg.Content {
		t.Errorf("Content: got %q, want %q", decoded.Content, msg.Content)
	}
}
