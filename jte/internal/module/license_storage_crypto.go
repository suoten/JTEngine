package module

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// ===================================================================
// P2-9: licenses.json 离线破解防护 —— AES-256-GCM 加密存储
// ===================================================================
//
// 原实现将 licenses.json 以明文 JSON 落盘，攻击者获取文件后可：
//   - 直接读取/篡改授权信息（修改过期时间、模块列表）
//   - 复制到其他机器克隆授权（配合指纹校验可缓解，但明文仍泄露业务信息）
//
// 修复方案：
//   - 用机器指纹派生 AES-256 密钥（SHA-256(fingerprint) → 32B）
//   - AES-256-GCM 加密 JSON 数据（认证加密，防篡改）
//   - 文件格式: magic(4B "JTL1") + nonce(12B) + ciphertext+tag
//   - 向后兼容：检测明文 JSON（首字节为 '{'）时自动迁移为加密格式
//
// 安全说明：
//   - 指纹派生密钥仅在原机器可解密，文件被复制到其他机器无法解密
//   - GCM 认证标签防篡改：修改任何字节都会导致解密失败
//   - 机器指纹变化（硬件更换）会导致密钥不匹配，需重新激活授权

const licensesFileMagic = "JTL1" // JTE License v1

// saveEncryptedLicenseStore 将 licenseStore 加密后写入文件。
// AUTO-FIX-2026-06-30 [P2-9]: AES-256-GCM 加密，密钥由机器指纹派生。
func saveEncryptedLicenseStore(configDir, fingerprint string, store *licenseStore, logger *zap.Logger) error {
	if configDir == "" {
		return nil
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}

	plaintext, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal license store: %w", err)
	}

	key := deriveLicenseStorageKey(fingerprint)
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	// 文件格式: magic + nonce + ciphertext(含 auth tag)
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(licensesFileMagic))
	out := make([]byte, 0, len(licensesFileMagic)+len(nonce)+len(ciphertext))
	out = append(out, []byte(licensesFileMagic)...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	licenseFile := filepath.Join(configDir, "licenses.json")
	tmpFile := licenseFile + ".tmp"
	if err := os.WriteFile(tmpFile, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmpFile, licenseFile)
}

// loadEncryptedLicenseStore 读取并解密 licenseStore。
// AUTO-FIX-2026-06-30 [P2-9]: 支持加密格式 + 明文兼容迁移。
func loadEncryptedLicenseStore(configDir, fingerprint string, logger *zap.Logger) (*licenseStore, error) {
	if configDir == "" {
		return &licenseStore{}, nil
	}

	licenseFile := filepath.Join(configDir, "licenses.json")
	raw, err := os.ReadFile(licenseFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &licenseStore{}, nil
		}
		return nil, err
	}

	// 向后兼容：明文 JSON（首字节为 '{'）→ 解析后迁移为加密格式
	if len(raw) > 0 && raw[0] == '{' {
		logger.Warn("licenses.json is plaintext, migrating to AES-256-GCM encrypted format",
			zap.String("file", licenseFile))
		var store licenseStore
		if err := json.Unmarshal(raw, &store); err != nil {
			// 兼容旧格式（纯 license 数组）
			var licenses []*License
			if jErr := json.Unmarshal(raw, &licenses); jErr == nil {
				store.Licenses = licenses
			} else {
				return nil, fmt.Errorf("parse plaintext license store: %w", err)
			}
		}
		// 异步迁移为加密格式
		_ = saveEncryptedLicenseStore(configDir, fingerprint, &store, logger)
		return &store, nil
	}

	// 加密格式: magic(4B) + nonce(12B) + ciphertext
	if len(raw) < len(licensesFileMagic)+12 {
		return nil, errors.New("license file too short (corrupted or unknown format)")
	}

	magic := string(raw[:len(licensesFileMagic)])
	if magic != licensesFileMagic {
		return nil, fmt.Errorf("invalid license file magic: %q (expected %q)", magic, licensesFileMagic)
	}

	key := deriveLicenseStorageKey(fingerprint)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	nonceLen := gcm.NonceSize()
	nonce := raw[len(licensesFileMagic) : len(licensesFileMagic)+nonceLen]
	ciphertext := raw[len(licensesFileMagic)+nonceLen:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(licensesFileMagic))
	if err != nil {
		return nil, fmt.Errorf("decrypt license store (fingerprint mismatch or file tampered): %w", err)
	}

	var store licenseStore
	if err := json.Unmarshal(plaintext, &store); err != nil {
		return nil, fmt.Errorf("unmarshal decrypted license store: %w", err)
	}
	return &store, nil
}

// deriveLicenseStorageKey 从机器指纹派生 32 字节 AES-256 密钥。
// 指纹不同则密钥不同，文件复制到其他机器无法解密。
func deriveLicenseStorageKey(fingerprint string) []byte {
	h := sha256.Sum256([]byte("jte-license-storage-key|" + fingerprint))
	return h[:]
}
