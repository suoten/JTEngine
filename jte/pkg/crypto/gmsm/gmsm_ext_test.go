package gmsm

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// ============================================================================
// SM4-ECB 测试
// ============================================================================

func TestSM4_ECB_StandardVector(t *testing.T) {
	// GB/T 32907-2016 附录 A.1 标准向量
	// 密钥: 0123456789ABCDEFFEDCBA9876543210
	// 明文: 0123456789ABCDEFFEDCBA9876543210
	// ECB 密文: 681EDF34D206965E86B3E94F536E4246
	key, _ := hex.DecodeString("0123456789ABCDEFFEDCBA9876543210")
	plain, _ := hex.DecodeString("0123456789ABCDEFFEDCBA9876543210")
	wantCipher, _ := hex.DecodeString("681EDF34D206965E86B3E94F536E4246")

	ct, err := SM4EncryptECB(key, plain)
	if err != nil {
		t.Fatalf("SM4 ECB encrypt: %v", err)
	}
	// PKCS7 填充后为 2 块，取第 1 块对比标准 ECB 向量
	if len(ct) == 32 {
		if !bytes.Equal(ct[:16], wantCipher) {
			t.Errorf("SM4 ECB first block = %x, want %x", ct[:16], wantCipher)
		}
	} else if len(ct) == 16 {
		if !bytes.Equal(ct, wantCipher) {
			t.Errorf("SM4 ECB = %x, want %x", ct, wantCipher)
		}
	} else {
		t.Errorf("SM4 ECB ciphertext length = %d", len(ct))
	}

	// 解密回环
	pt, err := SM4DecryptECB(key, ct)
	if err != nil {
		t.Fatalf("SM4 ECB decrypt: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Errorf("SM4 ECB roundtrip = %x, want %x", pt, plain)
	}
}

func TestSM4_ECB_Roundtrip(t *testing.T) {
	key, _ := SM4GenerateKey()
	samples := [][]byte{
		[]byte(""),
		[]byte("hello"),
		[]byte("国密 SM4-ECB 测试"),
		make([]byte, 256),
	}
	for i, pt := range samples {
		ct, err := SM4EncryptECB(key, pt)
		if err != nil {
			t.Fatalf("case %d encrypt: %v", i, err)
		}
		decrypted, err := SM4DecryptECB(key, ct)
		if err != nil {
			t.Fatalf("case %d decrypt: %v", i, err)
		}
		if !bytes.Equal(decrypted, pt) {
			t.Errorf("case %d roundtrip mismatch", i)
		}
	}
}

func TestSM4_ECB_NotBlockAligned(t *testing.T) {
	key, _ := SM4GenerateKey()
	// 构造非对齐密文
	bad := make([]byte, 15)
	if _, err := SM4DecryptECB(key, bad); err == nil {
		t.Error("SM4 ECB should reject non-block-aligned ciphertext")
	}
}

// ============================================================================
// SM4-CTR 测试
// ============================================================================

func TestSM4_CTR_Roundtrip(t *testing.T) {
	key, _ := SM4GenerateKey()
	iv, _ := SM4GenerateIV()
	samples := [][]byte{
		[]byte(""),
		[]byte("hello"),
		[]byte("国密 SM4-CTR 流式加密测试"),
		make([]byte, 4096),
	}
	for i, pt := range samples {
		ct, err := SM4EncryptCTR(key, iv, pt)
		if err != nil {
			t.Fatalf("case %d encrypt: %v", i, err)
		}
		// CTR 模式密文长度等于明文长度
		if len(ct) != len(pt) {
			t.Errorf("case %d: CTR ciphertext length = %d, want %d", i, len(ct), len(pt))
		}
		decrypted, err := SM4DecryptCTR(key, iv, ct)
		if err != nil {
			t.Fatalf("case %d decrypt: %v", i, err)
		}
		if !bytes.Equal(decrypted, pt) {
			t.Errorf("case %d roundtrip mismatch", i)
		}
	}
}

func TestSM4_CTR_AutoIV(t *testing.T) {
	key, _ := SM4GenerateKey()
	pt := []byte("CTR with auto-generated IV")
	// iv=nil 时自动生成随机 IV 并前置
	ct, err := SM4EncryptCTR(key, nil, pt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// 输出应为 IV(16) + ciphertext
	if len(ct) != 16+len(pt) {
		t.Errorf("ciphertext length = %d, want %d", len(ct), 16+len(pt))
	}
	// iv=nil 解密时自动从密文头部提取 IV
	pt2, err := SM4DecryptCTR(key, nil, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt2, pt) {
		t.Errorf("CTR auto-IV roundtrip mismatch: %x != %x", pt2, pt)
	}
}

func TestSM4_CTR_InvalidIV(t *testing.T) {
	key, _ := SM4GenerateKey()
	badIV := make([]byte, 8) // 错误长度
	if _, err := SM4EncryptCTR(key, badIV, []byte("test")); err == nil {
		t.Error("SM4 CTR should reject invalid IV length")
	}
}

// ============================================================================
// SM4-CBC auto-IV 测试
// ============================================================================

func TestSM4_CBC_AutoIV(t *testing.T) {
	key, _ := SM4GenerateKey()
	pt := []byte("CBC with auto-generated IV")
	// iv=nil 时自动生成随机 IV 并前置
	ct, err := SM4EncryptCBC(key, nil, pt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// 输出应为 IV(16) + ciphertext(对齐后)
	if len(ct) < 16+16 {
		t.Errorf("ciphertext too short: %d", len(ct))
	}
	// iv=nil 解密时自动从密文头部提取 IV
	pt2, err := SM4DecryptCBC(key, nil, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt2, pt) {
		t.Errorf("CBC auto-IV roundtrip mismatch: %x != %x", pt2, pt)
	}
}

// ============================================================================
// SM4GenerateIV 测试
// ============================================================================

func TestSM4GenerateIV(t *testing.T) {
	iv1, err := SM4GenerateIV()
	if err != nil {
		t.Fatalf("generate IV: %v", err)
	}
	if len(iv1) != 16 {
		t.Errorf("IV length = %d, want 16", len(iv1))
	}
	iv2, _ := SM4GenerateIV()
	if bytes.Equal(iv1, iv2) {
		t.Error("two generated IVs should differ (random)")
	}
}

// ============================================================================
// SM2 C1C2C3/C1C3C2 模式测试
// ============================================================================

func TestSM2_EncryptC1C3C2_Roundtrip(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("SM2 C1C3C2 mode test")
	ct, err := SM2EncryptWithMode(pub, pt, SM2ModeC1C3C2)
	if err != nil {
		t.Fatalf("encrypt C1C3C2: %v", err)
	}
	decrypted, err := SM2DecryptWithMode(priv, ct, SM2ModeC1C3C2)
	if err != nil {
		t.Fatalf("decrypt C1C3C2: %v", err)
	}
	if !bytes.Equal(decrypted, pt) {
		t.Errorf("C1C3C2 roundtrip mismatch: %x != %x", decrypted, pt)
	}
}

func TestSM2_EncryptC1C2C3_Roundtrip(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("SM2 C1C2C3 mode test")
	ct, err := SM2EncryptWithMode(pub, pt, SM2ModeC1C2C3)
	if err != nil {
		t.Fatalf("encrypt C1C2C3: %v", err)
	}
	decrypted, err := SM2DecryptWithMode(priv, ct, SM2ModeC1C2C3)
	if err != nil {
		t.Fatalf("decrypt C1C2C3: %v", err)
	}
	if !bytes.Equal(decrypted, pt) {
		t.Errorf("C1C2C3 roundtrip mismatch: %x != %x", decrypted, pt)
	}
}

func TestSM2_CrossModeIncompatible(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("cross mode test")
	// 用 C1C3C2 加密
	ctC1C3C2, err := SM2EncryptWithMode(pub, pt, SM2ModeC1C3C2)
	if err != nil {
		t.Fatalf("encrypt C1C3C2: %v", err)
	}
	// 用 C1C2C3 解密应失败（密文拼接顺序不同）
	_, err = SM2DecryptWithMode(priv, ctC1C3C2, SM2ModeC1C2C3)
	if err == nil {
		t.Error("decrypting C1C3C2 ciphertext with C1C2C3 mode should fail")
	}
}

func TestSM2_DefaultCompat(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("default compat test")
	// SM2Encrypt 默认输出 C1C3C2
	ct, err := SM2Encrypt(pub, pt)
	if err != nil {
		t.Fatalf("SM2Encrypt: %v", err)
	}
	// 用 SM2DecryptWithMode(C1C3C2) 应能解密
	dec, err := SM2DecryptWithMode(priv, ct, SM2ModeC1C3C2)
	if err != nil {
		t.Fatalf("decrypt with C1C3C2 mode: %v", err)
	}
	if !bytes.Equal(dec, pt) {
		t.Error("default encrypt should be compatible with C1C3C2 decrypt")
	}
}

// ============================================================================
// SM2 ASN1 编码测试
// ============================================================================

func TestSM2_ASN1_Roundtrip(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("SM2 ASN1 format test")
	ct, err := SM2EncryptASN1(pub, pt)
	if err != nil {
		t.Fatalf("encrypt ASN1: %v", err)
	}
	dec, err := SM2DecryptASN1(priv, ct)
	if err != nil {
		t.Fatalf("decrypt ASN1: %v", err)
	}
	if !bytes.Equal(dec, pt) {
		t.Errorf("ASN1 roundtrip mismatch: %x != %x", dec, pt)
	}
}

// ============================================================================
// ZeroPadding 测试
// ============================================================================

func TestZeroPad_Unpad(t *testing.T) {
	cases := [][]byte{
		[]byte("hello"),
		[]byte("1234567890abcdef"), // 恰好 1 块
		[]byte("1234567890abcdefghijk"),
		[]byte(""),
	}
	for i, data := range cases {
		padded := ZeroPad(data, 16)
		// 填充后长度应为 16 的倍数
		if len(padded)%16 != 0 {
			t.Errorf("case %d: padded length %d not multiple of 16", i, len(padded))
		}
		unpadded := ZeroUnpad(padded)
		if !bytes.Equal(unpadded, data) {
			t.Errorf("case %d: ZeroUnpad mismatch: %x != %x", i, unpadded, data)
		}
	}
}

func TestZeroPad_ExactBlock(t *testing.T) {
	// 恰好为 blockSize 整数倍时不应添加额外填充
	data := make([]byte, 32)
	padded := ZeroPad(data, 16)
	if len(padded) != 32 {
		t.Errorf("exact block should not add padding, got length %d", len(padded))
	}
}

// ============================================================================
// Buffer Pool 测试
// ============================================================================

func TestBufferPool_AcquireRelease(t *testing.T) {
	buf1 := AcquireBuffer()
	if buf1 == nil {
		t.Fatal("AcquireBuffer returned nil")
	}
	*buf1 = append(*buf1, "test data"...)
	if len(*buf1) != 9 {
		t.Errorf("buffer length = %d, want 9", len(*buf1))
	}

	ReleaseBuffer(buf1)

	buf2 := AcquireBuffer()
	if len(*buf2) != 0 {
		t.Errorf("after release, buffer length should be 0, got %d", len(*buf2))
	}
	// 确保容量仍然存在
	if cap(*buf2) < 9 {
		t.Errorf("buffer capacity should be preserved, got %d", cap(*buf2))
	}
	ReleaseBuffer(buf2)
}

// ============================================================================
// SM4 流式加密测试
// ============================================================================

func TestSM4_Stream_Roundtrip(t *testing.T) {
	key, _ := SM4GenerateKey()

	// 测试不同大小的数据
	sizes := []int{0, 1, 100, 64 * 1024, 128 * 1024, 300 * 1024}
	for _, size := range sizes {
		t.Run("size_"+itoa(size), func(t *testing.T) {
			plaintext := make([]byte, size)
			for i := range plaintext {
				plaintext[i] = byte(i % 256)
			}

			// 加密
			var encrypted bytes.Buffer
			err := SM4EncryptStream(key, bytes.NewReader(plaintext), &encrypted)
			if err != nil {
				t.Fatalf("encrypt stream: %v", err)
			}

			// 解密
			var decrypted bytes.Buffer
			err = SM4DecryptStream(key, &encrypted, &decrypted)
			if err != nil {
				t.Fatalf("decrypt stream: %v", err)
			}

			if !bytes.Equal(decrypted.Bytes(), plaintext) {
				t.Errorf("stream roundtrip mismatch for size %d: got %d bytes, want %d bytes",
					size, decrypted.Len(), len(plaintext))
			}
		})
	}
}

func TestSM4_Stream_TamperDetection(t *testing.T) {
	key, _ := SM4GenerateKey()
	plaintext := []byte("stream tamper detection test")

	var encrypted bytes.Buffer
	_ = SM4EncryptStream(key, bytes.NewReader(plaintext), &encrypted)

	// 篡改密文中的某个字节
	encBytes := encrypted.Bytes()
	if len(encBytes) > 20 {
		encBytes[20] ^= 0xff
	}

	var decrypted bytes.Buffer
	err := SM4DecryptStream(key, bytes.NewReader(encBytes), &decrypted)
	if err == nil {
		t.Error("stream decrypt should detect tampered ciphertext")
	}
}

func TestSM4_Stream_WrongKey(t *testing.T) {
	key1, _ := SM4GenerateKey()
	key2, _ := SM4GenerateKey()

	var encrypted bytes.Buffer
	_ = SM4EncryptStream(key1, bytes.NewReader([]byte("secret data")), &encrypted)

	var decrypted bytes.Buffer
	err := SM4DecryptStream(key2, &encrypted, &decrypted)
	if err == nil {
		t.Error("stream decrypt with wrong key should fail")
	}
}

// ============================================================================
// CryptoConfig 测试
// ============================================================================

func TestCryptoConfig_Default(t *testing.T) {
	opts := DefaultCryptoConfig()
	if !opts.SM2Enabled || !opts.SM3Enabled || !opts.SM4Enabled {
		t.Error("default config should enable all algorithms")
	}
	if opts.SM2DefaultMode != SM2ModeC1C3C2 {
		t.Error("default SM2 mode should be C1C3C2")
	}
}

func TestCryptoConfig_AllEnabled(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, err := NewCryptoConfig(DefaultCryptoConfig())
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if !cfg.SM2Enabled() {
		t.Error("SM2 should be enabled")
	}
	if !cfg.SM3Enabled() {
		t.Error("SM3 should be enabled")
	}
	if !cfg.SM4Enabled() {
		t.Error("SM4 should be enabled")
	}
	if cfg.IsHardwareMode() {
		t.Error("should be software mode by default")
	}
	if cfg.ProviderName() != "software" {
		t.Errorf("provider name = %s, want 'software'", cfg.ProviderName())
	}
	if len(cfg.SM4Key()) != 16 {
		t.Errorf("SM4 key length = %d, want 16", len(cfg.SM4Key()))
	}
}

func TestCryptoConfig_SM4KeyFromEnv(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "fedcba9876543210fedcba9876543210")
	cfg, err := NewCryptoConfig(&CryptoConfigOptions{
		SM4Enabled:     true,
		SM3Enabled:     true,
		SM4KeyHex:      "", // 从环境变量读取
		SM2DefaultMode: SM2ModeC1C3C2,
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	expected, _ := hex.DecodeString("fedcba9876543210fedcba9876543210")
	if !bytes.Equal(cfg.SM4Key(), expected) {
		t.Errorf("SM4 key from env mismatch: %x", cfg.SM4Key())
	}
}

func TestCryptoConfig_SM4MissingKey(t *testing.T) {
	// 确保环境变量未设置
	t.Setenv("JTE_SM4_KEY", "")
	_, err := NewCryptoConfig(&CryptoConfigOptions{
		SM4Enabled: true,
		SM4KeyHex:  "",
	})
	if err == nil {
		t.Error("should fail when SM4 key is missing")
	}
}

func TestCryptoConfig_SM4InvalidKey(t *testing.T) {
	_, err := NewCryptoConfig(&CryptoConfigOptions{
		SM4Enabled: true,
		SM4KeyHex:  "short",
	})
	if err == nil {
		t.Error("should fail with invalid SM4 key")
	}
}

func TestCryptoConfig_RotateSM4Key(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	oldKey := cfg.SM4Key()
	newKeyHex := "fedcba9876543210fedcba9876543210"
	if err := cfg.RotateSM4Key(newKeyHex); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	newKey := cfg.SM4Key()
	if bytes.Equal(oldKey, newKey) {
		t.Error("key should change after rotation")
	}
}

func TestCryptoConfig_RotateSM4Key_Invalid(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	if err := cfg.RotateSM4Key("short"); err == nil {
		t.Error("should reject invalid key length")
	}
}

func TestCryptoConfig_SetEnabled(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	cfg.SetEnabled(false, true, false)
	if cfg.SM2Enabled() {
		t.Error("SM2 should be disabled")
	}
	if !cfg.SM3Enabled() {
		t.Error("SM3 should be enabled")
	}
	if cfg.SM4Enabled() {
		t.Error("SM4 should be disabled")
	}
}

func TestCryptoConfig_SM2PrivateKey(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	priv, _, _ := SM2GenerateKeyPair()
	privHex := hex.EncodeToString(MarshalSM2PrivateKey(priv))

	cfg, err := NewCryptoConfig(&CryptoConfigOptions{
		SM2Enabled:     true,
		SM3Enabled:     true,
		SM4Enabled:     true,
		SM4KeyHex:      "0123456789abcdef0123456789abcdef",
		SM2PrivKeyHex:  privHex,
		SM2DefaultMode: SM2ModeC1C3C2,
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}

	keyBytes, err := cfg.SM2PrivateKey()
	if err != nil {
		t.Fatalf("get SM2 private key: %v", err)
	}
	if len(keyBytes) != 32 {
		t.Errorf("SM2 private key length = %d, want 32", len(keyBytes))
	}
}

func TestCryptoConfig_SM2PrivateKeyNotSet(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("JTE_SM2_PRIV_KEY", "")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())
	_, err := cfg.SM2PrivateKey()
	if err == nil {
		t.Error("should fail when SM2 private key is not set")
	}
}

func TestCryptoConfig_SetSM2PrivateKey(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	priv, _, _ := SM2GenerateKeyPair()
	privHex := hex.EncodeToString(MarshalSM2PrivateKey(priv))

	if err := cfg.SetSM2PrivateKey(privHex); err != nil {
		t.Fatalf("set SM2 private key: %v", err)
	}
	keyBytes, _ := cfg.SM2PrivateKey()
	if len(keyBytes) != 32 {
		t.Errorf("SM2 private key length = %d, want 32", len(keyBytes))
	}

	// 无效密钥
	if err := cfg.SetSM2PrivateKey("short"); err == nil {
		t.Error("should reject invalid SM2 key")
	}
}

func TestCryptoConfig_SM2KeyFromEnv(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	priv, _, _ := SM2GenerateKeyPair()
	privHex := hex.EncodeToString(MarshalSM2PrivateKey(priv))
	t.Setenv("JTE_SM2_PRIV_KEY", privHex)

	cfg, err := NewCryptoConfig(&CryptoConfigOptions{
		SM2Enabled:     true,
		SM3Enabled:     true,
		SM4Enabled:     true,
		SM4KeyHex:      "",
		SM2PrivKeyHex:  "",
		SM2DefaultMode: SM2ModeC1C3C2,
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	keyBytes, err := cfg.SM2PrivateKey()
	if err != nil {
		t.Fatalf("get SM2 private key: %v", err)
	}
	if len(keyBytes) != 32 {
		t.Errorf("SM2 private key length = %d, want 32", len(keyBytes))
	}
}

func TestCryptoConfig_GlobalConfig(t *testing.T) {
	ResetGlobalConfig()
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")

	err := InitGlobalConfig(DefaultCryptoConfig())
	if err != nil {
		t.Fatalf("init global config: %v", err)
	}

	cfg := GetGlobalConfig()
	if cfg == nil {
		t.Fatal("global config should not be nil after init")
	}
	if !cfg.SM4Enabled() {
		t.Error("SM4 should be enabled in global config")
	}

	// 再次调用应返回相同配置（sync.Once）
	err2 := InitGlobalConfig(DefaultCryptoConfig())
	if err2 != nil {
		t.Fatalf("second init should not error: %v", err2)
	}
	cfg2 := GetGlobalConfig()
	if cfg != cfg2 {
		t.Error("global config should be the same instance")
	}

	ResetGlobalConfig()
}

// ============================================================================
// HSMProvider Mock 测试
// ============================================================================

// mockHSMProvider 用于测试硬件对接接口。
type mockHSMProvider struct {
	name string
}

func (m *mockHSMProvider) Name() string { return m.name }
func (m *mockHSMProvider) SM2Sign(keyID string, uid, data []byte) ([]byte, error) {
	return nil, nil
}
func (m *mockHSMProvider) SM2Verify(keyID string, uid, data, signature []byte) (bool, error) {
	return true, nil
}
func (m *mockHSMProvider) SM2Encrypt(keyID string, plaintext []byte) ([]byte, error) {
	return plaintext, nil
}
func (m *mockHSMProvider) SM2Decrypt(keyID string, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}
func (m *mockHSMProvider) SM4Encrypt(keyID string, mode string, iv, plaintext []byte) ([]byte, error) {
	return plaintext, nil
}
func (m *mockHSMProvider) SM4Decrypt(keyID string, mode string, iv, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}
func (m *mockHSMProvider) Close() error { return nil }

func TestCryptoConfig_HardwareMode(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	if cfg.IsHardwareMode() {
		t.Error("should start in software mode")
	}

	mock := &mockHSMProvider{name: "mock-hsm"}
	cfg.SetProvider(mock)

	if !cfg.IsHardwareMode() {
		t.Error("should be in hardware mode after SetProvider")
	}
	if cfg.ProviderName() != "mock-hsm" {
		t.Errorf("provider name = %s, want 'mock-hsm'", cfg.ProviderName())
	}

	// 切换回软件模式
	cfg.SetProvider(nil)
	if cfg.IsHardwareMode() {
		t.Error("should be back in software mode")
	}
}

func TestCryptoConfig_Close(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, _ := NewCryptoConfig(DefaultCryptoConfig())

	mock := &mockHSMProvider{name: "mock-hsm"}
	cfg.SetProvider(mock)

	if err := cfg.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close 后应该回到软件模式
	if cfg.IsHardwareMode() {
		t.Error("should be software mode after close")
	}
}

// ============================================================================
// 辅助函数
// ============================================================================

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ============================================================================
// 集成测试：CryptoConfig + DataCipher 联动
// ============================================================================

func TestIntegration_CryptoConfigWithDataCipher(t *testing.T) {
	// 场景：通过 CryptoConfig 管理 SM4 密钥，使用 DataCipher 加密敏感数据
	t.Setenv("JTE_SM4_KEY", "0123456789abcdef0123456789abcdef")
	cfg, err := NewCryptoConfig(DefaultCryptoConfig())
	if err != nil {
		t.Fatalf("create config: %v", err)
	}

	// 使用 CryptoConfig 的密钥进行 SM4-GCM 加解密
	key := cfg.SM4Key()
	plaintext := []byte("敏感身份证号: 110101199001011234")

	ciphertext, err := SM4EncryptGCM(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// 轮换密钥后旧密文无法解密
	newKeyHex := "fedcba9876543210fedcba9876543210"
	if err := cfg.RotateSM4Key(newKeyHex); err != nil {
		t.Fatalf("rotate key: %v", err)
	}

	// 用旧密钥仍然可以解密（旧密钥在本地变量中）
	decrypted, err := SM4DecryptGCM(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt with old key: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Error("decryption with old key should succeed")
	}

	// 用新密钥无法解密旧密文
	_, err = SM4DecryptGCM(cfg.SM4Key(), ciphertext)
	if err == nil {
		t.Error("decryption with new key should fail for old ciphertext")
	}

	// 用新密钥加密新数据
	newCt, err := SM4EncryptGCM(cfg.SM4Key(), plaintext)
	if err != nil {
		t.Fatalf("encrypt with new key: %v", err)
	}
	newDec, err := SM4DecryptGCM(cfg.SM4Key(), newCt)
	if err != nil {
		t.Fatalf("decrypt with new key: %v", err)
	}
	if !bytes.Equal(newDec, plaintext) {
		t.Error("new key encrypt/decrypt roundtrip failed")
	}
}

// ============================================================================
// SM4 各模式标准向量综合验证
// ============================================================================

func TestSM4_AllModes_Roundtrip(t *testing.T) {
	key, _ := SM4GenerateKey()
	plaintext := []byte("国密 SM4 全模式测试 - GCM/CBC/ECB/CTR")

	// GCM
	ctGCM, err := SM4EncryptGCM(key, plaintext)
	if err != nil {
		t.Fatalf("GCM encrypt: %v", err)
	}
	ptGCM, err := SM4DecryptGCM(key, ctGCM)
	if err != nil {
		t.Fatalf("GCM decrypt: %v", err)
	}
	if !bytes.Equal(ptGCM, plaintext) {
		t.Error("GCM roundtrip failed")
	}

	// CBC with auto IV
	ctCBC, err := SM4EncryptCBC(key, nil, plaintext)
	if err != nil {
		t.Fatalf("CBC encrypt: %v", err)
	}
	ptCBC, err := SM4DecryptCBC(key, nil, ctCBC)
	if err != nil {
		t.Fatalf("CBC decrypt: %v", err)
	}
	if !bytes.Equal(ptCBC, plaintext) {
		t.Error("CBC roundtrip failed")
	}

	// ECB
	ctECB, err := SM4EncryptECB(key, plaintext)
	if err != nil {
		t.Fatalf("ECB encrypt: %v", err)
	}
	ptECB, err := SM4DecryptECB(key, ctECB)
	if err != nil {
		t.Fatalf("ECB decrypt: %v", err)
	}
	if !bytes.Equal(ptECB, plaintext) {
		t.Error("ECB roundtrip failed")
	}

	// CTR with auto IV
	ctCTR, err := SM4EncryptCTR(key, nil, plaintext)
	if err != nil {
		t.Fatalf("CTR encrypt: %v", err)
	}
	ptCTR, err := SM4DecryptCTR(key, nil, ctCTR)
	if err != nil {
		t.Fatalf("CTR decrypt: %v", err)
	}
	if !bytes.Equal(ptCTR, plaintext) {
		t.Error("CTR roundtrip failed")
	}

	// 确保不同模式产生不同密文
	if bytes.Equal(ctGCM, ctCBC) {
		t.Error("GCM and CBC should produce different ciphertexts")
	}
}

// ============================================================================
// 环境变量配置验证
// ============================================================================

func TestCryptoConfig_NoEnvVar(t *testing.T) {
	// 确保环境变量未设置
	os.Unsetenv("JTE_SM4_KEY")
	_, err := NewCryptoConfig(&CryptoConfigOptions{
		SM4Enabled: true,
		SM4KeyHex:  "",
	})
	if err == nil {
		t.Error("should fail when no SM4 key is available")
	}
	if !strings.Contains(err.Error(), "JTE_SM4_KEY") {
		t.Errorf("error message should mention JTE_SM4_KEY: %v", err)
	}
}
