package middleware

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ipConnCounter 按 IP 统计活跃并发连接数，用于防止单 IP 通过海量并发连接
// 耗尽服务器文件描述符（Slowloris 变种攻击）。
// INDUSTRIAL-FIX-2026-07-25 [P2]: 后台定期清理计数为零的 IP 条目，
// 防止 IoT 场景下大量设备 IP 频繁连接/断开导致 sync.Map 无限增长（内存泄漏）。
type ipConnCounter struct {
	counts  sync.Map // map[string]*atomic.Int32
	stopCh  chan struct{}
	stopOnce sync.Once
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

// connLimitCleanupInterval 清理间隔（5 分钟）
const connLimitCleanupInterval = 5 * time.Minute

// startCleanupLoop 启动后台清理协程，定期移除计数为零的 IP 条目。
// 防止 IoT 场景下大量设备 IP 累积导致 sync.Map 无限增长。
func (c *ipConnCounter) startCleanupLoop(logger *zap.Logger) {
	go func() {
		ticker := time.NewTicker(connLimitCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				// R62-FIX [P2]: 将 recover 移到循环内部，确保单次 cleanupStale panic
				// 不会导致清理协程整体退出。原实现 recover 在 goroutine 级别，
				// panic 后协程静默退出，sync.Map 中计数为零的 IP 条目永不被清理，
				// 长期运行后内存无限增长（IoT 场景下设备 IP 众多且频繁连接/断开）。
				func() {
					defer func() {
						if r := recover(); r != nil {
							if logger != nil {
								logger.Error("connlimit cleanupStale panic recovered",
									zap.Any("panic", r),
									zap.Stack("stack"))
							}
						}
					}()
					c.cleanupStale(logger)
				}()
			}
		}
	}()
}

// cleanupStale 扫描并删除计数为零的 IP 条目。
// 使用 LoadAndDelete 确保并发安全：删除时如果计数非零（恰好有新连接），
// 则保留该条目，不影响正在使用的 IP。
func (c *ipConnCounter) cleanupStale(logger *zap.Logger) {
	removed := 0
	c.counts.Range(func(key, value any) bool {
		counter := value.(*atomic.Int32)
		if counter.Load() <= 0 {
			c.counts.Delete(key)
			removed++
		}
		return true
	})
	if removed > 0 && logger != nil {
		logger.Debug("connlimit: cleaned up stale IP entries",
			zap.Int("removed", removed))
	}
}

// Stop 停止后台清理协程（幂等，供 graceful shutdown 调用）
func (c *ipConnCounter) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
}

// ConnLimit 限制单 IP 并发连接数中间件，防止 Slowloris/资源耗尽攻击。
// maxPerIP <=0 时不做限制（关闭防护）。
// 超过限制的请求返回 429 Too Many Requests。
// INDUSTRIAL-FIX-2026-07-25 [P2]: 启动后台清理协程定期回收计数为零的 IP 条目，
// 防止长期运行后 sync.Map 无限增长（IoT 场景下设备 IP 众多且频繁连接/断开）。
func ConnLimit(maxPerIP int) gin.HandlerFunc {
	if maxPerIP <= 0 {
		// 防护关闭，透传
		return func(c *gin.Context) { c.Next() }
	}
	counter := &ipConnCounter{
		stopCh: make(chan struct{}),
	}
	counter.startCleanupLoop(nil)

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
