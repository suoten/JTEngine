package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerateCSRFToken_Length(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("GenerateCSRFToken: %v", err)
	}
	// 32 字节 → 64 hex 字符
	if len(token) != CSRFTokenLength*2 {
		t.Errorf("token length = %d, want %d", len(token), CSRFTokenLength*2)
	}
}

func TestGenerateCSRFToken_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		token, err := GenerateCSRFToken()
		if err != nil {
			t.Fatalf("GenerateCSRFToken: %v", err)
		}
		if seen[token] {
			t.Fatalf("duplicate token generated at iteration %d", i)
		}
		seen[token] = true
	}
}

func TestSetCSRFToken_SetsCookie(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	token, err := SetCSRFToken(c)
	if err != nil {
		t.Fatalf("SetCSRFToken: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}
	// 验证 cookie 已设置
	cookies := w.Result().Cookies()
	var found bool
	for _, ck := range cookies {
		if ck.Name == CSRFTokenCookie {
			found = true
			if ck.Value != token {
				t.Errorf("cookie value = %q, want %q", ck.Value, token)
			}
			if ck.HttpOnly != true {
				t.Error("cookie should be HttpOnly")
			}
			if ck.MaxAge != CSRFCookieMaxAge {
				t.Errorf("cookie MaxAge = %d, want %d", ck.MaxAge, CSRFCookieMaxAge)
			}
		}
	}
	if !found {
		t.Fatal("csrf_token cookie not set")
	}
	// 验证 context 中也存有 token
	ctxToken, exists := c.Get("csrf_token")
	if !exists {
		t.Fatal("csrf_token not set in context")
	}
	if ctxToken != token {
		t.Errorf("context token = %v, want %q", ctxToken, token)
	}
}

func TestCSRFMiddleware_SafeMethodPasses(t *testing.T) {
	tests := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range tests {
		t.Run(method, func(t *testing.T) {
			router := gin.New()
			router.Use(CSRFMiddleware())
			router.Handle(method, "/t", func(c *gin.Context) { c.Status(200) })

			req := httptest.NewRequest(method, "/t", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("method %s status = %d, want 200", method, w.Code)
			}
		})
	}
}

func TestCSRFMiddleware_LoginPathPasses(t *testing.T) {
	paths := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/trial",
		"/api/v1/auth/activate",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			router := gin.New()
			router.Use(CSRFMiddleware())
			router.POST(path, func(c *gin.Context) { c.Status(200) })

			req := httptest.NewRequest("POST", path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("path %s status = %d, want 200", path, w.Code)
			}
		})
	}
}

func TestCSRFMiddleware_MissingTokenRejected(t *testing.T) {
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/api/v1/data", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("POST", "/api/v1/data", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestCSRFMiddleware_MismatchedTokenRejected(t *testing.T) {
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/api/v1/data", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("POST", "/api/v1/data", nil)
	req.AddCookie(&http.Cookie{Name: CSRFTokenCookie, Value: "cookie-token"})
	req.Header.Set(CSRFTokenHeader, "header-token-different")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestCSRFMiddleware_MatchingTokenPasses(t *testing.T) {
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/api/v1/data", func(c *gin.Context) { c.Status(200) })

	token := "matching-token-value"
	req := httptest.NewRequest("POST", "/api/v1/data", nil)
	req.AddCookie(&http.Cookie{Name: CSRFTokenCookie, Value: token})
	req.Header.Set(CSRFTokenHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCSRFMiddleware_MissingCookieRejected(t *testing.T) {
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/api/v1/data", func(c *gin.Context) { c.Status(200) })

	// 有 header 但无 cookie
	req := httptest.NewRequest("POST", "/api/v1/data", nil)
	req.Header.Set(CSRFTokenHeader, "some-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing cookie)", w.Code)
	}
}
