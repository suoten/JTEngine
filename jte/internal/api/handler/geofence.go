package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// GeofenceHandler 电子围栏管理
type GeofenceHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

// NewGeofenceHandler 构造电子围栏 handler
func NewGeofenceHandler(store storage.Interface, logger *zap.Logger) *GeofenceHandler {
	return &GeofenceHandler{store: store, logger: logger}
}

// List 查询电子围栏列表
// GET /api/v1/geofences
func (h *GeofenceHandler) List(c *gin.Context) {
	opts := storage.ListOptions{
		Page:     parseIntDefault(c.Query("page"), 1),
		PageSize: parseIntDefault(c.Query("page_size"), 20),
		OrgID:    c.Query("org_id"),
	}
	result, err := h.store.ListGeofences(context.Background(), opts)
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

// Create 新增电子围栏
// POST /api/v1/geofences
func (h *GeofenceHandler) Create(c *gin.Context) {
	var g storage.Geofence
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if g.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "name is required"})
		return
	}
	if g.Type < 1 || g.Type > 3 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "type must be 1(circle)/2(rect)/3(polygon)"})
		return
	}
	if g.ID == "" {
		g.ID = generateDeviceID()
	}
	if g.Params == "" {
		g.Params = "{}"
	}
	now := time.Now()
	if g.StartTime.IsZero() {
		g.StartTime = now
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now
	}
	if err := h.store.SaveGeofence(context.Background(), &g); err != nil {
		h.logger.Error("create geofence failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.logger.Info("geofence created", zap.String("id", g.ID), zap.String("name", g.Name))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": g})
}

// Get 查询单个电子围栏
// GET /api/v1/geofences/:id
func (h *GeofenceHandler) Get(c *gin.Context) {
	id := c.Param("id")
	g, err := h.store.GetGeofence(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": g})
}

// Update 更新电子围栏
// PUT /api/v1/geofences/:id
func (h *GeofenceHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var g storage.Geofence
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	g.ID = id
	if err := h.store.SaveGeofence(context.Background(), &g); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated", "data": g})
}

// Delete 删除电子围栏
// DELETE /api/v1/geofences/:id
func (h *GeofenceHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteGeofence(context.Background(), id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	h.logger.Info("geofence deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}
