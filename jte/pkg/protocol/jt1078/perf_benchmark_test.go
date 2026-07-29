package jt1078

// ====================================================================
// [P2-补充] JT/T 1078 编解码性能基准测试
// 确保 RTP 解析延迟 < 1μs
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// BenchmarkRTPPacketParse RTP 包解析基准测试
func BenchmarkRTPPacketParse(b *testing.B) {
	// 构造标准 RTP 包（12B 头 + 4B payload）
	data := make([]byte, 16)
	data[0] = 0x80 // V=2, P=0, X=0, CC=0
	data[1] = 0x60 // M=0, PT=96
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 1000)
	binary.BigEndian.PutUint32(data[8:12], 0x01020304)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseRTPPacket(data)
	}
}

// BenchmarkRTPPacketBuild RTP 包构建基准测试
func BenchmarkRTPPacketBuild(b *testing.B) {
	header := &RTPHeader{
		Version:     2,
		PayloadType: 96,
		SeqNum:      1,
		Timestamp:   1000,
		SSRC:       0x01020304,
	}
	payload := []byte{0x00, 0x00, 0x00, 0x01}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = BuildRTPPacket(header, payload)
	}
}
