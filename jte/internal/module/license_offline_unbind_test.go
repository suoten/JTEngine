package module

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// newTestLicenseManager 构造一个不加载 RSA 密钥、不读配置目录的 LicenseManager，
// 仅用于测试 HMAC 离线解绑路径。
func newTestLicenseManager(secret []byte, fingerprint string) *LicenseManager {
	return &LicenseManager{
		fingerprint:         fingerprint,
		logger:              zap.NewNop(),
		trials:              make(map[string]*TrialInfo),
		offlineCache:        make(map[string]*offlineCacheEntry),
		expiredDataRetention: make(map[string]*expiredDataEntry),
		offlineUnbindSecret: secret,
	}
}

// signHMACUnbindCert 用指定 secret 手工签发一个 UNBIND- 前缀的 HMAC 凭证，
// 复刻 generateOfflineUnbindCert 降级路径的签名逻辑，供验证测试使用。
func signHMACUnbindCert(t *testing.T, licenseID, fingerprint string, timestamp int64, secret []byte) string {
	t.Helper()
	if len(secret) == 0 {
		secret = defaultOfflineUnbindHMACSecret
	}
	key := sha256.Sum256(append([]byte(fingerprint), secret...))
	signingPayload := fmt.Sprintf("%s|%s|%d", licenseID, fingerprint, timestamp)
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(signingPayload))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	certPayload := fmt.Sprintf("%s|%s|%d|%s", licenseID, fingerprint, timestamp, signature)
	return "UNBIND-" + base64.StdEncoding.EncodeToString([]byte(certPayload))
}

// TestDeriveOfflineUnbindKey_InjectedSecretDiffersFromDefault 验证注入自定义
// secret 后派生的 HMAC key 与使用默认 secret 派生的 key 不同——这是修复硬编码
// secret 安全价值的前提。
func TestDeriveOfflineUnbindKey_InjectedSecretDiffersFromDefault(t *testing.T) {
	const fp = "test-fingerprint-abc"
	customSecret := []byte("a-very-long-and-random-custom-secret-32bytes!")

	mDefault := newTestLicenseManager(nil, fp)
	mCustom := newTestLicenseManager(customSecret, fp)

	keyDefault := mDefault.deriveOfflineUnbindKey(fp)
	keyCustom := mCustom.deriveOfflineUnbindKey(fp)

	if fmt.Sprintf("%x", keyDefault) == fmt.Sprintf("%x", keyCustom) {
		t.Fatal("injected secret must produce a different key from the default; otherwise the fix has no security value")
	}
}

// TestDeriveOfflineUnbindKey_NilSecretFallsBackToDefault 验证未注入 secret
// （nil）时回退到 defaultOfflineUnbindHMACSecret，保持向后兼容。
func TestDeriveOfflineUnbindKey_NilSecretFallsBackToDefault(t *testing.T) {
	const fp = "test-fingerprint-xyz"
	mNil := newTestLicenseManager(nil, fp)

	// 直接用默认 secret 派生，作为基准
	keyRef := sha256.Sum256(append([]byte(fp), defaultOfflineUnbindHMACSecret...))
	keyGot := mNil.deriveOfflineUnbindKey(fp)

	if fmt.Sprintf("%x", keyGot) != fmt.Sprintf("%x", keyRef[:]) {
		t.Fatal("nil secret must fall back to defaultOfflineUnbindHMACSecret")
	}
}

// TestVerifyOfflineUnbindCertWithSecret_SignAndVerify 验证用自定义 secret
// 签发的凭证可用同一 secret 通过 VerifyOfflineUnbindCertWithSecret 校验。
func TestVerifyOfflineUnbindCertWithSecret_SignAndVerify(t *testing.T) {
	const (
		licenseID   = "lic-12345"
		fingerprint = "fp-abcdef"
	)
	var ts int64 = 1719000000
	customSecret := []byte("another-very-long-random-secret-32bytes!!")

	cert := signHMACUnbindCert(t, licenseID, fingerprint, ts, customSecret)

	gotID, gotFP, gotTS, err := VerifyOfflineUnbindCertWithSecret(cert, customSecret)
	if err != nil {
		t.Fatalf("verify with matching secret failed: %v", err)
	}
	if gotID != licenseID || gotFP != fingerprint || gotTS != ts {
		t.Fatalf("verified payload mismatch: got id=%s fp=%s ts=%d", gotID, gotFP, gotTS)
	}
}

// TestVerifyOfflineUnbindCertWithSecret_CrossSecretFails 验证用 secret A
// 签发的凭证无法用 secret B 通过校验——这是修复的核心安全收益。
func TestVerifyOfflineUnbindCertWithSecret_CrossSecretFails(t *testing.T) {
	const (
		licenseID   = "lic-67890"
		fingerprint = "fp-cross"
	)
	var ts int64 = 1719000001
	secretA := []byte("secret-A-very-long-random-32bytes-min!!")
	secretB := []byte("secret-B-very-long-random-32bytes-min!!")

	cert := signHMACUnbindCert(t, licenseID, fingerprint, ts, secretA)

	_, _, _, err := VerifyOfflineUnbindCertWithSecret(cert, secretB)
	if err == nil {
		t.Fatal("verification must fail when secret differs from signing secret")
	}
	if !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestVerifyOfflineUnbindCert_BackwardCompatDefaultSecret 验证用默认 secret
// 签发的旧凭证可通过原 VerifyOfflineUnbindCert（nil secret）校验——保证既有
// 部署升级后已签发的凭证不失效。
func TestVerifyOfflineUnbindCert_BackwardCompatDefaultSecret(t *testing.T) {
	const (
		licenseID   = "lic-legacy"
		fingerprint = "fp-legacy"
	)
	var ts int64 = 1719000002

	// 用 nil secret 走默认路径签发（模拟修复前的行为）
	cert := signHMACUnbindCert(t, licenseID, fingerprint, ts, nil)

	// 用原 API（无 secret 参数）校验
	gotID, gotFP, gotTS, err := VerifyOfflineUnbindCert(cert)
	if err != nil {
		t.Fatalf("backward-compat verify failed: %v", err)
	}
	if gotID != licenseID || gotFP != fingerprint || gotTS != ts {
		t.Fatalf("verified payload mismatch: got id=%s fp=%s ts=%d", gotID, gotFP, gotTS)
	}
}

// TestVerifyOfflineUnbindCertWithSecret_DefaultSecretVerifiesLegacyCert
// 验证显式传 nil secret 时 VerifyOfflineUnbindCertWithSecret 也能校验默认
// secret 签发的凭证——保证两个 API 语义一致。
func TestVerifyOfflineUnbindCertWithSecret_DefaultSecretVerifiesLegacyCert(t *testing.T) {
	const (
		licenseID   = "lic-legacy2"
		fingerprint = "fp-legacy2"
	)
	var ts int64 = 1719000003

	cert := signHMACUnbindCert(t, licenseID, fingerprint, ts, nil)

	if _, _, _, err := VerifyOfflineUnbindCertWithSecret(cert, nil); err != nil {
		t.Fatalf("VerifyOfflineUnbindCertWithSecret with nil must accept default-signed cert: %v", err)
	}
}

// TestVerifyOfflineUnbindCert_RejectsMalformedPrefix 验证缺少 UNBIND- 前缀
// 的凭证被拒绝。
func TestVerifyOfflineUnbindCert_RejectsMalformedPrefix(t *testing.T) {
	_, _, _, err := VerifyOfflineUnbindCert("INVALID-prefix-payload")
	if err == nil {
		t.Fatal("must reject cert without UNBIND- prefix")
	}
	if !strings.Contains(err.Error(), "missing UNBIND- prefix") {
		t.Fatalf("unexpected error: %v", err)
	}
}
