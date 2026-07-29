// Package gmsm 提供国密算法的配置管理。
//
// CryptoConfig 统一管理国密算法的开关、软/硬加密切换和密钥配置。
// 支持从配置文件（YAML）或环境变量加载配置。

package gmsm

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
)

// ============================================================================
// CryptoConfig 国密配置
// ============================================================================

// CryptoConfig 国密算法统一配置（等保2.0 合规）。
// 通过配置文件或环境变量初始化，控制各算法的开关和密钥来源。
type CryptoConfig struct {
	mu sync.RWMutex

	// 各算法开关
	sm2Enabled bool
	sm3Enabled bool
	sm4Enabled bool

	// 软件/硬件切换
	provider HSMProvider // nil 表示使用软件实现

	// SM4 主密钥（hex 编码，16 字节 = 32 字符）
	sm4KeyHex string
	sm4Key    []byte // 解码后的密钥

	// SM2 密钥对（hex 编码）
	sm2PrivKeyHex string
	sm2PrivKey    []byte // 解码后的私钥 D 标量

	// SM2 默认密文模式
	sm2DefaultMode SM2CiphertextMode

	// 是否已初始化
	initialized bool
}

// CryptoConfigOptions 配置选项，用于构建 CryptoConfig。
type CryptoConfigOptions struct {
	// SM2Enabled 是否启用 SM2 算法
	SM2Enabled bool
	// SM3Enabled 是否启用 SM3 算法
	SM3Enabled bool
	// SM4Enabled 是否启用 SM4 算法
	SM4Enabled bool

	// SM4KeyHex SM4 主密钥（hex 编码，32 字符 = 16 字节）。
	// 为空时尝试从环境变量 JTE_SM4_KEY 读取。
	SM4KeyHex string

	// SM2PrivKeyHex SM2 私钥 D 标量（hex 编码，64 字符 = 32 字节）。
	// 为空时尝试从环境变量 JTE_SM2_PRIV_KEY 读取。
	// 为空且 SM2Enabled=true 时将自动生成新密钥对。
	SM2PrivKeyHex string

	// SM2DefaultMode SM2 默认密文拼接模式（默认 C1C3C2）
	SM2DefaultMode SM2CiphertextMode

	// Provider 硬件安全模块（nil 表示使用软件实现）
	Provider HSMProvider
}

// DefaultCryptoConfig 返回默认配置（全部启用、软件实现、C1C3C2 模式）。
func DefaultCryptoConfig() *CryptoConfigOptions {
	return &CryptoConfigOptions{
		SM2Enabled:     true,
		SM3Enabled:     true,
		SM4Enabled:     true,
		SM2DefaultMode: SM2ModeC1C3C2,
	}
}

// NewCryptoConfig 根据选项创建国密配置。
func NewCryptoConfig(opts *CryptoConfigOptions) (*CryptoConfig, error) {
	if opts == nil {
		opts = DefaultCryptoConfig()
	}

	cfg := &CryptoConfig{
		sm2Enabled:     opts.SM2Enabled,
		sm3Enabled:     opts.SM3Enabled,
		sm4Enabled:     opts.SM4Enabled,
		provider:       opts.Provider,
		sm2DefaultMode: opts.SM2DefaultMode,
	}

	// 加载 SM4 主密钥
	if opts.SM4Enabled {
		keyHex := opts.SM4KeyHex
		if keyHex == "" {
			keyHex = os.Getenv("JTE_SM4_KEY")
		}
		if keyHex == "" {
			return nil, errors.New("SM4 主密钥未配置：请设置 crypto.sm4_key 或环境变量 JTE_SM4_KEY")
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("SM4 主密钥 hex 解码失败: %w", err)
		}
		if len(key) != 16 {
			return nil, fmt.Errorf("SM4 主密钥长度必须为 16 字节（32 hex 字符），当前 %d 字节", len(key))
		}
		cfg.sm4KeyHex = keyHex
		cfg.sm4Key = key
	}

	// 加载 SM2 私钥
	if opts.SM2Enabled {
		privHex := opts.SM2PrivKeyHex
		if privHex == "" {
			privHex = os.Getenv("JTE_SM2_PRIV_KEY")
		}
		if privHex != "" {
			privBytes, err := hex.DecodeString(privHex)
			if err != nil {
				return nil, fmt.Errorf("SM2 私钥 hex 解码失败: %w", err)
			}
			if len(privBytes) != 32 {
				return nil, fmt.Errorf("SM2 私钥长度必须为 32 字节（64 hex 字符），当前 %d 字节", len(privBytes))
			}
			cfg.sm2PrivKeyHex = privHex
			cfg.sm2PrivKey = privBytes
		}
		// privHex 为空时不报错，调用方可通过 SM2GenerateKeyPair() 动态生成
	}

	cfg.initialized = true
	return cfg, nil
}

// SM2Enabled 返回 SM2 是否启用。
func (c *CryptoConfig) SM2Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sm2Enabled
}

// SM3Enabled 返回 SM3 是否启用。
func (c *CryptoConfig) SM3Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sm3Enabled
}

// SM4Enabled 返回 SM4 是否启用。
func (c *CryptoConfig) SM4Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sm4Enabled
}

// IsHardwareMode 返回是否使用硬件加密模式。
func (c *CryptoConfig) IsHardwareMode() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.provider != nil
}

// ProviderName 返回当前 Provider 名称。
func (c *CryptoConfig) ProviderName() string {
	if c == nil {
		return "nil"
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.provider != nil {
		return c.provider.Name()
	}
	return "software"
}

// SM4Key 返回 SM4 主密钥（调用方不应修改返回的切片）。
func (c *CryptoConfig) SM4Key() []byte {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sm4Key
}

// SM2DefaultMode 返回 SM2 默认密文拼接模式。
func (c *CryptoConfig) SM2DefaultMode() SM2CiphertextMode {
	if c == nil {
		return SM2ModeC1C3C2
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sm2DefaultMode
}

// SM2PrivateKey 返回配置的 SM2 私钥（可能为 nil，调用方需动态生成）。
func (c *CryptoConfig) SM2PrivateKey() ([]byte, error) {
	if c == nil {
		return nil, errors.New("config is nil")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sm2PrivKey == nil {
		return nil, errors.New("SM2 私钥未配置，请设置 crypto.sm2_priv_key 或环境变量 JTE_SM2_PRIV_KEY，或调用 SM2GenerateKeyPair() 动态生成")
	}
	return c.sm2PrivKey, nil
}

// SetProvider 动态切换加解密 Provider（软件 ↔ 硬件）。
// 传入 nil 切换回软件实现。
func (c *CryptoConfig) SetProvider(provider HSMProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 关闭旧的 Provider
	if c.provider != nil {
		_ = c.provider.Close()
	}
	c.provider = provider
}

// RotateSM4Key 轮换 SM4 主密钥。
func (c *CryptoConfig) RotateSM4Key(newKeyHex string) error {
	key, err := hex.DecodeString(newKeyHex)
	if err != nil {
		return fmt.Errorf("新 SM4 主密钥 hex 解码失败: %w", err)
	}
	if len(key) != 16 {
		return fmt.Errorf("新 SM4 主密钥长度必须为 16 字节，当前 %d 字节", len(key))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sm4KeyHex = newKeyHex
	c.sm4Key = key
	return nil
}

// SetSM2PrivateKey 设置 SM2 私钥。
func (c *CryptoConfig) SetSM2PrivateKey(privKeyHex string) error {
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return fmt.Errorf("SM2 私钥 hex 解码失败: %w", err)
	}
	if len(privBytes) != 32 {
		return fmt.Errorf("SM2 私钥长度必须为 32 字节，当前 %d 字节", len(privBytes))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sm2PrivKeyHex = privKeyHex
	c.sm2PrivKey = privBytes
	return nil
}

// SetEnabled 动态开关各算法。
func (c *CryptoConfig) SetEnabled(sm2, sm3, sm4 bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sm2Enabled = sm2
	c.sm3Enabled = sm3
	c.sm4Enabled = sm4
}

// Close 关闭配置，释放硬件资源。
func (c *CryptoConfig) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		err := c.provider.Close()
		c.provider = nil
		return err
	}
	return nil
}

// ============================================================================
// 全局默认配置（简化调用方使用）
// ============================================================================

var (
	globalConfig     *CryptoConfig
	globalConfigOnce sync.Once
	globalConfigErr  error
)

// InitGlobalConfig 初始化全局默认配置（仅执行一次）。
func InitGlobalConfig(opts *CryptoConfigOptions) error {
	globalConfigOnce.Do(func() {
		globalConfig, globalConfigErr = NewCryptoConfig(opts)
	})
	return globalConfigErr
}

// GetGlobalConfig 返回全局配置。
// 必须先调用 InitGlobalConfig()，否则返回 nil。
func GetGlobalConfig() *CryptoConfig {
	return globalConfig
}

// ResetGlobalConfig 重置全局配置（仅用于测试）。
func ResetGlobalConfig() {
	globalConfig = nil
	globalConfigOnce = sync.Once{}
	globalConfigErr = nil
}
