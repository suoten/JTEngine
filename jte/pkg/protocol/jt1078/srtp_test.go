package jt1078

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"
)

// ===================================================================
// P2-8 SRTP 密钥交换与加密 —— 测试
// ===================================================================

// makeRTP 构造一个最小可用的 RTP 包用于测试（12B 头 + N 字节 payload）。
func makeRTP(seqNum uint16, ssrc uint32, payload []byte) []byte {
	buf := make([]byte, 12+len(payload))
	buf[0] = 0x80 // V=2, 无 Padding/Extension/CSRC
	buf[1] = 0x60 // M=0, PT=96
	binary.BigEndian.PutUint16(buf[2:4], seqNum)
	binary.BigEndian.PutUint32(buf[4:8], 12345) // timestamp
	binary.BigEndian.PutUint32(buf[8:12], ssrc)
	copy(buf[12:], payload)
	return buf
}

func TestDeriveKeyMaterial(t *testing.T) {
	master := []byte("0123456789abcdef") // 16B
	km := DeriveKeyMaterial(master)

	if len(km.EncKey) != 16 {
		t.Fatalf("enc key len = %d, want 16", len(km.EncKey))
	}
	if len(km.AuthKey) != 20 {
		t.Fatalf("auth key len = %d, want 20", len(km.AuthKey))
	}
	if len(km.Salt) != 14 {
		t.Fatalf("salt len = %d, want 14", len(km.Salt))
	}
	// 密钥独立性：enc/auth/salt 互不相同
	if string(km.EncKey) == string(km.AuthKey[:16]) {
		t.Fatal("enc key == auth key (应相互独立)")
	}
	if string(km.EncKey) == string(km.Salt[:16]) {
		t.Fatal("enc key == salt (应相互独立)")
	}
	// 不同 masterKey 派生不同密钥
	km2 := DeriveKeyMaterial([]byte("abcdef0123456789"))
	if string(km.EncKey) == string(km2.EncKey) {
		t.Fatal("不同 masterKey 派生出相同 enc key")
	}
}

func TestSRTPSession_EncryptDecryptRoundTrip_AES(t *testing.T) {
	master, err := GenerateSRTPMasterKey()
	if err != nil {
		t.Fatalf("generate master key: %v", err)
	}
	enc, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("new encrypt session: %v", err)
	}
	dec, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("new decrypt session: %v", err)
	}

	payload := []byte("hello jt1078 srtp payload")
	rtp := makeRTP(0x1000, 0x12345678, payload)

	srtp, err := enc.Encrypt(rtp)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// SRTP = RTP(header+enc_payload) + 10B auth tag
	if len(srtp) != len(rtp)+SRTPAuthTagLen {
		t.Fatalf("srtp len = %d, want %d", len(srtp), len(rtp)+SRTPAuthTagLen)
	}
	// 头部不应被加密（前 12 字节保持明文）
	if string(srtp[:12]) != string(rtp[:12]) {
		t.Fatal("RTP header was encrypted (应保留明文)")
	}

	decrypted, err := dec.Decrypt(srtp)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(rtp) {
		t.Fatal("decrypted != original RTP")
	}
}

func TestSRTPSession_AuthTagTampering(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i)
	}
	enc, _ := NewSRTPSession(master, "AES-128-CM")
	dec, _ := NewSRTPSession(master, "AES-128-CM")

	rtp := makeRTP(0x2000, 0xAABBCCDD, []byte("sensitive payload"))
	srtp, _ := enc.Encrypt(rtp)

	// 篡改认证标签最后一字节
	srtp[len(srtp)-1] ^= 0xFF
	if _, err := dec.Decrypt(srtp); err == nil {
		t.Fatal("认证标签被篡改但解密成功 (应拒绝)")
	}
}

func TestSRTPSession_PayloadTampering(t *testing.T) {
	master := make([]byte, 16)
	enc, _ := NewSRTPSession(master, "AES-128-CM")
	dec, _ := NewSRTPSession(master, "AES-128-CM")

	rtp := makeRTP(0x3000, 0x11223344, []byte("payload data"))
	srtp, _ := enc.Encrypt(rtp)

	// 篡改加密后的 payload（header 之后、auth tag 之前）
	srtp[15] ^= 0x01
	if _, err := dec.Decrypt(srtp); err == nil {
		t.Fatal("payload 被篡改但解密成功 (应拒绝)")
	}
}

func TestSRTPSession_ROCRollover(t *testing.T) {
	master := make([]byte, 16)
	enc, _ := NewSRTPSession(master, "AES-128-CM")
	dec, _ := NewSRTPSession(master, "AES-128-CM")

	// 跨越回绕点：0xFFFE → 0xFFFF → 0x0000
	for _, seq := range []uint16{0xFFFE, 0xFFFF, 0x0000} {
		rtp := makeRTP(seq, 0x55555555, []byte("x"))
		srtp, err := enc.Encrypt(rtp)
		if err != nil {
			t.Fatalf("encrypt seq=0x%04X: %v", seq, err)
		}
		if _, err := dec.Decrypt(srtp); err != nil {
			t.Fatalf("decrypt seq=0x%04X (ROC 回绕): %v", seq, err)
		}
	}
	// 验证 ROC 已自增
	if enc.roc == 0 {
		t.Fatal("ROC 未自增 (回绕后应为 1)")
	}
}

func TestSRTPSession_IVUniqueness(t *testing.T) {
	master := make([]byte, 16)
	s, _ := NewSRTPSession(master, "AES-128-CM")

	iv1 := s.buildIV(0x12345678, 0x1000)
	iv2 := s.buildIV(0x12345678, 0x1001)
	iv3 := s.buildIV(0x87654321, 0x1000)

	if string(iv1) == string(iv2) {
		t.Fatal("相同 SSRC 不同 SeqNum 产生相同 IV (IV 复用)")
	}
	if string(iv1) == string(iv3) {
		t.Fatal("不同 SSRC 相同 SeqNum 产生相同 IV (IV 复用)")
	}
}

// TestSRTPSession_IVUniquenessAcrossRollover 验证 SeqNum 回绕后 IV 不复用（P0 回归）。
// AUTO-FIX-2026-07-02 [P0]: 原 buildIV 未折叠 ROC，导致 seq=0x0000 在 ROC=0/1 时 IV 碰撞。
// 修复后 ROC 折叠进 IV bytes 4-7，回绕前后 IV 必须不同。
func TestSRTPSession_IVUniquenessAcrossRollover(t *testing.T) {
	master := make([]byte, 16)
	s, _ := NewSRTPSession(master, "AES-128-CM")
	ssrc := uint32(0xCAFEBABE)

	// ROC=0, SEQ=0x0000
	ivBefore := s.buildIV(ssrc, 0x0000)

	// 模拟回绕：0xFFFF -> 0x0000，触发 ROC 自增到 1
	s.updateROC(0xFFFF)
	s.updateROC(0x0000)
	if s.roc != 1 {
		t.Fatalf("回绕后 ROC = %d, want 1", s.roc)
	}

	// ROC=1, SEQ=0x0000 —— 修复前与 ivBefore 相同（IV 复用漏洞），修复后必须不同
	ivAfter := s.buildIV(ssrc, 0x0000)
	if string(ivBefore) == string(ivAfter) {
		t.Fatal("SeqNum 回绕后 IV 复用 (ROC 未折叠进 IV) —— 安全漏洞")
	}

	// ROC 仅影响 IV bytes 4-7，SSRC(bytes 0-3) 与 SEQ(bytes 8-9) 应保持一致
	if string(ivBefore[0:4]) != string(ivAfter[0:4]) {
		t.Error("SSRC 段(bytes 0-3)不应随 ROC 改变")
	}
	if string(ivBefore[8:10]) != string(ivAfter[8:10]) {
		t.Error("SEQ 段(bytes 8-9)不应随 ROC 改变")
	}
	// bytes 4-7 应不同（ROC 0 vs 1）
	if string(ivBefore[4:8]) == string(ivAfter[4:8]) {
		t.Error("ROC 段(bytes 4-7)应在回绕后改变")
	}
}

// TestSRTPSession_IVRolloverRoundTrip 验证回绕后加解密仍能正确配对。
// 加密端和解密端各自维护 ROC，只要按相同 seq 顺序处理，ROC 保持同步，IV 一致。
func TestSRTPSession_IVRolloverRoundTrip(t *testing.T) {
	master := make([]byte, 16)
	enc, _ := NewSRTPSession(master, "AES-128-CM")
	dec, _ := NewSRTPSession(master, "AES-128-CM")

	// 跨越回绕点：0xFFFE -> 0xFFFF -> 0x0000 -> 0x0001
	payload := []byte("rollover payload")
	for _, seq := range []uint16{0xFFFE, 0xFFFF, 0x0000, 0x0001} {
		rtp := makeRTP(seq, 0x55555555, payload)
		srtp, err := enc.Encrypt(rtp)
		if err != nil {
			t.Fatalf("encrypt seq=0x%04X: %v", seq, err)
		}
		decrypted, err := dec.Decrypt(srtp)
		if err != nil {
			t.Fatalf("decrypt seq=0x%04X: %v", seq, err)
		}
		if string(decrypted) != string(rtp) {
			t.Fatalf("seq=0x%04X 解密结果与原文不一致", seq)
		}
	}
	if enc.roc != 1 || dec.roc != 1 {
		t.Fatalf("回绕后 ROC enc=%d dec=%d, want both 1", enc.roc, dec.roc)
	}
}

func TestSRTPSession_ShortKey(t *testing.T) {
	if _, err := NewSRTPSession([]byte("short"), "AES-128-CM"); err == nil {
		t.Fatal("短密钥应返回错误")
	}
}

func TestSRTPSession_UnsupportedCipher(t *testing.T) {
	master := make([]byte, 16)
	if _, err := NewSRTPSession(master, "DES-CBC"); err == nil {
		t.Fatal("不支持的加密套件应返回错误")
	}
}

func TestSRTPSession_SM4ProviderPath(t *testing.T) {
	// 使用 AES 块作为 SM4 的替身，验证 pluggable 接口路径正确工作。
	// 实际 SM4 由 module-crypto 注册。
	original := sm4Provider
	defer func() { sm4Provider = original }()

	sm4Provider = &mockSM4Provider{}
	master := make([]byte, 16)
	enc, err := NewSRTPSession(master, "SM4-CBC")
	if err != nil {
		t.Fatalf("new SM4 session: %v", err)
	}
	dec, err := NewSRTPSession(master, "SM4-CBC")
	if err != nil {
		t.Fatalf("new SM4 decrypt session: %v", err)
	}

	rtp := makeRTP(0x4000, 0xDEADBEEF, []byte("sm4 test payload"))
	srtp, err := enc.Encrypt(rtp)
	if err != nil {
		t.Fatalf("sm4 encrypt: %v", err)
	}
	decrypted, err := dec.Decrypt(srtp)
	if err != nil {
		t.Fatalf("sm4 decrypt: %v", err)
	}
	if string(decrypted) != string(rtp) {
		t.Fatal("SM4 解密结果与原文不一致")
	}
}

func TestSRTPSession_SM4NotRegistered(t *testing.T) {
	original := sm4Provider
	defer func() { sm4Provider = original }()
	sm4Provider = nil

	master := make([]byte, 16)
	if _, err := NewSRTPSession(master, "SM4-CBC"); err == nil {
		t.Fatal("SM4 未注册时应返回错误")
	}
}

func TestSRTPSession_TooShort(t *testing.T) {
	master := make([]byte, 16)
	dec, _ := NewSRTPSession(master, "AES-128-CM")
	if _, err := dec.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("过短数据应返回错误")
	}
}

func TestComputePayloadOffset_CSRCAndExtension(t *testing.T) {
	// 带 2 个 CSRC (8B) + Extension (4B header + 4B data)
	buf := make([]byte, 12+8+8)
	buf[0] = 0x82 // V=2, CSRC count=2
	buf[0] |= 0x10 // Extension bit
	// CSRC[0], CSRC[1] (8B)
	binary.BigEndian.PutUint32(buf[12:16], 0x11111111)
	binary.BigEndian.PutUint32(buf[16:20], 0x22222222)
	// Extension header: ID(2B) + Len(2B)=1 (表示 4B data)
	binary.BigEndian.PutUint16(buf[20:22], 0xABCD)
	binary.BigEndian.PutUint16(buf[22:24], 1)
	// Extension data (4B)
	binary.BigEndian.PutUint32(buf[24:28], 0x33333333)

	offset := computePayloadOffset(buf)
	if offset != 28 {
		t.Fatalf("offset = %d, want 28", offset)
	}
}

func TestGenerateSRTPMasterKey(t *testing.T) {
	k1, err := GenerateSRTPMasterKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if len(k1) != 16 {
		t.Fatalf("key len = %d, want 16", len(k1))
	}
	k2, _ := GenerateSRTPMasterKey()
	if string(k1) == string(k2) {
		t.Fatal("两次生成的密钥相同 (应随机)")
	}
}

// mockSM4Provider 使用 AES 块作为 SM4 替身，仅用于测试 pluggable 接口路径。
type mockSM4Provider struct{}

func (m *mockSM4Provider) NewCipher(key []byte) (cipher.Block, error) {
	return aes.NewCipher(key)
}
