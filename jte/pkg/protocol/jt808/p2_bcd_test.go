package jt808

import (
	"testing"
)

// ---------------------------------------------------------------------------
// P2 修复验证测试：BCDToString BCD 合法性校验
// ---------------------------------------------------------------------------

func TestP2_BCDToString_InvalidBCD(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
		want    string
	}{
		{"正常BCD", []byte{0x01, 0x23, 0x45}, false, "012345"},
		{"全零BCD", []byte{0x00, 0x00, 0x00}, false, "000000"},
		{"高位非法(0xAB)", []byte{0xAB, 0x01}, true, ":;01"},
		{"低位非法(0x0F)", []byte{0x0F, 0x01}, true, "0?01"},
		{"高位和低位都非法(0xFF)", []byte{0xFF}, true, "??"},
		{"空输入", []byte{}, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BCDToString(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("BCDToString(%v) expected error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("BCDToString(%v) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("BCDToString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestP2_BCDToStringSafe_NoError(t *testing.T) {
	// BCDToStringSafe 应该始终返回字符串，不返回 error
	result := BCDToStringSafe([]byte{0xAB, 0xCD})
	if result == "" {
		t.Error("BCDToStringSafe should return non-empty string even for invalid BCD")
	}
}

// ---------------------------------------------------------------------------
// P2 修复验证测试：StringToBCD targetLen 参数
// ---------------------------------------------------------------------------

func TestP2_StringToBCD_TargetLen(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		targetLen int
		wantLen   int
		wantErr   bool
	}{
		{"默认6字节", "123456789012", 6, 6, false},
		{"4字节BCD", "12345678", 4, 4, false},
		{"2字节BCD", "1234", 2, 2, false},
		{"1字节BCD", "12", 1, 1, false},
		{"0长度(默认6)", "123456789012", 0, 6, false},
		{"负数(默认6)", "123456789012", -1, 6, false},
		{"短输入自动补零", "1", 6, 6, false},
		{"长输入截断", "12345678901234567890", 6, 6, false},
		{"纯非数字", "abc", 6, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bcd, err := StringToBCD(tt.input, tt.targetLen)
			if tt.wantErr {
				if err == nil {
					t.Errorf("StringToBCD(%q, %d) expected error, got nil", tt.input, tt.targetLen)
				}
				return
			}
			if err != nil {
				t.Errorf("StringToBCD(%q, %d) unexpected error: %v", tt.input, tt.targetLen, err)
				return
			}
			if len(bcd) != tt.wantLen {
				t.Errorf("StringToBCD(%q, %d) length = %d, want %d", tt.input, tt.targetLen, len(bcd), tt.wantLen)
			}
		})
	}
}

func TestP2_StringToBCD6_Wrapper(t *testing.T) {
	// StringToBCD6 应该等价于 StringToBCD(s, 6)
	input := "013800000000"
	bcd6, err1 := StringToBCD6(input)
	bcd, err2 := StringToBCD(input, 6)

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if len(bcd6) != 6 || len(bcd) != 6 {
		t.Fatalf("unexpected lengths: %d, %d", len(bcd6), len(bcd))
	}
	for i := range bcd6 {
		if bcd6[i] != bcd[i] {
			t.Errorf("StringToBCD6 vs StringToBCD mismatch at byte %d: 0x%02X vs 0x%02X", i, bcd6[i], bcd[i])
		}
	}
}

func TestP2_StringToBCD_RoundTrip(t *testing.T) {
	// 验证 StringToBCD → BCDToString 往返
	inputs := []string{
		"013800000000",
		"260723120000", // 12位（标准 BCD 时间）
		"000000000000",
		"991231235959",
	}

	for _, input := range inputs {
		bcd, err := StringToBCD6(input)
		if err != nil {
			t.Fatalf("StringToBCD6(%q) error: %v", input, err)
		}
		output, err := BCDToString(bcd)
		if err != nil {
			t.Fatalf("BCDToString(%v) error: %v", bcd, err)
		}
		if output != input {
			t.Errorf("round-trip mismatch: got %q, want %q", output, input)
		}
	}
}
