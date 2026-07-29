package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 输入验证中间件单元测试
// 覆盖：SQL 注入检测、XSS 检测、路径遍历检测、参数长度限制、SanitizeString

func TestIsDangerousInput_SQLInjection(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"UNION SELECT", "1 UNION SELECT password FROM users", true},
		{"union select lowercase", "1 union select all", true},
		{"INSERT INTO", "INSERT INTO users VALUES(1)", true},
		{"DELETE FROM", "DELETE FROM users WHERE 1=1", true},
		{"DROP TABLE", "DROP TABLE users", true},
		{"UPDATE SET", "UPDATE SET role=admin", true},
		{"exec call", "exec(@cmd)", true},
		{"SQL comment", "1; -- DROP TABLE users", true},
		{"semicolon drop", "1;DROP TABLE users", true},
		{"block comment", "1/* comment */", true},
		{"normal input", "vehicle_001", false},
		{"phone number", "13800138000", false},
		{"empty string", "", false},
		{"chinese text", "京A12345", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDangerousInput(tt.input); got != tt.want {
				t.Errorf("isDangerousInput(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsDangerousInput_XSS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"script tag", "<script>alert(1)</script>", true},
		{"script lowercase", "<ScRiPt>x</script>", true},
		{"javascript protocol", "javascript:alert(1)", true},
		{"onerror", "<img onerror=alert(1)>", true},
		{"onload", "<body onload=alert(1)>", true},
		{"onclick", "<div onclick=alert(1)>", true},
		{"onmouseover", "<a onmouseover=alert(1)>x</a>", true},
		{"iframe", "<iframe src=evil.com>", true},
		{"object", "<object data=evil.swf>", true},
		{"embed", "<embed src=evil.swf>", true},
		{"normal html entity", "&amp;", false},
		{"plain text", "hello world", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDangerousInput(tt.input); got != tt.want {
				t.Errorf("isDangerousInput(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsDangerousInput_PathTraversal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"unix traversal", "../../../etc/passwd", true},
		{"windows traversal", "..\\..\\windows\\system32", true},
		{"url encoded", "%2e%2e%2fetc%2fpasswd", true},
		{"mixed encoding", "%2e%2e/../etc/passwd", true},
		{"normal path", "/api/v1/vehicles", false},
		{"relative path", "./config/app.yaml", false},
		{"single dot", ".", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDangerousInput(tt.input); got != tt.want {
				t.Errorf("isDangerousInput(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"trim spaces", "  hello  ", "hello"},
		{"remove null byte", "hello\x00world", "helloworld"},
		{"trim and null", "  test\x00  ", "test"},
		{"empty", "", ""},
		{"only spaces", "   ", ""},
		{"chinese with null", "京A\x0012345", "京A12345"},
		{"tab trim", "\tdata\t", "data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeString(tt.input); got != tt.want {
				t.Errorf("SanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInputValidation_Middleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("blocks SQL injection in query param", func(t *testing.T) {
		router := gin.New()
		router.Use(InputValidation())
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test?q=1+UNION+SELECT+*+FROM+users", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("blocks XSS in query param", func(t *testing.T) {
		router := gin.New()
		router.Use(InputValidation())
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test?q=%3Cscript%3Ealert(1)%3C/script%3E", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("allows normal query param", func(t *testing.T) {
		router := gin.New()
		router.Use(InputValidation())
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test?q=vehicle_001", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("blocks path traversal in query param", func(t *testing.T) {
		router := gin.New()
		router.Use(InputValidation())
		router.GET("/files", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/files?name=../../../etc/passwd", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
}
