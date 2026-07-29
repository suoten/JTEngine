package handler

import (
	"strings"
	"testing"
)

// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: isValidIDField 安全校验函数单元测试
// 此函数是 alarm handler 的第一道安全防线，防止 SQL/路径/格式化字符串注入。
// 之前零测试覆盖，存在安全回归风险。

func TestIsValidIDField(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// 合法输入
		{"alphanumeric", "vehicle001", true},
		{"with underscore", "vehicle_001", true},
		{"with hyphen", "vehicle-001", true},
		{"mixed case", "VehicleID-001", true},
		{"digits only", "123456", true},
		{"single char", "a", true},
		{"exactly 64 chars", strings.Repeat("a", 64), true},

		// 非法输入
		{"empty", "", false},
		{"with space", "vehicle 001", false},
		{"with slash", "vehicle/001", false},
		{"with backslash", "vehicle\\001", false},
		{"with dot", "vehicle.001", false},
		{"with colon", "vehicle:001", false},
		{"with semicolon", "vehicle;001", false},
		{"with quote", "vehicle'001", false},
		{"with double quote", `vehicle"001`, false},
		{"with bracket", "vehicle<001>", false},
		{"with pipe", "vehicle|001", false},
		{"with ampersand", "vehicle&001", false},
		{"with percent", "vehicle%001", false},
		{"with at sign", "vehicle@001", false},
		{"with plus", "vehicle+001", false},
		{"with equals", "vehicle=001", false},
		{"with question mark", "vehicle?001", false},
		{"with hash", "vehicle#001", false},
		{"with exclamation", "vehicle!001", false},
		{"with star", "vehicle*001", false},
		{"with parentheses", "vehicle(001)", false},
		{"with brackets", "vehicle[001]", false},
		{"with braces", "vehicle{001}", false},
		{"with caret", "vehicle^001", false},
		{"with tilde", "vehicle~001", false},
		{"with backtick", "vehicle`001", false},
		{"with dollar", "vehicle$001", false},
		{"with comma", "vehicle,001", false},
		{"with tab", "vehicle\t001", false},
		{"with newline", "vehicle\n001", false},
		{"with null byte", "vehicle\x00001", false},
		{"SQL injection attempt", "1'; DROP TABLE--", false},
		{"path traversal", "../../../etc", false},
		{"XSS attempt", "<script>alert(1)</script>", false},

		// 长度边界
		{"65 chars too long", strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidIDField(tt.input); got != tt.want {
				t.Errorf("isValidIDField(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
