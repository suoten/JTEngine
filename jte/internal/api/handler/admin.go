package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/config"
	"github.com/jte-engine/jte/internal/module"
	"github.com/jte-engine/jte/pkg/protocol/jt808"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type AdminHandler struct {
	store         storage.Interface
	logger        *zap.Logger
	commandSender *CommandSender
	rbac          *module.RBACManager
	// AUTO-FIX-2026-07-02: WebSocket 推送接口（权限变更时通知前端刷新）
	// 使用接口解耦，避免 import cycle
	wsHub WSPublisher
}

// WSPublisher WebSocket 消息推送接口（websocket.Hub 实现此接口）
// AUTO-FIX-2026-07-02: 权限动态化 - 用户角色/权限变更时通过 WebSocket 推送 permission_changed 事件
type WSPublisher interface {
	Publish(topic string, msgType string, data interface{})
}

func NewAdminHandler(store storage.Interface, logger *zap.Logger, commandSender *CommandSender) *AdminHandler {
	return &AdminHandler{store: store, logger: logger, commandSender: commandSender}
}

// SetRBACManager 注入用户管理器（RBACManager）
func (h *AdminHandler) SetRBACManager(rbac *module.RBACManager) {
	h.rbac = rbac
}

// SetWSHub 注入 WebSocket 推送器（权限变更时推送 permission_changed 事件）
func (h *AdminHandler) SetWSHub(hub WSPublisher) {
	h.wsHub = hub
}

func (h *AdminHandler) UpdateVehicle(c *gin.Context) {
	id := c.Param("id")
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	vehicle, err := h.store.GetVehicle(context.Background(), id)
	if err != nil || vehicle == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "vehicle not found"})
		return
	}

	if plateNo, ok := updates["plate_no"].(string); ok {
		vehicle.PlateNo = plateNo
	}
	if plateColor, ok := updates["plate_color"].(float64); ok {
		vehicle.PlateColor = int(plateColor)
	}
	if manufacturer, ok := updates["manufacturer"].(string); ok {
		vehicle.Manufacturer = manufacturer
	}
	if terminalType, ok := updates["terminal_type"].(string); ok {
		vehicle.TerminalType = terminalType
	}

	if err := h.store.SaveVehicle(context.Background(), vehicle); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated", "data": vehicle})
}

func (h *AdminHandler) DeleteVehicle(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteVehicle(context.Background(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

func (h *AdminHandler) SendCommand(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		CommandType string `json:"command_type"`
		Parameter   string `json:"parameter"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if h.commandSender == nil {
		h.logger.Info("admin command sent (no sender)",
			zap.String("terminal_id", id),
			zap.String("command_type", req.CommandType))
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "command queued", "terminal_id": id})
		return
	}

	vehicle, err := h.store.GetVehicle(context.Background(), id)
	if err != nil || vehicle == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "vehicle not found"})
		return
	}

	phone := vehicle.Phone
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle has no phone number"})
		return
	}

	if !h.commandSender.IsDeviceOnline(phone) {
		h.logger.Info("admin command queued (device offline)",
			zap.String("terminal_id", id),
			zap.String("phone", phone),
			zap.String("command_type", req.CommandType))
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "command queued (device offline)", "terminal_id": id, "phone": phone})
		return
	}

	var sendErr error
	switch req.CommandType {
	case "location_query":
		sendErr = h.commandSender.SendLocationQuery(phone)
	case "text_message":
		sendErr = h.commandSender.SendTextMessage(phone, req.Parameter, 0)
	case "photo":
		sendErr = h.commandSender.SendPhotoCommand(phone, 1, 0, 0, 1, 1)
	case "terminal_control":
		params := map[uint32][]byte{0x0001: []byte(req.Parameter)}
		msg := h.commandSender.BuildCommandMessage(1, params)
		sendErr = h.commandSender.SendToDevice(phone, jt808.MsgIDCommand, msg)
	default:
		h.logger.Info("admin command sent (unknown type, logging only)",
			zap.String("terminal_id", id),
			zap.String("command_type", req.CommandType))
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "command logged", "terminal_id": id})
		return
	}

	if sendErr != nil {
		h.logger.Error("admin command send failed",
			zap.String("terminal_id", id),
			zap.String("phone", phone),
			zap.String("command_type", req.CommandType),
			zap.Error(sendErr))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": fmt.Sprintf("command send failed: %v", sendErr), "terminal_id": id})
		return
	}

	h.logger.Info("admin command sent",
		zap.String("terminal_id", id),
		zap.String("phone", phone),
		zap.String("command_type", req.CommandType))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "command sent", "terminal_id": id, "phone": phone})
}

func (h *AdminHandler) HandleAlarm(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Action  string `json:"action"`
		Remarks string `json:"remarks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	h.logger.Info("alarm handled by admin",
		zap.String("alarm_id", id),
		zap.String("action", req.Action))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "alarm handled", "id": id})
}

func (h *AdminHandler) CreatePlatform(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	h.logger.Info("admin create platform",
		zap.String("name", req.Name),
		zap.String("host", req.Host))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created"})
}

func (h *AdminHandler) UpdatePlatform(c *gin.Context) {
	id := c.Param("id")
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	h.logger.Info("admin update platform", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated"})
}

func (h *AdminHandler) DeletePlatform(c *gin.Context) {
	id := c.Param("id")
	h.logger.Info("admin delete platform", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

func (h *AdminHandler) GetConfig(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"max_devices":    20,
			"version":        "1.0.0",
			"protocols":      []string{"jt808", "jt1078"},
			"modules_loaded": []string{},
		}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"logging": gin.H{
			"level":  cfg.Logging.Level,
			"format": cfg.Logging.Format,
		},
		"gateway": gin.H{
			"tcp_port":           cfg.Gateway.TCPPort,
			"udp_port":           cfg.Gateway.UDPPort,
			"heartbeat_interval": cfg.Gateway.HeartbeatInterval,
			"heartbeat_timeout":  cfg.Gateway.HeartbeatTimeout,
			"max_devices":        cfg.Gateway.MaxDevices,
		},
		"api": gin.H{
			"port":       cfg.API.Port,
			"rate_limit": cfg.API.RateLimit,
		},
		"storage": gin.H{
			"type": cfg.Storage.Type,
		},
	}})
}

// GetMapConfig 返回地图API Key配置，供前端动态加载地图SDK。
// AUTO-FIX-2026-06-26: 地图API Key配置化（原为前端硬编码 YOUR_TIANDITU_KEY）[2026-06-26]
func (h *AdminHandler) GetMapConfig(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{
			"provider":     "tianditu",
			"tianditu_key": "",
			"amap_key":     "",
			"baidu_key":    "",
		})
		return
	}

	provider := cfg.Map.Provider
	if provider == "" {
		provider = "tianditu"
	}

	c.JSON(http.StatusOK, gin.H{
		"provider":      provider,
		"tianditu_key":  cfg.Map.TiandituKey,
		"amap_key":      cfg.Map.AMapKey,
		"amap_security": cfg.Map.AMapSecurity,
		"baidu_key":     cfg.Map.BaiduKey,
	})
}

func (h *AdminHandler) UpdateConfig(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "config not available"})
		return
	}

	var applied []string
	var rejected []string

	for key, val := range req {
		if !config.IsHotReloadable(key) {
			rejected = append(rejected, key+" (restart required)")
			continue
		}

		switch key {
		case "logging.level":
			if s, ok := val.(string); ok {
				cfg.Logging.Level = s
				applied = append(applied, key)
			}
		case "logging.format":
			if s, ok := val.(string); ok {
				cfg.Logging.Format = s
				applied = append(applied, key)
			}
		case "gateway.heartbeat_interval":
			if f, ok := val.(float64); ok {
				cfg.Gateway.HeartbeatInterval = int(f)
				applied = append(applied, key)
			}
		case "gateway.heartbeat_timeout":
			if f, ok := val.(float64); ok {
				cfg.Gateway.HeartbeatTimeout = int(f)
				applied = append(applied, key)
			}
		case "api.rate_limit":
			if f, ok := val.(float64); ok {
				cfg.API.RateLimit = int(f)
				applied = append(applied, key)
			}
		}
	}

	h.logger.Info("admin config update",
		zap.Strings("applied", applied),
		zap.Strings("rejected", rejected))

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "config updated",
		"data": gin.H{
			"applied":  applied,
			"rejected": rejected,
		},
	})
}

// AUTO-FIX-2026-06-26: 用户管理后端实现（原为空壳 stub）[2026-06-26]
func (h *AdminHandler) ListUsers(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusOK, gin.H{"users": []interface{}{}})
		return
	}

	users := h.rbac.ListUsers()
	// 过滤掉密码哈希
	result := make([]gin.H, 0, len(users))
	for _, u := range users {
		result = append(result, gin.H{
			"id":            u.ID,
			"username":      u.Username,
			"role":          string(u.Role),
			"display_name":  u.DisplayName,
			"enabled":       u.Enabled,
			"created_at":    u.CreatedAt.Format(time.RFC3339),
			"updated_at":    u.UpdatedAt.Format(time.RFC3339),
			"last_login_at": u.LastLoginAt.Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"users": result,
		"total": len(result),
	})
}

func (h *AdminHandler) CreateUser(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}

	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required"`
		Role        string `json:"role" binding:"required"`
		DisplayName string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 校验角色合法性
	validRoles := map[string]bool{
		"super_admin": true, "admin": true, "operator": true, "readonly": true,
	}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid role, must be one of: super_admin, admin, operator, readonly"})
		return
	}

	user, err := h.rbac.CreateUser(req.Username, req.Password, module.Role(req.Role), req.DisplayName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	h.logger.Info("user created", zap.String("username", req.Username), zap.String("role", req.Role))

	c.JSON(http.StatusCreated, gin.H{
		"id":           user.ID,
		"username":     user.Username,
		"role":         string(user.Role),
		"display_name": user.DisplayName,
		"enabled":      user.Enabled,
		"created_at":   user.CreatedAt.Format(time.RFC3339),
	})
}

func (h *AdminHandler) UpdateUser(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}

	id := c.Param("id")
	var req struct {
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 校验角色合法性
	if req.Role != "" {
		validRoles := map[string]bool{
			"super_admin": true, "admin": true, "operator": true, "readonly": true,
		}
		if !validRoles[req.Role] {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid role"})
			return
		}
	}

	existing := h.rbac.GetUser(id)
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "user not found"})
		return
	}

	role := module.Role(req.Role)
	if role == "" {
		role = existing.Role
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = existing.DisplayName
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	} else {
		enabled = existing.Enabled
	}

	if err := h.rbac.UpdateUser(id, role, displayName, enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	h.logger.Info("user updated", zap.String("id", id), zap.String("role", string(role)))

	// AUTO-FIX-2026-07-02: 权限变更 WebSocket 推送 - 通知前端刷新权限
	// 前端订阅 permission_changed 主题，收到后调用 /auth/permissions 重新拉取权限
	if h.wsHub != nil {
		h.wsHub.Publish("permission_changed", "permission_changed", gin.H{
			"user_id":   id,
			"role":      string(role),
			"enabled":   enabled,
			"timestamp": time.Now().Unix(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated"})
}

func (h *AdminHandler) DeleteUser(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}

	id := c.Param("id")

	// 防止删除最后一个 super_admin
	user := h.rbac.GetUser(id)
	if user != nil && user.Role == module.RoleSuperAdmin {
		users := h.rbac.ListUsers()
		superAdminCount := 0
		for _, u := range users {
			if u.Role == module.RoleSuperAdmin && u.Enabled {
				superAdminCount++
			}
		}
		if superAdminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "cannot delete the last super_admin"})
			return
		}
	}

	if err := h.rbac.DeleteUser(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}

	h.logger.Info("user deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

// ListRoles 列出所有角色
// GET /api/v1/roles
func (h *AdminHandler) ListRoles(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusOK, gin.H{"roles": []interface{}{}})
		return
	}
	roles := h.rbac.ListRoles()
	result := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		permStrs := make([]string, 0, len(r.Permissions))
		for _, p := range r.Permissions {
			permStrs = append(permStrs, string(p))
		}
		result = append(result, gin.H{
			"id":           r.ID,
			"name":         r.Name,
			"display_name": r.DisplayName,
			"permissions":  permStrs,
			"built_in":     r.BuiltIn,
			"created_at":   r.CreatedAt.Format(time.RFC3339),
			"updated_at":   r.UpdatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result, "total": len(result)})
}

// CreateRole 创建自定义角色
// POST /api/v1/roles
func (h *AdminHandler) CreateRole(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}
	var req struct {
		Name        string   `json:"name" binding:"required"`
		DisplayName string   `json:"display_name"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	perms := make([]module.Permission, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		perms = append(perms, module.Permission(p))
	}
	role, err := h.rbac.CreateRole(req.Name, req.DisplayName, perms)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	h.logger.Info("role created", zap.String("name", req.Name))
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "created", "data": gin.H{
		"id":           role.ID,
		"name":         role.Name,
		"display_name": role.DisplayName,
		"built_in":     role.BuiltIn,
	}})
}

// UpdateRole 更新角色
// PUT /api/v1/roles/:id
func (h *AdminHandler) UpdateRole(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}
	id := c.Param("id")
	var req struct {
		DisplayName string   `json:"display_name"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	perms := make([]module.Permission, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		perms = append(perms, module.Permission(p))
	}
	if err := h.rbac.UpdateRole(id, req.DisplayName, perms); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	h.logger.Info("role updated", zap.String("id", id))

	// AUTO-FIX-2026-07-02: 角色权限变更 WebSocket 推送 - 通知所有使用该角色的用户刷新权限
	// user_id 留空表示广播给所有用户（前端会检查是否为自己的角色）
	if h.wsHub != nil {
		h.wsHub.Publish("permission_changed", "permission_changed", gin.H{
			"role_id":   id,
			"timestamp": time.Now().Unix(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated"})
}

// DeleteRole 删除角色
// DELETE /api/v1/roles/:id
func (h *AdminHandler) DeleteRole(c *gin.Context) {
	if h.rbac == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "user manager not available"})
		return
	}
	id := c.Param("id")
	if err := h.rbac.DeleteRole(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	h.logger.Info("role deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}