package gateway

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestGenerateSecureAuthCode_LengthAndHex 鉴权码长度 32 且为 hex
func TestGenerateSecureAuthCode_LengthAndHex(t *testing.T) {
	code := generateSecureAuthCode()
	if len(code) != 32 {
		t.Fatalf("expected len 32, got %d", len(code))
	}
	for _, c := range code {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("auth code contains non-hex char: %q", code)
		}
	}
}

// TestGenerateSecureAuthCode_Uniqueness 1000 次生成无重复
func TestGenerateSecureAuthCode_Uniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		code := generateSecureAuthCode()
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate auth code generated: %s", code)
		}
		seen[code] = struct{}{}
	}
}

// TestAuthCodeManager_GenerateAndValidate 正常生成与校验
func TestAuthCodeManager_GenerateAndValidate(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	phone := "13800138000"
	deviceFP := "IMEI-12345/TerminalID-67890"
	code := m.Generate(phone, deviceFP, "1.1.1.1:1234", "sess-1")

	if code == "" {
		t.Fatal("empty auth code")
	}
	if code == phone {
		t.Fatal("auth code should not equal phone number")
	}

	valid, reason := m.Validate(phone, code, "1.1.1.1:1234")
	if !valid {
		t.Fatalf("expected valid, got reason: %s", reason)
	}
}

// TestAuthCodeManager_Validate_NotFound 鉴权码不存在
func TestAuthCodeManager_Validate_NotFound(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	valid, reason := m.Validate("13800138000", "nonexistent", "1.1.1.1:1234")
	if valid {
		t.Fatal("expected invalid for nonexistent code")
	}
	if reason != "auth_code_not_found" {
		t.Fatalf("expected reason auth_code_not_found, got %s", reason)
	}
}

// TestAuthCodeManager_Validate_PhoneMismatch 鉴权码与手机号不匹配（伪造）
func TestAuthCodeManager_Validate_PhoneMismatch(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	code := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")
	// 攻击者用该鉴权码但不同手机号
	valid, reason := m.Validate("13900139000", code, "2.2.2.2:5678")
	if valid {
		t.Fatal("expected invalid for phone mismatch")
	}
	if reason != "phone_mismatch" {
		t.Fatalf("expected reason phone_mismatch, got %s", reason)
	}
}

// TestAuthCodeManager_Validate_DifferentIP 不同 IP 仍放行（仅告警）
func TestAuthCodeManager_Validate_DifferentIP(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	code := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")
	// 同 phone 同 code 不同 IP → 仍有效（NAT/重连可能变 IP）
	valid, _ := m.Validate("13800138000", code, "3.3.3.3:9999")
	if !valid {
		t.Fatal("expected valid for different IP (warn only)")
	}
}

// TestAuthCodeManager_Generate_RevokeOld 同手机号重新注册撤销旧码
func TestAuthCodeManager_Generate_RevokeOld(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	oldCode := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")
	newCode := m.Generate("13800138000", "fp2", "1.1.1.1:1234", "sess-2")

	if oldCode == newCode {
		t.Fatal("new code should differ from old")
	}
	// 旧码应失效
	valid, reason := m.Validate("13800138000", oldCode, "1.1.1.1:1234")
	if valid {
		t.Fatal("old code should be revoked")
	}
	if reason != "auth_code_not_found" {
		t.Fatalf("expected reason auth_code_not_found, got %s", reason)
	}
	// 新码有效
	valid, _ = m.Validate("13800138000", newCode, "1.1.1.1:1234")
	if !valid {
		t.Fatal("new code should be valid")
	}
}

// TestAuthCodeManager_Revoke 显式撤销
func TestAuthCodeManager_Revoke(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	code := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")
	m.Revoke("13800138000")
	valid, _ := m.Validate("13800138000", code, "1.1.1.1:1234")
	if valid {
		t.Fatal("expected invalid after revoke")
	}
}

// TestAuthCodeManager_CleanupExpired 过期清理
func TestAuthCodeManager_CleanupExpired(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	code := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")

	// 立即清理（maxAge=0）应清掉
	m.CleanupExpired(0)
	valid, _ := m.Validate("13800138000", code, "1.1.1.1:1234")
	if valid {
		t.Fatal("expected invalid after cleanup with maxAge=0")
	}
}

// TestAuthCodeManager_CleanupExpired_KeepsRecent 近期记录保留
func TestAuthCodeManager_CleanupExpired_KeepsRecent(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	code := m.Generate("13800138000", "fp1", "1.1.1.1:1234", "sess-1")
	m.CleanupExpired(1 * time.Hour)
	valid, _ := m.Validate("13800138000", code, "1.1.1.1:1234")
	if !valid {
		t.Fatal("expected valid for recent code after cleanup")
	}
}

// TestAuthCodeManager_DeviceFPRecorded 设备指纹被记录
func TestAuthCodeManager_DeviceFPRecorded(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	fp := "IMEI-123/TERM-456"
	m.Generate("13800138000", fp, "1.1.1.1:1234", "sess-1")
	m.mu.RLock()
	rec := m.byPhone["13800138000"]
	m.mu.RUnlock()
	if rec == nil {
		t.Fatal("record not found")
	}
	if rec.DeviceFP != fp {
		t.Fatalf("expected device fp %s, got %s", fp, rec.DeviceFP)
	}
}

// TestAuthCodeManager_NotPhoneLike 鉴权码不像手机号（防猜测）
func TestAuthCodeManager_NotPhoneLike(t *testing.T) {
	m := NewAuthCodeManager(zap.NewNop())
	phone := "13800138000"
	code := m.Generate(phone, "fp", "1.1.1.1:1234", "sess-1")
	if strings.HasPrefix(code, phone) {
		t.Fatal("auth code should not start with phone number")
	}
	if strings.Contains(code, phone) {
		t.Fatal("auth code should not contain phone number")
	}
}
