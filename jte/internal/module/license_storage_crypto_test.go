package module

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// P2-9: licenses.json AES-256-GCM 加密存储 —— 测试
// ===================================================================

func TestLicenseStorage_EncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp := "test-fingerprint-abc123"
	logger := zap.NewNop()

	original := &licenseStore{
		Licenses: []*License{
			{
				ID:        "lic-001",
				Modules:   []string{"protocol_809", "video"},
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
				CustomerID: "cust-1",
			},
		},
		Trials: map[string]*TrialInfo{
			"protocol_809": {
				ModuleName: "protocol_809",
				ExpiresAt:  time.Now().Add(15 * 24 * time.Hour),
			},
		},
	}

	if err := saveEncryptedLicenseStore(dir, fp, original, logger); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadEncryptedLicenseStore(dir, fp, logger)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Licenses) != 1 {
		t.Fatalf("licenses count = %d, want 1", len(loaded.Licenses))
	}
	if loaded.Licenses[0].ID != "lic-001" {
		t.Errorf("license ID = %q, want lic-001", loaded.Licenses[0].ID)
	}
	if len(loaded.Licenses[0].Modules) != 2 {
		t.Errorf("modules count = %d, want 2", len(loaded.Licenses[0].Modules))
	}
	if _, ok := loaded.Trials["protocol_809"]; !ok {
		t.Fatal("trial protocol_809 missing")
	}
}

func TestLicenseStorage_WrongFingerprintFails(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()

	original := &licenseStore{
		Licenses: []*License{{ID: "lic-secret"}},
	}
	if err := saveEncryptedLicenseStore(dir, "fingerprint-A", original, logger); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 用不同指纹解密应失败（文件复制到其他机器无法读取）
	if _, err := loadEncryptedLicenseStore(dir, "fingerprint-B", logger); err == nil {
		t.Fatal("用不同指纹解密应失败 (文件不可跨机器克隆)")
	}
}

func TestLicenseStorage_TamperedFileFails(t *testing.T) {
	dir := t.TempDir()
	fp := "fp-tamper-test"
	logger := zap.NewNop()

	if err := saveEncryptedLicenseStore(dir, fp, &licenseStore{
		Licenses: []*License{{ID: "lic-1"}},
	}, logger); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 篡改文件内容
	licenseFile := filepath.Join(dir, "licenses.json")
	data, _ := os.ReadFile(licenseFile)
	if len(data) < 20 {
		t.Fatalf("file too short: %d bytes", len(data))
	}
	data[len(data)-1] ^= 0xFF // 翻转最后一字节
	if err := os.WriteFile(licenseFile, data, 0600); err != nil {
		t.Fatalf("write tampered file: %v", err)
	}

	if _, err := loadEncryptedLicenseStore(dir, fp, logger); err == nil {
		t.Fatal("篡改后的文件应解密失败 (GCM 认证标签校验)")
	}
}

func TestLicenseStorage_PlaintextMigration(t *testing.T) {
	dir := t.TempDir()
	fp := "fp-migrate-test"
	logger := zap.NewNop()

	// 写入明文 JSON（旧格式）
	plaintext := `{"licenses":[{"id":"legacy-lic","modules":["video"]}],"trials":null}`
	licenseFile := filepath.Join(dir, "licenses.json")
	if err := os.WriteFile(licenseFile, []byte(plaintext), 0600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}

	// 加载应自动迁移
	loaded, err := loadEncryptedLicenseStore(dir, fp, logger)
	if err != nil {
		t.Fatalf("load plaintext: %v", err)
	}
	if len(loaded.Licenses) != 1 || loaded.Licenses[0].ID != "legacy-lic" {
		t.Fatalf("migration failed: %+v", loaded.Licenses)
	}

	// 验证文件已迁移为加密格式（首字节不再是 '{'）
	migrated, _ := os.ReadFile(licenseFile)
	if len(migrated) == 0 || migrated[0] == '{' {
		t.Fatal("明文文件未迁移为加密格式")
	}
	if string(migrated[:4]) != licensesFileMagic {
		t.Fatalf("迁移后 magic = %q, want %q", string(migrated[:4]), licensesFileMagic)
	}
}

func TestLicenseStorage_PlaintextLegacyArrayMigration(t *testing.T) {
	dir := t.TempDir()
	fp := "fp-legacy-array"
	logger := zap.NewNop()

	// 写入纯 license 数组（更旧格式）
	plaintext := `[{"id":"old-lic","modules":["audio"]}]`
	licenseFile := filepath.Join(dir, "licenses.json")
	if err := os.WriteFile(licenseFile, []byte(plaintext), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 注意：纯数组首字节是 '['，不是 '{'，会被当作加密格式解析 → 应返回错误
	// 这是预期行为：极旧格式需手动迁移。验证它不会被误认为加密文件。
	_, err := loadEncryptedLicenseStore(dir, fp, logger)
	if err == nil {
		t.Fatal("纯数组旧格式应返回错误 (不支持自动迁移 '[' 开头格式)")
	}
}

func TestLicenseStorage_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()

	// 空目录应返回空 store，不报错
	loaded, err := loadEncryptedLicenseStore(dir, "fp-empty", logger)
	if err != nil {
		t.Fatalf("load from empty dir: %v", err)
	}
	if loaded == nil || len(loaded.Licenses) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestLicenseStorage_NonExistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	logger := zap.NewNop()

	loaded, err := loadEncryptedLicenseStore(dir, "fp-noexist", logger)
	if err != nil {
		t.Fatalf("load from non-existent dir: %v", err)
	}
	if loaded == nil || len(loaded.Licenses) != 0 {
		t.Fatal("expected empty store for non-existent dir")
	}
}

func TestDeriveLicenseStorageKey(t *testing.T) {
	key1 := deriveLicenseStorageKey("fingerprint-A")
	key2 := deriveLicenseStorageKey("fingerprint-B")
	key1Again := deriveLicenseStorageKey("fingerprint-A")

	if len(key1) != 32 {
		t.Fatalf("key length = %d, want 32 (AES-256)", len(key1))
	}
	if string(key1) == string(key2) {
		t.Fatal("不同指纹应派生不同密钥")
	}
	if string(key1) != string(key1Again) {
		t.Fatal("相同指纹应派生相同密钥 (确定性)")
	}
}

func TestGetMachineFingerprint_DoesNotPanic(t *testing.T) {
	// 验证新增的主板/BIOS 维度不会导致 panic（跨平台兼容性）
	fp, err := GetMachineFingerprint()
	if err != nil {
		t.Fatalf("GetMachineFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("fingerprint should not be empty")
	}
	if len(fp) != 32 {
		t.Fatalf("fingerprint length = %d, want 32 (16 bytes hex)", len(fp))
	}
	// 二次调用应稳定（确定性）
	fp2, _ := GetMachineFingerprint()
	if fp != fp2 {
		t.Fatal("fingerprint 不稳定 (两次调用结果不同)")
	}
}
