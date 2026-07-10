package module

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
)

// rsaPublicKeyPEM 是 JTE 官方用于签发模块授权码的 RSA-2048 公钥。
// 对应的私钥由官网保管，用于签发 license 和模块签名。
// 此公钥嵌入代码用于验证模块签名和 license 签名。
const rsaPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA5ev7Kln98iMblV70lVf5
Xu0qkLg6QHiNeIm3f0Zm7okPHWKS0tl2zPlPBy1E5CdQpJBhLLDDtUEOLw5piQKh
vnZ9JcN5i/qAF1qxq21GzAe/gxn554HyY5zXQIDIxvl+jEea/0A2EtYIBtHqYbt0
OxxUn6buu76VoDmfGpkw1kNjLrzkDXRzE6xDgA8Abu5firaJt8Vg9ZcFWXfbcY+E
iEJOIKIAhJICEuecsIA9c/ac8vTgd4Y+oUElzYGs+9Bc95QvzMy6ZF5VPBrFcSbE
b9mX16c9P0IMrtfpanSt9JJ9ANoFunUquhXGbsMHyp+tvjxh3G3d1vcCsS17y3mf
vwIDAQAB
-----END PUBLIC KEY-----`

var (
	parsedRSAPublicKey     *rsa.PublicKey
	parsedECDSAPublicKey   *ecdsa.PublicKey
	parsedEd25519PublicKey ed25519.PublicKey
)

func init() {
	block, _ := pem.Decode([]byte(rsaPublicKeyPEM))
	if block == nil {
		log.Fatal("failed to decode embedded RSA public key PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatalf("failed to parse embedded RSA public key: %v", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		log.Fatal("embedded public key is not an RSA key")
	}
	parsedRSAPublicKey = rsaPub
}

func VerifySignature(modulePath, sigPath string) error {
	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("read module file: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature file: %w", err)
	}

	hash := sha256.Sum256(moduleData)

	if parsedEd25519PublicKey != nil {
		if ed25519.Verify(parsedEd25519PublicKey, moduleData, sigData) {
			return nil
		}
	}

	if parsedECDSAPublicKey != nil {
		if ecdsa.VerifyASN1(parsedECDSAPublicKey, hash[:], sigData) {
			return nil
		}
	}

	if parsedRSAPublicKey != nil {
		if err := rsa.VerifyPKCS1v15(parsedRSAPublicKey, crypto.SHA256, hash[:], sigData); err == nil {
			return nil
		}
	}

	return fmt.Errorf("signature verification failed: no valid key matched")
}

func VerifySignatureECDSA(modulePath, sigPath string) error {
	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("read module file: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature file: %w", err)
	}

	if parsedECDSAPublicKey == nil {
		return fmt.Errorf("ECDSA public key not initialized")
	}

	hash := sha256.Sum256(moduleData)
	if !ecdsa.VerifyASN1(parsedECDSAPublicKey, hash[:], sigData) {
		return fmt.Errorf("ECDSA signature verification failed")
	}

	return nil
}

func SetPublicKey(pemData string) error {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}

	switch key := pub.(type) {
	case *rsa.PublicKey:
		parsedRSAPublicKey = key
	case *ecdsa.PublicKey:
		parsedECDSAPublicKey = key
	case ed25519.PublicKey:
		parsedEd25519PublicKey = key
	default:
		return fmt.Errorf("unsupported key type: %T", pub)
	}

	return nil
}

func SetEd25519PublicKey(key ed25519.PublicKey) {
	parsedEd25519PublicKey = key
}
