package jt808

import (
	"testing"
)

// BenchmarkEscape 基准测试：转义编码性能
func BenchmarkEscape(b *testing.B) {
	// 模拟一个典型的 JT808 位置上报报文（含 0x7E 和 0x7D）
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	// 插入一些需要转义的字节
	data[10] = 0x7E
	data[50] = 0x7D
	data[100] = 0x7E
	data[200] = 0x7D

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Escape(data)
	}
}

// BenchmarkUnescape 基准测试：反转义解码性能
func BenchmarkUnescape(b *testing.B) {
	// 先转义生成测试数据
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	data[10] = 0x7E
	data[50] = 0x7D
	data[100] = 0x7E
	data[200] = 0x7D
	escaped := Escape(data)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = Unescape(escaped)
	}
}

// BenchmarkEscapeSmall 基准测试：小编码性能（模拟心跳包）
func BenchmarkEscapeSmall(b *testing.B) {
	data := []byte{0x01, 0x02, 0x7E, 0x03, 0x7D, 0x04}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Escape(data)
	}
}

// BenchmarkUnescapeSmall 基准测试：小解码性能
func BenchmarkUnescapeSmall(b *testing.B) {
	data := []byte{0x01, 0x02, 0x7D, 0x02, 0x03, 0x7D, 0x01, 0x04}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = Unescape(data)
	}
}

// BenchmarkSplitByDelimiter 基准测试：报文分割性能
func BenchmarkSplitByDelimiter(b *testing.B) {
	// 模拟 10 个报文粘包
	msg := []byte{0x7E, 0x01, 0x02, 0x03, 0x04, 0x05, 0x7E}
	data := make([]byte, 0, len(msg)*10)
	for i := 0; i < 10; i++ {
		data = append(data, msg...)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = SplitByDelimiter(data)
	}
}

// BenchmarkWrapWithDelimiter 基准测试：报文包装性能
func BenchmarkWrapWithDelimiter(b *testing.B) {
	data := make([]byte, 128)
	for i := range data {
		data[i] = byte(i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = WrapWithDelimiter(data)
	}
}

// BenchmarkStripDelimiter 基准测试：报文去包装性能
func BenchmarkStripDelimiter(b *testing.B) {
	data := make([]byte, 130)
	data[0] = 0x7E
	data[len(data)-1] = 0x7E
	for i := 1; i < len(data)-1; i++ {
		data[i] = byte(i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = StripDelimiter(data)
	}
}

// BenchmarkEscapeUnescapeRoundTrip 基准测试：转义+反转义往返性能
func BenchmarkEscapeUnescapeRoundTrip(b *testing.B) {
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i)
	}
	data[10] = 0x7E
	data[50] = 0x7D
	data[100] = 0x7E
	data[200] = 0x7D
	data[300] = 0x7E

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		escaped := Escape(data)
		_, _ = Unescape(escaped)
	}
}
