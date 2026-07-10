// Package gmsm 提供国密 SM2/SM3/SM4 算法的统一封装（等保2.0 合规要求）。
//
// 基于 github.com/emmansun/gmsm 实现，覆盖：
//   - SM3（GB/T 32905-2016）密码杂凑算法
//   - SM4（GB/T 32907-2016）分组密码算法（GCM/CBC/ECB）
//   - SM2（GB/T 32918-2016）椭圆曲线公钥密码（签名/验签/加密/解密）
//
// 用于：密码哈希、关键数据加密存储、JWT 签名、审计日志防篡改。
package gmsm

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"
)

// ============================================================================
// SM3（GB/T 32905-2016）密码杂凑算法
// ============================================================================

// SM3Hash 返回数据的 SM3 摘要（32 字节）。
func SM3Hash(data []byte) []byte {
	h := sm3.New()
	h.Write(data)
	return h.Sum(nil)
}

// SM3HashHex 返回数据的 SM3 摘要的十六进制字符串（64 字符）。
func SM3HashHex(data []byte) string {
	return hex.EncodeToString(SM3Hash(data))
}

// SM3HMAC 返回基于 SM3 的 HMAC（用于审计日志防篡改）。
// HMAC(K, m) = SM3((K ⊕ opad) || SM3((K ⊕ ipad) || m))
func SM3HMAC(key, data []byte) []byte {
	const blockSize = 64 // SM3 块大小
	k := key
	if len(k) > blockSize {
		k = SM3Hash(k)
	}
	if len(k) < blockSize {
		padded := make([]byte, blockSize)
		copy(padded, k)
		k = padded
	}
	ipad := make([]byte, blockSize)
	opad := make([]byte, blockSize)
	for i := 0; i < blockSize; i++ {
		ipad[i] = k[i] ^ 0x36
		opad[i] = k[i] ^ 0x5c
	}
	inner := sm3.New()
	inner.Write(ipad)
	inner.Write(data)
	outer := sm3.New()
	outer.Write(opad)
	outer.Write(inner.Sum(nil))
	return outer.Sum(nil)
}

// ============================================================================
// SM4（GB/T 32907-2016）分组密码算法
// ============================================================================

// SM4GenerateKey 生成 16 字节（128 位）SM4 随机密钥。
func SM4GenerateKey() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate SM4 key: %w", err)
	}
	return key, nil
}

// SM4EncryptGCM 使用 SM4-GCM 模式加密（认证加密，推荐用于关键数据存储）。
// 返回 nonce(12B) + ciphertext + tag(16B) 拼接的字节串。
func SM4EncryptGCM(key, plaintext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 GCM new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("SM4 GCM mode: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("SM4 GCM nonce: %w", err)
	}
	// Seal 返回 nonce || ciphertext || tag 形式（gcm.Seal(nil, nonce, pt, nil) 结果）
	// 为便于存储，我们输出 nonce + 密文+tag
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ciphertext))
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// SM4DecryptGCM 解密 SM4EncryptGCM 的输出。
func SM4DecryptGCM(key, data []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 GCM new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("SM4 GCM mode: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("SM4 GCM: ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("SM4 GCM decrypt: %w", err)
	}
	return plaintext, nil
}

// SM4EncryptCBC 使用 SM4-CBC + PKCS7 填充加密，iv 作为参数传入。
func SM4EncryptCBC(key, iv, plaintext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CBC new cipher: %w", err)
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

// SM4DecryptCBC 解密 SM4-CBC + PKCS7。
func SM4DecryptCBC(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CBC new cipher: %w", err)
	}
	if len(ciphertext)%block.BlockSize() != 0 {
		return nil, errors.New("SM4 CBC: ciphertext not block-aligned")
	}
	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext)
}

// ============================================================================
// SM2（GB/T 32918-2016）椭圆曲线公钥密码算法
// ============================================================================

// SM2PrivateKey 包装 SM2 私钥。
type SM2PrivateKey = sm2.PrivateKey

// SM2GenerateKeyPair 生成 SM2 密钥对。
func SM2GenerateKeyPair() (*sm2.PrivateKey, *ecdsa.PublicKey, error) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate SM2 key pair: %w", err)
	}
	return priv, &priv.PublicKey, nil
}

// DefaultSM2UID 是 GB/T 32918.3 规定的默认用户标识（16 字节 ASCII）。
var DefaultSM2UID = []byte("1234567812345678")

// SM2Sign 使用默认 UID 对消息签名（完整 SM2 流程：ZA=SM3(ENTL||UID||...||pub)，
// e=SM3(ZA||msg)，对 e 签名）。输出 ASN.1 格式签名。
// 注意：必须使用 SignWithSM2 而非 Sign(nil)，后者将输入当作预计算摘要，
// 不绑定完整消息，存在安全隐患。
func SM2Sign(priv *sm2.PrivateKey, data []byte) ([]byte, error) {
	return priv.SignWithSM2(rand.Reader, DefaultSM2UID, data)
}

// SM2Verify 验证 SM2Sign 产生的签名（完整 SM2 验签流程）。
func SM2Verify(pub *ecdsa.PublicKey, data, signature []byte) bool {
	return sm2.VerifyASN1WithSM2(pub, DefaultSM2UID, data, signature)
}

// SM2SignWithUID 使用指定 UID 签名（GB/T 32918.2 标准，UID 参与 ZA 哈希）。
func SM2SignWithUID(priv *sm2.PrivateKey, uid, data []byte) ([]byte, error) {
	return priv.SignWithSM2(rand.Reader, uid, data)
}

// SM2VerifyWithUID 使用指定 UID 验签。
func SM2VerifyWithUID(pub *ecdsa.PublicKey, uid, data, signature []byte) bool {
	return sm2.VerifyASN1WithSM2(pub, uid, data, signature)
}

// SM2Encrypt 使用 SM2 加密（输出 C1||C3||C2 格式，emmansun 默认）。
func SM2Encrypt(pub *ecdsa.PublicKey, plaintext []byte) ([]byte, error) {
	return sm2.Encrypt(rand.Reader, pub, plaintext, nil)
}

// SM2Decrypt 解密 SM2 密文。
func SM2Decrypt(priv *sm2.PrivateKey, ciphertext []byte) ([]byte, error) {
	return sm2.Decrypt(priv, ciphertext)
}

// MarshalSM2PrivateKey 将 SM2 私钥序列化为 D 标量字节（32 字节，big-endian）。
// 该格式与 module-crypto KeyManager 一致，版本无关、稳定可移植。
func MarshalSM2PrivateKey(priv *sm2.PrivateKey) []byte {
	d := priv.D.Bytes()
	// 左填充至 32 字节（SM2 曲线为 256 位）
	out := make([]byte, 32)
	copy(out[32-len(d):], d)
	return out
}

// ParseSM2PrivateKey 从 D 标量字节重建 SM2 私钥。
func ParseSM2PrivateKey(dBytes []byte) (*sm2.PrivateKey, error) {
	if len(dBytes) == 0 {
		return nil, errors.New("empty SM2 private key bytes")
	}
	// emmansun/gmsm 的 NewPrivateKey 自动处理曲线与公钥点计算
	priv, err := sm2.NewPrivateKey(dBytes)
	if err != nil {
		return nil, fmt.Errorf("parse SM2 private key: %w", err)
	}
	return priv, nil
}

// MarshalSM2PublicKey 将 SM2 公钥序列化为非压缩格式（0x04 || X || Y，65 字节）。
func MarshalSM2PublicKey(pub *ecdsa.PublicKey) []byte {
	out := make([]byte, 1+32+32)
	out[0] = 0x04
	x := pub.X.Bytes()
	y := pub.Y.Bytes()
	copy(out[1+32-len(x):], x)
	copy(out[1+32+32-len(y):], y)
	return out
}

// ParseSM2PublicKey 从非压缩字节解析 SM2 公钥。
func ParseSM2PublicKey(data []byte) (*ecdsa.PublicKey, error) {
	if len(data) != 65 || data[0] != 0x04 {
		return nil, errors.New("invalid SM2 public key format")
	}
	curve := sm2.P256()
	x := new(big.Int).SetBytes(data[1:33])
	y := new(big.Int).SetBytes(data[33:65])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("SM2 public key point not on curve")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// ============================================================================
// PKCS7 填充（GB/T 32907-2016 配套）
// ============================================================================

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := make([]byte, padding)
	for i := range padtext {
		padtext[i] = byte(padding)
	}
	return append(data, padtext...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("invalid padding: empty data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > len(data) {
		return nil, fmt.Errorf("invalid padding: %d", padding)
	}
	return data[:len(data)-padding], nil
}

// 确保 big.Int 被引用（部分签名路径使用）
var _ = (*big.Int)(nil)
