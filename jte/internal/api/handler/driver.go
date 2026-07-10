package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// DriverHandler 驾驶员管理
type DriverHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

// NewDriverHandler 构造驾驶员管理 handler
func NewDriverHandler(store storage.Interface, logger *zap.Logger) *DriverHandler {
	return &DriverHandler{store: store, logger: logger}
}

// List 查询驾驶员列表
// GET /api/v1/drivers
func (h *DriverHandler) List(c *gin.Context) {
	opts := storage.ListOptions{
		Page:     parseIntDefault(c.Query("page"), 1),
		PageSize: parseIntDefault(c.Query("page_size"), 20),
		Phone:    c.Query("phone"),
	}
	result, err := h.store.QueryDrivers(context.Background(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":  0,
		"data":  result.Items,
		"total": result.Total,
		"page":  result.Page,
		"size":  result.Size,
	})
}

// Create 新增驾驶员
// POST /api/v1/drivers
func (h *DriverHandler) Create(c *gin.Context) {
	var d storage.DriverInfoData
	if err := c.ShouldBindJSON(&d); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if d.DriverName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "driver_name is required"})
		return
	}
	if d.ID == "" {
		d.ID = generateDeviceID()
	}
	d.ReceivedAt = time.Now()
	if d.Source == "" {
		d.Source = "api"
	}
	if err := h.store.SaveDriverInfo(context.Background(), &d); err != nil {
		h.logger.Error("create driver failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.logger.Info("driver created", zap.String("id", d.ID), zap.String("name", d.DriverName))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": d})
}

// Update 更新驾驶员信息
// PUT /api/v1/drivers/:id
func (h *DriverHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var req storage.DriverInfoData
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	req.ID = id
	req.ReceivedAt = time.Now()
	if req.Source == "" {
		req.Source = "api"
	}
	if err := h.store.SaveDriverInfo(context.Background(), &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated", "data": req})
}

// Delete 删除驾驶员
// DELETE /api/v1/drivers/:id
func (h *DriverHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteDriver(context.Background(), id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	h.logger.Info("driver deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}
