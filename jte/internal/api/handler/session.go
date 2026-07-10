package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type SessionHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

func NewSessionHandler(store storage.Interface, logger *zap.Logger) *SessionHandler {
	return &SessionHandler{store: store, logger: logger}
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

	result, err := h.store.ListSessions(c.Request.Context(), opts)
	if err != nil {
		h.logger.Error("list sessions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": result.Items,
		"total":    result.Total,
		"page":     result.Page,
		"size":     result.Size,
	})
}

// Get 返回指定会话详情（/sessions/:id）
func (h *SessionHandler) Get(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "missing session id"})
		return
	}

	session, err := h.store.GetSession(c.Request.Context(), id)
	if err != nil {
		h.logger.Error("get session", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "session not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}