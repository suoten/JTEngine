package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/suoten/jt-engine/internal/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// getTestSecret 返回测试用 JWT 签名密钥（32 bytes，仅用于单元测试）
func getTestSecret() string {
	if v := os.Getenv("JTE_TEST_JWT_SECRET"); v != "" {
		return v
	}
	return strings.Repeat("a", 32) // 32 bytes for HS256
}

// getTestKidSecret 返回测试用 kid 签名密钥（32 bytes，仅用于单元测试）
func getTestKidSecret() string {
	if v := os.Getenv("JTE_TEST_JWT_KID_SECRET"); v != "" {
		return v
	}
	return strings.Repeat("b", 32) // 32 bytes for HS256
}

// newTestJWTConfig 构造测试用 JWTConfig（双 kid 轮换）。
func newTestJWTConfig() *config.JWTConfig {
	return &config.JWTConfig{
		Secrets: map[string]string{
			"default":     getTestSecret(),
			"kid-2026-06": getTestKidSecret(),
		},
		ActiveKid: "kid-2026-06",
	}
}

// signToken 用指定 secret 和 kid 签发 HS256 token。
func signToken(secret, kid string, claims jwt.MapClaims) string {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	if kid != "" {
		t.Header["kid"] = kid
	}
	s, _ := t.SignedString([]byte(secret))
	return s
}

// === VerifyJWT 测试 ===

func TestVerifyJWT_ValidToken(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub":      "user1",
		"username": "alice",
		"role":     "admin",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})

	token, err := VerifyJWT(tokenStr, secret, jwtCfg)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if !token.Valid {
		t.Fatal("expected token.Valid to be true")
	}
}

func TestVerifyJWT_KidRotation(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	defaultSecret := getTestSecret()
	kidSecret := getTestKidSecret()

	// 用 kid-2026-06 签发的 token 应该优先用对应 secret 验证（而非默认 secret）
	tokenStr := signToken(kidSecret, "kid-2026-06", jwt.MapClaims{
		"sub": "user2",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	token, err := VerifyJWT(tokenStr, defaultSecret, jwtCfg)
	if err != nil {
		t.Fatalf("kid rotation: expected valid token, got error: %v", err)
	}
	if !token.Valid {
		t.Fatal("kid rotation: expected token.Valid to be true")
	}
}

func TestVerifyJWT_KidFallbackToDefault(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	defaultSecret := getTestSecret()

	// 无 kid 的 token 应回退到默认 secret
	tokenStr := signToken(defaultSecret, "", jwt.MapClaims{
		"sub": "user-fallback",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	token, err := VerifyJWT(tokenStr, defaultSecret, jwtCfg)
	if err != nil {
		t.Fatalf("fallback: expected valid token, got error: %v", err)
	}
	if !token.Valid {
		t.Fatal("fallback: expected token.Valid to be true")
	}
}

func TestVerifyJWT_ExpiredToken(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub": "user3",
		"exp": time.Now().Add(-time.Hour).Unix(), // 已过期
	})

	_, err := VerifyJWT(tokenStr, secret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestVerifyJWT_InvalidSignature(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	correctSecret := getTestSecret()
	wrongSecret := "wrong-secret-with-32-bytes-xxxxx!!!"
	tokenStr := signToken(wrongSecret, "", jwt.MapClaims{
		"sub": "user4",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := VerifyJWT(tokenStr, correctSecret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
}

// TestVerifyJWT_AlgNoneAttack 验证 alg=none 攻击被拒绝（关键安全测试）。
func TestVerifyJWT_AlgNoneAttack(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()

	// 构造 alg=none token（无签名），模拟攻击者伪造
	unsafeToken := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "hacker",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, _ := unsafeToken.SignedString(jwt.UnsafeAllowNoneSignatureType)

	_, err := VerifyJWT(tokenStr, secret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for alg=none attack, got nil (CRITICAL security vulnerability!)")
	}
}

func TestVerifyJWT_EmptyToken(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()

	_, err := VerifyJWT("", secret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestVerifyJWT_NilJWTConfig(t *testing.T) {
	// jwtCfg 为 nil 时应回退到默认 secret
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub": "user-nil-cfg",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	token, err := VerifyJWT(tokenStr, secret, nil)
	if err != nil {
		t.Fatalf("nil config: expected valid token, got error: %v", err)
	}
	if !token.Valid {
		t.Fatal("nil config: expected token.Valid to be true")
	}
}

// === ExtractAndVerifyJWT 测试 ===

func TestExtractAndVerifyJWT_BearerHeader(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub":      "user5",
		"username": "bob",
		"role":     "operator",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenStr)

	if err := ExtractAndVerifyJWT(c, secret, jwtCfg); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if uid, _ := c.Get("user_id"); uid != "user5" {
		t.Errorf("expected user_id=user5, got %v", uid)
	}
	if uname, _ := c.Get("username"); uname != "bob" {
		t.Errorf("expected username=bob, got %v", uname)
	}
	if role, _ := c.Get("role"); role != "operator" {
		t.Errorf("expected role=operator, got %v", role)
	}
}

func TestExtractAndVerifyJWT_QueryParam(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub": "user6",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?token="+tokenStr, nil)

	if err := ExtractAndVerifyJWT(c, secret, jwtCfg); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if uid, _ := c.Get("user_id"); uid != "user6" {
		t.Errorf("expected user_id=user6, got %v", uid)
	}
}

func TestExtractAndVerifyJWT_InvalidToken(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?token=invalid-token-string", nil)

	err := ExtractAndVerifyJWT(c, secret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestExtractAndVerifyJWT_MissingToken(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	err := ExtractAndVerifyJWT(c, secret, jwtCfg)
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestExtractAndVerifyJWT_PermissionsClaim(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()
	tokenStr := signToken(secret, "", jwt.MapClaims{
		"sub":         "user7",
		"permissions": []interface{}{"monitor", "vehicle", "alarm"},
		"exp":         time.Now().Add(time.Hour).Unix(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?token="+tokenStr, nil)

	if err := ExtractAndVerifyJWT(c, secret, jwtCfg); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	perms, exists := c.Get("permissions")
	if !exists {
		t.Fatal("expected permissions to be set in context")
	}
	permStrs, ok := perms.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", perms)
	}
	if len(permStrs) != 3 {
		t.Errorf("expected 3 permissions, got %d", len(permStrs))
	}
}

// TestExtractAndVerifyJWT_AbortsContext 验证失败时 c.IsAborted() 为 true。
func TestExtractAndVerifyJWT_AbortsContext(t *testing.T) {
	jwtCfg := newTestJWTConfig()
	secret := getTestSecret()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	_ = ExtractAndVerifyJWT(c, secret, jwtCfg)
	if !c.IsAborted() {
		t.Fatal("expected context to be aborted on auth failure")
	}
}
