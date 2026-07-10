package middleware

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// ipConnCounter 按 IP 统计活跃并发连接数，用于防止单 IP 通过海量并发连接
// 耗尽服务器文件描述符（Slowloris 变种攻击）。
type ipConnCounter struct {
	counts sync.Map // map[string]*atomic.Int32
}

// acquire 增加指定 IP 的活跃连接计数，返回是否允许（未超限）。
func (c *ipConnCounter) acquire(ip string, maxPerIP int) bool {
	v, _ := c.counts.LoadOrStore(ip, &atomic.Int32{})
	counter := v.(*atomic.Int32)
	newCount := counter.Add(1)
	if newCount > int32(maxPerIP) {
		// 超限，回退计数
		counter.Add(-1)
		return false
	}
	return true
}

// release 释放指定 IP 的活跃连接计数。
func (c *ipConnCounter) release(ip string) {
	if v, ok := c.counts.Load(ip); ok {
		counter := v.(*atomic.Int32)
		// 仅在计数 > 0 时递减，防止异常重复 release 导致负数
		for {
			old := counter.Load()
			if old <= 0 {
				return
			}
			if counter.CompareAndSwap(old, old-1) {
				return
			}
		}
	}
}

// clientIP 提取客户端 IP，兼容反向代理（X-Forwarded-For / X-Real-IP）。
// 取 X-Forwarded-For 第一个值（最接近客户端的代理）；无代理头时用 RemoteAddr。
func clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For: client, proxy1, proxy2
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// gin 的 ClientIP 已处理 RemoteAddr 的 host:port
	return c.ClientIP()
}

// ConnLimit 限制单 IP 并发连接数中间件，防止 Slowloris/资源耗尽攻击。
// maxPerIP <=0 时不做限制（关闭防护）。
// 超过限制的请求返回 429 Too Many Requests。
func ConnLimit(maxPerIP int) gin.HandlerFunc {
	if maxPerIP <= 0 {
		// 防护关闭，透传
		return func(c *gin.Context) { c.Next() }
	}
	counter := &ipConnCounter{}

	return func(c *gin.Context) {
		ip := clientIP(c)
		if !counter.acquire(ip, maxPerIP) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "too many concurrent connections from this IP",
			})
			c.Abort()
			return
		}
		// 使用 defer 确保请求结束（含 panic）后释放计数
		defer counter.release(ip)
		c.Next()
	}
}
