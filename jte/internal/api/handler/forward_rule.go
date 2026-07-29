package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
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
// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 修复注释与代码行为不符
// 原注释"添加 context 超时取消机制"但 select 只检查一次 ctx，10s 超时形同虚设。
// 现在真正实现：内部 goroutine 执行 ReloadForwardRules，外部 select 等待完成或超时。
// 超时则记录日志并返回，ReloadForwardRules 仍可能在后台继续执行（无法强制终止 Go goroutine），
// 但至少 API 调用方不会无限期等待，且日志可观测超时事件。
func (h *ForwardRuleHandler) notifyReload(platformID string) {
	h.reloadersMu.RLock()
	r, ok := h.reloaders[platformID]
	h.reloadersMu.RUnlock()
	if !ok {
		return
	}
	// 异步调用避免 API 阻塞；ReloadForwardRules 内部为同步读 storage+原子写指针，耗时极短
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	go func() {
		defer cancel()
		defer func() {
			if rv := recover(); rv != nil {
				h.logger.Error("reload forward rules panic",
					zap.String("platform_id", platformID),
					zap.Any("panic", rv))
			}
		}()
		// 启动 ReloadForwardRules 并等待完成或超时
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if rv := recover(); rv != nil {
					h.logger.Error("reload forward rules inner panic",
						zap.String("platform_id", platformID),
						zap.Any("panic", rv))
				}
			}()
			r.ReloadForwardRules()
		}()
		select {
		case <-done:
			// 正常完成
		case <-ctx.Done():
			h.logger.Warn("reload forward rules timeout",
				zap.String("platform_id", platformID),
				zap.Duration("timeout", 10*time.Second))
		}
	}()
}

// List 列出转发规则
// GET /api/v1/forward-rules?platform_id=xxx&page=1&page_size=20
// platform_id 为空时返回全部规则（管理后台审计用）
// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 添加分页支持
// 原先直接返回全部规则，10 倍流量场景下规则数增长会导致响应包过大和内存飙升
func (h *ForwardRuleHandler) List(c *gin.Context) {
	platformID := c.Query("platform_id")
	rules, err := h.store.ListForwardRules(context.Background(), platformID)
	if err != nil {
		respondInternalError(c, h.logger, err, "ForwardRule.List")
		return
	}
	// 软分页（规则数量通常较少，内存切片即可）
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}
	total := len(rules)
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
		"data":      rules[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
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
		respondInternalError(c, h.logger, err, "ForwardRule.Create.SaveForwardRule")
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
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "forward rule not found"})
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
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "forward rule not found"})
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
		respondInternalError(c, h.logger, err, "ForwardRule.Update.SaveForwardRule")
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
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "forward rule not found"})
		return
	}
	if err := h.store.DeleteForwardRule(context.Background(), id); err != nil {
		respondInternalError(c, h.logger, err, "ForwardRule.Delete.DeleteForwardRule")
		return
	}
	h.notifyReload(rule.PlatformID)
	h.logger.Info("forward rule deleted",
		zap.String("id", id),
		zap.String("platform_id", rule.PlatformID))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}
