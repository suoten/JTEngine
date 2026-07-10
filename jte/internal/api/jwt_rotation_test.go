package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// TestTokenBlacklist_RevokeAndCheck 单 token 撤销
func TestTokenBlacklist_RevokeAndCheck(t *testing.T) {
	bl := NewTokenBlacklist(nil, zap.NewNop())
	// 未撤销的 token
	if bl.IsRevoked("jti-123") {
		t.Fatal("未撤销的 token 不应命中黑名单")
	}
	// 撤销
	bl.Revoke("jti-123", time.Now().Add(1*time.Hour))
	if !bl.IsRevoked("jti-123") {
		t.Fatal("已撤销的 token 应命中黑名单")
	}
	// 空 jti
	if bl.IsRevoked("") {
		t.Fatal("空 jti 不应命中黑名单")
	}
}

// TestTokenBlacklist_RevokeAll 全局撤销
func TestTokenBlacklist_RevokeAll(t *testing.T) {
	bl := NewTokenBlacklist(nil, zap.NewNop())
	oldIat := time.Now().Add(-1 * time.Hour)
	newIat := time.Now().Add(1 * time.Second)
	// 全局撤销前，旧 token 不被拒绝
	if bl.IsGlobalRevoked(oldIat) {
		t.Fatal("全局撤销前不应拒绝")
	}
	// 触发全局撤销
	bl.RevokeAll()
	// 撤销点之前的 token 被拒绝
	if !bl.IsGlobalRevoked(oldIat) {
		t.Fatal("全局撤销后，旧 token 应被拒绝")
	}
	// 撤销点之后的 token 不被拒绝（新签发的）
	if bl.IsGlobalRevoked(newIat) {
		t.Fatal("全局撤销后，新签发的 token 不应被拒绝")
	}
}

// TestTokenBlacklist_Cleanup 清理过期记录
func TestTokenBlacklist_Cleanup(t *testing.T) {
	bl := NewTokenBlacklist(nil, zap.NewNop())
	// 已过期的撤销记录
	bl.Revoke("expired-jti", time.Now().Add(-1*time.Minute))
	// 未过期的撤销记录
	bl.Revoke("active-jti", time.Now().Add(1*time.Hour))
	bl.Cleanup()
	if bl.IsRevoked("expired-jti") {
		t.Fatal("过期记录应被清理")
	}
	if !bl.IsRevoked("active-jti") {
		t.Fatal("未过期记录不应被清理")
	}
}

// TestJWTRotationManager_EmergencyRotate 应急轮换
func TestJWTRotationManager_EmergencyRotate(t *testing.T) {
	jwtCfg := &config.JWTConfig{
		Secrets:    map[string]string{"old-kid": string(make([]byte, 48))},
		ActiveKid:  "old-kid",
		RotateDays: 90,
	}
	jwtCfg.EnsureKidCreatedAt()
	bl := NewTokenBlacklist(nil, zap.NewNop())
	mgr := NewJWTRotationManager(jwtCfg, bl, zap.NewNop())

	oldKid := jwtCfg.ActiveKid
	newKid, err := mgr.EmergencyRotate()
	if err != nil {
		t.Fatalf("应急轮换失败: %v", err)
	}
	if newKid == "" {
		t.Fatal("新 kid 不应为空")
	}
	if newKid == oldKid {
		t.Fatal("新 kid 不应等于旧 kid")
	}
	if jwtCfg.ActiveKid != newKid {
		t.Fatal("ActiveKid 应更新为新 kid")
	}
	// 旧 kid 应仍存在（7 天保留期）
	if _, ok := jwtCfg.GetSecret(oldKid); !ok {
		t.Fatal("旧 kid 应保留 7 天")
	}
	// 全局撤销应已触发
	if !bl.IsGlobalRevoked(time.Now().Add(-1 * time.Hour)) {
		t.Fatal("应急轮换应触发全局撤销")
	}
}

// TestJWTRotationManager_checkAndRotate_NotExpired 未到期不轮换
func TestJWTRotationManager_checkAndRotate_NotExpired(t *testing.T) {
	jwtCfg := &config.JWTConfig{
		Secrets:    map[string]string{"kid-1": string(make([]byte, 48))},
		ActiveKid:  "kid-1",
		RotateDays: 90,
	}
	jwtCfg.EnsureKidCreatedAt()
	mgr := NewJWTRotationManager(jwtCfg, nil, zap.NewNop())
	oldActive := jwtCfg.ActiveKid
	mgr.checkAndRotate() // 刚创建，未到期
	if jwtCfg.ActiveKid != oldActive {
		t.Fatal("未到期不应轮换")
	}
}

// TestJWTRotationManager_checkAndRotate_Expired 到期自动轮换
func TestJWTRotationManager_checkAndRotate_Expired(t *testing.T) {
	jwtCfg := &config.JWTConfig{
		Secrets:    map[string]string{"kid-1": string(make([]byte, 48))},
		ActiveKid:  "kid-1",
		RotateDays: 1,
	}
	jwtCfg.EnsureKidCreatedAt()
	// 手动将创建时间设为 2 天前（超过 1 天轮换周期）
	jwtCfg.SetTestKidCreatedAt("kid-1", time.Now().Add(-48*time.Hour))
	mgr := NewJWTRotationManager(jwtCfg, nil, zap.NewNop())
	mgr.checkAndRotate()
	if jwtCfg.ActiveKid == "kid-1" {
		t.Fatal("到期应轮换到新 kid")
	}
}

// TestGenerateKid 唯一性与格式
func TestGenerateKid(t *testing.T) {
	kids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		kid := generateKid()
		if len(kid) < 10 {
			t.Fatalf("kid 长度不足: %s", kid)
		}
		if kids[kid] {
			t.Fatalf("kid 重复: %s", kid)
		}
		kids[kid] = true
	}
}

// TestGenerateSecret 强度
func TestGenerateSecret(t *testing.T) {
	secret, err := generateSecret(48)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if len(secret) < 32 {
		t.Fatalf("密钥长度不足 32 字节: %d", len(secret))
	}
}

// TestLoadJWTSecretsFromEnv 环境变量加载
func TestLoadJWTSecretsFromEnv(t *testing.T) {
	os.Setenv("JTE_JWT_SECRET_kid1", "a-very-long-secret-key-32-bytes-min!")
	os.Setenv("JTE_JWT_SECRET_kid2", "another-very-long-secret-key-32-bytes!!")
	os.Setenv("JTE_JWT_ACTIVE_KID", "kid1")
	defer os.Unsetenv("JTE_JWT_SECRET_kid1")
	defer os.Unsetenv("JTE_JWT_SECRET_kid2")
	defer os.Unsetenv("JTE_JWT_ACTIVE_KID")

	secrets, activeKid := LoadJWTSecretsFromEnv()
	if len(secrets) != 2 {
		t.Fatalf("应加载 2 个密钥，实际 %d", len(secrets))
	}
	if secrets["kid1"] != "a-very-long-secret-key-32-bytes-min!" {
		t.Fatal("kid1 密钥不匹配")
	}
	if activeKid != "kid1" {
		t.Fatalf("activeKid 应为 kid1，实际 %s", activeKid)
	}
}

// TestLoadJWTSecretsFromFile 文件加载
func TestLoadJWTSecretsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jwt_keys.json")
	content := `{"secrets":{"kid1":"a-very-long-secret-key-32-bytes-min!"},"active_kid":"kid1"}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	secrets, activeKid, err := LoadJWTSecretsFromFile(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(secrets) != 1 {
		t.Fatalf("应加载 1 个密钥，实际 %d", len(secrets))
	}
	if activeKid != "kid1" {
		t.Fatalf("activeKid 应为 kid1，实际 %s", activeKid)
	}
}

// TestInitJWTFromKMS_Env KMS env 模式
func TestInitJWTFromKMS_Env(t *testing.T) {
	os.Setenv("JTE_JWT_SECRET_kmskid", "a-very-long-secret-key-32-bytes-min!!")
	defer os.Unsetenv("JTE_JWT_SECRET_kmskid")
	jwtCfg := &config.JWTConfig{RotateDays: 90}
	err := InitJWTFromKMS(jwtCfg, "env", "", zap.NewNop())
	if err != nil {
		t.Fatalf("env KMS 加载失败: %v", err)
	}
	if _, ok := jwtCfg.GetSecret("kmskid"); !ok {
		t.Fatal("kmskid 应存在")
	}
}

// TestInitJWTFromKMS_WeakSecret 弱密钥拒绝
func TestInitJWTFromKMS_WeakSecret(t *testing.T) {
	os.Setenv("JTE_JWT_SECRET_weak", "short")
	defer os.Unsetenv("JTE_JWT_SECRET_weak")
	jwtCfg := &config.JWTConfig{RotateDays: 90}
	err := InitJWTFromKMS(jwtCfg, "env", "", zap.NewNop())
	if err == nil {
		t.Fatal("弱密钥应被拒绝")
	}
}

// TestInitJWTFromKMS_Config 回退模式
func TestInitJWTFromKMS_Config(t *testing.T) {
	jwtCfg := &config.JWTConfig{RotateDays: 90}
	err := InitJWTFromKMS(jwtCfg, "config", "", zap.NewNop())
	if err != nil {
		t.Fatalf("config 模式不应报错: %v", err)
	}
}
