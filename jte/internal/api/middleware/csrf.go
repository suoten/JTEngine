package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	CSRFTokenHeader = "X-CSRF-Token"
	CSRFTokenCookie = "csrf_token"
	CSRFTokenLength = 32
	CSRFCookieMaxAge = 86400 // 1 天
)

// GenerateCSRFToken 生成随机 CSRF token
func GenerateCSRFToken() (string, error) {
	b := make([]byte, CSRFTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SetCSRFToken 下发 CSRF token：HttpOnly + SameSite=Strict Cookie
// 同时通过 gin.Context 传递，供 handler 写入响应体
func SetCSRFToken(c *gin.Context) (string, error) {
	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(CSRFTokenCookie, token, CSRFCookieMaxAge, "/", "", false, true)
	c.Set("csrf_token", token)
	return token, nil
}

// CSRFMiddleware CSRF 防护中间件
// 对 POST/PUT/DELETE 校验 X-CSRF-Token 请求头与 cookie 一致
// GET/OPTIONS/HEAD 放行；登录/刷新接口放行（首次请求无 token）
func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method

		// 安全方法放行
		if method == http.MethodGet || method == http.MethodOptions || method == http.MethodHead {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 登录/刷新接口放行（首次请求尚无 CSRF token）
		if strings.HasPrefix(path, "/api/v1/auth/login") ||
			strings.HasPrefix(path, "/api/v1/auth/refresh") ||
			strings.HasPrefix(path, "/api/v1/auth/trial") ||
			strings.HasPrefix(path, "/api/v1/auth/activate") {
			c.Next()
			return
		}

		// 校验请求头 token 与 cookie token 一致
		headerToken := c.GetHeader(CSRFTokenHeader)
		cookieToken, err := c.Cookie(CSRFTokenCookie)

		if err != nil || headerToken == "" || headerToken != cookieToken {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "csrf token invalid",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
