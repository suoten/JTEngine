package middleware

import (
	"crypto/tls"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware 安全响应头中间件
// 设置常见安全头，防御 XSS/点击劫持/MIME 嗅探等攻击。
// AUTO-FIX-2026-07-02: HSTS 仅在 HTTPS 下设置（HTTP 下设置 HSTS 会导致
// 浏览器缓存后无法降级回 HTTP，且 HTTP 下 HSTS 无意义）。
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")

		// HSTS 仅在 HTTPS 下设置（防止 HTTP 降级攻击，但 HTTP 下设置有害）
		if c.Request.TLS != nil || isHTTPSProxy(c) {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}

		// CSP 策略：允许内联样式（Vue 运行时需要）和同源资源
		c.Header("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; "+
				"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; "+
				"connect-src 'self' wss: ws:; font-src 'self' data:")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}

// isHTTPSProxy 检测反向代理后的 HTTPS（X-Forwarded-Proto: https）
func isHTTPSProxy(c *gin.Context) bool {
	if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
		return true
	}
	return false
}

// RequireTLS 中间件：强制 HTTPS，HTTP 请求返回 426 Upgrade Required
// 仅在 TLS 启用时挂载到非 TLS 端口（生产环境推荐）
func RequireTLS() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.TLS == nil && !isHTTPSProxy(c) {
			c.JSON(http.StatusUpgradeRequired, gin.H{
				"code":    426,
				"message": "HTTPS required: please use HTTPS protocol",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// TLSCertConfig TLS 证书配置（支持国密 SSL 和标准 TLS）
type TLSCertConfig struct {
	// CertFile 证书文件路径（PEM 格式）
	CertFile string
	// KeyFile 私钥文件路径（PEM 格式）
	KeyFile string
	// AutoRenew 是否启用证书自动续期
	AutoRenew bool
	// MinVersion 最小 TLS 版本（默认 tls.VersionTLS12）
	MinVersion uint16
	// CipherSuites 密码套件（国密 SSL 可加入 ECC-SM2-SM4-SM3 套件）
	CipherSuites []uint16
}

// DefaultTLSConfig 返回安全的 TLS 配置（等保2.0 合规）
// - 强制 TLS 1.2+（禁用 TLS 1.0/1.1）
// - 优先使用前向保密（PFS）密码套件
// - 禁用已知不安全的套件（RC4/MD5/SHA1/3DES）
func DefaultTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			// TLS 1.2 强密码套件（前向保密）
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
		// TLS 1.3 默认安全，无需指定 CipherSuites
		PreferServerCipherSuites: true,
		CurvePreferences:         []tls.CurveID{tls.X25519, tls.CurveP256},
	}, nil
}
