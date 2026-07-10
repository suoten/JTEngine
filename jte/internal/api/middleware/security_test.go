package middleware

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// generateTestCert 生成临时自签 ECDSA 证书与私钥（PEM 编码），仅用于测试。
func generateTestCert() (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "jte-test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM, nil
}

func TestSecurityHeadersMiddleware_SetsHeaders(t *testing.T) {
	router := gin.New()
	router.Use(SecurityHeadersMiddleware())
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, want := range checks {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	// HTTP 请求不应设置 HSTS
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set on HTTP, got %q", hsts)
	}
}

func TestSecurityHeadersMiddleware_HSTSOnHTTPSProxy(t *testing.T) {
	router := gin.New()
	router.Use(SecurityHeadersMiddleware())
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("HSTS should be set when X-Forwarded-Proto=https")
	}
	if want := "max-age=31536000"; hsts != want && len(hsts) < len(want) {
		t.Errorf("HSTS = %q, want contains %q", hsts, want)
	}
}

func TestIsHTTPSProxy(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"https", true},
		{"http", false},
		{"", false},
	}
	for _, tt := range tests {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/", nil)
		if tt.header != "" {
			c.Request.Header.Set("X-Forwarded-Proto", tt.header)
		}
		if got := isHTTPSProxy(c); got != tt.want {
			t.Errorf("isHTTPSProxy(header=%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestRequireTLS_HTTPRejected(t *testing.T) {
	router := gin.New()
	router.Use(RequireTLS())
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil) // 无 TLS
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", w.Code)
	}
}

func TestRequireTLS_HTTPSProxyPasses(t *testing.T) {
	router := gin.New()
	router.Use(RequireTLS())
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (HTTPS proxy)", w.Code)
	}
}

// generateSelfSignedCert 生成临时自签证书用于 DefaultTLSConfig 测试。
func generateSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	cert, key, err := generateTestCert()
	if err != nil {
		t.Fatalf("generate test cert: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, cert, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestDefaultTLSConfig_ValidCert(t *testing.T) {
	certFile, keyFile := generateSelfSignedCert(t)
	cfg, err := DefaultTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatalf("DefaultTLSConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want %x", cfg.MinVersion, tls.VersionTLS12)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("Certificates len = %d, want 1", len(cfg.Certificates))
	}
	if len(cfg.CipherSuites) == 0 {
		t.Error("CipherSuites should not be empty")
	}
	// 验证仅使用 PFS 套件（ECDHE）
	for _, cs := range cfg.CipherSuites {
		if !isPFSCipher(cs) {
			t.Errorf("cipher %x is not PFS (ECDHE)", cs)
		}
	}
	// 验证曲线偏好
	if len(cfg.CurvePreferences) < 2 {
		t.Error("CurvePreferences should include X25519 and P256")
	}
}

func TestDefaultTLSConfig_InvalidCert(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "nonexistent.pem")
	keyFile := filepath.Join(dir, "nonexistent.key")
	_, err := DefaultTLSConfig(certFile, keyFile)
	if err == nil {
		t.Fatal("expected error for nonexistent cert files")
	}
}

// isPFSCipher 检查是否为前向保密密码套件（含 ECDHE）。
func isPFSCipher(cs uint16) bool {
	switch cs {
	case tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:
		return true
	}
	return false
}
