package handler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

// ForwardRuleReloader 转发规则热更新回调接口。
// JT809Client 实现此接口；API 修改规则后调用 Reload 通知客户端重新加载快照。
// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则持久化 + 热更新
type ForwardRuleReloader interface {
	ReloadForwardRules()
}

// ForwardRuleHandler 809 转发规则管理 API
// 提供转发规则的 CRUD 接口，并支持通过 Reloader 触发 JT809Client 热更新。
type ForwardRuleHandler struct {
	store   storage.Interface
	logger  *zap.Logger
	// reloaders 按 platformID 索引，每个 platformID 对应一个 JT809Client 实例
	// 修改规则后调用对应客户端的 ReloadForwardRules 通知其重新加载内存快照
	reloadersMu sync.RWMutex
	reloaders   map[string]ForwardRuleReloader
}

// NewForwardRuleHandler 构造转发规则 handler
func NewForwardRuleHandler(store storage.Interface, logger *zap.Logger) *ForwardRuleHandler {
	return &ForwardRuleHandler{
		store:     store,
		logger:    logger,
		reloaders: make(map[string]ForwardRuleReloader),
	}
}

// RegisterReloader 注册某个上级平台的转发规则热更新回调。
// platformID 对应 storage.ForwardRule.PlatformID，匹配 JT809Client.cfg.ID。
func (h *ForwardRuleHandler) RegisterReloader(platformID string, r ForwardRuleReloader) {
	h.reloadersMu.Lock()
	defer h.reloadersMu.Unlock()
	h.reloaders[platformID] = r
	h.logger.Info("forward rule reloader registered",
		zap.String("platform_id", platformID))
}

// notifyReload 通知指定平台的客户端重新加载转发规则。
func (h *ForwardRuleHandler) notifyReload(platformID string) {
	h.reloadersMu.RLock()
	r, ok := h.reloaders[platformID]
	h.reloadersMu.RUnlock()
	if !ok {
		return
	}
	// 异步调用避免 API 阻塞；ReloadForwardRules 内部为同步读 storage+原子写指针，耗时极短
	go func() {
		defer func() {
			if rv := recover(); rv != nil {
				h.logger.Error("reload forward rules panic",
					zap.String("platform_id", platformID),
					zap.Any("panic", rv))
			}
		}()
		r.ReloadForwardRules()
	}()
}

// List 列出转发规则
// GET /api/v1/forward-rules?platform_id=xxx
// platform_id 为空时返回全部规则（管理后台审计用）
func (h *ForwardRuleHandler) List(c *gin.Context) {
	platformID := c.Query("platform_id")
	rules, err := h.store.ListForwardRules(context.Background(), platformID)
	if err != nil {
		h.logger.Error("list forward rules failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": rules,
	})
}

// Create 新增转发规则
// POST /api/v1/forward-rules
func (h *ForwardRuleHandler) Create(c *gin.Context) {
	var rule storage.ForwardRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if rule.PlatformID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "platform_id is required"})
		return
	}
	if rule.DataType != "location" && rule.DataType != "alarm" && rule.DataType != "video" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "data_type must be location/alarm/video"})
		return
	}
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("fr_%s_%d", rule.PlatformID, time.Now().UnixNano())
	}
	if err := h.store.SaveForwardRule(context.Background(), &rule); err != nil {
		h.logger.Error("create forward rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.notifyReload(rule.PlatformID)
	h.logger.Info("forward rule created",
		zap.String("id", rule.ID),
		zap.String("platform_id", rule.PlatformID),
		zap.String("data_type", rule.DataType))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": rule})
}

// Get 查询单条转发规则
// GET /api/v1/forward-rules/:id
func (h *ForwardRuleHandler) Get(c *gin.Context) {
	id := c.Param("id")
	rule, err := h.store.GetForwardRule(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": rule})
}

// Update 更新转发规则（PUT 语义为全量替换）
// PUT /api/v1/forward-rules/:id
func (h *ForwardRuleHandler) Update(c *gin.Context) {
	id := c.Param("id")
	// 先校验存在性
	existing, err := h.store.GetForwardRule(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	var rule storage.ForwardRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	rule.ID = id
	// 不允许修改 PlatformID（会导致规则归属混乱）
	rule.PlatformID = existing.PlatformID
	if rule.DataType != "location" && rule.DataType != "alarm" && rule.DataType != "video" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "data_type must be location/alarm/video"})
		return
	}
	if err := h.store.SaveForwardRule(context.Background(), &rule); err != nil {
		h.logger.Error("update forward rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.notifyReload(rule.PlatformID)
	h.logger.Info("forward rule updated",
		zap.String("id", rule.ID),
		zap.String("platform_id", rule.PlatformID))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": rule})
}

// Delete 删除转发规则
// DELETE /api/v1/forward-rules/:id
func (h *ForwardRuleHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	// 先查询以获取 platformID 用于触发 reload
	rule, err := h.store.GetForwardRule(context.Background(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	if err := h.store.DeleteForwardRule(context.Background(), id); err != nil {
		h.logger.Error("delete forward rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	h.notifyReload(rule.PlatformID)
	h.logger.Info("forward rule deleted",
		zap.String("id", id),
		zap.String("platform_id", rule.PlatformID))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}
