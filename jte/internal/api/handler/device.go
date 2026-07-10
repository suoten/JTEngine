package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// CreateDevice 注册新设备（终端）
// POST /api/v1/devices
func (h *DeviceHandler) CreateDevice(c *gin.Context) {
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
	// 复用 Vehicle 表作为设备登记，ID 由调用方提供或自动生成
	if v.ID == "" {
		v.ID = generateDeviceID()
	}
	if err := h.store.SaveVehicle(context.Background(), &v); err != nil {
		h.logger.Error("create device failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.logger.Info("device created", zap.String("id", v.ID), zap.String("phone", v.Phone))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": v})
}

// GetDevice 查询单个设备详情
// GET /api/v1/devices/:id
func (h *DeviceHandler) GetDevice(c *gin.Context) {
	id := c.Param("id")
	v, err := h.store.GetVehicle(context.Background(), id)
	if err != nil || v == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "device not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": v})
}

// UpdateDevice 更新设备信息
// PUT /api/v1/devices/:id
func (h *DeviceHandler) UpdateDevice(c *gin.Context) {
	id := c.Param("id")
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	v, err := h.store.GetVehicle(context.Background(), id)
	if err != nil || v == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "device not found"})
		return
	}
	if plateNo, ok := updates["plate_no"].(string); ok {
		v.PlateNo = plateNo
	}
	if plateColor, ok := updates["plate_color"].(float64); ok {
		v.PlateColor = int(plateColor)
	}
	if manufacturer, ok := updates["manufacturer"].(string); ok {
		v.Manufacturer = manufacturer
	}
	if terminalType, ok := updates["terminal_type"].(string); ok {
		v.TerminalType = terminalType
	}
	if terminalID, ok := updates["terminal_id"].(string); ok {
		v.TerminalID = terminalID
	}
	if err := h.store.SaveVehicle(context.Background(), v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated", "data": v})
}

// DeleteDevice 删除设备
// DELETE /api/v1/devices/:id
func (h *DeviceHandler) DeleteDevice(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteVehicle(context.Background(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.logger.Info("device deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

// generateDeviceID 生成设备 ID（16 字节十六进制）
func generateDeviceID() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> uint(i))
	}
	return fmtHexID(b)
}

// fmtHexID 字节转十六进制字符串
func fmtHexID(b []byte) string {
	const hexChars = "0123456789abcdef"
	r := make([]byte, len(b)*2)
	for i, v := range b {
		r[i*2] = hexChars[v>>4]
		r[i*2+1] = hexChars[v&0x0f]
	}
	return string(r)
}
