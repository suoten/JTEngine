// Package gmsm 提供国密 SM2/SM3/SM4 算法的统一封装（等保2.0 合规要求）。
//
// 基于 github.com/emmansun/gmsm 实现，覆盖：
//   - SM3（GB/T 32905-2016）密码杂凑算法
//   - SM4（GB/T 32907-2016）分组密码算法（GCM/CBC/ECB/CTR）
//   - SM2（GB/T 32918-2016）椭圆曲线公钥密码（签名/验签/加密/解密）
//
// 用于：密码哈希、关键数据加密存储、JWT 签名、审计日志防篡改。
//
// SM4 模式说明：
//   - GCM：认证加密（推荐用于关键数据存储），输出 nonce+ct+tag
//   - CBC：分组链接 + PKCS7 填充，需外部传入 IV
//   - ECB：电子密码本 + PKCS7 填充，无 IV（仅兼容旧系统，不推荐）
//   - CTR：计数器模式，流式加密，无需填充，输出与明文等长
//
// SM2 加密模式说明：
//   - C1C3C2：GB/T 32918.4 标准推荐顺序（默认）
//   - C1C2C3：旧版兼容顺序（部分早期终端使用）
//   - ASN1：ASN.1 DER 编码格式
//
// 硬件对接：
//   - HSMProvider 接口定义了硬件安全模块（HSM/SDF）的对接规范
//   - 软件实现为默认 Provider，可通过 CryptoConfig 切换
package gmsm

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"

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
// 使用标准库 crypto/hmac 包装 SM3，确保 HMAC 构造符合 RFC 2104 规范。
// HMAC(K, m) = SM3((K ⊕ opad) || SM3((K ⊕ ipad) || m))
func SM3HMAC(key, data []byte) []byte {
	mac := hmac.New(sm3.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// SM3HMACEqual 使用常量时间比较验证 HMAC-SM3，防止时序攻击。
// 返回 true 表示 msgMAC 是 key 和 msg 的有效 HMAC。
func SM3HMACEqual(key, msg, msgMAC []byte) bool {
	expected := SM3HMAC(key, msg)
	return subtle.ConstantTimeCompare(expected, msgMAC) == 1
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

// SM4GenerateIV 生成 16 字节（128 位）随机 IV，用于 CBC/CTR 模式。
func SM4GenerateIV() ([]byte, error) {
	iv := make([]byte, sm4.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("generate SM4 IV: %w", err)
	}
	return iv, nil
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
	// Seal 返回 ciphertext + tag
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
// 若 iv 为 nil 则自动生成随机 IV 并前置到密文中（iv(16B) + ciphertext）。
func SM4EncryptCBC(key, iv, plaintext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CBC new cipher: %w", err)
	}
	generateIV := false
	if iv == nil {
		iv, err = SM4GenerateIV()
		if err != nil {
			return nil, err
		}
		generateIV = true
	}
	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("SM4 CBC: IV length must be %d bytes, got %d", block.BlockSize(), len(iv))
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)
	if generateIV {
		out := make([]byte, 0, len(iv)+len(ciphertext))
		out = append(out, iv...)
		out = append(out, ciphertext...)
		return out, nil
	}
	return ciphertext, nil
}

// SM4DecryptCBC 解密 SM4-CBC + PKCS7。
// 若 iv 为 nil 且密文长度 > blockSize，自动将前 16 字节作为 IV。
func SM4DecryptCBC(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CBC new cipher: %w", err)
	}
	// 若 iv 为 nil 且密文长度 > blockSize，尝试将前 16 字节作为 IV
	if iv == nil && len(ciphertext) > block.BlockSize() {
		iv = ciphertext[:block.BlockSize()]
		ciphertext = ciphertext[block.BlockSize():]
	}
	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("SM4 CBC: IV length must be %d bytes, got %d", block.BlockSize(), len(iv))
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
// SM4-ECB 模式（GB/T 32907-2016 电子密码本）
// 注意：ECB 模式不隐藏明文模式，仅用于兼容旧系统，新系统应使用 GCM 或 CBC。
// ============================================================================

// SM4EncryptECB 使用 SM4-ECB + PKCS7 填充加密（无 IV）。
// 警告：ECB 模式对于重复明文块会产生相同密文块，不适合加密结构化数据。
func SM4EncryptECB(key, plaintext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 ECB new cipher: %w", err)
	}
	bs := block.BlockSize()
	padded := pkcs7Pad(plaintext, bs)
	ciphertext := make([]byte, len(padded))
	for i := 0; i < len(padded); i += bs {
		block.Encrypt(ciphertext[i:i+bs], padded[i:i+bs])
	}
	return ciphertext, nil
}

// SM4DecryptECB 解密 SM4-ECB + PKCS7。
func SM4DecryptECB(key, ciphertext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 ECB new cipher: %w", err)
	}
	bs := block.BlockSize()
	if len(ciphertext)%bs != 0 {
		return nil, errors.New("SM4 ECB: ciphertext not block-aligned")
	}
	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += bs {
		block.Decrypt(plaintext[i:i+bs], ciphertext[i:i+bs])
	}
	return pkcs7Unpad(plaintext)
}

// ============================================================================
// SM4-CTR 模式（计数器模式，流式加密）
// ============================================================================

// SM4EncryptCTR 使用 SM4-CTR 模式加密（流式加密，无需填充）。
// 返回 iv(16B) + ciphertext，密文长度与明文相同。
// 若 iv 为 nil 则自动生成随机 IV。
func SM4EncryptCTR(key, iv, plaintext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CTR new cipher: %w", err)
	}
	generateIV := false
	if iv == nil {
		iv, err = SM4GenerateIV()
		if err != nil {
			return nil, err
		}
		generateIV = true
	}
	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("SM4 CTR: IV length must be %d bytes, got %d", block.BlockSize(), len(iv))
	}
	ciphertext := make([]byte, len(plaintext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, plaintext)
	if generateIV {
		out := make([]byte, 0, len(iv)+len(ciphertext))
		out = append(out, iv...)
		out = append(out, ciphertext...)
		return out, nil
	}
	return ciphertext, nil
}

// SM4DecryptCTR 解密 SM4-CTR 密文（流式解密，无需去填充）。
// 若 iv 为 nil 且密文长度 > blockSize，自动将前 16 字节作为 IV。
func SM4DecryptCTR(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SM4 CTR new cipher: %w", err)
	}
	if iv == nil && len(ciphertext) > block.BlockSize() {
		iv = ciphertext[:block.BlockSize()]
		ciphertext = ciphertext[block.BlockSize():]
	}
	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("SM4 CTR: IV length must be %d bytes, got %d", block.BlockSize(), len(iv))
	}
	plaintext := make([]byte, len(ciphertext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}

// ============================================================================
// SM2（GB/T 32918-2016）椭圆曲线公钥密码算法
// ============================================================================

// SM2PrivateKey 包装 SM2 私钥。
type SM2PrivateKey = sm2.PrivateKey

// SM2CiphertextMode 定义 SM2 密文的拼接顺序。
type SM2CiphertextMode int

const (
	// SM2ModeC1C3C2 是 GB/T 32918.4 标准推荐的密文拼接顺序（默认）。
	// C1 = 椭圆曲线随机点，C3 = SM3 哈希，C2 = 加密数据
	SM2ModeC1C3C2 SM2CiphertextMode = iota
	// SM2ModeC1C2C3 是旧版兼容拼接顺序，部分早期终端使用此格式。
	SM2ModeC1C2C3
)

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

// SM2Encrypt 使用 SM2 加密（输出 C1||C3||C2 格式，GB/T 32918.4 默认）。
func SM2Encrypt(pub *ecdsa.PublicKey, plaintext []byte) ([]byte, error) {
	return sm2.Encrypt(rand.Reader, pub, plaintext, nil)
}

// SM2Decrypt 解密 SM2 密文（默认 C1C3C2 格式）。
func SM2Decrypt(priv *sm2.PrivateKey, ciphertext []byte) ([]byte, error) {
	return sm2.Decrypt(priv, ciphertext)
}

// SM2EncryptWithMode 使用指定密文拼接模式加密。
// mode 为 SM2ModeC1C3C2 或 SM2ModeC1C2C3。
func SM2EncryptWithMode(pub *ecdsa.PublicKey, plaintext []byte, mode SM2CiphertextMode) ([]byte, error) {
	var opts *sm2.EncrypterOpts
	switch mode {
	case SM2ModeC1C3C2:
		opts = sm2.NewPlainEncrypterOpts(sm2.MarshalUncompressed, sm2.C1C3C2)
	case SM2ModeC1C2C3:
		opts = sm2.NewPlainEncrypterOpts(sm2.MarshalUncompressed, sm2.C1C2C3)
	default:
		return nil, fmt.Errorf("unsupported SM2 ciphertext mode: %d", mode)
	}
	return sm2.Encrypt(rand.Reader, pub, plaintext, opts)
}

// SM2DecryptWithMode 使用指定密文拼接模式解密。
// mode 为 SM2ModeC1C3C2 或 SM2ModeC1C2C3。
func SM2DecryptWithMode(priv *sm2.PrivateKey, ciphertext []byte, mode SM2CiphertextMode) ([]byte, error) {
	var opts *sm2.DecrypterOpts
	switch mode {
	case SM2ModeC1C3C2:
		opts = sm2.NewPlainDecrypterOpts(sm2.C1C3C2)
	case SM2ModeC1C2C3:
		opts = sm2.NewPlainDecrypterOpts(sm2.C1C2C3)
	default:
		return nil, fmt.Errorf("unsupported SM2 ciphertext mode: %d", mode)
	}
	return priv.Decrypt(rand.Reader, ciphertext, opts)
}

// SM2EncryptASN1 使用 SM2 加密并输出 ASN.1 DER 编码格式（C1C3C2）。
// ASN.1 格式便于跨平台互操作，部分 Java/Python 国密库默认使用此格式。
func SM2EncryptASN1(pub *ecdsa.PublicKey, plaintext []byte) ([]byte, error) {
	return sm2.EncryptASN1(rand.Reader, pub, plaintext)
}

// SM2DecryptASN1 解密 ASN.1 DER 编码的 SM2 密文（C1C3C2）。
func SM2DecryptASN1(priv *sm2.PrivateKey, ciphertext []byte) ([]byte, error) {
	return priv.Decrypt(rand.Reader, ciphertext, sm2.ASN1DecrypterOpts)
}

// ============================================================================
// 硬件安全模块（HSM/SDF）对接接口
//
// HSMProvider 定义了硬件密码设备（如 PCI-E 密码卡、USB Key、服务器密码机）
// 需要实现的接口。软件实现为默认 Provider，可通过 CryptoConfig 切换到硬件实现。
//
// 对接流程：
//   1. 实现 HSMProvider 接口（如 SDFProvider 封装国密 SDF 接口）
//   2. 通过 CryptoConfig.SetProvider() 注册硬件 Provider
//   3. 所有加密操作自动路由到硬件设备
// ============================================================================

// HSMProvider 硬件安全模块对接接口。
// 实现此接口后，可通过 CryptoConfig 切换为硬件加解密模式。
type HSMProvider interface {
	// Name 返回 Provider 名称（如 "software"、"sdf-hsm"）
	Name() string

	// SM2Sign 使用硬件密钥签名（keyID 标识硬件中的密钥句柄）
	SM2Sign(keyID string, uid, data []byte) (signature []byte, err error)
	// SM2Verify 使用硬件密钥验签
	SM2Verify(keyID string, uid, data, signature []byte) (bool, error)
	// SM2Encrypt 使用硬件公钥加密
	SM2Encrypt(keyID string, plaintext []byte) (ciphertext []byte, err error)
	// SM2Decrypt 使用硬件私钥解密
	SM2Decrypt(keyID string, ciphertext []byte) (plaintext []byte, err error)

	// SM4Encrypt 使用硬件密钥加密（mode: "gcm"/"cbc"/"ecb"/"ctr"）
	SM4Encrypt(keyID string, mode string, iv, plaintext []byte) (ciphertext []byte, err error)
	// SM4Decrypt 使用硬件密钥解密
	SM4Decrypt(keyID string, mode string, iv, ciphertext []byte) (plaintext []byte, err error)

	// Close 关闭硬件会话，释放资源
	Close() error
}

// ============================================================================
// SM2 密钥序列化（与 module-crypto KeyManager 兼容）
// ============================================================================

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
// 填充方式（PKCS7 / ZeroPadding）
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
		return nil, errors.New("invalid padding")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > len(data) {
		return nil, errors.New("invalid padding")
	}
	// 循环验证所有 padding 字节，防止 padding oracle 攻击。
	// 使用统一错误消息，不泄露具体 padding 值或失败位置。
	for i := len(data) - padding; i < len(data); i++ {
		if int(data[i]) != padding {
			return nil, errors.New("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}

// ZeroPad 使用 ZeroPadding 方式填充数据至 blockSize 的整数倍。
// 注意：ZeroPadding 在明文末尾恰好为 0x00 时会产生歧义，仅在兼容旧系统时使用。
func ZeroPad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == blockSize {
		return data
	}
	padtext := make([]byte, padding)
	return append(data, padtext...)
}

// ZeroUnpad 去除 ZeroPadding。
func ZeroUnpad(data []byte) []byte {
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] != 0 {
			return data[:i+1]
		}
	}
	return nil
}

// ============================================================================
// Buffer Pool（减少 GC 压力，适用于高频加密场景）
// ============================================================================

// sm4BufferPool 复用 SM4 加密用的临时缓冲区（16KB 起步）。
var sm4BufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 16*1024)
		return &b
	},
}

// AcquireBuffer 从池中获取一个缓冲区。
func AcquireBuffer() *[]byte {
	return sm4BufferPool.Get().(*[]byte)
}

// ReleaseBuffer 将缓冲区归还池中（重置长度但保留容量）。
func ReleaseBuffer(buf *[]byte) {
	*buf = (*buf)[:0]
	sm4BufferPool.Put(buf)
}

// ============================================================================
// 流式加密（适用于大文件/大数据场景）
//
// 使用 SM4-GCM 分块处理，每块独立认证，支持 io.Reader → io.Writer 流式传输。
// 适用于加密数十 MB 至 GB 级别的大文件，避免一次性加载到内存。
// ============================================================================

// SM4ChunkSize 流式加密的默认块大小（64KB）。
const SM4ChunkSize = 64 * 1024

// SM4EncryptStream 使用 SM4-GCM 流式加密数据。
// 从 reader 读取数据，分块加密后写入 writer。
// 输出格式：nonce(12B) + [chunkLen(4B) + ciphertext + tag(16B)] * N
// 每个块独立认证，支持随机访问解密。
func SM4EncryptStream(key []byte, reader io.Reader, writer io.Writer) error {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return fmt.Errorf("SM4 stream new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("SM4 stream GCM mode: %w", err)
	}

	// 写入主 nonce（用于派生每块的 nonce）
	masterNonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(masterNonce); err != nil {
		return fmt.Errorf("SM4 stream nonce: %w", err)
	}
	if _, err := writer.Write(masterNonce); err != nil {
		return fmt.Errorf("SM4 stream write nonce: %w", err)
	}

	buf := AcquireBuffer()
	defer ReleaseBuffer(buf)

	chunk := make([]byte, SM4ChunkSize)
	counter := uint32(0)

	for {
		n, readErr := reader.Read(chunk)
		if n > 0 {
			// 派生本块 nonce：masterNonce 的前 8 字节 + counter(4B)
			blockNonce := make([]byte, gcm.NonceSize())
			copy(blockNonce, masterNonce)
			blockNonce[len(blockNonce)-4] = byte(counter >> 24)
			blockNonce[len(blockNonce)-3] = byte(counter >> 16)
			blockNonce[len(blockNonce)-2] = byte(counter >> 8)
			blockNonce[len(blockNonce)-1] = byte(counter)

			ciphertext := gcm.Seal(nil, blockNonce, chunk[:n], nil)

			// 写入块长度（4 字节大端）+ 密文+tag
			*buf = (*buf)[:0]
			*buf = append(*buf, byte(len(ciphertext)>>24), byte(len(ciphertext)>>16),
				byte(len(ciphertext)>>8), byte(len(ciphertext)))
			*buf = append(*buf, ciphertext...)

			if _, err := writer.Write(*buf); err != nil {
				return fmt.Errorf("SM4 stream write chunk %d: %w", counter, err)
			}
			counter++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("SM4 stream read: %w", readErr)
		}
	}
	return nil
}

// SM4DecryptStream 流式解密 SM4EncryptStream 的输出。
func SM4DecryptStream(key []byte, reader io.Reader, writer io.Writer) error {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return fmt.Errorf("SM4 stream new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("SM4 stream GCM mode: %w", err)
	}

	nonceSize := gcm.NonceSize()
	masterNonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(reader, masterNonce); err != nil {
		return fmt.Errorf("SM4 stream read nonce: %w", err)
	}

	buf := AcquireBuffer()
	defer ReleaseBuffer(buf)

	counter := uint32(0)
	lenBuf := make([]byte, 4)

	for {
		// 读取块长度
		_, err := io.ReadFull(reader, lenBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("SM4 stream read chunk len: %w", err)
		}
		chunkLen := int(lenBuf[0])<<24 | int(lenBuf[1])<<16 | int(lenBuf[2])<<8 | int(lenBuf[3])
		if chunkLen <= 0 || chunkLen > SM4ChunkSize+16 {
			return fmt.Errorf("SM4 stream: invalid chunk length %d", chunkLen)
		}

		// 读取密文+tag
		ciphertext := make([]byte, chunkLen)
		if _, err := io.ReadFull(reader, ciphertext); err != nil {
			return fmt.Errorf("SM4 stream read chunk %d: %w", counter, err)
		}

		// 派生本块 nonce
		blockNonce := make([]byte, nonceSize)
		copy(blockNonce, masterNonce)
		blockNonce[len(blockNonce)-4] = byte(counter >> 24)
		blockNonce[len(blockNonce)-3] = byte(counter >> 16)
		blockNonce[len(blockNonce)-2] = byte(counter >> 8)
		blockNonce[len(blockNonce)-1] = byte(counter)

		plaintext, err := gcm.Open(nil, blockNonce, ciphertext, nil)
		if err != nil {
			return fmt.Errorf("SM4 stream decrypt chunk %d: %w", counter, err)
		}

		*buf = (*buf)[:0]
		*buf = append(*buf, plaintext...)
		if _, err := writer.Write(*buf); err != nil {
			return fmt.Errorf("SM4 stream write chunk %d: %w", counter, err)
		}
		counter++
	}
	return nil
}
