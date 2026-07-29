package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	CSRFTokenHeader  = "X-CSRF-Token"
	CSRFTokenCookie  = "csrf_token"
	CSRFTokenLength  = 32
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
// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: Secure 参数根据 TLS 动态设置，
// HTTPS 下 Secure=true 防止 Cookie 通过 HTTP 明文传输。
// 考虑反向代理场景：TLS 在代理层终止时 c.Request.TLS 为 nil，需检查 X-Forwarded-Proto。
func SetCSRFToken(c *gin.Context) (string, error) {
	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}
	secure := c.Request.TLS != nil || isHTTPSProxy(c)
	c.SetSameSite(http.SameSiteStrictMode)
	// FIXED: [CSRF] HttpOnly 必须为 false，否则前端 JavaScript 无法读取 cookie 值
	// 来设置 X-CSRF-Token 请求头，导致所有写操作（POST/PUT/DELETE）被 CSRF 中间件拒绝（403）。
	// 双提交 Cookie 模式要求前端能读取 cookie 值，因此 HttpOnly=false 是正确的设计。
	c.SetCookie(CSRFTokenCookie, token, CSRFCookieMaxAge, "/", "", secure, false)
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
