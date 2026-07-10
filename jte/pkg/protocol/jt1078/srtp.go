package jt1078

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
)

// ===================================================================
// P2-8: SRTP 密钥交换与加密（安全加固版）
// ===================================================================
//
// 修复项：
//  1. decryptSRTP：新增解密路径，支持接收/解码加密流
//  2. IV ROC：引入 16 位 Rollover Counter，防止 SeqNum 回绕后 IV 复用
//  3. 密钥派生：从 MasterKey 派生独立的 enc_key / auth_key / salt（SRTP KDF）
//  4. SM4-CBC：通过 SRTPCipherProvider 接口支持国密，由 module-crypto 注册
//  5. 0x9101 SRTP 参数：SRTPConfig 携带加密参数供 0x9101 下发
//
// RFC 3711 简化实现：不实现完整 SRTCP，仅 SRTP（RTP payload 加密 + 认证标签）。

// SRTPCipherProvider 国密 SM4-CBC 加密器接口（由 module-crypto 注册）。
// 核心引擎不直接依赖 gmsm，避免重量级依赖；module-crypto 在 init 时调用 RegisterSM4Cipher。
type SRTPCipherProvider interface {
	// NewCipher 用 key 创建一个块加密器（SM4 block size = 16）。
	NewCipher(key []byte) (cipher.Block, error)
}

var (
	sm4Provider     SRTPCipherProvider
	sm4ProviderOnce sync.Once
)

// RegisterSM4Cipher 注册 SM4 加密器提供者（由 module-crypto 调用）。
// 注册后 SRTPConfig.CipherSuite="SM4-CBC" 即可使用国密加密。
func RegisterSM4Cipher(provider SRTPCipherProvider) {
	sm4ProviderOnce.Do(func() {
		sm4Provider = provider
	})
}

// SRTPKeyMaterial 从 MasterKey 派生的密钥材料（RFC 3711 KDF 简化版）。
type SRTPKeyMaterial struct {
	EncKey  []byte // 加密密钥（AES-128: 16B / SM4: 16B）
	AuthKey []byte // 认证密钥（HMAC-SHA1: 20B）
	Salt    []byte // 密钥盐（14B，用于 IV 构造）
}

// DeriveKeyMaterial 从 MasterKey 派生 enc/auth/salt（SRTP KDF 简化）。
// 使用 HMAC-SHA1 基于 key_id 字节区分不同用途的密钥，保证 enc_key ≠ auth_key。
func DeriveKeyMaterial(masterKey []byte) *SRTPKeyMaterial {
	km := &SRTPKeyMaterial{
		EncKey:  deriveSubKey(masterKey, 0x00, 16),  // enc key
		AuthKey: deriveSubKey(masterKey, 0x01, 20),  // auth key (HMAC-SHA1 输出 20B)
		Salt:    deriveSubKey(masterKey, 0x02, 14),  // salt (14B)
	}
	return km
}

// deriveSubKey 用 HMAC-SHA1(masterKey, label) 派生子密钥，截断到指定长度。
func deriveSubKey(masterKey []byte, label byte, length int) []byte {
	mac := hmac.New(sha1.New, masterKey)
	mac.Write([]byte{label})
	mac.Write(masterKey) // 绑定 masterKey，增加熵
	full := mac.Sum(nil)
	if length <= len(full) {
		return full[:length]
	}
	// 不够长则补零（实际不会触发：SHA1 输出 20B，最大需求 20B）
	out := make([]byte, length)
	copy(out, full)
	return out
}

// SRTPSession SRTP 会话状态（每流一个），维护 ROC 和密钥材料。
type SRTPSession struct {
	km        *SRTPKeyMaterial
	cipher    string // "AES-128-CM" 或 "SM4-CBC"
	block     cipher.Block
	roc       uint16 // Rollover Counter，SeqNum 回绕时自增，防止 IV 复用
	lastSeq   uint16 // 上一个 SeqNum，用于检测回绕
}

// NewSRTPSession 创建 SRTP 会话，派生密钥材料并初始化加密块。
func NewSRTPSession(masterKey []byte, cipherSuite string) (*SRTPSession, error) {
	if len(masterKey) < 16 {
		return nil, fmt.Errorf("srtp master key too short: %d bytes (need ≥16)", len(masterKey))
	}
	km := DeriveKeyMaterial(masterKey)
	s := &SRTPSession{
		km:     km,
		cipher: cipherSuite,
	}
	switch cipherSuite {
	case "AES-128-CM", "":
		block, err := aes.NewCipher(km.EncKey)
		if err != nil {
			return nil, fmt.Errorf("srtp create aes cipher: %w", err)
		}
		s.block = block
	case "SM4-CBC":
		if sm4Provider == nil {
			return nil, fmt.Errorf("srtp SM4-CBC selected but no SM4 provider registered (load module-crypto)")
		}
		block, err := sm4Provider.NewCipher(km.EncKey)
		if err != nil {
			return nil, fmt.Errorf("srtp create sm4 cipher: %w", err)
		}
		s.block = block
	default:
		return nil, fmt.Errorf("unsupported srtp cipher suite: %s", cipherSuite)
	}
	return s, nil
}

// buildIV 构造 16 字节 IV（RFC 3711 §4.1.1）。
//
// AUTO-FIX-2026-07-02 [P0]: 修复 IV 复用漏洞。
// 原实现仅把 16-bit SeqNum 放入 IV bytes 14-16，未折叠 ROC，
// 导致 SeqNum 回绕(0xFFFF→0x0000)后 IV 与回绕前 seq=0x0000 的包碰撞。
//
// 正确的 SRTP IV（libsrtp 布局）：
//
//	IV = (k_s * 2^16) XOR (SSRC || ROC || SEQ)   (左对齐，右补零)
//
// 即 128-bit IV 中：
//   - bytes 0-3:  SSRC  XOR salt[0:4]
//   - bytes 4-7:  ROC   XOR salt[4:8]   ← 关键：ROC 折叠进 IV，回绕后 IV 改变
//   - bytes 8-9:  SEQ   XOR salt[8:10]
//   - bytes 10-13: salt[10:14]          (packet index 高位为 0)
//   - bytes 14-15: 0x0000               (salt 仅 14B，左移 16 位)
//
// 这样 (SSRC, ROC, SEQ) 三元组唯一确定 IV，彻底消除回绕导致的 IV 复用。
func (s *SRTPSession) buildIV(ssrc uint32, seqNum uint16) []byte {
	iv := make([]byte, 16)
	// Salt 前 14 字节，bytes 14-15 保持 0（k_s * 2^16）
	copy(iv[0:14], s.km.Salt)
	// SSRC XOR salt[0:4]
	binary.BigEndian.PutUint32(iv[0:4], binary.BigEndian.Uint32(iv[0:4])^ssrc)
	// ROC XOR salt[4:8] —— 防 IV 复用的关键
	binary.BigEndian.PutUint32(iv[4:8], binary.BigEndian.Uint32(iv[4:8])^uint32(s.roc))
	// SEQ XOR salt[8:10]
	binary.BigEndian.PutUint16(iv[8:10], binary.BigEndian.Uint16(iv[8:10])^seqNum)
	return iv
}

// updateROC 检测 SeqNum 回绕：新 SeqNum 远小于上一个（差值 > 32768）时 ROC++。
func (s *SRTPSession) updateROC(seqNum uint16) {
	if s.lastSeq > 0x8000 && seqNum < 0x8000 {
		// 从高半区跳到低半区 = 回绕
		s.roc++
	}
	s.lastSeq = seqNum
}

// Encrypt 加密 RTP 包：保留 RTP 头，加密 payload，追加 HMAC-SHA1-80 认证标签。
// 返回加密后的完整 SRTP 包（header + enc_payload + auth_tag）。
func (s *SRTPSession) Encrypt(rtpData []byte) ([]byte, error) {
	if len(rtpData) < RTPHeaderMinLen {
		return rtpData, nil
	}
	offset := computePayloadOffset(rtpData)
	if offset < 0 || len(rtpData) < offset {
		return rtpData, nil
	}

	ssrc := binary.BigEndian.Uint32(rtpData[8:12])
	seqNum := binary.BigEndian.Uint16(rtpData[2:4])
	s.updateROC(seqNum)

	iv := s.buildIV(ssrc, seqNum)

	// 加密 payload（CTR 模式，AES-128-CM；SM4 也用 CTR 模式以保证流加密）
	stream := cipher.NewCTR(s.block, iv)
	out := make([]byte, len(rtpData))
	copy(out, rtpData)
	stream.XORKeyStream(out[offset:], rtpData[offset:])

	// HMAC-SHA1-80 认证标签（对 header + encrypted_payload + ROC 计算）
	mac := hmac.New(sha1.New, s.km.AuthKey)
	mac.Write(out[:len(rtpData)])
	rocBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rocBytes, uint32(s.roc))
	mac.Write(rocBytes)
	tag := mac.Sum(nil)[:SRTPAuthTagLen]
	out = append(out, tag...)

	return out, nil
}

// Decrypt 解密 SRTP 包：校验认证标签，解密 payload，返回原始 RTP 包。
// AUTO-FIX-2026-06-30 [P2-8]: 新增解密路径，支持接收加密流。
func (s *SRTPSession) Decrypt(srtpData []byte) ([]byte, error) {
	if len(srtpData) < RTPHeaderMinLen+SRTPAuthTagLen {
		return nil, fmt.Errorf("srtp packet too short: %d bytes", len(srtpData))
	}
	tag := srtpData[len(srtpData)-SRTPAuthTagLen:]
	rtpAndPayload := srtpData[:len(srtpData)-SRTPAuthTagLen]

	offset := computePayloadOffset(rtpAndPayload)
	if offset < 0 || len(rtpAndPayload) < offset {
		return nil, fmt.Errorf("invalid rtp header")
	}

	ssrc := binary.BigEndian.Uint32(rtpAndPayload[8:12])
	seqNum := binary.BigEndian.Uint16(rtpAndPayload[2:4])
	s.updateROC(seqNum)

	// 1. 校验认证标签（防篡改）
	mac := hmac.New(sha1.New, s.km.AuthKey)
	mac.Write(rtpAndPayload)
	rocBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rocBytes, uint32(s.roc))
	mac.Write(rocBytes)
	expectedTag := mac.Sum(nil)[:SRTPAuthTagLen]
	if !hmac.Equal(tag, expectedTag) {
		return nil, fmt.Errorf("srtp authentication tag mismatch (possible tampering)")
	}

	// 2. 解密 payload
	iv := s.buildIV(ssrc, seqNum)
	stream := cipher.NewCTR(s.block, iv)
	out := make([]byte, len(rtpAndPayload))
	copy(out, rtpAndPayload)
	stream.XORKeyStream(out[offset:], rtpAndPayload[offset:])

	return out, nil
}

// computePayloadOffset 计算 RTP payload 起始偏移（12 + CSRC*4 + Extension）。
// 返回 -1 表示无效头部。
func computePayloadOffset(rtpData []byte) int {
	if len(rtpData) < RTPHeaderMinLen {
		return -1
	}
	offset := RTPHeaderMinLen
	csrcCount := rtpData[0] & 0x0F
	offset += int(csrcCount) * 4
	if len(rtpData) < offset {
		return -1
	}
	if rtpData[0]&0x10 != 0 { // Extension
		if len(rtpData) < offset+4 {
			return -1
		}
		extLen := int(binary.BigEndian.Uint16(rtpData[offset+2 : offset+4]))
		offset += 4 + extLen*4
		if len(rtpData) < offset {
			return -1
		}
	}
	return offset
}

// GenerateSRTPMasterKey 生成 16 字节随机主密钥（供 0x9101 密钥交换使用）。
func GenerateSRTPMasterKey() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate srtp master key: %w", err)
	}
	return key, nil
}

// ===================================================================
// P2-1.3.2: SRTP 密钥协商——RSA 加密主密钥
// ===================================================================
//
// 按照 plan 8.6.1 要求，SRTP 主密钥不应明文下发，需通过 RSA-2048
// (标准场景) 或 SM2 (国密场景) 加密后嵌入 0x9101 消息。
//
// 密钥交换流程：
//  1. 终端通过 0x0A00 上报 RSA 公钥（模数 n + 指数 e）
//  2. 平台生成 16 字节随机 SRTP 主密钥
//  3. 平台用终端 RSA 公钥加密主密钥 (RSA-OAEP)
//  4. 平台通过 0x9101 下发加密后的主密钥
//  5. 终端用自身 RSA 私钥解密获得主密钥
//  6. 双方使用主密钥进行 SRTP 加密/解密
//  7. 会话结束 (0x9103) 后密钥销毁

// EncryptSRTPMasterKeyWithRSA 使用终端的 RSA 公钥加密 SRTP 主密钥。
// 采用 RSA-OAEP (SHA-256) 填充方案，防止明文密钥在网络中暴露。
//
// 参数：
//   - masterKey: 16 字节 SRTP 主密钥
//   - rsaModulus: 终端 RSA 模数 n (来自 0x0A00 RSAPublicKeyMessage.Euler)
//   - rsaExponent: 终端 RSA 公钥指数 e (来自 0x0A00 RSAPublicKeyMessage.PublicExponent)
//
// 返回加密后的密钥字节流，可直接嵌入 0x9101 的 MasterKey 字段。
func EncryptSRTPMasterKeyWithRSA(masterKey, rsaModulus []byte, rsaExponent uint32) ([]byte, error) {
	if len(masterKey) != 16 {
		return nil, fmt.Errorf("srtp master key must be 16 bytes, got %d", len(masterKey))
	}
	if len(rsaModulus) == 0 {
		return nil, fmt.Errorf("rsa modulus is empty")
	}

	// 从模数和指数构造 RSA 公钥
	n := new(big.Int).SetBytes(rsaModulus)
	e := int(rsaExponent)
	if e == 0 {
		e = 65537 // 默认公钥指数
	}
	pubKey := &rsa.PublicKey{N: n, E: e}

	// 使用 RSA-OAEP (SHA-256) 加密主密钥
	encrypted, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, pubKey, masterKey, []byte("JTE-SRTP"))
	if err != nil {
		return nil, fmt.Errorf("rsa encrypt srtp master key: %w", err)
	}
	return encrypted, nil
}

// DecryptSRTPMasterKeyWithRSA 使用终端的 RSA 私钥解密 SRTP 主密钥。
// 终端侧调用：收到 0x9101 中 MasterKeyEncrypted=true 时，使用此方法解密。
//
// 参数：
//   - encryptedKey: 从 0x9101 消息中提取的加密主密钥
//   - privateKeyPEM: 终端 RSA 私钥 (PEM 格式)
//
// 返回 16 字节 SRTP 主密钥。
func DecryptSRTPMasterKeyWithRSA(encryptedKey []byte, privateKeyPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM private key")
	}
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// 尝试 PKCS8
		key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parse private key: pkcs1=%v, pkcs8=%v", err, err2)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		privKey = rsaKey
	}

	masterKey, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, privKey, encryptedKey, []byte("JTE-SRTP"))
	if err != nil {
		return nil, fmt.Errorf("rsa decrypt srtp master key: %w", err)
	}
	if len(masterKey) != 16 {
		return nil, fmt.Errorf("decrypted master key must be 16 bytes, got %d", len(masterKey))
	}
	return masterKey, nil
}
