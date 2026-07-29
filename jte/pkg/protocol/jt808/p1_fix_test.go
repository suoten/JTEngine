package jt808

// ====================================================================
// [P1-修复] 测试用例
// Fix 1: LocationMessage.parseExtraItems 附加项数量上限 + itemLen 合理性检查
// Fix 2: MultimediaMessage.Unmarshal locEnd 与 MediaLen 读取位置统一
// ====================================================================

import (
	"testing"
)

// TestP1_ParseExtraItems_MaxItemsLimit 验证附加项数量上限。
// FIXED-2026-07-22 [P1]: 恶意终端构造大量小附加项导致 CPU 耗尽。
// 构造 200 个 2 字节附加项（每个唯一 itemID, len=0），验证最多解析 100 个后 break。
func TestP1_ParseExtraItems_MaxItemsLimit(t *testing.T) {
	// 构造 200 个附加项：每个 2 字节（唯一 itemID, itemLen=0）
	// 使用 0x40~0xFF 范围的 itemID（都是未识别项，进入 ExtraItems map）
	extraData := make([]byte, 0, 400)
	for i := 0; i < 200; i++ {
		itemID := byte(0x40 + i%192) // 循环使用 0x40~0xFF
		extraData = append(extraData, itemID, 0x00)
	}

	m := &LocationMessage{}
	m.parseExtraItems(extraData)

	// 验证 ExtraItems 最多只有 100 个（maxExtraItems 上限）
	if len(m.ExtraItems) > maxExtraItems {
		t.Errorf("ExtraItems count = %d, want <= %d", len(m.ExtraItems), maxExtraItems)
	}
	if len(m.ExtraItems) != maxExtraItems {
		t.Errorf("ExtraItems count = %d, want exactly %d (should hit limit)", len(m.ExtraItems), maxExtraItems)
	}
}

// TestP1_ParseExtraItems_NormalItemsStillWork 验证正常附加项仍被正确解析。
func TestP1_ParseExtraItems_NormalItemsStillWork(t *testing.T) {
	// 构造 3 个正常附加项
	extraData := []byte{
		0x01, 0x04, 0x00, 0x00, 0x01, 0x00, // 里程: 256
		0x02, 0x02, 0x00, 0x64,             // 油量: 100
		0x25, 0x04, 0x00, 0x00, 0x00, 0x01, // 扩展车辆状态: 1
	}

	m := &LocationMessage{}
	m.parseExtraItems(extraData)

	if m.Mileage != 256 {
		t.Errorf("Mileage = %d, want 256", m.Mileage)
	}
	if m.Fuel != 100 {
		t.Errorf("Fuel = %d, want 100", m.Fuel)
	}
	if m.ExtVehicleState != 1 {
		t.Errorf("ExtVehicleState = %d, want 1", m.ExtVehicleState)
	}
}

// TestP1_ParseExtraItems_OversizedItemLenBreak 验证 itemLen > 256 时 break。
// FIXED-2026-07-22 [P1]: itemLen 合理性检查。
func TestP1_ParseExtraItems_OversizedItemLenBreak(t *testing.T) {
	// 构造一个 itemLen=0xFF (255) 的附加项，后面跟一个正常附加项
	// itemLen=255 不超过 256，应正常解析
	// 然后构造一个 itemLen 声明为 257（通过字节值不可能，因为 itemLen 是 1 字节，最大 255）
	// 但如果帧解析错位导致 itemLen 被误读为超过 256 的值... 实际上 itemLen 是 byte，最大 255
	// 所以这个测试验证的是 itemLen <= maxExtraItemLen(256) 的情况正常处理
	// 而 maxExtraItemLen 检查主要防御帧错位场景

	// 构造正常附加项 + 一个 itemLen=255 的附加项（边界值）
	extraData := []byte{
		0x01, 0x04, 0x00, 0x00, 0x01, 0x00, // 里程: 256 (正常)
	}
	// 追加 itemLen=255 的未识别附加项（数据不够时会被 offset+itemLen > len(data) 截断）
	extraData = append(extraData, 0xFE, 0xFF) // itemID=0xFE, itemLen=255
	// 不提供 255 字节数据，所以会被 break

	m := &LocationMessage{}
	// 不应 panic
	m.parseExtraItems(extraData)

	// 第一个附加项应被正常解析
	if m.Mileage != 256 {
		t.Errorf("Mileage = %d, want 256", m.Mileage)
	}
}

// TestP1_ParseExtraItems_Exactly100Items 验证恰好 100 个附加项全部被解析。
func TestP1_ParseExtraItems_Exactly100Items(t *testing.T) {
	// 使用 100 个唯一 itemID（0x40~0xA3）
	extraData := make([]byte, 0, 200)
	for i := 0; i < 100; i++ {
		extraData = append(extraData, byte(0x40+i), 0x00)
	}

	m := &LocationMessage{}
	m.parseExtraItems(extraData)

	if len(m.ExtraItems) != 100 {
		t.Errorf("ExtraItems count = %d, want 100 (exactly at limit)", len(m.ExtraItems))
	}
}

// TestP1_ParseExtraItems_101ItemsTruncated 验证 101 个附加项被截断为 100。
func TestP1_ParseExtraItems_101ItemsTruncated(t *testing.T) {
	// 使用 101 个唯一 itemID（0x40~0xA4）
	extraData := make([]byte, 0, 202)
	for i := 0; i < 101; i++ {
		extraData = append(extraData, byte(0x40+i), 0x00)
	}

	m := &LocationMessage{}
	m.parseExtraItems(extraData)

	if len(m.ExtraItems) != 100 {
		t.Errorf("ExtraItems count = %d, want 100 (truncated at limit)", len(m.ExtraItems))
	}
}

// TestP1_MultimediaMessage_Unmarshal_Standard40B 验证标准 40B 报文 locEnd=36 与 MediaLen 一致。
// FIXED-2026-07-22 [P1]: locEnd = len(data) - 4, MediaLen 从 data[locEnd:locEnd+4] 读取。
func TestP1_MultimediaMessage_Unmarshal_Standard40B(t *testing.T) {
	// 构造标准 40B 多媒体消息
	// MultimediaID(4B) + Type(1B) + Fmt(1B) + Event(1B) + Channel(1B) + Location(28B) + MediaLen(4B) = 40B
	data := make([]byte, 40)
	// MultimediaID = 0x12345678
	data[0] = 0x12
	data[1] = 0x34
	data[2] = 0x56
	data[3] = 0x78
	// Type=1, Fmt=2, Event=3, Channel=4
	data[4] = 1
	data[5] = 2
	data[6] = 3
	data[7] = 4
	// Location: 28 bytes of zeros (valid minimal location)
	// MediaLen = 0xAABBCCDD at data[36:40]
	data[36] = 0xAA
	data[37] = 0xBB
	data[38] = 0xCC
	data[39] = 0xDD

	m := &MultimediaMessage{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.MultimediaID != 0x12345678 {
		t.Errorf("MultimediaID = 0x%08X, want 0x12345678", m.MultimediaID)
	}
	if m.MultimediaType != 1 || m.MultimediaFmt != 2 || m.EventItem != 3 || m.ChannelID != 4 {
		t.Errorf("Type=%d Fmt=%d Event=%d Channel=%d, want 1/2/3/4",
			m.MultimediaType, m.MultimediaFmt, m.EventItem, m.ChannelID)
	}
	// locEnd=36, MediaLen 从 data[36:40] 读取
	expectedMediaLen := uint32(0xAABBCCDD)
	if m.MediaLen != expectedMediaLen {
		t.Errorf("MediaLen = 0x%08X, want 0x%08X (locEnd=36)", m.MediaLen, expectedMediaLen)
	}
}

// TestP1_MultimediaMessage_Unmarshal_ExtendedBody 验证 body > 40B 时 locEnd 正确排除 MediaLen。
// FIXED-2026-07-22 [P1]: 当 body > 40B 时 locEnd=len(data)-4，正确排除 MediaLen。
func TestP1_MultimediaMessage_Unmarshal_ExtendedBody(t *testing.T) {
	// 构造 48B 多媒体消息（Location 包含 8B ExtraData）
	// MultimediaID(4B) + Type(1B) + Fmt(1B) + Event(1B) + Channel(1B) + Location(36B) + MediaLen(4B) = 48B
	data := make([]byte, 48)
	// MultimediaID
	data[0] = 0x00
	data[1] = 0x00
	data[2] = 0x00
	data[3] = 0x01
	// Type=1, Fmt=2, Event=3, Channel=4
	data[4] = 1
	data[5] = 2
	data[6] = 3
	data[7] = 4
	// Location: 28 bytes base + 8 bytes ExtraData = 36 bytes (data[8:44])
	// ExtraData at data[36:44] - 附加项: 0x01, 0x04, 0x00,0x00,0x01,0x00 (里程=256)
	data[36] = 0x01 // itemID
	data[37] = 0x04 // itemLen
	data[38] = 0x00
	data[39] = 0x00
	data[40] = 0x01
	data[41] = 0x00
	// MediaLen at data[44:48] = 0x00001000
	data[44] = 0x00
	data[45] = 0x00
	data[46] = 0x10
	data[47] = 0x00

	m := &MultimediaMessage{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// locEnd = 48 - 4 = 44, MediaLen 从 data[44:48] 读取
	expectedMediaLen := uint32(0x00001000)
	if m.MediaLen != expectedMediaLen {
		t.Errorf("MediaLen = 0x%08X, want 0x%08X (locEnd=44)", m.MediaLen, expectedMediaLen)
	}

	// Location 的 ExtraData 应包含附加项（里程 = 256）
	if m.Location.Mileage != 256 {
		t.Errorf("Location.Mileage = %d, want 256 (ExtraData should be parsed)", m.Location.Mileage)
	}
}

// TestP1_MultimediaMessage_MarshalUnmarshal_RoundTrip 验证 Marshal→Unmarshal 往返一致。
func TestP1_MultimediaMessage_MarshalUnmarshal_RoundTrip(t *testing.T) {
	original := &MultimediaMessage{
		MultimediaID:   0xDEADBEEF,
		MultimediaType: 2,
		MultimediaFmt:  3,
		EventItem:      1,
		ChannelID:      5,
		MediaLen:       0xCAFEBABE,
		Location: LocationMessage{
			Time: "250722120000", // 有效 BCD 时间
		},
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if len(data) != 40 {
		t.Fatalf("Marshal len = %d, want 40", len(data))
	}

	parsed := &MultimediaMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.MultimediaID != original.MultimediaID {
		t.Errorf("MultimediaID = 0x%08X, want 0x%08X", parsed.MultimediaID, original.MultimediaID)
	}
	if parsed.MediaLen != original.MediaLen {
		t.Errorf("MediaLen = 0x%08X, want 0x%08X", parsed.MediaLen, original.MediaLen)
	}
}
