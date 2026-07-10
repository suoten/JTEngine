package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/suoten/jt-engine/internal/config"
)

// makeTestToken 生成 HMAC HS256 JWT token（带可选 kid header）。
func makeTestToken(t *testing.T, secret string, claims jwt.MapClaims, kid string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	str, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return str
}

func TestAuth_PublicPathsPass(t *testing.T) {
	paths := []string{
		"/api/v1/health",
		"/api/v1/auth/login",
		"/api/v1/auth/status",
		"/api/v1/auth/trial",
		"/api/v1/auth/refresh",
		"/swagger/doc.json",
		"/ws/notifications",
		"/assets/app.js",
	}
	mw := Auth("test-secret-at-least-32-bytes-long!!!", nil)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			called := false
			router := gin.New()
			router.Use(mw)
			router.GET(path, func(c *gin.Context) { called = true; c.Status(200) })

			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("public path %s status = %d, want 200", path, w.Code)
			}
			if !called {
				t.Errorf("handler not called for %s", path)
			}
		})
	}
}

func TestAuth_MissingToken(t *testing.T) {
	mw := Auth("test-secret-at-least-32-bytes-long!!!", nil)
	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/api/v1/secure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_TokenFromQuery(t *testing.T) {
	secret := "test-secret-at-least-32-bytes-long!!!"
	token := makeTestToken(t, secret, jwt.MapClaims{
		"sub":  "user1",
		"role": "admin",
	}, "")
	mw := Auth(secret, nil)

	var capturedRole string
	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) {
		if r, ok := c.Get("role"); ok {
			capturedRole = r.(string)
		}
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/api/v1/secure?token="+token, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if capturedRole != "admin" {
		t.Errorf("role = %q, want admin", capturedRole)
	}
}

func TestAuth_ValidTokenSetsContext(t *testing.T) {
	secret := "test-secret-at-least-32-bytes-long!!!"
	token := makeTestToken(t, secret, jwt.MapClaims{
		"sub":         "u123",
		"username":    "alice",
		"role":        "operator",
		"permissions": []interface{}{"vehicle:view", "alarm:list"},
	}, "")
	mw := Auth(secret, nil)

	var captured struct {
		userID, username, role string
		perms                  []string
	}
	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) {
		if v, ok := c.Get("user_id"); ok {
			captured.userID = v.(string)
		}
		if v, ok := c.Get("username"); ok {
			captured.username = v.(string)
		}
		if v, ok := c.Get("role"); ok {
			captured.role = v.(string)
		}
		if v, ok := c.Get("permissions"); ok {
			captured.perms = v.([]string)
		}
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/api/v1/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if captured.userID != "u123" {
		t.Errorf("user_id = %q, want u123", captured.userID)
	}
	if captured.username != "alice" {
		t.Errorf("username = %q, want alice", captured.username)
	}
	if captured.role != "operator" {
		t.Errorf("role = %q, want operator", captured.role)
	}
	if len(captured.perms) != 2 {
		t.Errorf("perms len = %d, want 2", len(captured.perms))
	}
}

func TestAuth_KidLookup(t *testing.T) {
	jwtCfg := &config.JWTConfig{
		Secrets:   map[string]string{"k1": "kid-secret-at-least-32-bytes-long!!"},
		ActiveKid: "k1",
	}
	token := makeTestToken(t, "kid-secret-at-least-32-bytes-long!!", jwt.MapClaims{
		"sub": "u456",
	}, "k1")
	mw := Auth("default-secret-at-least-32-bytes-long!!!", jwtCfg)

	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/api/v1/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (kid lookup); body=%s", w.Code, w.Body.String())
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	mw := Auth("test-secret-at-least-32-bytes-long!!!", nil)
	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/api/v1/secure", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_DataScopeInjection(t *testing.T) {
	secret := "test-secret-at-least-32-bytes-long!!!"
	token := makeTestToken(t, secret, jwt.MapClaims{
		"sub": "u789",
		"data_scope": map[string]interface{}{
			"scope_type":  "org",
			"org_id":      "org-001",
			"vehicle_ids": []interface{}{"v1", "v2"},
		},
	}, "")
	mw := Auth(secret, nil)

	var scope DataScopeInfo
	router := gin.New()
	router.Use(mw)
	router.GET("/api/v1/secure", func(c *gin.Context) {
		scope = GetDataScope(c)
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/api/v1/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if scope.ScopeType != "org" {
		t.Errorf("ScopeType = %q, want org", scope.ScopeType)
	}
	if scope.OrgID != "org-001" {
		t.Errorf("OrgID = %q, want org-001", scope.OrgID)
	}
	if len(scope.VehicleIDs) != 2 {
		t.Errorf("VehicleIDs len = %d, want 2", len(scope.VehicleIDs))
	}
}

func TestGetDataScope_DefaultAll(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	scope := GetDataScope(c)
	if scope.ScopeType != "all" {
		t.Errorf("default ScopeType = %q, want all", scope.ScopeType)
	}
}

func TestApplyDataScopeToParams_All(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	params := ApplyDataScopeToParams(c, map[string]string{"q": "test"})
	if _, ok := params["org_id"]; ok {
		t.Error("scope=all should not add org_id")
	}
	if params["q"] != "test" {
		t.Errorf("q = %q, want test", params["q"])
	}
}

func TestApplyDataScopeToParams_Org(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("data_scope", DataScopeInfo{ScopeType: "org", OrgID: "org-42"})
	params := ApplyDataScopeToParams(c, nil)
	if params["org_id"] != "org-42" {
		t.Errorf("org_id = %q, want org-42", params["org_id"])
	}
}

func TestApplyDataScopeToParams_Vehicle(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("data_scope", DataScopeInfo{ScopeType: "vehicle", VehicleIDs: []string{"v1", "v2"}})
	params := ApplyDataScopeToParams(c, nil)
	if params["vehicle_ids"] != "v1,v2" {
		t.Errorf("vehicle_ids = %q, want v1,v2", params["vehicle_ids"])
	}
}

func TestRequirePermission_SuperAdminBypass(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("role", "super_admin"); c.Next() }, RequirePermission("any:perm"))
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (super_admin bypass)", w.Code)
	}
}

func TestRequirePermission_HasPermission(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("role", "operator")
		c.Set("permissions", []string{"vehicle:view", "alarm:list"})
		c.Next()
	}, RequirePermission("alarm:list"))
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequirePermission_MissingPermission(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("role", "operator")
		c.Set("permissions", []string{"vehicle:view"})
		c.Next()
	}, RequirePermission("alarm:delete"))
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestRequirePermission_NoPermissionsSet(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("role", "operator"); c.Next() }, RequirePermission("any:perm"))
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (no permissions set)", w.Code)
	}
}
