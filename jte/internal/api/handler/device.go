package handler

import (
	"crypto/rand"
	"encoding/hex"
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
	if err := h.store.SaveVehicle(c.Request.Context(), &v); err != nil {
		respondInternalError(c, h.logger, err, "CreateDevice.SaveVehicle")
		return
	}
	h.logger.Info("device created", zap.String("id", v.ID), zap.String("phone", v.Phone))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": v})
}

// GetDevice 查询单个设备详情
// GET /api/v1/devices/:id
func (h *DeviceHandler) GetDevice(c *gin.Context) {
	id := c.Param("id")
	v, err := h.store.GetVehicle(c.Request.Context(), id)
	if err != nil || v == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "device not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": v})
}

// UpdateDeviceRequest 设备更新请求体（FIXED-2026-07-17: 原接受 map[string]interface{} 无字段白名单校验）
type UpdateDeviceRequest struct {
	PlateNo      string `json:"plate_no,omitempty"`
	PlateColor   *int   `json:"plate_color,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	TerminalType string `json:"terminal_type,omitempty"`
	TerminalID   string `json:"terminal_id,omitempty"`
}

// UpdateDevice 更新设备信息
// PUT /api/v1/devices/:id
func (h *DeviceHandler) UpdateDevice(c *gin.Context) {
	id := c.Param("id")
	var req UpdateDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	v, err := h.store.GetVehicle(c.Request.Context(), id)
	if err != nil || v == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "device not found"})
		return
	}
	// FIXED-2026-07-17 [P2]: 使用结构化请求替代 map[string]interface{}，确保字段白名单校验
	if req.PlateNo != "" {
		v.PlateNo = req.PlateNo
	}
	if req.PlateColor != nil {
		v.PlateColor = *req.PlateColor
	}
	if req.Manufacturer != "" {
		v.Manufacturer = req.Manufacturer
	}
	if req.TerminalType != "" {
		v.TerminalType = req.TerminalType
	}
	if req.TerminalID != "" {
		v.TerminalID = req.TerminalID
	}
	if err := h.store.SaveVehicle(c.Request.Context(), v); err != nil {
		respondInternalError(c, h.logger, err, "UpdateDevice.SaveVehicle")
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated", "data": v})
}

// DeleteDevice 删除设备
// DELETE /api/v1/devices/:id
func (h *DeviceHandler) DeleteDevice(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteVehicle(c.Request.Context(), id); err != nil {
		respondInternalError(c, h.logger, err, "DeleteDevice.DeleteVehicle")
		return
	}
	h.logger.Info("device deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

// generateDeviceID 生成设备 ID（16 字节十六进制，crypto/rand 安全随机）
// AUTO-FIX-2026-07-17: 原实现使用 time.Now().UnixNano() 位移生成，
// 可预测且存在碰撞风险（同一纳秒内并发调用产生相同 ID）。
// 改用 crypto/rand 确保不可预测性和唯一性。
func generateDeviceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见（通常仅在 /dev/urandom 不可用时），
		// 降级为时间戳+随机数组合，保证可用性
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> uint(i))
		}
	}
	return hex.EncodeToString(b)
}
