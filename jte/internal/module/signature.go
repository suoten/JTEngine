package module

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
)

// rsaPublicKeyPEM 是 JTE 开源版用于签发模块授权码的 RSA-2048 公钥。
// 对应的私钥位于 modules/local_keys.pem，用于本地签名模块和 license。
// 生产环境请替换为自有密钥对。
const rsaPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAzUUm4OaZXTJd3TYWN5hY
ci/vIQ1FxYOgymfDGv3VSc6Fj3TbXaiw41Sd8Fuf68vG2BbTiibatF7jog2llKPI
avn+kn1yieoaSlV3p+zLtaeK96UMz1+VCVHoQ1P0QC+8bOXXAxlElku5Prl1O0ES
Aca9dJCtO9u5AhwC37XAnDJwFwidaGE8YMFr4t39Hf8KyhClSZbt/ZNxmDUskX9S
6pJyKCSROZ9/I+hYXDA/VhjLgFhdFieWc2VJbASLaZUVy5xD1ekaBmzcpQD4pxYd
KSTqeA3t4nG7OpJMv7+NXtipIyoPRK9t549mGHsnBZfZfcFeP1Hvhq1TmYWlr8Dx
SQIDAQAB
-----END PUBLIC KEY-----`

var (
	// keyMu 保护以下公钥变量的并发访问
	keyMu                  sync.Mutex
	parsedRSAPublicKey     *rsa.PublicKey
	parsedECDSAPublicKey   *ecdsa.PublicKey
	parsedEd25519PublicKey ed25519.PublicKey

	// embeddedInitOnce 确保内嵌 RSA 公钥只解析一次
	embeddedInitOnce sync.Once
	// embeddedInitErr 记录内嵌公钥初始化错误
	embeddedInitErr error
	// embeddedKeyParsed 缓存解析出的内嵌 RSA 公钥，仅在 parsedRSAPublicKey 未被外部设置时使用
	embeddedKey *rsa.PublicKey
)

// lazyInit 延迟解析内嵌的 RSA 公钥，替代原 init() 函数。
// 使用 sync.Once 保证解析只执行一次，将原 log.Fatal 改为返回 error。
// 调用方（如 Loader.loadModule、parseAndVerifyLicense）在需要使用
// 公钥前必须先调用此函数并检查错误。
//
// [P0-安全] 原 init() 中使用 log.Fatal 会导致整个进程在公钥解析失败时
// 立即退出，无法被上层捕获和优雅处理。改为返回 error 后，调用方可以
// 在模块加载时显式处理错误（如记录日志、降级、拒绝启动等）。
//
// 注意：此函数仅解析内嵌公钥，不会覆盖通过 SetPublicKey 外部设置的公钥。
// 如果 parsedRSAPublicKey 已被 SetPublicKey 设置，则保留外部设置的值。
func lazyInit() error {
	embeddedInitOnce.Do(func() {
		block, _ := pem.Decode([]byte(rsaPublicKeyPEM))
		if block == nil {
			embeddedInitErr = errors.New("failed to decode embedded RSA public key PEM block")
			return
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			embeddedInitErr = fmt.Errorf("failed to parse embedded RSA public key: %w", err)
			return
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			embeddedInitErr = errors.New("embedded public key is not an RSA key")
			return
		}
		embeddedKey = rsaPub
		// 仅在 parsedRSAPublicKey 未被外部设置时才使用内嵌公钥
		keyMu.Lock()
		if parsedRSAPublicKey == nil {
			parsedRSAPublicKey = rsaPub
		}
		keyMu.Unlock()
	})
	return embeddedInitErr
}

// VerifySignature 验证模块文件签名。
// 首先调用 lazyInit() 确保内嵌公钥已就绪，失败则直接返回错误。
func VerifySignature(modulePath, sigPath string) error {
	// [P0-安全] 显式初始化公钥，失败时返回错误而非 log.Fatal
	if err := lazyInit(); err != nil {
		return fmt.Errorf("signature key initialization failed: %w", err)
	}

	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("read module file: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature file: %w", err)
	}

	hash := sha256.Sum256(moduleData)

	keyMu.Lock()
	defer keyMu.Unlock()

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

// VerifySignatureECDSA 使用 ECDSA 公钥验证签名。
// 首先调用 lazyInit() 确保公钥已就绪。
func VerifySignatureECDSA(modulePath, sigPath string) error {
	// [P0-安全] 显式初始化公钥
	if err := lazyInit(); err != nil {
		return fmt.Errorf("signature key initialization failed: %w", err)
	}

	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("read module file: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature file: %w", err)
	}

	keyMu.Lock()
	defer keyMu.Unlock()

	if parsedECDSAPublicKey == nil {
		return fmt.Errorf("ECDSA public key not initialized")
	}

	hash := sha256.Sum256(moduleData)
	if !ecdsa.VerifyASN1(parsedECDSAPublicKey, hash[:], sigData) {
		return fmt.Errorf("ECDSA signature verification failed")
	}

	return nil
}

// SetPublicKey 设置 PEM 格式的公钥，用于替换或补充内嵌的默认公钥。
// 此函数是线程安全的，使用互斥锁保护公钥变量。
func SetPublicKey(pemData string) error {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}

	keyMu.Lock()
	defer keyMu.Unlock()

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

// SetEd25519PublicKey 设置 Ed25519 公钥。
// 此函数是线程安全的。
func SetEd25519PublicKey(key ed25519.PublicKey) {
	keyMu.Lock()
	defer keyMu.Unlock()
	parsedEd25519PublicKey = key
}

// getPublicKeys 返回当前已设置的公钥的快照。
// 调用方应在持有返回值期间进行验证操作，避免并发修改。
// 此函数是线程安全的。
//
// [P0-安全] 替代直接访问包级变量 parsedRSAPublicKey/parsedECDSAPublicKey，
// 避免数据竞争。
func getPublicKeys() (rsaKey *rsa.PublicKey, ecdsaKey *ecdsa.PublicKey, ed25519Key ed25519.PublicKey) {
	keyMu.Lock()
	defer keyMu.Unlock()
	return parsedRSAPublicKey, parsedECDSAPublicKey, parsedEd25519PublicKey
}
