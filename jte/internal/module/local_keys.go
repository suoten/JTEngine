package module

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// LocalKeyPair 保存本机生成的 RSA 密钥对，用于对试用记录和离线解绑凭证签名。
// 私钥仅存于本机 configDir，公钥可提交给官网用于验证。
type LocalKeyPair struct {
	PrivateKey *rsa.PrivateKey
}

// GenerateOrLoadLocalKeys 加载或生成本机 RSA-2048 密钥对。
// 密钥文件存储在 configDir/local_keys.pem，权限 0600。
func GenerateOrLoadLocalKeys(configDir string) (*LocalKeyPair, error) {
	if configDir == "" {
		return nil, fmt.Errorf("configDir is empty")
	}

	keyPath := filepath.Join(configDir, "local_keys.pem")

	// 尝试加载已有密钥
	data, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err == nil && key != nil {
				return &LocalKeyPair{PrivateKey: key}, nil
			}
		}
	}

	// 生成新密钥对
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}

	// 保存私钥
	keyBytes := x509.MarshalPKCS1PrivateKey(privKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})

	tmpPath := keyPath + ".tmp"
	if err := os.WriteFile(tmpPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	if err := os.Rename(tmpPath, keyPath); err != nil {
		return nil, fmt.Errorf("rename key file: %w", err)
	}

	return &LocalKeyPair{PrivateKey: privKey}, nil
}

// Sign 对 data 进行 RSA-SHA256 签名，返回签名字节。
func (kp *LocalKeyPair) Sign(data []byte) ([]byte, error) {
	if kp == nil || kp.PrivateKey == nil {
		return nil, fmt.Errorf("local private key not available")
	}
	hash := sha256.Sum256(data)
	return rsa.SignPKCS1v15(rand.Reader, kp.PrivateKey, crypto.SHA256, hash[:])
}

// SignBase64 对 data 进行签名并返回 Base64 编码字符串。
func (kp *LocalKeyPair) SignBase64(data []byte) (string, error) {
	sig, err := kp.Sign(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// PublicKeyPEM 返回公钥的 PEM 编码，可提交给官网用于验证签名。
func (kp *LocalKeyPair) PublicKeyPEM() (string, error) {
	if kp == nil || kp.PrivateKey == nil {
		return "", fmt.Errorf("local private key not available")
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&kp.PrivateKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})
	return string(pubPEM), nil
}

// VerifyLocalSignature 使用本机公钥验证签名（用于本地校验试用记录）。
func VerifyLocalSignature(pubKey *rsa.PublicKey, data []byte, signature []byte) error {
	if pubKey == nil {
		return fmt.Errorf("public key not available")
	}
	hash := sha256.Sum256(data)
	return rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], signature)
}
