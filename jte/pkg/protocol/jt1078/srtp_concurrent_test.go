package jt1078

// ===================================================================
// FIXED-2026-07-23 [P0]: SRTPSession 并发安全测试
// 验证 Encrypt/Decrypt 在并发环境下不会 panic 或产生数据竞争
// ===================================================================

import (
	"encoding/binary"
	"sync"
	"testing"
)

// TestP0_SRTPSession_ConcurrentEncryptDecrypt 并发 100 goroutine
// 同时调用 Encrypt 和 Decrypt，验证无 panic、无数据竞争
func TestP0_SRTPSession_ConcurrentEncryptDecrypt(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i)
	}
	s, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("NewSRTPSession: %v", err)
	}

	// 预加密一批包供 Decrypt 使用
	var encrypted [][]byte
	for i := 0; i < 100; i++ {
		rtp := makeRTP(uint16(i), 0x12345678, []byte{0x01, 0x02, 0x03, 0x04})
		enc, err := s.Encrypt(rtp)
		if err != nil {
			t.Fatalf("pre-encrypt %d: %v", i, err)
		}
		encrypted = append(encrypted, enc)
	}

	// 重置 session 用于并发测试
	s2, err := NewSRTPSession(master, "AES-128-CM")
	if err != nil {
		t.Fatalf("NewSRTPSession s2: %v", err)
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines) // 50 Encrypt + 50 Decrypt = 100

	// 50 goroutine 并发 Encrypt
	for i := 0; i < goroutines/2; i++ {
		go func(seq int) {
			defer wg.Done()
			rtp := makeRTP(uint16(seq), 0x12345678, []byte{0xAA, 0xBB, 0xCC})
			_, _ = s2.Encrypt(rtp) // 不校验结果，仅验证不 panic
		}(i)
	}

	// 50 goroutine 并发 Decrypt
	for i := 0; i < goroutines/2; i++ {
		go func(idx int) {
			defer wg.Done()
			if idx >= len(encrypted) {
				return
			}
			// Decrypt 可能因 ROC 不同步而返回 tag mismatch 错误，
			// 但不应 panic — 这是预期的并发行为
			_, _ = s2.Decrypt(encrypted[idx])
		}(i % len(encrypted))
	}

	wg.Wait()
	// 如果执行到这里说明没有 panic
}

// TestP0_SRTPSession_RaceDetector 与 -race 配合使用，检测数据竞争
func TestP0_SRTPSession_RaceDetector(t *testing.T) {
	master := make([]byte, 16)
	s, _ := NewSRTPSession(master, "AES-128-CM")

	var wg sync.WaitGroup
	// 并发 Encrypt
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rtp := makeRTP(1, 0x12345678, []byte{0x01})
			_, _ = s.Encrypt(rtp)
		}()
	}
	// 并发 Encrypt 不同 seq
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			rtp := makeRTP(uint16(seq), 0x12345678, []byte{0x02})
			_, _ = s.Encrypt(rtp)
		}(i)
	}
	wg.Wait()
}

// TestP0_SRTPSession_EncryptDecryptRoundTrip 验证加密后解密能还原
func TestP0_SRTPSession_EncryptDecryptRoundTrip(t *testing.T) {
	master := make([]byte, 16)
	for i := range master {
		master[i] = byte(i + 1)
	}
	sEnc, _ := NewSRTPSession(master, "AES-128-CM")
	sDec, _ := NewSRTPSession(master, "AES-128-CM")

	payload := []byte("Hello SRTP World!")
	rtp := makeRTP(0x0001, 0xCAFEBABE, payload)

	enc, err := sEnc.Encrypt(rtp)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// 使用相同密钥的另一个 session 解密
	dec, err := sDec.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	// 验证 payload 还原
	if string(dec[12:]) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", string(dec[12:]), string(payload))
	}

	// 验证 header 还原
	if dec[0] != rtp[0] || dec[1] != rtp[1] {
		t.Error("header mismatch after round-trip")
	}

	// 验证 seqNum 还原
	if binary.BigEndian.Uint16(dec[2:4]) != 0x0001 {
		t.Error("seqNum mismatch after round-trip")
	}
}
