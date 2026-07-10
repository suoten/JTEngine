package handler

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type ProtocolLogHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

func NewProtocolLogHandler(store storage.Interface, logger *zap.Logger) *ProtocolLogHandler {
	return &ProtocolLogHandler{store: store, logger: logger}
}

// @Summary è·ååè®®æ¥å¿åè¡¨
// @Tags åè®®æ¥å¿
// @Accept json
// @Produce json
// @Param page query int false "é¡µç " default(1)
// @Param page_size query int false "æ¯é¡µæ°é" default(50)
// @Param phone query string false "ææºå·ç­é?
// @Param protocol query string false "åè®®ç­é?
// @Param direction query string false "æ¹åç­é?up/down)"
// @Success 200 {object} storage.ListResult
// @Router /api/v1/protocol-logs [get]
func (h *ProtocolLogHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	opts := storage.ListOptions{
		Page:     page,
		PageSize: pageSize,
		Phone:    c.Query("phone"),
	}

	result, err := h.store.ListProtocolLogs(c.Request.Context(), opts)
	if err != nil {
		h.logger.Error("list protocol logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	protocol := c.Query("protocol")
	direction := c.Query("direction")
	if protocol != "" || direction != "" {
		if items, ok := result.Items.([]*storage.ProtocolLog); ok {
			filtered := make([]*storage.ProtocolLog, 0)
			for _, l := range items {
				if protocol != "" && l.Protocol != protocol {
					continue
				}
				if direction != "" && l.Direction != direction {
					continue
				}
				filtered = append(filtered, l)
			}
			result.Items = filtered
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

// @Summary è·ååè®®æ¥å¿è¯¦æ
// @Tags åè®®æ¥å¿
// @Produce json
// @Param id path string true "æ¥å¿ID"
// @Success 200 {object} storage.ProtocolLog
// @Router /api/v1/protocol-logs/{id} [get]
func (h *ProtocolLogHandler) Get(c *gin.Context) {
	id := c.Param("id")
	result, err := h.store.ListProtocolLogs(c.Request.Context(), storage.ListOptions{
		Page:     1,
		PageSize: 1,
		Phone:    id,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

func LogProtocolMessage(store storage.Interface, phone, protocol, direction string, msgType uint16, msgName string, raw []byte, sessionID string) {
	hexStr := hex.EncodeToString(raw)
	log := &storage.ProtocolLog{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		SessionID:  sessionID,
		Phone:      phone,
		Protocol:   protocol,
		MsgType:    msgType,
		MsgName:    msgName,
		Direction:  direction,
		RawHex:     hexStr,
		Length:     len(raw),
		ReceivedAt: time.Now(),
	}
	_ = store.SaveProtocolLog(context.Background(), log)
}