package trace

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: HTTP 中间件 - trace_id 注入
// ===================================================================

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

// HeaderTraceID HTTP 头部名称（用于跨服务传播）
const HeaderTraceID = "X-Trace-Id"

// traceIDRegex 校验 trace_id 格式（32 字符十六进制）
var traceIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// GinMiddleware gin 中间件：为每个请求注入 trace_id
// 1. 优先从 X-Trace-Id 头部获取（跨服务调用）
// 2. 否则生成新的 trace_id
// 3. 存入 context 和响应头部
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" || !traceIDRegex.MatchString(traceID) {
			traceID = NewTraceID()
		}

		// 存入 context
		ctx := WithTraceID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)

		// 存入 gin context（便于 handler 直接获取）
		c.Set("trace_id", traceID)

		// 响应头部回传
		c.Header(HeaderTraceID, traceID)

		c.Next()
	}
}

// GetTraceIDFromGin 从 gin context 获取 trace_id
func GetTraceIDFromGin(c *gin.Context) string {
	if v, ok := c.Get("trace_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return GetTraceID(c.Request.Context())
}

// NetHTTPMiddleware 标准 http.Handler 中间件
func NetHTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(HeaderTraceID)
		if traceID == "" || !traceIDRegex.MatchString(traceID) {
			traceID = NewTraceID()
		}

		ctx := WithTraceID(r.Context(), traceID)
		r = r.WithContext(ctx)

		w.Header().Set(HeaderTraceID, traceID)

		next.ServeHTTP(w, r)
	})
}
