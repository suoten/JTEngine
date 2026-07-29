package jt1078

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
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

	iv1 := s.buildIV(0x12345678, 0x1000, 0)
	iv2 := s.buildIV(0x12345678, 0x1001, 0)
	iv3 := s.buildIV(0x87654321, 0x1000, 0)

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
	ivBefore := s.buildIV(ssrc, 0x0000, 0)

	// 模拟回绕：0xFFFF -> 0x0000，触发 ROC 自增到 1
	s.mu.Lock()
	s.updateROC(0xFFFF)
	s.updateROC(0x0000)
	if s.roc != 1 {
		s.mu.Unlock()
		t.Fatalf("回绕后 ROC = %d, want 1", s.roc)
	}
	s.mu.Unlock()

	// ROC=1, SEQ=0x0000 —— 修复前与 ivBefore 相同（IV 复用漏洞），修复后必须不同
	ivAfter := s.buildIV(ssrc, 0x0000, 1)
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

// ===================================================================
// P0 SRTP 解析冲突修复测试 —— FIXED-2026-07-22
// ===================================================================

// TestP0_RealtimeRequest_Standard22B_NoSRTPFalsePositive 验证标准 22B 报文
// （含 TransportMode=0）不会被误判为 SRTP 启用。
// 修复前：srtpStart 回退到 21，data[21]=TransportMode 被当作 SRTPEnabled 检查。
// 修复后：TransportMode==0 时不解析 SRTP。
func TestP0_RealtimeRequest_Standard22B_NoSRTPFalsePositive(t *testing.T) {
	// 构造 22B 报文：标准 21B + TransportMode=0
	data := make([]byte, 22)
	copy(data[0:16], []byte("192.168.1.100"))
	binary.BigEndian.PutUint16(data[16:18], 10000)
	data[18] = 1 // LogicChannel
	data[19] = 0 // MediaType
	data[20] = 0 // StreamType
	data[21] = 0 // TransportMode = 0 (UDP)

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SRTPEnabled {
		t.Fatal("TransportMode=0 时不应触发 SRTP（标准 22B 报文不含 SRTP）")
	}
	if parsed.TransportMode != 0 {
		t.Errorf("TransportMode: got %d, want 0", parsed.TransportMode)
	}
}

// TestP0_RealtimeRequest_Standard21B_NoSRTP 验证标准 21B 报文不触发 SRTP。
func TestP0_RealtimeRequest_Standard21B_NoSRTP(t *testing.T) {
	data := make([]byte, 21)
	copy(data[0:16], []byte("192.168.1.100"))
	binary.BigEndian.PutUint16(data[16:18], 10000)
	data[18] = 1 // LogicChannel
	data[19] = 0 // MediaType
	data[20] = 0 // StreamType

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SRTPEnabled {
		t.Fatal("标准 21B 报文不应触发 SRTP")
	}
	if parsed.TransportMode != 0 {
		t.Errorf("TransportMode: got %d, want 0 (默认 UDP)", parsed.TransportMode)
	}
}

// TestP0_RealtimeRequest_TransportMode1_NoSRTP 验证 TransportMode=1 但无 SRTP 字段时不误判。
// 22B 报文，data[21]=1（TCP 模式），但 len(data) 不超过 22，不应触发 SRTP。
func TestP0_RealtimeRequest_TransportMode1_NoSRTP(t *testing.T) {
	data := make([]byte, 22)
	copy(data[0:16], []byte("192.168.1.100"))
	binary.BigEndian.PutUint16(data[16:18], 10000)
	data[18] = 1 // LogicChannel
	data[19] = 0 // MediaType
	data[20] = 0 // StreamType
	data[21] = 1 // TransportMode = 1 (TCP)

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SRTPEnabled {
		t.Fatal("22B 报文（len==srtpStart）不应触发 SRTP")
	}
	if parsed.TransportMode != 1 {
		t.Errorf("TransportMode: got %d, want 1", parsed.TransportMode)
	}
}

// TestP0_RealtimeRequest_SRTPWithTransportMode1 验证 TransportMode=1 + SRTP 字段正确解析。
func TestP0_RealtimeRequest_SRTPWithTransportMode1(t *testing.T) {
	masterKey := []byte("0123456789abcdef") // 16B
	orig := &RealtimeRequestMessage{
		IPAddress:    "192.168.1.100",
		Port:         10000,
		LogicChannel: 1,
		MediaType:    0,
		StreamType:   0,
		TransportMode: 1,
		SRTPEnabled:  true,
		CipherSuite:  "AES-128-CM",
		MasterKey:    masterKey,
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 21B + TransportMode(1B) + SRTPEnabled(1B) + MasterKeyEncrypted(1B) + CSLen(1B) + CS(10B) + MKLen(1B) + MK(16B) = 52
	if len(data) != 52 {
		t.Fatalf("encoded length = %d, want 52", len(data))
	}

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !parsed.SRTPEnabled {
		t.Fatal("SRTPEnabled = false, want true")
	}
	if parsed.TransportMode != 1 {
		t.Errorf("TransportMode: got %d, want 1", parsed.TransportMode)
	}
	if parsed.CipherSuite != orig.CipherSuite {
		t.Errorf("CipherSuite = %q, want %q", parsed.CipherSuite, orig.CipherSuite)
	}
	if string(parsed.MasterKey) != string(orig.MasterKey) {
		t.Errorf("MasterKey mismatch")
	}
}

// TestP0_RealtimeRequest_SRTPWithUDP_ReturnsError 验证 SRTP + TransportMode=0 在 Marshal 时返回错误。
func TestP0_RealtimeRequest_SRTPWithUDP_ReturnsError(t *testing.T) {
	orig := &RealtimeRequestMessage{
		IPAddress:    "192.168.1.100",
		Port:         10000,
		LogicChannel: 1,
		TransportMode: 0, // UDP
		SRTPEnabled:  true,
		CipherSuite:  "AES-128-CM",
		MasterKey:    make([]byte, 16),
	}
	_, err := orig.Marshal()
	if err == nil {
		t.Fatal("SRTP with TransportMode=0 should return error")
	}
}

// TestP0_RealtimeRequest_22BWithByte1AtPos21 验证 22B 报文中 data[21]=1
// （TransportMode=1）不会被误判为 SRTPEnabled=true。
// 修复前：srtpStart=21，data[21]=1 会被当作 SRTPEnabled=true。
// 修复后：srtpStart 固定为 22，TransportMode > 0 时检查 data[22]（不存在，不触发）。
func TestP0_RealtimeRequest_22BWithByte1AtPos21(t *testing.T) {
	data := make([]byte, 22)
	copy(data[0:16], []byte("10.0.0.1\x00\x00\x00\x00\x00\x00\x00\x00"))
	binary.BigEndian.PutUint16(data[16:18], 8080)
	data[18] = 3  // LogicChannel
	data[19] = 2  // MediaType
	data[20] = 1  // StreamType
	data[21] = 1  // TransportMode=1 (TCP) — 修复前会被误判为 SRTPEnabled

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SRTPEnabled {
		t.Fatal("data[21]=1 是 TransportMode，不应被误判为 SRTPEnabled")
	}
	if parsed.TransportMode != 1 {
		t.Errorf("TransportMode: got %d, want 1", parsed.TransportMode)
	}
}

// TestSRTPSession_ConcurrentEncryptDecrypt [P0-1] 验证 SRTPSession 在 100 个 goroutine
// 并发调用 Encrypt/Decrypt 时不发生数据竞争或 panic。
// 使用 -race 标志运行可检测竞态条件。
func TestSRTPSession_ConcurrentEncryptDecrypt(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i + 1)
	}
	enc, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("new encrypt session: %v", err)
	}
	dec, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("new decrypt session: %v", err)
	}

	const goroutines = 100
	done := make(chan error, goroutines*2)

	// 并发加密：每个 goroutine 使用不同的 seqNum
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			seq := uint16(idx % 0xFFFF)
			rtp := makeRTP(seq, 0x12345678, []byte("concurrent payload"))
			srtp, err := enc.Encrypt(rtp)
			if err != nil {
				done <- fmt.Errorf("goroutine %d encrypt: %w", idx, err)
				return
			}
			// 立即解密验证
			decrypted, err := dec.Decrypt(srtp)
			if err != nil {
				done <- fmt.Errorf("goroutine %d decrypt: %w", idx, err)
				return
			}
			if string(decrypted) != string(rtp) {
				done <- fmt.Errorf("goroutine %d: decrypted != original", idx)
				return
			}
			done <- nil
		}(i)
	}

	// 并发解密：额外 100 个 goroutine 直接调用 Decrypt（使用预加密数据）
	prtp := makeRTP(0x7FFF, 0x87654321, []byte("pre-encrypted"))
	psrtp, err := enc.Encrypt(prtp)
	if err != nil {
		t.Fatalf("pre-encrypt: %v", err)
	}
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			// 复制 srtp 避免共享底层数组
			srtpCopy := make([]byte, len(psrtp))
			copy(srtpCopy, psrtp)
			// 由于 Decrypt 会修改 ROC 状态，并发调用同一 dec 可能导致 ROC 不一致
			// 这里验证不 panic / 不 data race 即可
			_, _ = dec.Decrypt(srtpCopy)
			done <- nil
		}(i)
	}

	// 收集结果
	for i := 0; i < goroutines*2; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// TestRealtimeRequest_InvalidIP_ReturnsError [P1-6] 验证非合法 IPv4 地址在 Marshal 时返回错误
func TestRealtimeRequest_InvalidIP_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
	}{
		{"valid_ipv4", "192.168.1.100", false},
		{"valid_ipv4_short", "10.0.0.1", false},
		{"empty_ip", "", false}, // 空 IP 允许（由终端填充）
		{"invalid_not_ip", "not-an-ip", true},
		{"invalid_with_port", "192.168.1.1:8080", true},
		{"invalid_ipv6", "::1", true}, // 1078 仅支持 IPv4
		{"invalid_too_long", "999.999.999.999.999.999", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &RealtimeRequestMessage{
				IPAddress:    tt.ip,
				Port:         10000,
				LogicChannel: 1,
			}
			_, err := msg.Marshal()
			if tt.wantErr && err == nil {
				t.Fatal("expected error for invalid IP, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for IP %q: %v", tt.ip, err)
			}
		})
	}
}
