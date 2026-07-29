package jt808

// ====================================================================
// [P2-补充] 协议编解码模糊测试 — 扩展
// 覆盖：LocationMessage、RegisterMessage、CommandMessage、所有区域消息
// 运行：go test -fuzz=FuzzLocation -fuzztime=30s
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// FuzzLocationMessageUnmarshal 模糊测试 LocationMessage.Unmarshal
func FuzzLocationMessageUnmarshal(f *testing.F) {
	// 种子语料：正常位置报告、边界值、异常数据
	normal := make([]byte, 28)
	binary.BigEndian.PutUint32(normal[4:8], 39<<24|90000000) // lat
	binary.BigEndian.PutUint32(normal[8:12], 116<<24|40000000) // lon
	copy(normal[20:26], []byte("240721120000"))

	f.Add(normal)          // 合法 28 字节
	f.Add(make([]byte, 0)) // 空
	f.Add(make([]byte, 1)) // 单字节
	f.Add(make([]byte, 27)) // 不足
	f.Add(make([]byte, 100)) // 超长
	f.Add(make([]byte, 255)) // 最大 fuzz input

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &LocationMessage{}
		_ = msg.Unmarshal(data)
		// 确保不 panic
	})
}

// FuzzRegisterMessageUnmarshal 模糊测试 RegisterMessage.Unmarshal
func FuzzRegisterMessageUnmarshal(f *testing.F) {
	f.Add(make([]byte, 37))  // 最小合法
	f.Add(make([]byte, 0))   // 空
	f.Add(make([]byte, 1))   // 单字节
	f.Add(make([]byte, 200)) // 超长

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &RegisterMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzCircularAreaSetUnmarshal 模糊测试 CircularAreaSetMessage.Unmarshal
func FuzzCircularAreaSetUnmarshal(f *testing.F) {
	// 合法：3B头 + 1个24B区域
	seed := make([]byte, 27)
	binary.BigEndian.PutUint16(seed[1:3], 1) // count=1
	f.Add(seed)

	// 声明10个但只有2个数据
	short := make([]byte, 51)
	binary.BigEndian.PutUint16(short[1:3], 10) // count=10
	f.Add(short)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 3)) // 只有头部
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &CircularAreaSetMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzRectAreaSetUnmarshal 模糊测试 RectAreaSetMessage.Unmarshal
func FuzzRectAreaSetUnmarshal(f *testing.F) {
	seed := make([]byte, 31) // 3B头 + 1个28B区域
	binary.BigEndian.PutUint16(seed[1:3], 1)
	f.Add(seed)

	huge := make([]byte, 3)
	binary.BigEndian.PutUint16(huge[1:3], 65535) // count=65535
	f.Add(huge)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &RectAreaSetMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzPolygonAreaSetUnmarshal 模糊测试 PolygonAreaSetMessage.Unmarshal
func FuzzPolygonAreaSetUnmarshal(f *testing.F) {
	seed := make([]byte, 22) // 14B头 + 1个8B点
	binary.BigEndian.PutUint16(seed[12:14], 1)
	f.Add(seed)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 14))
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &PolygonAreaSetMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzCommandMessageUnmarshal 模糊测试 CommandMessage.Unmarshal
func FuzzCommandMessageUnmarshal(f *testing.F) {
	// 合法：1B count + 1个param(5B header + 2B value)
	seed := make([]byte, 8)
	seed[0] = 1 // count=1
	binary.BigEndian.PutUint32(seed[1:5], 0x00000001) // paramID
	seed[5] = 2 // paramLen
	seed[6] = 0xAA
	seed[7] = 0xBB
	f.Add(seed)

	// 声明 255 个参数但只有 1 个数据
	short := make([]byte, 8)
	short[0] = 255
	f.Add(short)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 1))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &CommandMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzCanDataUnmarshal 模糊测试 CanDataMessage.Unmarshal
func FuzzCanDataUnmarshal(f *testing.F) {
	seed := make([]byte, 12) // 6B time + 1B count=1 + 5B item header
	seed[6] = 1
	f.Add(seed)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 7)) // 只有头部
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &CanDataMessage{}
		_ = msg.Unmarshal(data)
	})
}
