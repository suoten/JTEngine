package module

import (
	"testing"
	"time"
)

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-6]: 存储分级定价 + 永久授权版本锁定 测试
// ===================================================================

func TestLicense_HasFeature(t *testing.T) {
	tests := []struct {
		name     string
		tier     string
		features []string
		want     string
		has      bool
	}{
		{"free no archive", TierFree, nil, FeatureArchive, false},
		{"standard no archive", TierStandard, []string{FeatureVideo}, FeatureArchive, false},
		{"professional has archive", TierProfessional, nil, FeatureArchive, true},
		{"enterprise has all", TierEnterprise, nil, FeatureArchive, true},
		{"explicit feature", TierStandard, []string{FeatureArchive}, FeatureArchive, true},
		{"unlimited feature", TierStandard, []string{FeatureUnlimited}, FeatureAI, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &License{Tier: tt.tier, Features: tt.features}
			if got := l.HasFeature(tt.want); got != tt.has {
				t.Errorf("HasFeature(%s) = %v, want %v", tt.want, got, tt.has)
			}
		})
	}
}

func TestLicense_IsPermanent(t *testing.T) {
	l := &License{LicenseType: LicenseTypePermanent}
	if !l.IsPermanent() {
		t.Errorf("expected IsPermanent=true")
	}
	l2 := &License{LicenseType: LicenseTypeSubscription}
	if l2.IsPermanent() {
		t.Errorf("expected IsPermanent=false for subscription")
	}
	l3 := &License{} // 未设置 = 订阅
	if l3.IsPermanent() {
		t.Errorf("expected IsPermanent=false for unset type")
	}
}

func TestLicense_GetMaxVehicles(t *testing.T) {
	tests := []struct {
		name string
		lic  *License
		want int
	}{
		{"explicit value", &License{MaxVehicles: 500}, 500},
		{"free tier default", &License{Tier: TierFree}, 10},
		{"standard tier default", &License{Tier: TierStandard}, 10000},
		{"professional tier default", &License{Tier: TierProfessional}, 100000},
		{"enterprise 1000000 cap", &License{Tier: TierEnterprise}, 1000000},
		{"unknown tier", &License{Tier: "unknown"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lic.GetMaxVehicles(); got != tt.want {
				t.Errorf("GetMaxVehicles() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLicense_ValidateVehicleCount(t *testing.T) {
	// free tier: max 10
	l := &License{Tier: TierFree}
	if err := l.ValidateVehicleCount(5); err != nil {
		t.Errorf("expected 5 < 10 to pass, got %v", err)
	}
	if err := l.ValidateVehicleCount(10); err == nil {
		t.Errorf("expected 10 >= 10 to fail")
	}
	if err := l.ValidateVehicleCount(15); err == nil {
		t.Errorf("expected 15 >= 10 to fail")
	}
}

func TestLicense_ValidateVehicleCount_EnterpriseCap(t *testing.T) {
	// enterprise 上限 100 万
	l := &License{Tier: TierEnterprise}
	if err := l.ValidateVehicleCount(999999); err != nil {
		t.Errorf("999999 < 1000000 should pass, got %v", err)
	}
	if err := l.ValidateVehicleCount(1000000); err == nil {
		t.Errorf("1000000 >= 1000000 should fail (enterprise cap)")
	}
}

func TestLicense_ValidateVGroups(t *testing.T) {
	// standard tier: max 2 vgroups
	l := &License{Tier: TierStandard}
	if err := l.ValidateVGroups(2); err != nil {
		t.Errorf("expected 2 <= 2 to pass, got %v", err)
	}
	if err := l.ValidateVGroups(3); err == nil {
		t.Errorf("expected 3 > 2 to fail")
	}
}

func TestLicense_ValidateVGroups_Enterprise(t *testing.T) {
	l := &License{Tier: TierEnterprise}
	if err := l.ValidateVGroups(100); err != nil {
		t.Errorf("enterprise should be unlimited, got %v", err)
	}
}

func TestLicense_ValidateReplica(t *testing.T) {
	// free tier: max 1 replica
	l := &License{Tier: TierFree}
	if err := l.ValidateReplica(1); err != nil {
		t.Errorf("expected 1 <= 1 to pass, got %v", err)
	}
	if err := l.ValidateReplica(2); err == nil {
		t.Errorf("expected 2 > 1 to fail")
	}
}

func TestLicense_ValidateArchive(t *testing.T) {
	tests := []struct {
		name string
		lic  *License
		want bool // true = should pass
	}{
		{"free no archive", &License{Tier: TierFree}, false},
		{"standard no archive", &License{Tier: TierStandard}, false},
		{"professional has archive", &License{Tier: TierProfessional}, true},
		{"enterprise has archive", &License{Tier: TierEnterprise}, true},
		{"explicit archive feature", &License{Tier: TierStandard, Features: []string{FeatureArchive}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.lic.ValidateArchive()
			if tt.want && err != nil {
				t.Errorf("expected pass, got %v", err)
			}
			if !tt.want && err == nil {
				t.Errorf("expected fail, got nil")
			}
		})
	}
}

func TestLicense_ValidateMajorVersion(t *testing.T) {
	// 永久授权锁定到主版本 2，当前是 3 → 应失败
	l := &License{LicenseType: LicenseTypePermanent, MajorVersion: 2}
	if err := l.ValidateMajorVersion(3); err == nil {
		t.Errorf("expected version lock to fail (license=2, host=3)")
	}

	// 永久授权锁定到主版本 3，当前是 3 → 应通过
	l2 := &License{LicenseType: LicenseTypePermanent, MajorVersion: 3}
	if err := l2.ValidateMajorVersion(3); err != nil {
		t.Errorf("expected version match to pass, got %v", err)
	}

	// 订阅授权不锁版本
	l3 := &License{LicenseType: LicenseTypeSubscription, MajorVersion: 2}
	if err := l3.ValidateMajorVersion(3); err != nil {
		t.Errorf("subscription should not lock version, got %v", err)
	}

	// 未声明版本 → 兼容旧授权
	l4 := &License{LicenseType: LicenseTypePermanent, MajorVersion: 0}
	if err := l4.ValidateMajorVersion(3); err != nil {
		t.Errorf("unset major version should pass, got %v", err)
	}
}

func TestMajorVersionUpgradeFee(t *testing.T) {
	tests := []struct {
		price float64
		want  float64
	}{
		{1000.0, 500.0},
		{10000.0, 5000.0},
		{0.0, 0.0},
	}
	for _, tt := range tests {
		if got := MajorVersionUpgradeFee(tt.price); got != tt.want {
			t.Errorf("MajorVersionUpgradeFee(%v) = %v, want %v", tt.price, got, tt.want)
		}
	}
}

func TestLicense_HasSupport(t *testing.T) {
	l := &License{SupportTier: SupportStandard}
	if !l.HasSupport() {
		t.Errorf("standard support should return true")
	}
	l2 := &License{SupportTier: SupportNone}
	if l2.HasSupport() {
		t.Errorf("none support should return false")
	}
	l3 := &License{SupportTier: ""}
	if l3.HasSupport() {
		t.Errorf("empty support should return false")
	}
}

func TestGetTierDefaults(t *testing.T) {
	maxV, maxVG, maxR, features, ok := GetTierDefaults(TierFree)
	if !ok {
		t.Errorf("expected tier defaults to exist")
	}
	if maxV != 10 {
		t.Errorf("expected free max vehicles=10, got %d", maxV)
	}
	if maxVG != 1 {
		t.Errorf("expected free max vgroups=1, got %d", maxVG)
	}
	if maxR != 1 {
		t.Errorf("expected free max replica=1, got %d", maxR)
	}
	if len(features) != 0 {
		t.Errorf("expected free no features, got %v", features)
	}
}

func TestGetTierDefaults_Unknown(t *testing.T) {
	_, _, _, _, ok := GetTierDefaults("unknown")
	if ok {
		t.Errorf("expected unknown tier to return ok=false")
	}
}

func TestValidateTier(t *testing.T) {
	valid := []string{TierFree, TierStandard, TierProfessional, TierEnterprise, "FREE", "Standard"}
	for _, tier := range valid {
		if !ValidateTier(tier) {
			t.Errorf("expected tier %s to be valid", tier)
		}
	}
	invalid := []string{"", "unknown", "basic", "premium"}
	for _, tier := range invalid {
		if ValidateTier(tier) {
			t.Errorf("expected tier %s to be invalid", tier)
		}
	}
}

func TestLicenseManager_GetActiveLicense(t *testing.T) {
	m := &LicenseManager{
		licenses: []*License{
			{ID: "lic1", Tier: TierFree, ExpiresAt: time.Now().Add(24 * time.Hour)},
			{ID: "lic2", Tier: TierProfessional, ExpiresAt: time.Now().Add(24 * time.Hour)},
			{ID: "lic3", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	active := m.GetActiveLicense()
	if active == nil {
		t.Fatalf("expected non-nil active license")
	}
	if active.Tier != TierProfessional {
		t.Errorf("expected professional (highest tier), got %s", active.Tier)
	}
}

func TestLicenseManager_GetActiveLicense_SkipsExpired(t *testing.T) {
	m := &LicenseManager{
		licenses: []*License{
			{ID: "lic1", Tier: TierEnterprise, ExpiresAt: time.Now().Add(-1 * time.Hour)}, // 已过期
			{ID: "lic2", Tier: TierStandard, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	active := m.GetActiveLicense()
	if active == nil {
		t.Fatalf("expected non-nil active license")
	}
	if active.Tier != TierStandard {
		t.Errorf("expected standard (enterprise expired), got %s", active.Tier)
	}
}

func TestLicenseManager_GetActiveLicense_None(t *testing.T) {
	m := &LicenseManager{
		licenses: []*License{},
	}
	if active := m.GetActiveLicense(); active != nil {
		t.Errorf("expected nil when no licenses, got %v", active)
	}
}

func TestLicenseManager_ValidateVehicleCount_NoLicense(t *testing.T) {
	m := &LicenseManager{licenses: []*License{}}
	// 无授权时应返回 nil（向后兼容）
	if err := m.ValidateVehicleCount(1000000); err != nil {
		t.Errorf("expected nil without license, got %v", err)
	}
}

func TestLicenseManager_ValidateArchive_NoLicense(t *testing.T) {
	m := &LicenseManager{licenses: []*License{}}
	if err := m.ValidateArchive(); err != nil {
		t.Errorf("expected nil without license, got %v", err)
	}
}

func TestLicenseManager_ValidateArchive_WithLicense(t *testing.T) {
	m := &LicenseManager{
		licenses: []*License{
			{ID: "lic1", Tier: TierFree, ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	// free tier 无 archive 功能
	if err := m.ValidateArchive(); err == nil {
		t.Errorf("expected error for free tier archive")
	}
}
