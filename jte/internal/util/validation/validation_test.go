package validation

import (
	"testing"
)

func TestValidatePhone(t *testing.T) {
	tests := []struct {
		phone string
		wantErr bool
	}{
		{"13800138000", false},
		{"15912345678", false},
		{"18600000000", false},
		{"", true},
		{"12345678901", true},  // 不以1[3-9]开头
		{"1380013800", true},   // 少于11位
		{"138001380001", true}, // 多于11位
		{"abc12345678", true},  // 非数字
	}

	for _, tt := range tests {
		t.Run(tt.phone, func(t *testing.T) {
			err := ValidatePhone(tt.phone)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePhone(%q) error = %v, wantErr %v", tt.phone, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDeviceID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"device-001", false},
		{"dev_123", false},
		{"ABC123", false},
		{"", true},
		{"a b c", true},           // 空格不允许
		{"设备ID", true},           // 中文不允许
		{string(make([]byte, 65)), true}, // 超长
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err := ValidateDeviceID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDeviceID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCoordinates(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lng     float64
		wantErr bool
	}{
		{"valid", 39.9042, 116.4074, false},
		{"zero", 0, 0, false},
		{"max", 90, 180, false},
		{"min", -90, -180, false},
		{"lat_over", 91, 0, true},
		{"lat_under", -91, 0, true},
		{"lng_over", 0, 181, true},
		{"lng_under", 0, -181, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCoordinates(tt.lat, tt.lng)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCoordinates(%f, %f) error = %v, wantErr %v",
					tt.lat, tt.lng, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSpeed(t *testing.T) {
	tests := []struct {
		speed   float64
		wantErr bool
	}{
		{0, false},
		{60.5, false},
		{300, false},
		{-1, true},
		{301, true},
	}

	for _, tt := range tests {
		err := ValidateSpeed(tt.speed)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateSpeed(%f) error = %v, wantErr %v", tt.speed, err, tt.wantErr)
		}
	}
}

func TestValidateDirection(t *testing.T) {
	tests := []struct {
		dir     int
		wantErr bool
	}{
		{0, false},
		{180, false},
		{359, false},
		{-1, true},
		{360, true},
	}

	for _, tt := range tests {
		err := ValidateDirection(tt.dir)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateDirection(%d) error = %v, wantErr %v", tt.dir, err, tt.wantErr)
		}
	}
}

func TestValidatePagination(t *testing.T) {
	tests := []struct {
		name     string
		page     int
		pageSize int
		wantPage int
		wantSize int
	}{
		{"default", 0, 0, 1, 20},
		{"negative", -1, -1, 1, 20},
		{"normal", 2, 50, 2, 50},
		{"over_max", 1, 2000, 1, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ValidatePagination(tt.page, tt.pageSize)
			if p.Page != tt.wantPage {
				t.Errorf("Page = %d, want %d", p.Page, tt.wantPage)
			}
			if p.PageSize != tt.wantSize {
				t.Errorf("PageSize = %d, want %d", p.PageSize, tt.wantSize)
			}
		})
	}
}

func TestValidateTimeRange(t *testing.T) {
	// 正常范围
	start, end, err := ValidateTimeRange("2024-01-01 00:00:00", "2024-01-31 23:59:59")
	if err != nil {
		t.Fatalf("ValidateTimeRange normal: %v", err)
	}
	if start.IsZero() || end.IsZero() {
		t.Error("times should not be zero")
	}

	// 开始大于结束
	_, _, err = ValidateTimeRange("2024-12-01", "2024-01-01")
	if err == nil {
		t.Error("should error when start > end")
	}

	// 范围过大
	_, _, err = ValidateTimeRange("2020-01-01", "2024-12-31")
	if err == nil {
		t.Error("should error when range > 365 days")
	}

	// 空值
	_, _, err = ValidateTimeRange("", "")
	if err != nil {
		t.Errorf("empty range should not error: %v", err)
	}

	// 无效格式
	_, _, err = ValidateTimeRange("invalid", "")
	if err == nil {
		t.Error("should error for invalid format")
	}
}

func TestValidateStringLength(t *testing.T) {
	if err := ValidateStringLength("name", "abc", 1, 10); err != nil {
		t.Errorf("valid string: %v", err)
	}
	if err := ValidateStringLength("name", "", 1, 10); err == nil {
		t.Error("empty string should error when min=1")
	}
	if err := ValidateStringLength("name", "abcdefghijklmnop", 1, 10); err == nil {
		t.Error("long string should error when max=10")
	}
}

func TestValidateIP(t *testing.T) {
	tests := []struct {
		ip      string
		wantErr bool
	}{
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"::1", false},
		{"", true},
		{"999.999.999.999", true},
		{"not-an-ip", true},
	}

	for _, tt := range tests {
		err := ValidateIP(tt.ip)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateIP(%q) error = %v, wantErr %v", tt.ip, err, tt.wantErr)
		}
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port    int
		wantErr bool
	}{
		{80, false},
		{443, false},
		{8080, false},
		{65535, false},
		{1, false},
		{0, true},
		{-1, true},
		{65536, true},
	}

	for _, tt := range tests {
		err := ValidatePort(tt.port)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
		}
	}
}

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		input  string
		want   string
	}{
		{"  hello  ", "hello"},
		{"hello\x00world", "helloworld"},
		{"hello\x01\x02world", "helloworld"},
		{"hello\nworld", "hello\nworld"},
		{"\t\ttabs\t", "tabs"},
	}

	for _, tt := range tests {
		got := SanitizeString(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidatePageFromString(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"10", 10, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		got, err := ValidatePageFromString(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePageFromString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ValidatePageFromString(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestValidatePageSizeFromString(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 20, false},
		{"50", 50, false},
		{"1000", 1000, false},
		{"0", 0, true},
		{"2000", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		got, err := ValidatePageSizeFromString(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePageSizeFromString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ValidatePageSizeFromString(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		username string
		wantErr  bool
	}{
		{"admin", false},
		{"user_123", false},
		{"abc", false},
		{"", true},
		{"ab", true},              // too short
		{"abcdefghijklmnopqrstuvwxyz1234567890", true}, // too long (36 chars)
		{"user-name", true},       // hyphen not allowed
	}

	for _, tt := range tests {
		err := ValidateUsername(tt.username)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateUsername(%q) error = %v, wantErr %v", tt.username, err, tt.wantErr)
		}
	}
}
