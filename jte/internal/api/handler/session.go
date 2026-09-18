package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type SessionHandler struct {
	store    storage.Interface
	sessions *gateway.SessionManager
	logger   *zap.Logger
}

func NewSessionHandler(store storage.Interface, sessions *gateway.SessionManager, logger *zap.Logger) *SessionHandler {
	return &SessionHandler{store: store, sessions: sessions, logger: logger}
}

// List godoc
// @Summary è·åä¼è¯åè¡¨
// @Description åé¡µæ¥è¯¢ç»ç«¯è¿æ¥ä¼è¯ä¿¡æ¯
// @Tags ä¼è¯
// @Accept json
// @Produce json
// @Param page query int false "é¡µç " default(1)
// @Param page_size query int false "æ¯é¡µæ°é" default(20)
// @Param phone query string false "ææºå·ç­é?
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/sessions [get]
func (h *SessionHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	opts := storage.ListOptions{
		Page:     page,
		PageSize: pageSize,
		Phone:    c.Query("phone"),
	}

	// FIXED-2026-09-18 [P1]: 网关 SessionManager 从未调用 SaveSession 落库，
	// 导致 /sessions 永远返回空列表（会话管理页/总览最近会话 No Data）。
	// 优先读网关内存中的活跃会话，为空时再兑底查存储。
	var items []*storage.SessionData
	total := int64(0)
	if h.sessions != nil {
		for _, s := range h.sessions.List() {
			phone := s.GetPhone()
			if opts.Phone != "" && (phone == "" || phone != opts.Phone) {
				continue
			}
			items = append(items, &storage.SessionData{
				ID:           s.ID,
				Phone:        phone,
				Protocol:     string(s.GetProtocol()),
				RemoteAddr:   s.RemoteAddr,
				Status:       s.GetStatus(),
				LastActive:   s.GetLastActive(),
			})
		}
		total = int64(len(items))
	}
	if len(items) == 0 {
		result, err := h.store.ListSessions(c.Request.Context(), opts)
		if err != nil {
			h.logger.Error("list sessions", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
			return
		}
		items, _ = result.Items.([]*storage.SessionData)
		total = result.Total
		page, pageSize = result.Page, result.Size
		c.JSON(http.StatusOK, gin.H{
			"sessions": items,
			"total":    total,
			"page":     page,
			"size":     pageSize,
		})
		return
	}

	// 内存会话分页（List 顺序不定，按最后活跃倒序后分页）
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].LastActive.After(items[i].LastActive) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	c.JSON(http.StatusOK, gin.H{
		"sessions": items[start:end],
		"total":    total,
		"page":     page,
		"size":     pageSize,
	})
}

// Get 返回指定会话详情（/sessions/:id）
func (h *SessionHandler) Get(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "missing session id"})
		return
	}

	// FIXED-2026-09-18: 优先读网关活跃会话，未命中再查存储
	if h.sessions != nil {
		if s, ok := h.sessions.Get(id); ok {
			c.JSON(http.StatusOK, gin.H{"session": gin.H{
				"id":           s.ID,
				"phone":        s.GetPhone(),
				"protocol":     string(s.Protocol),
				"remote_addr":  s.RemoteAddr,
				"status":       s.GetStatus(),
				"last_active":  s.GetLastActive(),
			}})
			return
		}
	}

	session, err := h.store.GetSession(c.Request.Context(), id)
	if err != nil {
		h.logger.Error("get session", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "session not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}