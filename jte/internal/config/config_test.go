package config

import (
	"testing"
	"time"
)

func TestJWTConfigGetSecret(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1", "k2": "secret2"},
		ActiveKid: "k1",
	}

	// 测试存在的 kid
	secret, ok := cfg.GetSecret("k1")
	if !ok || secret != "secret1" {
		t.Errorf("GetSecret(k1) = %q, %v; want %q, true", secret, ok, "secret1")
	}

	// 测试不存在的 kid
	_, ok = cfg.GetSecret("nonexistent")
	if ok {
		t.Error("GetSecret(nonexistent) should return false")
	}

	// 测试 nil 配置
	var nilCfg *JWTConfig
	_, ok = nilCfg.GetSecret("k1")
	if ok {
		t.Error("GetSecret on nil config should return false")
	}
}

func TestJWTConfigGetActiveSecret(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1"},
		ActiveKid: "k1",
	}

	kid, secret, ok := cfg.GetActiveSecret()
	if !ok || kid != "k1" || secret != "secret1" {
		t.Errorf("GetActiveSecret() = %q, %q, %v; want %q, %q, true", kid, secret, ok, "k1", "secret1")
	}

	// 无 ActiveKid
	cfg.ActiveKid = ""
	_, _, ok = cfg.GetActiveSecret()
	if ok {
		t.Error("GetActiveSecret with empty ActiveKid should return false")
	}

	// nil 配置
	var nilCfg *JWTConfig
	_, _, ok = nilCfg.GetActiveSecret()
	if ok {
		t.Error("GetActiveSecret on nil config should return false")
	}
}

func TestJWTRotateJWTKey(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1"},
		ActiveKid: "k1",
	}

	cfg.RotateJWTKey("k2", "secret2")

	// 新 kid 应该是 active
	if cfg.ActiveKid != "k2" {
		t.Errorf("ActiveKid = %q, want %q", cfg.ActiveKid, "k2")
	}

	// 旧 kid 应该仍然存在
	secret, ok := cfg.GetSecret("k1")
	if !ok || secret != "secret1" {
		t.Errorf("old kid k1 should still exist: %q, %v", secret, ok)
	}

	// 新 kid 应该存在
	secret, ok = cfg.GetSecret("k2")
	if !ok || secret != "secret2" {
		t.Errorf("new kid k2 should exist: %q, %v", secret, ok)
	}

	// 创建时间应该被记录
	_, ok = cfg.GetActiveKidCreatedAt()
	if !ok {
		t.Error("GetActiveKidCreatedAt should return true after rotation")
	}
}

func TestJWTCleanupExpiredKids(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1", "k2": "secret2", "k3": "secret3"},
		ActiveKid: "k3",
	}
	// k1 创建于 10 天前（应被清理）
	cfg.SetTestKidCreatedAt("k1", time.Now().AddDate(0, 0, -10))
	// k2 创建于 3 天前（应保留）
	cfg.SetTestKidCreatedAt("k2", time.Now().AddDate(0, 0, -3))
	// k3 是 active（应保留）

	cfg.CleanupExpiredKids()

	// k1 应该被删除
	_, ok := cfg.GetSecret("k1")
	if ok {
		t.Error("expired kid k1 should be cleaned up")
	}

	// k2 应该保留
	_, ok = cfg.GetSecret("k2")
	if !ok {
		t.Error("kid k2 should still exist (within 7 days)")
	}

	// k3 (active) 应该保留
	_, ok = cfg.GetSecret("k3")
	if !ok {
		t.Error("active kid k3 should still exist")
	}
}

func TestJWTCleanupExpiredKidsRateLimit(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1", "k2": "secret2"},
		ActiveKid: "k2",
	}
	cfg.SetTestKidCreatedAt("k1", time.Now().AddDate(0, 0, -10))

	// 第一次清理
	cfg.CleanupExpiredKids()
	_, ok := cfg.GetSecret("k1")
	if ok {
		t.Error("expired kid should be cleaned up on first call")
	}

	// 重新添加过期 kid
	cfg.mu.Lock()
	cfg.Secrets["k1"] = "secret1"
	cfg.kidCreatedAt["k1"] = time.Now().AddDate(0, 0, -10)
	cfg.mu.Unlock()

	// 第二次清理（应该被限流，不执行）
	cfg.CleanupExpiredKids()
	_, ok = cfg.GetSecret("k1")
	if !ok {
		t.Error("second cleanup should be rate-limited, kid should still exist")
	}
}

func TestJWTEnsureKidCreatedAt(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"k1": "secret1", "k2": "secret2"},
		ActiveKid: "k1",
	}

	cfg.EnsureKidCreatedAt()

	_, ok1 := cfg.GetActiveKidCreatedAt()
	// k1 是 active，应该有创建时间
	if !ok1 {
		t.Error("active kid should have creation time after EnsureKidCreatedAt")
	}
}

func TestJWTSetSecretsFromKMS(t *testing.T) {
	cfg := &JWTConfig{
		Secrets:   map[string]string{"old": "old_secret"},
		ActiveKid: "old",
	}

	newSecrets := map[string]string{"new1": "s1", "new2": "s2"}
	cfg.SetSecretsFromKMS(newSecrets, "new1")

	if cfg.ActiveKid != "new1" {
		t.Errorf("ActiveKid = %q, want %q", cfg.ActiveKid, "new1")
	}

	secret, ok := cfg.GetSecret("new1")
	if !ok || secret != "s1" {
		t.Errorf("GetSecret(new1) = %q, %v", secret, ok)
	}

	// 旧密钥应该被替换
	_, ok = cfg.GetSecret("old")
	if ok {
		t.Error("old secret should be replaced")
	}
}

func TestJWTConfigNilSafety(t *testing.T) {
	var cfg *JWTConfig

	// 所有方法在 nil 接收者上应该安全返回
	cfg.RotateJWTKey("k1", "s1")       // 不应 panic
	cfg.CleanupExpiredKids()            // 不应 panic
	cfg.EnsureKidCreatedAt()            // 不应 panic
	cfg.SetSecretsFromKMS(nil, "")      // 不应 panic
	cfg.SetTestKidCreatedAt("k1", time.Now()) // 不应 panic

	_, ok := cfg.GetActiveKidCreatedAt()
	if ok {
		t.Error("GetActiveKidCreatedAt on nil should return false")
	}
}
