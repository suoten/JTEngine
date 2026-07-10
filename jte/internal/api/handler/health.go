package handler

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/gateway"
	"github.com/jte-engine/jte/internal/maintenance"
)

// HealthCheck godoc
// @Summary 健康检查
// @Description 检查JTE引擎运行状态
// @Tags 系统
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/health [get]
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"status":  "running",
			"version": "1.0.0",
		},
	})
}

// HealthHandler 蓝绿部署健康检查端点（v3.0 A.6.1）
//   - /healthz：负载均衡健康检查（进程存活即 200，不需要鉴权）
//   - /readyz：蓝绿部署流量切换判断（维护模式期间 503，正常 200，不需要鉴权）
type HealthHandler struct {
	sessions        *gateway.SessionManager
	maintenanceMode *maintenance.Mode
	startTime       time.Time
}

// NewHealthHandler 创建健康检查处理器
func NewHealthHandler(sessions *gateway.SessionManager, mm *maintenance.Mode, startTime time.Time) *HealthHandler {
	return &HealthHandler{
		sessions:        sessions,
		maintenanceMode: mm,
		startTime:       startTime,
	}
}

// Healthz 负载均衡健康检查端点
// 返回 200 + JSON：{"status":"ok","uptime":12345,"connections":1234,"memory_mb":5678}
// 只要进程存活就返回 200，用于负载均衡健康检查
func (h *HealthHandler) Healthz(c *gin.Context) {
	uptime := int64(time.Since(h.startTime).Seconds())
	connections := 0
	if h.sessions != nil {
		connections = h.sessions.OnlineCount()
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	memoryMB := ms.Sys / (1024 * 1024)

	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"uptime":      uptime,
		"connections": connections,
		"memory_mb":   memoryMB,
	})
}

// Readyz 蓝绿部署就绪检查端点
// 维护模式期间返回 503（不 ready），正常时返回 200
// 用于蓝绿部署流量切换判断
func (h *HealthHandler) Readyz(c *gin.Context) {
	if h.maintenanceMode != nil && h.maintenanceMode.IsActive() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "not_ready",
			"reason":  "maintenance mode active",
			"message": "service is in maintenance mode",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
