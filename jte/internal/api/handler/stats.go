package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type StatsHandler struct {
	store    storage.Interface
	sessions interface{ OnlineCount() int }
	logger   *zap.Logger
}

func NewStatsHandler(store storage.Interface, sessions interface{ OnlineCount() int }, logger *zap.Logger) *StatsHandler {
	return &StatsHandler{store: store, sessions: sessions, logger: logger}
}

// Stats godoc
// @Summary ç³»ç»ç»è®¡
// @Description è·åå¨çº¿è®¾å¤æ°ãæ¥è­¦æ°ãä¼è¯æ°ç­ç»è®¡ä¿¡æ?
// @Tags ç»è®¡
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/stats [get]
func (h *StatsHandler) Stats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	onlineCount, _ := h.store.GetOnlineCount(ctx)
	offlineCount, _ := h.store.GetOfflineCount(ctx)
	now := time.Now()
	alarmCount, _ := h.store.GetAlarmCount(ctx, now.Add(-24*time.Hour), now)
	sessionCount := 0
	if h.sessions != nil {
		sessionCount = h.sessions.OnlineCount()
	}

	// 字段名与前端 Overview.vue 契约对齐：online_count/total_sessions/alarm_count
	c.JSON(http.StatusOK, gin.H{
		"online_count":    onlineCount,
		"offline_count":   offlineCount,
		"total_devices":   onlineCount + offlineCount,
		"total_sessions":  sessionCount,
		"alarm_count":     alarmCount,
		"protocol_count":  7, // 808/809/1078/1045/905/1253/32960
	})
}

// Overview 返回仪表盘概览统计（/stats/overview）
func (h *StatsHandler) Overview(c *gin.Context) {
	h.Stats(c) // 复用 Stats 逻辑
}

// Online 返回在线设备数（/stats/online）
func (h *StatsHandler) Online(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	onlineCount, _ := h.store.GetOnlineCount(ctx)
	sessionCount := 0
	if h.sessions != nil {
		sessionCount = h.sessions.OnlineCount()
	}

	c.JSON(http.StatusOK, gin.H{
		"online_count":   onlineCount,
		"session_count":  sessionCount,
	})
}

// AlarmCount 返回报警统计（/stats/alarms）
func (h *StatsHandler) AlarmCount(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	now := time.Now()
	alarmCount24h, _ := h.store.GetAlarmCount(ctx, now.Add(-24*time.Hour), now)
	alarmCount7d, _ := h.store.GetAlarmCount(ctx, now.Add(-7*24*time.Hour), now)

	c.JSON(http.StatusOK, gin.H{
		"alarm_count_24h": alarmCount24h,
		"alarm_count_7d":  alarmCount7d,
	})
}