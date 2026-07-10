package middleware

import (
	"net/http"


	"github.com/gin-gonic/gin"
)

func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowAll := false
	originsMap := make(map[string]bool)
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
			break
		}
		originsMap[o] = true
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// AUTO-FIX-2026-06-29 [P1]: 原实现 allowAll 时回显 origin + Allow-Credentials: true，
		// 形成 "*" + Credentials 危险组合——任意网站可携带用户凭证（Cookie/JWT）发起跨域请求，
		// 构成 CSWSH/CSRF 风险。修复：allowAll 仅返回 Access-Control-Allow-Origin: *（不带
		// Allow-Credentials），浏览器会自动拒绝带凭证的跨域请求；带凭证的请求必须配置具体
		// origin 白名单（originsMap 命中时才回显 origin + Allow-Credentials: true）。
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-CSRF-Token")
			c.Header("Access-Control-Max-Age", "86400")
		} else if originsMap[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-CSRF-Token")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "86400")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}