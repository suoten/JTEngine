package gateway

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// ============================================================================
// JT/T 808-2019 修复项单元测试 — 鉴权码防克隆 IP变更频率检测
// ============================================================================

func TestFix_AuthCodeManager_IPChangeFrequencyAlert(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())

	phone := "13800138000"
	authCode := mgr.Generate(phone, "fp1", "10.0.0.1", "session-1")

	// 第1次 IP 变更: 10.0.0.1 -> 10.0.0.2
	valid, _ := mgr.Validate(phone, authCode, "10.0.0.2")
	if !valid {
		t.Error("1st IP change should be valid")
	}

	// 第2次 IP 变更
	valid, _ = mgr.Validate(phone, authCode, "10.0.0.3")
	if !valid {
		t.Error("2nd IP change should be valid")
	}

	// 第3次 IP 变更 (change count = 3, threshold = 3, 3 > 3 = false)
	valid, _ = mgr.Validate(phone, authCode, "10.0.0.4")
	if !valid {
		t.Error("3rd IP change should be valid")
	}

	// 第4次 IP 变更 (change count = 4, 4 > 3 = true → high-freq alert)
	// 仍然放行（仅告警不拒绝）
	valid, _ = mgr.Validate(phone, authCode, "10.0.0.5")
	if !valid {
		t.Error("4th IP change should still be valid (alert only, not block)")
	}
}

func TestFix_AuthCodeManager_SameIPNoChange(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())
	phone := "13800138001"
	authCode := mgr.Generate(phone, "fp2", "10.0.0.1", "session-2")

	for i := 0; i < 10; i++ {
		valid, _ := mgr.Validate(phone, authCode, "10.0.0.1")
		if !valid {
			t.Errorf("same IP validation %d should be valid", i)
		}
	}

	mgr.mu.RLock()
	rec := mgr.records[authCode]
	mgr.mu.RUnlock()
	if len(rec.IPChanges) != 0 {
		t.Errorf("IPChanges should be empty for same IP, got %d", len(rec.IPChanges))
	}
}

func TestFix_AuthCodeManager_PhoneMismatch(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())
	authCode := mgr.Generate("13800138000", "fp3", "10.0.0.1", "session-3")

	valid, reason := mgr.Validate("13900139000", authCode, "10.0.0.1")
	if valid {
		t.Error("should be invalid for phone mismatch")
	}
	if reason != "phone_mismatch" {
		t.Errorf("reason: got %q, want phone_mismatch", reason)
	}
}

func TestFix_AuthCodeManager_NotFound(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())

	valid, reason := mgr.Validate("13800138000", "nonexistent_code", "10.0.0.1")
	if valid {
		t.Error("should be invalid for nonexistent auth code")
	}
	if reason != "auth_code_not_found" {
		t.Errorf("reason: got %q, want auth_code_not_found", reason)
	}
}

func TestFix_AuthCodeManager_Revoke(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())
	phone := "13800138002"
	authCode := mgr.Generate(phone, "fp4", "10.0.0.1", "session-4")

	mgr.Revoke(phone)
	valid, reason := mgr.Validate(phone, authCode, "10.0.0.1")
	if valid {
		t.Error("should be invalid after revoke")
	}
	if reason != "auth_code_not_found" {
		t.Errorf("reason: got %q, want auth_code_not_found", reason)
	}
}

func TestFix_AuthCodeManager_CleanupExpired(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())
	phone := "13800138003"
	authCode := mgr.Generate(phone, "fp5", "10.0.0.1", "session-5")

	// 等待确保记录已过期
	time.Sleep(10 * time.Millisecond)
	mgr.CleanupExpired(1 * time.Millisecond)

	valid, _ := mgr.Validate(phone, authCode, "10.0.0.1")
	if valid {
		t.Error("should be invalid after cleanup")
	}
}

func TestFix_AuthCodeManager_GenerateRevokesOld(t *testing.T) {
	mgr := NewAuthCodeManager(zap.NewNop())
	phone := "13800138004"
	code1 := mgr.Generate(phone, "fp6", "10.0.0.1", "session-6")

	// 重新注册生成新码，旧码应被撤销
	code2 := mgr.Generate(phone, "fp6", "10.0.0.1", "session-7")
	if code1 == code2 {
		t.Fatal("new auth code should differ from old")
	}

	// 旧码应失效
	valid, _ := mgr.Validate(phone, code1, "10.0.0.1")
	if valid {
		t.Error("old auth code should be invalid after re-registration")
	}

	// 新码应有效
	valid, _ = mgr.Validate(phone, code2, "10.0.0.1")
	if !valid {
		t.Error("new auth code should be valid")
	}
}
