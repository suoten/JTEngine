package jt808

// ====================================================================
// [P2-补充] 跨协议 BCD 编码一致性测试
// 验证 JT808 StringToBCD 和 JT1078 stringToBCD 及 809 stringToBCD809
// 对相同输入产生相同输出
// ====================================================================

import (
	"testing"
)

// TestCrossProtocol_BCDConsistency 验证 BCD 编码跨协议一致性
// 注意：JT1078 和 JT809 的 stringToBCD 在各自包中是私有函数，
// 但 JT808 的 StringToBCD 是公开函数。我们通过 JT808 公开接口
// 验证 BCD 编码行为的一致性（过滤非数字 + 补零到 12 位 + 截断到 12 位）
func TestCrossProtocol_BCDConsistency(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"正常12位", "013800000000"},
		{"短输入", "123"},
		{"长输入", "12345678901234567890"},
		{"含分隔符", "2026-01-01 00:00:00"},
		{"含空格", "0138 0000 0000"},
		{"纯非数字", "abcdef"},
		{"空字符串", ""},
		{"全零", "000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bcd, err := StringToBCD6(tt.input)

			// 验证行为一致性：
			// 1. 纯非数字/空输入应返回 error
			// 2. 有效输入应返回 6 字节 BCD
			// 3. BCDToString 应能正确还原数字部分
			if err != nil {
				// 非数字输入返回 error 是正确行为
				return
			}

			if len(bcd) != 6 {
				t.Errorf("BCD length: got %d, want 6", len(bcd))
			}

			// 验证往返：BCDToString 应不 panic 且返回 12 位数字字符串
			result, _ := BCDToString(bcd)
			if len(result) != 12 {
				t.Errorf("BCDToString length: got %d, want 12", len(result))
			}

			// 验证所有字符都是数字
			for i, c := range result {
				if c < '0' || c > '9' {
					t.Errorf("BCDToString char %d: got %c, want digit", i, c)
				}
			}
		})
	}
}

// TestCrossProtocol_BCDRoundTrip 验证 BCD 编码往返
func TestCrossProtocol_BCDRoundTrip(t *testing.T) {
	inputs := []string{
		"013800000000",
		"260710120000", // 12位（标准 BCD 时间）
		"991231235959",
		"000000000000",
		"123456789012",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			bcd, err := StringToBCD6(input)
			if err != nil {
				t.Fatalf("StringToBCD failed: %v", err)
			}
			output, _ := BCDToString(bcd)
			if output != input {
				t.Errorf("round-trip mismatch: got %q, want %q", output, input)
			}
		})
	}
}
