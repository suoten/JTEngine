package jt1078

// ===================================================================
// FIXED-2026-07-23 [P1]: SRTPSession.Decrypt ROC 回绕认证失败测试
// 验证 Decrypt 在 ROC 回绕边界、乱序包场景下能正确认证
// ===================================================================

import (
	"encoding/binary"
	"testing"
)

// TestP1_SRTPSession_DecryptROC_Rollover 验证 ROC 回绕边界认证成功
// 发送端回绕后 ROC+1，接收端 ROC 尚未回绕时应通过 ROC+1 重试验证成功
func TestP1_SRTPSession_DecryptROC_Rollover(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i + 1)
	}

	// 发送端 session
	sEnc, _ := NewSRTPSession(master, "AES-128-CM")

	// 先发一些包触发 ROC 回绕
	for seq := uint16(0xFF00); seq < 0xFFFF; seq++ {
		rtp := makeRTP(seq, 0xCAFEBABE, []byte{0x01, 0x02})
		_, _ = sEnc.Encrypt(rtp)
	}
	// 最后一个高半区包
	rtpHigh := makeRTP(0xFFFE, 0xCAFEBABE, []byte("high"))
	encHigh, err := sEnc.Encrypt(rtpHigh)
	if err != nil {
		t.Fatalf("Encrypt high: %v", err)
	}

	// 回绕后的第一个包（低半区），ROC 已自增
	rtpLow := makeRTP(0x0001, 0xCAFEBABE, []byte("low"))
	encLow, err := sEnc.Encrypt(rtpLow)
	if err != nil {
		t.Fatalf("Encrypt low: %v", err)
	}

	// 接收端 session（独立，ROC=0）
	sDec, _ := NewSRTPSession(master, "AES-128-CM")

	// 先解高半区包（ROC=0，应该成功）
	decHigh, err := sDec.Decrypt(encHigh)
	if err != nil {
		t.Fatalf("Decrypt high seq should succeed: %v", err)
	}
	if string(decHigh[12:]) != "high" {
		t.Errorf("high payload mismatch: got %q", string(decHigh[12:]))
	}

	// 解回绕后的低半区包（接收端 ROC 仍=0，但发送端已 ROC=1）
	// 应通过 ROC+1 重试验证成功
	decLow, err := sDec.Decrypt(encLow)
	if err != nil {
		t.Fatalf("Decrypt low seq after rollover should succeed via ROC+1 retry: %v", err)
	}
	if string(decLow[12:]) != "low" {
		t.Errorf("low payload mismatch: got %q", string(decLow[12:]))
	}
}

// TestP1_SRTPSession_DecryptROC_OutOfOrder 验证乱序包认证成功
// 发送端在回绕后发送一个包，然后发送回绕前的旧包（ROC-1）
func TestP1_SRTPSession_DecryptROC_OutOfOrder(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i + 1)
	}

	sEnc, _ := NewSRTPSession(master, "AES-128-CM")

	// 发送高半区包
	rtpHigh := makeRTP(0xFFFF, 0x12345678, []byte("before-rollover"))
	encHigh, err := sEnc.Encrypt(rtpHigh)
	if err != nil {
		t.Fatalf("Encrypt high: %v", err)
	}

	// 触发回绕：发送低半区包（ROC 自增到 1）
	rtpLow := makeRTP(0x0001, 0x12345678, []byte("after-rollover"))
	encLow, err := sEnc.Encrypt(rtpLow)
	if err != nil {
		t.Fatalf("Encrypt low: %v", err)
	}

	// 接收端先收到回绕后的包（ROC 变为 1）
	sDec, _ := NewSRTPSession(master, "AES-128-CM")
	_, err = sDec.Decrypt(encLow)
	if err != nil {
		t.Fatalf("Decrypt post-rollover should succeed: %v", err)
	}

	// 然后收到回绕前的乱序包（ROC=0，但接收端 ROC 已为 1）
	// 应通过 ROC-1 重试验证成功
	decHigh, err := sDec.Decrypt(encHigh)
	if err != nil {
		t.Fatalf("Decrypt out-of-order pre-rollover should succeed via ROC-1 retry: %v", err)
	}
	if string(decHigh[12:]) != "before-rollover" {
		t.Errorf("payload mismatch: got %q", string(decHigh[12:]))
	}
}

// TestP1_SRTPSession_DecryptROC_TamperFails 验证篡改数据三次 ROC 尝试均失败
func TestP1_SRTPSession_DecryptROC_TamperFails(t *testing.T) {
	master := make([]byte, 16)
	sEnc, _ := NewSRTPSession(master, "AES-128-CM")
	sDec, _ := NewSRTPSession(master, "AES-128-CM")

	rtp := makeRTP(0x0001, 0x12345678, []byte("hello"))
	enc, err := sEnc.Encrypt(rtp)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// 篡改认证标签
	tampered := make([]byte, len(enc))
	copy(tampered, enc)
	tampered[len(tampered)-1] ^= 0xFF // 翻转 tag 最后一个字节

	_, err = sDec.Decrypt(tampered)
	if err == nil {
		t.Fatal("Decrypt tampered packet should fail")
	}
}

// TestP1_SRTPSession_DecryptROC_NormalRoundTrip 验证正常往返不受影响
func TestP1_SRTPSession_DecryptROC_NormalRoundTrip(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i + 1)
	}
	sEnc, _ := NewSRTPSession(master, "AES-128-CM")
	sDec, _ := NewSRTPSession(master, "AES-128-CM")

	payload := []byte("Normal test payload!")
	rtp := makeRTP(0x0100, 0xDEADBEEF, payload)

	enc, err := sEnc.Encrypt(rtp)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	dec, err := sDec.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if string(dec[12:]) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", string(dec[12:]), string(payload))
	}
	if binary.BigEndian.Uint16(dec[2:4]) != 0x0100 {
		t.Error("seqNum mismatch after round-trip")
	}
}
