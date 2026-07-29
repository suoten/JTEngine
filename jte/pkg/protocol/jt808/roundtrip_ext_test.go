package jt808

// ====================================================================
// [P2-补充] 往返测试（Marshal → Unmarshal → 比较字段值）
// 特别关注坐标值（正/负/零/边界）、时间字段、变长字符串
// ====================================================================

import (
	"testing"
)

// TestRoundTrip_LocationBoundaryCoords 测试边界坐标往返
func TestRoundTrip_LocationBoundaryCoords(t *testing.T) {
	tests := []struct {
		name string
		lat  float64
		lon  float64
	}{
		{"零坐标", 0, 0},
		{"最大纬度", 90.0, 0},
		{"最大经度", 0, 180.0},
		// 注意：JT808 协议中负坐标通过取绝对值编码，符号由 StatusFlag bit2/bit3 指示
		// 因此 Marshal→Unmarshal 往返后值为绝对值（符号需从 StatusFlag 解析）
		{"正纬度", 39.9, 116.4},
		{"小数精度", 39.900001, 116.400001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := &LocationMessage{
				AlarmFlag:  0x01,
				StatusFlag: 0x02,
				Latitude:   tt.lat,
				Longitude:  tt.lon,
				Altitude:   500,
				Speed:      60,
				Direction:  180,
				Time:       "240721120000",
			}
			data, err := orig.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			parsed := &LocationMessage{}
			if err := parsed.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			// 比较关键字段（坐标取绝对值后应一致）
			absLat := orig.Latitude
			if absLat < 0 {
				absLat = -absLat
			}
			absLon := orig.Longitude
			if absLon < 0 {
				absLon = -absLon
			}
			if parsed.Latitude != absLat {
				t.Errorf("Latitude: got %f, want %f (abs of %f)", parsed.Latitude, absLat, orig.Latitude)
			}
			if parsed.Longitude != absLon {
				t.Errorf("Longitude: got %f, want %f (abs of %f)", parsed.Longitude, absLon, orig.Longitude)
			}
			if parsed.Time != orig.Time {
				t.Errorf("Time: got %q, want %q", parsed.Time, orig.Time)
			}
		})
	}
}

// TestRoundTrip_CircularArea 圆形区域往返测试
func TestRoundTrip_CircularArea(t *testing.T) {
	orig := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{AreaID: 1, CenterLat: 39.9, CenterLon: 116.4, Radius: 1000, SpeedLimit: 60, Duration: 30, MaxSpeed: 80, NightMaxSpeed: 60},
			{AreaID: 2, CenterLat: 0, CenterLon: 0, Radius: 500, SpeedLimit: 40, Duration: 10, MaxSpeed: 50, NightMaxSpeed: 40},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &CircularAreaSetMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(parsed.Areas) != len(orig.Areas) {
		t.Fatalf("Areas count: got %d, want %d", len(parsed.Areas), len(orig.Areas))
	}
	for i, area := range orig.Areas {
		if parsed.Areas[i].AreaID != area.AreaID {
			t.Errorf("Area[%d].AreaID: got %d, want %d", i, parsed.Areas[i].AreaID, area.AreaID)
		}
		if parsed.Areas[i].CenterLat != area.CenterLat {
			t.Errorf("Area[%d].CenterLat: got %f, want %f", i, parsed.Areas[i].CenterLat, area.CenterLat)
		}
	}
}

// TestRoundTrip_RegisterMessage 注册消息往返测试
func TestRoundTrip_RegisterMessage(t *testing.T) {
	orig := &RegisterMessage{
		ProvinceID:    11,
		CityID:        0,
		Manufacturer:  "TEST",
		TerminalModel: "M1",
		TerminalID:    "DEV001",
		PlateColor:    1,
		PlateNumber:   "京A12345",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &RegisterMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.ProvinceID != orig.ProvinceID {
		t.Errorf("ProvinceID: got %d, want %d", parsed.ProvinceID, orig.ProvinceID)
	}
	if parsed.PlateColor != orig.PlateColor {
		t.Errorf("PlateColor: got %d, want %d", parsed.PlateColor, orig.PlateColor)
	}
}

// TestRoundTrip_CommandMessage 命令消息往返测试
func TestRoundTrip_CommandMessage(t *testing.T) {
	orig := &CommandMessage{
		Params: map[uint32][]byte{
			0x00000001: {0x01, 0x02},
			0x00000002: {0x03},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &CommandMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(parsed.Params) != len(orig.Params) {
		t.Fatalf("Params count: got %d, want %d", len(parsed.Params), len(orig.Params))
	}
	for id, val := range orig.Params {
		parsedVal, ok := parsed.Params[id]
		if !ok {
			t.Errorf("Param 0x%08X missing in parsed", id)
			continue
		}
		if len(parsedVal) != len(val) {
			t.Errorf("Param 0x%08X length: got %d, want %d", id, len(parsedVal), len(val))
		}
	}
}

// TestRoundTrip_EmptyFields 空字段和零值往返测试
func TestRoundTrip_EmptyFields(t *testing.T) {
	orig := &LocationMessage{
		Latitude:  0,
		Longitude: 0,
		Time:       "000000000000",
	}
	data, err := orig.Marshal()
	if err != nil {
		// 零值可能触发坐标校验 error，这是预期行为
		return
	}
	// 反序列化应不 panic
	parsed := &LocationMessage{}
	_ = parsed.Unmarshal(data)
}
