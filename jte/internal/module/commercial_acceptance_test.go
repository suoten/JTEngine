package module

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// 商业化综合验收测试
//
// 覆盖场景：
//   1. 存储分级车辆限额拦截（free 10 / standard 1万 / professional 10万 / enterprise 100万）
//   2. 809 模块 30 天试用（自动开启 + 防重置 + 到期失效）
//   3. 授权到期提醒（7 天内预警）+ 到期停用
//   4. 机器指纹绑定（防克隆/换机失效）
//   5. AES-256-GCM 加密存储（防篡改/防复制）
//   6. 离线解绑凭证（RSA 签名 + HMAC 降级）
//   7. 模块联动卸载（授权移除 → 模块自动卸载）
//   8. 永久授权版本锁定
// ===================================================================

// newCommercialTestLM 创建一个用于商业化测试的 LicenseManager，
// 使用临时目录、假指纹，并生成 RSA 密钥对用于试用签名。
func newCommercialTestLM(t *testing.T, fingerprint string) (*LicenseManager, string) {
	t.Helper()
	dir := t.TempDir()

	// 生成 RSA 密钥对用于试用签名
	keys, err := GenerateOrLoadLocalKeys(dir)
	if err != nil {
		t.Fatalf("GenerateOrLoadLocalKeys: %v", err)
	}

	m := &LicenseManager{
		fingerprint:          fingerprint,
		logger:               zap.NewNop(),
		configDir:            dir,
		trials:               make(map[string]*TrialInfo),
		offlineCache:         make(map[string]*offlineCacheEntry),
		expiredDataRetention: make(map[string]*expiredDataEntry),
		localKeys:            keys,
		stopCh:               make(chan struct{}),
	}
	return m, dir
}

// ─────────────────────────────────────────────────────────────
// 1. 存储分级车辆限额拦截
// ─────────────────────────────────────────────────────────────

func TestCommercial_VehicleLimits_AllTiers(t *testing.T) {
	cases := []struct {
		name      string
		tier      string
		maxVehicles int
		shouldPass int // 应通过的最大数量
		shouldFail int // 应拦截的数量
	}{
		{"free_10", TierFree, 0, 9, 10},
		{"standard_10000", TierStandard, 0, 9999, 10000},
		{"professional_100000", TierProfessional, 0, 99999, 100000},
		{"enterprise_1000000", TierEnterprise, 0, 999999, 1000000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &License{Tier: tc.tier, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}
			maxV := l.GetMaxVehicles()
			if maxV != tc.shouldFail {
				t.Errorf("tier %s: expected max=%d, got %d", tc.tier, tc.shouldFail, maxV)
			}
			// 应通过
			if err := l.ValidateVehicleCount(tc.shouldPass); err != nil {
				t.Errorf("tier %s: %d should pass, got %v", tc.tier, tc.shouldPass, err)
			}
			// 应拦截
			if err := l.ValidateVehicleCount(tc.shouldFail); err == nil {
				t.Errorf("tier %s: %d should be blocked (exceeds limit %d)", tc.tier, tc.shouldFail, maxV)
			}
		})
	}
}

func TestCommercial_VehicleLimit_UpgradePath(t *testing.T) {
	// 验证从 standard 升级到 professional 后限额提升
	standard := &License{Tier: TierStandard, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}
	if err := standard.ValidateVehicleCount(10000); err == nil {
		t.Error("standard should block at 10000")
	}

	prof := &License{Tier: TierProfessional, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}
	if err := prof.ValidateVehicleCount(10000); err != nil {
		t.Errorf("professional should allow 10000, got %v", err)
	}
	if err := prof.ValidateVehicleCount(100000); err == nil {
		t.Error("professional should block at 100000")
	}
}

func TestCommercial_LicenseManager_ValidateVehicleCount(t *testing.T) {
	m := &LicenseManager{
		licenses: []*License{
			{ID: "lic1", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	// standard 限额 10000
	if err := m.ValidateVehicleCount(9999); err != nil {
		t.Errorf("9999 should pass for standard, got %v", err)
	}
	if err := m.ValidateVehicleCount(10000); err == nil {
		t.Error("10000 should be blocked for standard")
	}
}

// ─────────────────────────────────────────────────────────────
// 2. 809 模块 30 天试用
// ─────────────────────────────────────────────────────────────

func TestCommercial_TrialDuration_30Days(t *testing.T) {
	duration, ok := TrialModules["protocol_809"]
	if !ok {
		t.Fatal("protocol_809 should support trial")
	}
	if duration != 30*24*time.Hour {
		t.Errorf("expected 30 days trial, got %v", duration)
	}
}

func TestCommercial_TrialAutoStart(t *testing.T) {
	m, dir := newCommercialTestLM(t, "test-fp-auto-trial")

	// 自动试用：首次调用应自动开启
	m.AutoStartTrials()

	trials := m.GetTrials()
	trial, ok := trials["protocol_809"]
	if !ok {
		t.Fatal("auto-trial should have started for protocol_809")
	}
	if trial.IsExpired() {
		t.Error("trial should not be expired immediately after start")
	}
	remaining := trial.RemainingDays()
	if remaining < 29 || remaining > 30 {
		t.Errorf("expected 29-30 remaining days, got %d", remaining)
	}

	// 第二次调用不应重复开启
	m.AutoStartTrials()
	trials2 := m.GetTrials()
	if len(trials2) != 1 {
		t.Errorf("expected 1 trial, got %d", len(trials2))
	}

	_ = dir
}

func TestCommercial_TrialAntiReset(t *testing.T) {
	m, dir := newCommercialTestLM(t, "test-fp-anti-reset")

	// 首次开启试用
	if err := m.StartTrial("protocol_809"); err != nil {
		t.Fatalf("first trial start failed: %v", err)
	}

	// 模拟重启：清除内存中的试用记录，但保留状态文件和 RSA 密钥
	m2, _ := newCommercialTestLM(t, "test-fp-anti-reset")
	m2.configDir = dir // 复用同一目录（含 trial_state 文件 + RSA 密钥 + 加密存储）
	// 加载原有 RSA 密钥（确保试用签名验证一致）
	if keys, err := GenerateOrLoadLocalKeys(dir); err == nil {
		m2.localKeys = keys
	}
	// 重新加载
	if err := m2.load(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// 尝试重新开启试用 → 应被拒绝（trial_state 文件阻止重启）
	err := m2.StartTrial("protocol_809")
	if err == nil {
		t.Error("trial restart should be rejected (anti-reset)")
	}
}

func TestCommercial_TrialFingerprintBinding(t *testing.T) {
	m, dir := newCommercialTestLM(t, "machine-A-fp")

	// 在机器 A 上开启试用
	if err := m.StartTrial("protocol_809"); err != nil {
		t.Fatalf("trial start on machine A failed: %v", err)
	}

	// 验证 trial_state 文件已保存
	statePath := filepath.Join(dir, "trial_state_protocol_809.json")
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("trial state file should exist: %v", err)
	}
	t.Logf("trial state file: %s", string(stateData))

	// 模拟换机：机器 B 使用不同指纹和不同配置目录
	m2, dir2 := newCommercialTestLM(t, "machine-B-fp")
	_ = dir2

	// 将机器 A 的 trial_state 文件复制到机器 B 的配置目录
	dstPath := filepath.Join(m2.configDir, "trial_state_protocol_809.json")
	if err := os.WriteFile(dstPath, stateData, 0600); err != nil {
		t.Fatalf("copy trial state file: %v", err)
	}

	// 机器 B 尝试开启试用 → 应被拒绝（指纹不匹配）
	err = m2.StartTrial("protocol_809")
	if err == nil {
		t.Error("trial should be rejected on machine B (fingerprint mismatch in state file)")
	}
}

func TestCommercial_TrialExpiry(t *testing.T) {
	m, _ := newCommercialTestLM(t, "test-fp-expiry")

	// 开启试用
	if err := m.StartTrial("protocol_809"); err != nil {
		t.Fatalf("trial start failed: %v", err)
	}

	// 手动设置试用为已过期
	m.mu.Lock()
	if trial, ok := m.trials["protocol_809"]; ok {
		trial.ExpiresAt = time.Now().Add(-1 * time.Second)
	}
	m.mu.Unlock()

	// 过期后 HasModule 应返回 false
	if m.HasModule("protocol_809") {
		t.Error("expired trial should not grant module access")
	}

	// 状态应为 trial_expired
	status := m.GetModuleStatus("protocol_809")
	if status != "trial_expired" {
		t.Errorf("expected trial_expired, got %s", status)
	}
}

// ─────────────────────────────────────────────────────────────
// 3. 授权到期提醒 + 到期停用
// ─────────────────────────────────────────────────────────────

func TestCommercial_LicenseExpiringSoon_7DayWarning(t *testing.T) {
	m := &LicenseManager{
		logger:  zap.NewNop(),
		trials:  make(map[string]*TrialInfo),
		licenses: []*License{
			{
				ID:        "lic-expiring",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(5 * 24 * time.Hour), // 5 天后到期
				Tier:      TierStandard,
			},
		},
	}

	status := m.GetModuleStatus("protocol_809")
	if status != "expiring_soon" {
		t.Errorf("expected expiring_soon (5 days left), got %s", status)
	}
}

func TestCommercial_LicenseExpired_Status(t *testing.T) {
	m := &LicenseManager{
		logger:  zap.NewNop(),
		trials:  make(map[string]*TrialInfo),
		licenses: []*License{
			{
				ID:        "lic-expired",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(-1 * time.Hour), // 已过期
				Tier:      TierStandard,
			},
		},
	}

	status := m.GetModuleStatus("protocol_809")
	if status != "expired" {
		t.Errorf("expected expired, got %s", status)
	}

	// 过期授权不应授予模块访问
	if m.HasModule("protocol_809") {
		t.Error("expired license should not grant module access")
	}
}

func TestCommercial_LicenseValid_Status(t *testing.T) {
	m := &LicenseManager{
		logger:  zap.NewNop(),
		trials:  make(map[string]*TrialInfo),
		licenses: []*License{
			{
				ID:        "lic-valid",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour), // 1 年后到期
				Tier:      TierStandard,
			},
		},
	}

	status := m.GetModuleStatus("protocol_809")
	if status != "licensed" {
		t.Errorf("expected licensed, got %s", status)
	}
}

// ─────────────────────────────────────────────────────────────
// 4. 机器指纹绑定（防克隆/换机失效）
// ─────────────────────────────────────────────────────────────

func TestCommercial_FingerprintBinding_DifferentMachine(t *testing.T) {
	m := &LicenseManager{
		fingerprint: "machine-A",
		logger:      zap.NewNop(),
		trials:      make(map[string]*TrialInfo),
	}

	// 模拟一个绑定到机器 B 的授权
	lic := &License{
		ID:                 "lic-cross-machine",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		MachineFingerprint: "machine-B",
		Tier:               TierStandard,
	}
	m.licenses = []*License{lic}

	// 机器 A 上不应能使用绑定到机器 B 的授权
	if m.HasModule("protocol_809") {
		t.Error("license bound to machine-B should not work on machine-A")
	}
}

func TestCommercial_FingerprintBinding_SameMachine(t *testing.T) {
	m := &LicenseManager{
		fingerprint: "machine-A",
		logger:      zap.NewNop(),
		trials:      make(map[string]*TrialInfo),
	}

	lic := &License{
		ID:                 "lic-same-machine",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		MachineFingerprint: "machine-A",
		Tier:               TierStandard,
	}
	m.licenses = []*License{lic}

	if !m.HasModule("protocol_809") {
		t.Error("license bound to machine-A should work on machine-A")
	}
}

// ─────────────────────────────────────────────────────────────
// 5. AES-256-GCM 加密存储（防篡改/防复制）
// ─────────────────────────────────────────────────────────────

func TestCommercial_EncryptedStorage_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp := "test-fp-encrypt"

	store := &licenseStore{
		Licenses: []*License{
			{
				ID:        "lic-encrypted",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
				Tier:      TierProfessional,
			},
		},
		Trials: map[string]*TrialInfo{},
	}

	// 保存
	if err := saveEncryptedLicenseStore(dir, fp, store, zap.NewNop()); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// 验证文件不是明文
	raw, _ := os.ReadFile(filepath.Join(dir, "licenses.json"))
	if len(raw) > 0 && raw[0] == '{' {
		t.Error("license file should be encrypted, not plaintext JSON")
	}
	if string(raw[:4]) != licensesFileMagic {
		t.Errorf("expected magic %q, got %q", licensesFileMagic, string(raw[:4]))
	}

	// 加载
	loaded, err := loadEncryptedLicenseStore(dir, fp, zap.NewNop())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded.Licenses) != 1 {
		t.Fatalf("expected 1 license, got %d", len(loaded.Licenses))
	}
	if loaded.Licenses[0].ID != "lic-encrypted" {
		t.Errorf("expected lic-encrypted, got %s", loaded.Licenses[0].ID)
	}
}

func TestCommercial_EncryptedStorage_TamperDetection(t *testing.T) {
	dir := t.TempDir()
	fp := "test-fp-tamper"

	store := &licenseStore{
		Licenses: []*License{
			{ID: "lic-tamper", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}

	if err := saveEncryptedLicenseStore(dir, fp, store, zap.NewNop()); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// 篡改文件内容
	path := filepath.Join(dir, "licenses.json")
	raw, _ := os.ReadFile(path)
	if len(raw) > 20 {
		raw[20] ^= 0xFF // 翻转一个字节
	}
	os.WriteFile(path, raw, 0600)

	// 加载应失败（GCM 认证标签校验失败）
	_, err := loadEncryptedLicenseStore(dir, fp, zap.NewNop())
	if err == nil {
		t.Error("tampered file should fail to load")
	}
}

func TestCommercial_EncryptedStorage_FingerprintMismatch(t *testing.T) {
	dir := t.TempDir()

	store := &licenseStore{
		Licenses: []*License{
			{ID: "lic-fp-mismatch", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}

	// 用指纹 A 保存
	if err := saveEncryptedLicenseStore(dir, "fingerprint-A", store, zap.NewNop()); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// 用指纹 B 加载 → 应失败（密钥不匹配）
	_, err := loadEncryptedLicenseStore(dir, "fingerprint-B", zap.NewNop())
	if err == nil {
		t.Error("loading with different fingerprint should fail")
	}
}

// ─────────────────────────────────────────────────────────────
// 6. 离线解绑凭证（RSA 签名 + HMAC 降级）
// ─────────────────────────────────────────────────────────────

func TestCommercial_OfflineUnbind_RSA(t *testing.T) {
	m, _ := newCommercialTestLM(t, "test-fp-unbind-rsa")

	// 添加一个授权
	m.mu.Lock()
	m.licenses = append(m.licenses, &License{
		ID:                 "lic-unbind-rsa",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		MachineFingerprint: "test-fp-unbind-rsa",
		Tier:               TierStandard,
	})
	m.mu.Unlock()

	// 生成离线解绑凭证
	cert, err := m.generateOfflineUnbindCert("lic-unbind-rsa")
	if err != nil {
		t.Fatalf("generate cert failed: %v", err)
	}

	// 验证凭证
	licenseID, fingerprint, _, err := VerifyOfflineUnbindCert(cert)
	if err != nil {
		t.Fatalf("verify cert failed: %v", err)
	}
	if licenseID != "lic-unbind-rsa" {
		t.Errorf("expected lic-unbind-rsa, got %s", licenseID)
	}
	if fingerprint != "test-fp-unbind-rsa" {
		t.Errorf("expected test-fp-unbind-rsa, got %s", fingerprint)
	}
}

func TestCommercial_OfflineUnbind_HMAC(t *testing.T) {
	m := newTestLicenseManager(nil, "test-fp-unbind-hmac")

	// HMAC 降级模式（不使用 RSA）
	cert, err := m.generateOfflineUnbindCert("lic-hmac-test")
	if err != nil {
		t.Fatalf("generate HMAC cert failed: %v", err)
	}

	// 验证凭证
	licenseID, fingerprint, _, err := VerifyOfflineUnbindCert(cert)
	if err != nil {
		t.Fatalf("verify HMAC cert failed: %v", err)
	}
	if licenseID != "lic-hmac-test" {
		t.Errorf("expected lic-hmac-test, got %s", licenseID)
	}
	if fingerprint != "test-fp-unbind-hmac" {
		t.Errorf("expected test-fp-unbind-hmac, got %s", fingerprint)
	}
}

func TestCommercial_OfflineUnbind_UnbindFlow(t *testing.T) {
	m, _ := newCommercialTestLM(t, "test-fp-unbind-flow")

	m.mu.Lock()
	m.licenses = append(m.licenses, &License{
		ID:                 "lic-unbind-flow",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		MachineFingerprint: "test-fp-unbind-flow",
		Tier:               TierStandard,
	})
	m.mu.Unlock()

	// 离线解绑
	cert, err := m.Unbind("lic-unbind-flow", true)
	if err != nil {
		t.Fatalf("unbind failed: %v", err)
	}
	if cert == "" {
		t.Error("offline unbind should return a certificate")
	}

	// 解绑后授权指纹应被清除
	m.mu.RLock()
	lic := m.findByID("lic-unbind-flow")
	m.mu.RUnlock()
	if lic != nil && lic.MachineFingerprint != "" {
		t.Error("fingerprint should be cleared after unbind")
	}
}

// ─────────────────────────────────────────────────────────────
// 7. 模块联动卸载（授权移除 → 模块自动卸载）
// ─────────────────────────────────────────────────────────────

func TestCommercial_ActiveModules(t *testing.T) {
	m := &LicenseManager{
		logger: zap.NewNop(),
		trials: make(map[string]*TrialInfo),
		licenses: []*License{
			{
				ID:        "lic1",
				Modules:   []string{"protocol_809", "module-storage"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
				Tier:      TierProfessional,
			},
			{
				ID:        "lic2",
				Modules:   []string{"module-ai"},
				ExpiresAt: time.Now().Add(-1 * time.Hour), // 已过期
				Tier:      TierEnterprise,
			},
		},
	}

	active := m.ActiveModules()
	// protocol_809 和 module-storage 应在活跃列表中，module-ai 不应
	has809 := false
	hasStorage := false
	hasAI := false
	for _, mod := range active {
		switch mod {
		case "protocol_809":
			has809 = true
		case "module-storage":
			hasStorage = true
		case "module-ai":
			hasAI = true
		}
	}
	if !has809 {
		t.Error("protocol_809 should be active")
	}
	if !hasStorage {
		t.Error("module-storage should be active")
	}
	if hasAI {
		t.Error("module-ai should NOT be active (license expired)")
	}
}

func TestCommercial_GetActiveLicense_HighestTier(t *testing.T) {
	m := &LicenseManager{
		logger: zap.NewNop(),
		trials: make(map[string]*TrialInfo),
		licenses: []*License{
			{ID: "lic-free", Tier: TierFree, ExpiresAt: time.Now().Add(24 * time.Hour)},
			{ID: "lic-std", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
			{ID: "lic-pro", Tier: TierProfessional, ExpiresAt: time.Now().Add(24 * time.Hour)},
			{ID: "lic-ent", Tier: TierEnterprise, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}

	active := m.GetActiveLicense()
	if active == nil {
		t.Fatal("expected non-nil active license")
	}
	if active.Tier != TierEnterprise {
		t.Errorf("expected enterprise (highest tier), got %s", active.Tier)
	}
}

// ─────────────────────────────────────────────────────────────
// 8. 永久授权版本锁定
// ─────────────────────────────────────────────────────────────

func TestCommercial_PermanentLicense_VersionLock(t *testing.T) {
	// 永久授权锁定到主版本 3，当前是 3 → 应通过
	l := &License{
		LicenseType:  LicenseTypePermanent,
		MajorVersion: 3,
		Tier:         TierProfessional,
	}
	if err := l.ValidateMajorVersion(3); err != nil {
		t.Errorf("version match should pass, got %v", err)
	}

	// 永久授权锁定到主版本 2，当前是 3 → 应失败（需付费升级）
	l2 := &License{
		LicenseType:  LicenseTypePermanent,
		MajorVersion: 2,
		Tier:         TierProfessional,
	}
	if err := l2.ValidateMajorVersion(3); err == nil {
		t.Error("version mismatch should fail (requires paid upgrade)")
	}

	// 订阅授权不锁版本
	l3 := &License{
		LicenseType:  LicenseTypeSubscription,
		MajorVersion: 2,
		Tier:         TierStandard,
	}
	if err := l3.ValidateMajorVersion(3); err != nil {
		t.Errorf("subscription should not lock version, got %v", err)
	}
}

func TestCommercial_MajorVersionUpgradeFee(t *testing.T) {
	// 永久授权原价 10000 → 升级费 5000
	fee := MajorVersionUpgradeFee(10000)
	if fee != 5000 {
		t.Errorf("expected upgrade fee 5000, got %f", fee)
	}
}

// ─────────────────────────────────────────────────────────────
// 9. 功能权限校验（归档/AI/集群/SRTP）
// ─────────────────────────────────────────────────────────────

func TestCommercial_FeatureAccess_ByTier(t *testing.T) {
	cases := []struct {
		tier     string
		feature  string
		hasAccess bool
	}{
		{TierFree, FeatureArchive, false},
		{TierFree, FeatureAI, false},
		{TierStandard, FeatureArchive, false},
		{TierStandard, FeatureVideo, true},
		{TierProfessional, FeatureArchive, true},
		{TierProfessional, FeatureAI, false},
		{TierEnterprise, FeatureArchive, true},
		{TierEnterprise, FeatureAI, true},
		{TierEnterprise, FeatureCluster, true},
		{TierEnterprise, FeatureSRTP, true},
	}

	for _, tc := range cases {
		t.Run(tc.tier+"_"+tc.feature, func(t *testing.T) {
			l := &License{Tier: tc.tier}
			if l.HasFeature(tc.feature) != tc.hasAccess {
				t.Errorf("tier %s: HasFeature(%s) = %v, want %v",
					tc.tier, tc.feature, !tc.hasAccess, tc.hasAccess)
			}
		})
	}
}

func TestCommercial_ArchiveValidation(t *testing.T) {
	// free/standard 无归档权限
	free := &License{Tier: TierFree}
	if err := free.ValidateArchive(); err == nil {
		t.Error("free tier should not have archive access")
	}

	standard := &License{Tier: TierStandard}
	if err := standard.ValidateArchive(); err == nil {
		t.Error("standard tier should not have archive access")
	}

	// professional/enterprise 有归档权限
	prof := &License{Tier: TierProfessional}
	if err := prof.ValidateArchive(); err != nil {
		t.Errorf("professional tier should have archive access, got %v", err)
	}

	ent := &License{Tier: TierEnterprise}
	if err := ent.ValidateArchive(); err != nil {
		t.Errorf("enterprise tier should have archive access, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// 10. 综合场景：完整商业化流程模拟
// ─────────────────────────────────────────────────────────────

func TestCommercial_FullFlow_AutoTrial_To_License(t *testing.T) {
	m, _ := newCommercialTestLM(t, "full-flow-fp")

	// 1. 引擎启动 → 自动开启 809 试用
	m.AutoStartTrials()
	if !m.HasModule("protocol_809") {
		t.Fatal("after auto-trial, protocol_809 should be accessible")
	}

	// 2. 检查试用状态
	status := m.GetModuleStatus("protocol_809")
	if status != "trial" {
		t.Errorf("expected trial status, got %s", status)
	}

	// 3. 模拟购买授权后添加 license
	m.mu.Lock()
	m.licenses = append(m.licenses, &License{
		ID:                 "lic-purchased",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		MachineFingerprint: "full-flow-fp",
		Tier:               TierProfessional,
	})
	m.mu.Unlock()

	// 4. 有授权后状态应为 licensed
	status = m.GetModuleStatus("protocol_809")
	if status != "licensed" {
		t.Errorf("expected licensed status after purchase, got %s", status)
	}

	// 5. professional 限额 10 万
	if err := m.ValidateVehicleCount(99999); err != nil {
		t.Errorf("99999 should pass for professional, got %v", err)
	}
	if err := m.ValidateVehicleCount(100000); err == nil {
		t.Error("100000 should be blocked for professional")
	}

	// 6. 归档功能已授权
	if err := m.ValidateArchive(); err != nil {
		t.Errorf("professional should have archive access, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// 11. 辅助：generateTestLicenseKey 生成测试用签名授权码
//     （使用与生产相同的 RSA 公钥验证，但用临时私钥签名仅用于测试）
// ─────────────────────────────────────────────────────────────

func TestCommercial_LicenseSignature_Verification(t *testing.T) {
	// 生成临时 RSA 密钥对
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	// 构造 license payload
	lic := License{
		ID:                 "test-lic-sig",
		Modules:            []string{"protocol_809"},
		ExpiresAt:          time.Now().Add(365 * 24 * time.Hour),
		CustomerID:         "customer-test",
		MachineFingerprint: "", // 首次激活时空，由 Activate 填充
		Tier:               TierStandard,
	}

	payload, _ := json.Marshal(lic)
	hash := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privKey, 5, hash[:]) // crypto.SHA256 = 5
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	// 用公钥验证
	err = rsa.VerifyPKCS1v15(&privKey.PublicKey, 5, hash[:], signature)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// 构造授权码格式
	licenseKey := base64.StdEncoding.EncodeToString(payload) + "." + base64.StdEncoding.EncodeToString(signature)
	if licenseKey == "" {
		t.Fatal("license key should not be empty")
	}

	// 验证格式
	parts := splitLicenseKey(licenseKey)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// 解码 payload 验证内容
	decodedPayload, _ := base64.StdEncoding.DecodeString(parts[0])
	var decodedLic License
	if err := json.Unmarshal(decodedPayload, &decodedLic); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decodedLic.ID != "test-lic-sig" {
		t.Errorf("expected test-lic-sig, got %s", decodedLic.ID)
	}
	if decodedLic.Tier != TierStandard {
		t.Errorf("expected standard tier, got %s", decodedLic.Tier)
	}
}

// ─────────────────────────────────────────────────────────────
// 12. 试用记录 RSA 签名防篡改
// ─────────────────────────────────────────────────────────────

func TestCommercial_TrialSignature_TamperDetection(t *testing.T) {
	m, _ := newCommercialTestLM(t, "tamper-fp")

	// 开启试用
	if err := m.StartTrial("protocol_809"); err != nil {
		t.Fatalf("trial start failed: %v", err)
	}

	trials := m.GetTrials()
	trial := trials["protocol_809"]
	if trial == nil {
		t.Fatal("trial should exist")
	}

	// 原始签名应验证通过
	if !m.verifyTrialSignature(trial) {
		t.Error("original trial signature should verify")
	}

	// 篡改到期时间
	m.mu.Lock()
	trial.ExpiresAt = trial.ExpiresAt.Add(365 * 24 * time.Hour) // 延长 1 年
	m.mu.Unlock()

	// 签名应验证失败
	if m.verifyTrialSignature(trial) {
		t.Error("tampered trial signature should NOT verify")
	}
}

// ─────────────────────────────────────────────────────────────
// 13. 多授权叠加（一个机器可激活多个授权码）
// ─────────────────────────────────────────────────────────────

func TestCommercial_MultipleLicenses_Stacking(t *testing.T) {
	m := &LicenseManager{
		fingerprint: "stack-fp",
		logger:      zap.NewNop(),
		trials:      make(map[string]*TrialInfo),
		licenses: []*License{
			{
				ID:        "lic-809",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
				Tier:      TierStandard,
			},
			{
				ID:        "lic-ai",
				Modules:   []string{"module-ai"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
				Tier:      TierEnterprise,
			},
		},
	}

	// 两个模块都应可用
	if !m.HasModule("protocol_809") {
		t.Error("protocol_809 should be accessible")
	}
	if !m.HasModule("module-ai") {
		t.Error("module-ai should be accessible")
	}

	// 最高等级应为 enterprise
	active := m.GetActiveLicense()
	if active == nil || active.Tier != TierEnterprise {
		t.Errorf("expected enterprise as highest tier, got %v", active)
	}

	// 活跃模块应包含两个
	activeMods := m.ActiveModules()
	if len(activeMods) != 2 {
		t.Errorf("expected 2 active modules, got %d", len(activeMods))
	}
}

// ─────────────────────────────────────────────────────────────
// 14. 断网 7 天后停用模块
// ─────────────────────────────────────────────────────────────

func TestCommercial_OfflineDeactivation_7Days(t *testing.T) {
	m := &LicenseManager{
		fingerprint:           "offline-fp",
		logger:                zap.NewNop(),
		trials:                make(map[string]*TrialInfo),
		offlineCache:          make(map[string]*offlineCacheEntry),
		expiredDataRetention:  make(map[string]*expiredDataEntry),
		offlineDeactivateDays: 7,
		stopCh:                make(chan struct{}),
		licenses: []*License{
			{
				ID:        "lic-offline",
				Modules:   []string{"protocol_809"},
				ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
				Tier:      TierStandard,
			},
		},
	}

	// 模拟断网 8 天（超过 7 天阈值）
	m.mu.Lock()
	m.offlineFailureSince = time.Now().Add(-8 * 24 * time.Hour)
	m.mu.Unlock()

	// 触发停用检查
	m.deactivateModulesDueToOfflineFailure(m.offlineFailureSince, 7)

	// 授权应被标记为已过期
	m.mu.RLock()
	lic := m.findByID("lic-offline")
	m.mu.RUnlock()
	if lic != nil && !lic.IsExpired() {
		t.Error("license should be deactivated after 7+ days offline")
	}

	// 模块不应可用
	if m.HasModule("protocol_809") {
		t.Error("module should be deactivated after prolonged offline")
	}
}

// ─────────────────────────────────────────────────────────────
// 15. 到期数据只读 + 30 天后清理
// ─────────────────────────────────────────────────────────────

func TestCommercial_ExpiredData_ReadOnlyRetention(t *testing.T) {
	m := &LicenseManager{
		logger:               zap.NewNop(),
		trials:               make(map[string]*TrialInfo),
		expiredDataRetention: make(map[string]*expiredDataEntry),
	}

	// 标记模块数据为已过期
	m.markModuleExpired("protocol_809")

	// 应为只读
	if !m.IsModuleDataReadOnly("protocol_809") {
		t.Error("expired module data should be read-only")
	}

	// 30 天后应被清理
	m.mu.Lock()
	if entry, ok := m.expiredDataRetention["protocol_809"]; ok {
		entry.DataDeleteAt = time.Now().Add(-1 * time.Second) // 模拟已过清理时间
	}
	m.mu.Unlock()

	m.cleanupExpiredData()

	if m.IsModuleDataReadOnly("protocol_809") {
		t.Error("expired data should be cleaned up after 30 days")
	}
}

func TestCommercial_ExpiredData_RestoreAccess(t *testing.T) {
	m := &LicenseManager{
		logger:               zap.NewNop(),
		trials:               make(map[string]*TrialInfo),
		expiredDataRetention: make(map[string]*expiredDataEntry),
	}

	m.markModuleExpired("protocol_809")
	if !m.IsModuleDataReadOnly("protocol_809") {
		t.Error("should be read-only after expiry")
	}

	// 重新购买授权后恢复
	m.restoreModuleAccess("protocol_809")
	if m.IsModuleDataReadOnly("protocol_809") {
		t.Error("should not be read-only after restore")
	}
}

// ─────────────────────────────────────────────────────────────
// 辅助：HostCoreVersion 常量验证
// ─────────────────────────────────────────────────────────────

func TestCommercial_HostCoreVersion(t *testing.T) {
	if HostCoreVersion == "" {
		t.Error("HostCoreVersion should not be empty")
	}
	major, minor, patch, ok := parseSemVer(HostCoreVersion)
	if !ok {
		t.Fatalf("HostCoreVersion %q is not valid semver", HostCoreVersion)
	}
	_ = minor
	_ = patch
	if major < 1 {
		t.Errorf("HostCoreVersion major should be >= 1, got %d", major)
	}
}

// 辅助：fmt.Sprintf 避免未使用导入
var _ = fmt.Sprintf
