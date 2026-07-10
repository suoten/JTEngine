package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type tokenBucket struct {
	tokens    float64
	maxTokens float64
	rate      float64
	lastTime  time.Time
	mu        sync.Mutex
}

func newTokenBucket(rate float64) *tokenBucket {
	return &tokenBucket{
		tokens:    rate,
		maxTokens: rate,
		rate:      rate,
		lastTime:  time.Now(),
	}
}

func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastTime = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

func RateLimit(rateLimit int) gin.HandlerFunc {
	bucket := newTokenBucket(float64(rateLimit))

	return func(c *gin.Context) {
		if !bucket.allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "too many requests",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// AIRateLimit 创建 AI 入站 API 专属限流中间件。
// AI 推理请求（含 LLM 调用）耗时长、成本高，需比全局限流更严格的速率保护，
// 防止 AI 接口被刷爆拖垮 DeepSeek/Ollama 等外部服务配额。
// rate 为每秒允许的请求数，<=0 时默认 10。
func AIRateLimit(rate int) gin.HandlerFunc {
	if rate <= 0 {
		rate = 10
	}
	bucket := newTokenBucket(float64(rate))

	return func(c *gin.Context) {
		if !bucket.allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "AI API rate limit exceeded, please slow down",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}