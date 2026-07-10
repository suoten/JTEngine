package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type VehicleHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

func NewVehicleHandler(store storage.Interface, logger *zap.Logger) *VehicleHandler {
	return &VehicleHandler{store: store, logger: logger}
}

// Create 注册新车辆
// POST /api/v1/vehicles
func (h *VehicleHandler) Create(c *gin.Context) {
	var v storage.Vehicle
	if err := c.ShouldBindJSON(&v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if v.Phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required"})
		return
	}
	if v.Protocol == "" {
		v.Protocol = "jt808"
	}
	now := time.Now()
	if v.RegisteredAt.IsZero() {
		v.RegisteredAt = now
	}
	v.LastActive = now
	if v.ID == "" {
		v.ID = generateDeviceID()
	}
	if err := h.store.SaveVehicle(context.Background(), &v); err != nil {
		h.logger.Error("create vehicle failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.logger.Info("vehicle created", zap.String("id", v.ID), zap.String("phone", v.Phone))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": v})
}

// List godoc
// @Summary è·åè½¦è¾åè¡¨
// @Description åé¡µæ¥è¯¢å·²æ³¨åè½¦è¾ä¿¡æ¯ï¼æ¯ææææºå·ãå¨çº¿ç¶æç­é?
// @Tags è½¦è¾
// @Accept json
// @Produce json
// @Param page query int false "é¡µç " default(1)
// @Param page_size query int false "æ¯é¡µæ°é" default(20)
// @Param phone query string false "ææºå·ç­é?
// @Param online query bool false "å¨çº¿ç¶æç­é?
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/vehicles [get]
func (h *VehicleHandler) List(c *gin.Context) {
	opts := storage.ListOptions{
		Page:     parseIntDefault(c.Query("page"), 1),
		PageSize: parseIntDefault(c.Query("page_size"), 20),
		Phone:    c.Query("phone"),
	}

	if onlineStr := c.Query("online"); onlineStr != "" {
		online := onlineStr == "true"
		opts.Online = &online
	}

	result, err := h.store.ListVehicles(context.Background(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	// AUTO-FIX-2026-07-02 [等保2.0]: 响应前脱敏手机号/车牌号
	var masked interface{} = result.Items
	if items, ok := result.Items.([]*storage.Vehicle); ok {
		masked = MaskVehicles(items)
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicles": masked,
		"total":    result.Total,
		"page":     result.Page,
		"size":     result.Size,
	})
}

// Get godoc
// @Summary è·åè½¦è¾è¯¦æ
// @Description æ ¹æ®è½¦è¾IDè·åè½¦è¾è¯¦ç»ä¿¡æ¯
// @Tags è½¦è¾
// @Produce json
// @Param id path string true "è½¦è¾ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/vehicles/{id} [get]
func (h *VehicleHandler) Get(c *gin.Context) {
	id := c.Param("id")
	vehicle, err := h.store.GetVehicle(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "vehicle not found"})
		return
	}

	// AUTO-FIX-2026-07-02 [等保2.0]: 响应前脱敏手机号/车牌号
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data":    MaskVehicle(vehicle),
	})
}

// GetLocation godoc
// @Summary è·åè½¦è¾ææ°ä½ç½?
// @Description æ ¹æ®è½¦è¾IDè·åææ°ä½ç½®æ°æ?
// @Tags è½¦è¾
// @Produce json
// @Param id path string true "è½¦è¾ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/vehicles/{id}/location [get]
func (h *VehicleHandler) GetLocation(c *gin.Context) {
	id := c.Param("id")
	loc, err := h.store.GetLatestLocation(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "location not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data":    loc,
	})
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}