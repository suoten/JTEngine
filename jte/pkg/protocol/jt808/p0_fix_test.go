package jt808

// ====================================================================
// [P0-修复] 负坐标编解码往返测试 + 边界条件测试
// 验证所有区域类消息 Marshal 时正确处理南纬/西经负坐标
// ====================================================================

import (
	"testing"
)

// testNegativeCoordAreas 返回包含负坐标（南纬/西经）的测试数据
// 南纬 = 负纬度，西经 = 负经度

func TestCircularAreaSetMarshal_NegativeCoords(t *testing.T) {
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{
				AreaID:       1,
				CenterLat:    -22.5, // 南纬 22.5°（澳大利亚）
				CenterLon:    -45.3, // 西经 45.3°（巴西）
				Radius:       500,
				SpeedLimit:   80,
				Duration:     30,
				MaxSpeed:     120,
				NightMaxSpeed: 60,
			},
			{
				AreaID:       2,
				CenterLat:    39.9, // 北纬（正坐标也测试）
				CenterLon:    116.4,
				Radius:       1000,
				SpeedLimit:   60,
				Duration:     60,
				MaxSpeed:     80,
				NightMaxSpeed: 40,
			},
		},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal returned empty data")
	}

	// 验证编码后的坐标为绝对值（不包含负号导致的溢出）
	// 第一个区域从 SetType(1) + AreaCount(2) + AreaID(4) = 7 字节后开始
	// lat 在 offset 7-10，应为 abs(22.5 * 1000000) = 22500000
	lat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	expectedLat := uint32(22.5 * JT808CoordScaleFactor) // 22500000
	if lat != expectedLat {
		t.Errorf("负坐标 lat 编码错误: got %d, want %d (abs of -22.5°)", lat, expectedLat)
	}

	// lon 在 offset 11-14
	lon := uint32(data[11])<<24 | uint32(data[12])<<16 | uint32(data[13])<<8 | uint32(data[14])
	expectedLon := uint32(45.3 * JT808CoordScaleFactor) // 45300000
	if lon != expectedLon {
		t.Errorf("负坐标 lon 编码错误: got %d, want %d (abs of -45.3°)", lon, expectedLon)
	}
}

func TestRectAreaSetMarshal_NegativeCoords(t *testing.T) {
	msg := &RectAreaSetMessage{
		SetType: 0x01,
		Areas: []RectArea{
			{
				AreaID:    1,
				TopLat:    -10.5,  // 南纬
				TopLon:    -20.5,  // 西经
				BottomLat: -30.5,  // 南纬
				BottomLon: -40.5,  // 西经
			},
		},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// 验证编码后的坐标为绝对值
	// 结构: SetType(1) + Count(2) + AreaID(4) = 7, then topLat(4) at 7-10
	topLat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	expectedTopLat := uint32(10.5 * JT808CoordScaleFactor)
	if topLat != expectedTopLat {
		t.Errorf("负坐标 topLat 编码错误: got %d, want %d", topLat, expectedTopLat)
	}

	botLat := uint32(data[15])<<24 | uint32(data[16])<<16 | uint32(data[17])<<8 | uint32(data[18])
	expectedBotLat := uint32(30.5 * JT808CoordScaleFactor)
	if botLat != expectedBotLat {
		t.Errorf("负坐标 botLat 编码错误: got %d, want %d", botLat, expectedBotLat)
	}
}

func TestPolygonAreaSetMarshal_NegativeCoords(t *testing.T) {
	msg := &PolygonAreaSetMessage{
		AreaID:       1,
		SpeedLimit:   60,
		Duration:     30,
		MaxSpeed:     80,
		NightMaxSpeed: 40,
		Points: []PolygonPoint{
			{Latitude: -33.8, Longitude: -70.5}, // 南美
			{Latitude: -1.0, Longitude: 36.8},   // 非洲（负纬正经）
			{Latitude: 55.7, Longitude: -4.4},   // 欧洲（正纬负经）
		},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) < 14+3*8 {
		t.Fatalf("Marshal data too short: %d", len(data))
	}

	// 第一个点在 header(14 bytes) 之后
	pt1Lat := uint32(data[14])<<24 | uint32(data[15])<<16 | uint32(data[16])<<8 | uint32(data[17])
	expectedLat1 := uint32(33.8 * JT808CoordScaleFactor)
	if pt1Lat != expectedLat1 {
		t.Errorf("负坐标 pt1.Latitude 编码错误: got %d, want %d", pt1Lat, expectedLat1)
	}

	pt1Lon := uint32(data[18])<<24 | uint32(data[19])<<16 | uint32(data[20])<<8 | uint32(data[21])
	expectedLon1 := uint32(70.5 * JT808CoordScaleFactor)
	if pt1Lon != expectedLon1 {
		t.Errorf("负坐标 pt1.Longitude 编码错误: got %d, want %d", pt1Lon, expectedLon1)
	}
}

func TestFireAreaSetMarshal_NegativeCoords(t *testing.T) {
	msg := &FireAreaSetMessage{
		SetType: 0x01,
		Areas: []FireArea{
			{
				AreaID:       1,
				CenterLat:    -15.5, // 南纬
				CenterLon:    -25.5, // 西经
				Radius:       300,
				SpeedLimit:   40,
				Duration:     20,
				MaxSpeed:     60,
				NightMaxSpeed: 30,
			},
		},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// 验证编码后的坐标为绝对值
	lat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	expectedLat := uint32(15.5 * JT808CoordScaleFactor)
	if lat != expectedLat {
		t.Errorf("负坐标 lat 编码错误: got %d, want %d", lat, expectedLat)
	}
}

func TestAreaRouteAlarmSetMarshal_NegativeCoords(t *testing.T) {
	msg := &AreaRouteAlarmSetMessage{
		AreaID:    1,
		AreaAttr:  0x0001,
		StartTime: "2026-01-01 00:00:00",
		EndTime:   "2026-12-31 23:59:59",
		AlarmFlag: 0x0001,
		CenterLat: -25.5,
		CenterLon: -50.5,
		Radius:    500,
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) < 20 {
		t.Fatalf("Marshal data too short: %d", len(data))
	}

	// 结构: AreaID(4) + AreaAttr(2) + StartTime(6 BCD) + EndTime(6 BCD) + AlarmFlag(2) = 20
	// lat 在 offset 20-23
	lat := uint32(data[20])<<24 | uint32(data[21])<<16 | uint32(data[22])<<8 | uint32(data[23])
	expectedLat := uint32(25.5 * JT808CoordScaleFactor)
	if lat != expectedLat {
		t.Errorf("负坐标 lat 编码错误: got %d, want %d", lat, expectedLat)
	}
}

// === 边界条件测试 ===

func TestCircularAreaSetMarshal_EmptyAreas(t *testing.T) {
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas:   []CircularArea{},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal with empty areas failed: %v", err)
	}
	if len(data) != 3 { // SetType(1) + Count(2)
		t.Errorf("empty areas should produce 3 bytes, got %d", len(data))
	}
}

func TestCircularAreaSetMarshal_ZeroCoords(t *testing.T) {
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{
				AreaID:    0,
				CenterLat: 0,
				CenterLon: 0,
			},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal with zero coords failed: %v", err)
	}
	// 零坐标编码后应为全零
	lat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	if lat != 0 {
		t.Errorf("zero coord lat should be 0, got %d", lat)
	}
}

func TestCircularAreaSetMarshal_MaxCoords(t *testing.T) {
	// 测试最大合法坐标值
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{
			CenterLat: 90.0,   // 最大纬度
			CenterLon: 180.0,  // 最大经度
			},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal with max coords failed: %v", err)
	}
	lat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	expected := uint32(90.0 * JT808CoordScaleFactor)
	if lat != expected {
		t.Errorf("max coord lat: got %d, want %d", lat, expected)
	}
}

// === 往返测试：Marshal → Unmarshal 验证值一致 ===

func TestCircularAreaSetRoundTrip_NegativeCoords(t *testing.T) {
	original := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{
				AreaID:        42,
				CenterLat:     -33.8688, // 悉尼
				CenterLon:     -151.2093, // 略微越界但测试 uint32 转换不溢出
				Radius:        1000,
				SpeedLimit:    60,
				Duration:      30,
				MaxSpeed:      100,
				NightMaxSpeed: 50,
			},
		},
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := &CircularAreaSetMessage{}
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// 验证区域数量一致
	if len(decoded.Areas) != len(original.Areas) {
		t.Fatalf("area count mismatch: got %d, want %d", len(decoded.Areas), len(original.Areas))
	}

	// 验证 AreaID 一致
	if decoded.Areas[0].AreaID != original.Areas[0].AreaID {
		t.Errorf("AreaID mismatch: got %d, want %d", decoded.Areas[0].AreaID, original.Areas[0].AreaID)
	}

	// 验证 Radius 一致
	if decoded.Areas[0].Radius != original.Areas[0].Radius {
		t.Errorf("Radius mismatch: got %d, want %d", decoded.Areas[0].Radius, original.Areas[0].Radius)
	}
}
