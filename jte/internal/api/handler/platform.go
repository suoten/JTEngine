package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/gateway"
	"go.uber.org/zap"
)

type PlatformHandler struct {
	sessions *gateway.SessionManager
	logger   *zap.Logger
}

func NewPlatformHandler(sessions *gateway.SessionManager, logger *zap.Logger) *PlatformHandler {
	return &PlatformHandler{sessions: sessions, logger: logger}
}

// List godoc
// @Summary è·å809å¹³å°åè¡¨
// @Description è·åææå·²è¿æ¥çJT809ä¸çº§å¹³å°ä¿¡æ¯
// @Tags å¹³å°
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/platforms [get]
func (h *PlatformHandler) List(c *gin.Context) {
	sessions := h.sessions.List()
	type platformInfo struct {
		ID         string `json:"id"`
		Phone      string `json:"phone"`
		Protocol   string `json:"protocol"`
		RemoteAddr string `json:"remote_addr"`
		Status     string `json:"status"`
	}

	var platforms []platformInfo
	for _, s := range sessions {
		if s.GetProtocol() == "jt809" {
			platforms = append(platforms, platformInfo{
				ID:         s.ID,
				Phone:      s.GetPhone(),
				Protocol:   string(s.GetProtocol()),
				RemoteAddr: s.RemoteAddr,
				Status:     s.GetStatus(),
			})
		}
	}

	// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 添加分页支持
	// 原先直接返回全部平台会话，10 倍流量场景下 1 万设备在线时
	// 10 个并发请求遍历 10 万次生成新切片，CPU 飙升
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}
	total := len(platforms)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      0,
		"message":   "ok",
		"data":      platforms[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// Status godoc
// @Summary è·åå¹³å°è¿æ¥ç¶æ?
// @Description æ ¹æ®å¹³å°IDè·å809ä¸çº§å¹³å°è¿æ¥ç¶æ?
// @Tags å¹³å°
// @Produce json
// @Param id path string true "å¹³å°ä¼è¯ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/platforms/{id}/status [get]
func (h *PlatformHandler) Status(c *gin.Context) {
	id := c.Param("id")
	session, ok := h.sessions.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "platform not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"id":          session.ID,
			"phone":       session.GetPhone(),
			"protocol":    string(session.GetProtocol()),
			"remote_addr": session.RemoteAddr,
			"status":      session.GetStatus(),
			"last_active": session.GetLastActive(),
		},
	})
}