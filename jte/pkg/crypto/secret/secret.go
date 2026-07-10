// Package secret 提供关键数据落盘加密服务（等保2.0 三级 - 数据机密性要求）。
//
// 基于 SM4-GCM 认证加密（GB/T 32907-2016 + GCM），主密钥来自配置或环境变量。
// 用于：手机号、身份证号、车牌号等敏感字段存储加密；审计日志防篡改 HMAC 密钥。
//
// 设计要点：
//   - 主密钥（Master Key）16 字节，来自 CryptoConfig.SM4Key（hex）或 JTE_SM4_KEY 环境变量
//   - 每次加密生成随机 nonce，密文 = nonce(12B) || ciphertext || tag(16B)
//   - 加密结果以 "enc:v1:" 前缀 + base64 编码存储，便于识别和向后兼容
//   - 未启用加密时（Enabled=false）透明透传明文，不影响现有数据
package secret

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jte-engine/jte/pkg/crypto/gmsm"
)

// CipherPrefix 加密数据前缀，用于识别密文（向后兼容明文数据）
const CipherPrefix = "enc:v1:"

// DataCipher 关键数据加密器（SM4-GCM 认证加密）。
// Enabled=false 时 Encrypt/Decrypt 透传原值，实现配置化开关。
type DataCipher struct {
	mu        sync.RWMutex
	masterKey []byte // 16 字节 SM4 主密钥
	enabled   bool
}

// NewDataCipher 从主密钥创建加密器。
// masterKeyHex 为 32 字符 hex（16 字节）；为空时尝试从 JTE_SM4_KEY 环境变量读取。
// enabled=false 时透传明文（开发环境或未启用国密时）。
func NewDataCipher(masterKeyHex string, enabled bool) (*DataCipher, error) {
	c := &DataCipher{enabled: enabled}
	if !enabled {
		return c, nil
	}

	if masterKeyHex == "" {
		masterKeyHex = os.Getenv("JTE_SM4_KEY")
	}
	if masterKeyHex == "" {
		return nil, errors.New("SM4 主密钥未配置：请设置 crypto.sm4_key 或环境变量 JTE_SM4_KEY")
	}

	key, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("SM4 主密钥 hex 解码失败: %w", err)
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("SM4 主密钥长度必须为 16 字节（32 hex 字符），当前 %d 字节", len(key))
	}

	c.masterKey = key
	return c, nil
}

// Enabled 返回加密器是否启用
func (c *DataCipher) Enabled() bool {
	if c == nil {
		return false
	}
	return c.enabled
}

// Encrypt 加密明文，返回带前缀的 base64 密文。
// 未启用加密时直接返回明文。
// 已经是密文（含 CipherPrefix）则原样返回（幂等）。
func (c *DataCipher) Encrypt(plaintext string) (string, error) {
	if c == nil || !c.enabled {
		return plaintext, nil
	}
	if plaintext == "" {
		return "", nil
	}
	// 幂等：已加密数据不重复加密
	if strings.HasPrefix(plaintext, CipherPrefix) {
		return plaintext, nil
	}

	c.mu.RLock()
	key := c.masterKey
	c.mu.RUnlock()

	ct, err := gmsm.SM4EncryptGCM(key, []byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("SM4 加密失败: %w", err)
	}
	return CipherPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt 解密密文，返回明文。
// 未启用加密时直接返回原值。
// 非 CipherPrefix 前缀的数据视为明文原样返回（向后兼容存量数据）。
func (c *DataCipher) Decrypt(ciphertext string) (string, error) {
	if c == nil || !c.enabled {
		return ciphertext, nil
	}
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, CipherPrefix) {
		// 存量明文数据，直接返回
		return ciphertext, nil
	}

	raw := strings.TrimPrefix(ciphertext, CipherPrefix)
	ct, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}

	c.mu.RLock()
	key := c.masterKey
	c.mu.RUnlock()

	pt, err := gmsm.SM4DecryptGCM(key, ct)
	if err != nil {
		return "", fmt.Errorf("SM4 解密失败: %w", err)
	}
	return string(pt), nil
}

// EncryptField 加密结构体字段（便捷方法）。
// 空字符串不加密，直接返回空。
func (c *DataCipher) EncryptField(value string) string {
	enc, err := c.Encrypt(value)
	if err != nil {
		// 加密失败不应阻断业务，返回原值并记录日志（由调用方记录）
		return value
	}
	return enc
}

// DecryptField 解密结构体字段（便捷方法，错误时返回原值）。
func (c *DataCipher) DecryptField(value string) string {
	dec, err := c.Decrypt(value)
	if err != nil {
		return value
	}
	return dec
}

// RotateMasterKey 轮换主密钥。
// 旧密钥需在外部维护用于解密存量数据；新密钥仅对新写入数据生效。
// 生产环境建议通过 reindex 任务批量重加密存量数据。
func (c *DataCipher) RotateMasterKey(newMasterKeyHex string) error {
	key, err := hex.DecodeString(newMasterKeyHex)
	if err != nil {
		return fmt.Errorf("新主密钥 hex 解码失败: %w", err)
	}
	if len(key) != 16 {
		return fmt.Errorf("新主密钥长度必须为 16 字节，当前 %d 字节", len(key))
	}
	c.mu.Lock()
	c.masterKey = key
	c.mu.Unlock()
	return nil
}

// GenerateMasterKey 生成随机 SM4 主密钥（16 字节，返回 32 hex 字符）。
// 用于首次部署或密钥轮换时生成新主密钥。
func GenerateMasterKey() (string, error) {
	key, err := gmsm.SM4GenerateKey()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// GenerateNonce 生成随机 nonce（用于其他需要随机数的场景）
func GenerateNonce(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
