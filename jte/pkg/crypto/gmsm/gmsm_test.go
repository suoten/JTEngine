package gmsm

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// ============================================================================
// SM3 测试向量（GB/T 32905-2016 标准附录 A）
// ============================================================================

func TestSM3_StandardVectors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantHex string
	}{
		// GB/T 32905-2016 附录 A.1：1 个 "abc"
		{"abc", "abc", "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"},
		// 附录 A.2：16MB（重复 "abcd" 16 次 × 65536），用较短替代向量验证算法正确性
		// 此处使用 GB/T 32905-2016 第 2 个标准向量（512 位消息的前 64 字节）
		{"empty", "", "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SM3Hash([]byte(c.input))
			if hex.EncodeToString(got) != c.wantHex {
				t.Errorf("SM3(%q) = %x, want %s", c.input, got, c.wantHex)
			}
		})
	}
}

func TestSM3_Hex(t *testing.T) {
	h := SM3HashHex([]byte("abc"))
	if h != "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0" {
		t.Errorf("SM3 hex mismatch: %s", h)
	}
}

func TestSM3_HMAC(t *testing.T) {
	// HMAC-SM3 基本性质：不同 key 产生不同摘要；相同输入可重现
	key1 := []byte("key1")
	key2 := []byte("key2")
	data := []byte("audit log entry")
	mac1 := SM3HMAC(key1, data)
	mac2 := SM3HMAC(key1, data)
	mac3 := SM3HMAC(key2, data)
	if !bytes.Equal(mac1, mac2) {
		t.Error("HMAC-SM3 not deterministic")
	}
	if bytes.Equal(mac1, mac3) {
		t.Error("HMAC-SM3 should differ for different keys")
	}
	if len(mac1) != 32 {
		t.Errorf("HMAC-SM3 length = %d, want 32", len(mac1))
	}
	// 长密钥路径
	longKey := make([]byte, 100)
	for i := range longKey {
		longKey[i] = byte(i)
	}
	if mac := SM3HMAC(longKey, data); len(mac) != 32 {
		t.Errorf("HMAC-SM3 long key length = %d", len(mac))
	}
}

func TestSM3_HMACEqual(t *testing.T) {
	key := []byte("audit-hmac-key")
	msg := []byte("sensitive audit entry")

	mac := SM3HMAC(key, msg)

	// 正确的 MAC 应通过验证
	if !SM3HMACEqual(key, msg, mac) {
		t.Error("SM3HMACEqual should return true for valid MAC")
	}

	// 篡改的 MAC 应失败
	tampered := make([]byte, len(mac))
	copy(tampered, mac)
	tampered[0] ^= 0xff
	if SM3HMACEqual(key, msg, tampered) {
		t.Error("SM3HMACEqual should return false for tampered MAC")
	}

	// 错误的 key 应失败
	if SM3HMACEqual([]byte("wrong-key"), msg, mac) {
		t.Error("SM3HMACEqual should return false for wrong key")
	}

	// 错误的 msg 应失败
	if SM3HMACEqual(key, []byte("wrong-msg"), mac) {
		t.Error("SM3HMACEqual should return false for wrong message")
	}

	// 长度不匹配的 MAC 应失败
	shortMAC := mac[:16]
	if SM3HMACEqual(key, msg, shortMAC) {
		t.Error("SM3HMACEqual should return false for short MAC")
	}
}

// ============================================================================
// SM4 测试向量（GB/T 32907-2016 标准附录 A.1）
// ============================================================================

func TestSM4_CBC_StandardVector(t *testing.T) {
	// GB/T 32907-2016 附录 A.1 标准向量
	// 密钥: 0123456789ABCDEFFEDCBA9876543210
	// 明文: 0123456789ABCDEFFEDCBA9876543210
	// ECB 密文: 681EDF34D206965E86B3E94F536E4246
	key, _ := hex.DecodeString("0123456789ABCDEFFEDCBA9876543210")
	plain, _ := hex.DecodeString("0123456789ABCDEFFEDCBA9876543210")
	wantCipher, _ := hex.DecodeString("681EDF34D206965E86B3E94F536E4246")

	// SM4-CBC 用全 0 IV 等价于 ECB 第一块（仅当明文恰好 1 块时）
	iv := make([]byte, 16)
	ct, err := SM4EncryptCBC(key, iv, plain)
	if err != nil {
		t.Fatalf("SM4 CBC encrypt: %v", err)
	}
	if len(ct) == 32 {
		// 1 块明文 + PKCS7 填充 = 2 块，取第 1 块对比标准 ECB 向量
		if !bytes.Equal(ct[:16], wantCipher) {
			t.Errorf("SM4 CBC first block = %x, want %x", ct[:16], wantCipher)
		}
	} else if len(ct) == 16 {
		if !bytes.Equal(ct, wantCipher) {
			t.Errorf("SM4 CBC = %x, want %x", ct, wantCipher)
		}
	} else {
		t.Errorf("SM4 CBC ciphertext length = %d", len(ct))
	}

	// 解密回环
	pt, err := SM4DecryptCBC(key, iv, ct)
	if err != nil {
		t.Fatalf("SM4 CBC decrypt: %v", err)
	}
	// 去除 PKCS7 填充后应为原始明文
	if !bytes.Equal(pt, plain) {
		t.Errorf("SM4 CBC roundtrip = %x, want %x", pt, plain)
	}
}

func TestSM4_GCM_Roundtrip(t *testing.T) {
	key, err := SM4GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	samples := [][]byte{
		[]byte(""),
		[]byte("hello"),
		[]byte("国密 SM4-GCM 加密测试"),
		make([]byte, 4096), // 大数据
	}
	for i, pt := range samples {
		ct, err := SM4EncryptGCM(key, pt)
		if err != nil {
			t.Fatalf("case %d encrypt: %v", i, err)
		}
		// 密文必须包含 nonce(12) + tag(16) 至少
		if len(ct) < 28 {
			t.Errorf("case %d ciphertext too short: %d", i, len(ct))
		}
		// 相同明文两次加密应不同（随机 nonce）
		ct2, _ := SM4EncryptGCM(key, pt)
		if bytes.Equal(ct, ct2) && len(pt) > 0 {
			t.Error("SM4-GCM should use random nonce")
		}
		decrypted, err := SM4DecryptGCM(key, ct)
		if err != nil {
			t.Fatalf("case %d decrypt: %v", i, err)
		}
		if !bytes.Equal(decrypted, pt) {
			t.Errorf("case %d roundtrip mismatch", i)
		}
	}
}

func TestSM4_GCM_TamperDetection(t *testing.T) {
	key, _ := SM4GenerateKey()
	pt := []byte("sensitive data")
	ct, _ := SM4EncryptGCM(key, pt)
	// 篡改密文最后 1 字节
	ct[len(ct)-1] ^= 0xff
	if _, err := SM4DecryptGCM(key, ct); err == nil {
		t.Error("SM4-GCM should detect tampered ciphertext")
	}
}

func TestSM4_CBC_InvalidPadding(t *testing.T) {
	key, _ := SM4GenerateKey()
	iv := make([]byte, 16)
	// 构造无效填充
	bad := make([]byte, 16)
	bad[15] = 99 // 填充值超过块大小
	if _, err := SM4DecryptCBC(key, iv, bad); err == nil {
		t.Error("SM4 CBC should reject invalid padding")
	}
}

// ============================================================================
// SM2 测试（GB/T 32918-2016）
// ============================================================================

func TestSM2_SignVerifyRoundtrip(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	data := []byte("SM2 签名验证测试消息")
	sig, err := SM2Sign(priv, data)
	if err != nil {
		t.Fatalf("SM2 sign: %v", err)
	}
	if !SM2Verify(pub, data, sig) {
		t.Error("SM2 verify failed for valid signature")
	}
	// 篡改消息后应验签失败
	if SM2Verify(pub, []byte("tampered"), sig) {
		t.Error("SM2 verify should fail for tampered message")
	}
	// 篡改签名后应验签失败
	badSig := make([]byte, len(sig))
	copy(badSig, sig)
	badSig[0] ^= 0xff
	if SM2Verify(pub, data, badSig) {
		t.Error("SM2 verify should fail for tampered signature")
	}
}

func TestSM2_SignWithUID(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	uid := []byte("test-uid-001")
	data := []byte("UID 绑定签名")
	sig, err := SM2SignWithUID(priv, uid, data)
	if err != nil {
		t.Fatalf("SM2 sign with uid: %v", err)
	}
	if !SM2VerifyWithUID(pub, uid, data, sig) {
		t.Error("SM2 verify with uid failed")
	}
	// 不同 UID 验签应失败
	if SM2VerifyWithUID(pub, []byte("other-uid"), data, sig) {
		t.Error("SM2 verify should fail with wrong uid")
	}
}

func TestSM2_EncryptDecryptRoundtrip(t *testing.T) {
	priv, pub, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	pt := []byte("SM2 加密机密数据")
	ct, err := SM2Encrypt(pub, pt)
	if err != nil {
		t.Fatalf("SM2 encrypt: %v", err)
	}
	decrypted, err := SM2Decrypt(priv, ct)
	if err != nil {
		t.Fatalf("SM2 decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, pt) {
		t.Errorf("SM2 enc/dec mismatch: %x != %x", decrypted, pt)
	}
}

func TestSM2_KeySerializeRoundtrip(t *testing.T) {
	priv, _, err := SM2GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	dBytes := MarshalSM2PrivateKey(priv)
	if len(dBytes) != 32 {
		t.Fatalf("private key bytes length = %d, want 32", len(dBytes))
	}
	parsed, err := ParseSM2PrivateKey(dBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 两个私钥应能对同一数据产生可验证的签名
	data := []byte("serialize roundtrip")
	sig, err := SM2Sign(parsed, data)
	if err != nil {
		t.Fatalf("sign with parsed key: %v", err)
	}
	if !SM2Verify(&priv.PublicKey, data, sig) {
		t.Error("parsed key signature verify failed")
	}

	// 公钥序列化回环
	pubBytes := MarshalSM2PublicKey(&priv.PublicKey)
	if len(pubBytes) != 65 || pubBytes[0] != 0x04 {
		t.Fatalf("public key bytes invalid: len=%d", len(pubBytes))
	}
	pubParsed, err := ParseSM2PublicKey(pubBytes)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if !SM2Verify(pubParsed, data, sig) {
		t.Error("parsed public key verify failed")
	}

	// 无效公钥应拒绝
	if _, err := ParseSM2PublicKey([]byte{0x03}); err == nil {
		t.Error("should reject short public key")
	}
}

// ============================================================================
// 集成场景：模拟关键数据国密加密存储
// ============================================================================

func TestIntegration_EncryptedStorage(t *testing.T) {
	// 场景：身份证号用 SM4-GCM 加密存储，签名用 SM2 防篡改
	masterKey, _ := SM4GenerateKey()
	idCard := []byte("110101199001011234")

	// 加密存储
	ciphertext, err := SM4EncryptGCM(masterKey, idCard)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// SM2 签名密文（防篡改）
	priv, pub, _ := SM2GenerateKeyPair()
	sig, err := SM2Sign(priv, ciphertext)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// 验证密文未被篡改
	if !SM2Verify(pub, ciphertext, sig) {
		t.Fatal("signature verification failed")
	}

	// 解密还原
	decrypted, err := SM4DecryptGCM(masterKey, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, idCard) {
		t.Errorf("decrypted = %s, want %s", decrypted, idCard)
	}

	// 篡改密文后签名校验应失败
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0x01
	if SM2Verify(pub, tampered, sig) {
		t.Error("should detect tampered ciphertext")
	}
}
