package module

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestGetMachineFingerprint(t *testing.T) {
	fp, err := GetMachineFingerprint()
	if err != nil {
		t.Fatalf("GetMachineFingerprint failed: %v", err)
	}
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}
	t.Logf("Machine fingerprint: %s", fp)
}

func TestGetMachineFingerprintConsistent(t *testing.T) {
	fp1, _ := GetMachineFingerprint()
	fp2, _ := GetMachineFingerprint()
	if fp1 != fp2 {
		t.Errorf("fingerprints should be consistent: %s != %s", fp1, fp2)
	}
}

func TestAuthManagerLoginLogout(t *testing.T) {
	logger := zap.NewNop()
	client := NewWebsiteClient("http://localhost:8081")
	mgr := NewAuthManager(logger, client)

	err := mgr.Login("JTE-TEST-1234-5678-ABCD")
	if err == nil {
		t.Log("Login succeeded (requires website server)")
	}

	info, ok := mgr.GetAuthInfo("JTE-TEST-1234-5678-ABCD")
	if ok && info != nil {
		t.Logf("Auth info: key=%s, valid=%v", info.LicenseKey, info.Valid)
	}
}

func TestAuthCacheExpiry(t *testing.T) {
	logger := zap.NewNop()
	client := NewWebsiteClient("http://localhost:8081")
	mgr := NewAuthManager(logger, client)
	mgr.cacheTTL = 1 * time.Hour

	info := &AuthInfo{
		LicenseKey: "JTE-TEST-0000",
		MachineFP:  "fp_test",
		Version:    "professional",
		ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
		CachedAt:   time.Now(),
		Valid:      true,
	}
	mgr.cache["JTE-TEST-0000"] = info

	if !mgr.Validate("JTE-TEST-0000") {
		t.Error("should validate with fresh cache")
	}

	info.CachedAt = time.Now().Add(-2 * time.Hour)
	info.Valid = true

	_ = mgr.Validate("JTE-TEST-0000")
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"JTE-1234-5678-ABCD-EFGH", "JTE-****EFGH"},
		{"short", "****"},
	}

	for _, tt := range tests {
		got := maskKey(tt.input)
		if got != tt.expected {
			t.Errorf("maskKey(%s) = %s, want %s", tt.input, got, tt.expected)
		}
	}
}