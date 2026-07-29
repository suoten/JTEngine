package handler

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	ws "github.com/suoten/jt-engine/internal/api/websocket"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/protocol/jt808"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type DeviceHandler struct {
	store         storage.Interface
	logger        *zap.Logger
	commandSender *CommandSender
}

func NewDeviceHandler(store storage.Interface, logger *zap.Logger, commandSender *CommandSender) *DeviceHandler {
	return &DeviceHandler{store: store, logger: logger, commandSender: commandSender}
}

func (h *DeviceHandler) List(c *gin.Context) {
	opts := storage.ListOptions{
		Page:     getIntQuery(c, "page", 1),
		PageSize: getIntQuery(c, "page_size", 20),
	}

	if phone := c.Query("phone"); phone != "" {
		opts.Phone = phone
	}
	if online := c.Query("online"); online != "" {
		val := online == "true"
		opts.Online = &val
	}

	result, err := h.store.ListVehicles(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items":     result.Items,
			"total":     result.Total,
			"page":      opts.Page,
			"page_size": opts.PageSize,
		},
	})
}

func (h *DeviceHandler) SendCommand(c *gin.Context) {
	var req struct {
		Phone   string                 `json:"phone" binding:"required"`
		Command string                 `json:"command" binding:"required"`
		Params  map[string]interface{} `json:"params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if h.commandSender == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "command sender not available"})
		return
	}

	if !h.commandSender.IsDeviceOnline(req.Phone) {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": fmt.Sprintf("device %s not online", req.Phone)})
		return
	}

	var err error
	switch req.Command {
	case "location_query":
		err = h.commandSender.SendLocationQuery(req.Phone)
	case "text_message":
		text := ""
		if t, ok := req.Params["text"].(string); ok {
			text = t
		}
		sign := byte(0)
		if s, ok := req.Params["sign"].(float64); ok {
			sign = byte(int(s))
		}
		err = h.commandSender.SendTextMessage(req.Phone, text, sign)
	case "photo":
		channelId := byte(1)
		if ch, ok := req.Params["channel_id"].(float64); ok {
			channelId = byte(int(ch))
		}
		cmd := byte(0)
		if cm, ok := req.Params["cmd"].(float64); ok {
			cmd = byte(int(cm))
		}
		err = h.commandSender.SendPhotoCommand(req.Phone, channelId, cmd, 0, 1, 1)
	case "terminal_control":
		cmdType := uint32(0x0001)
		if ct, ok := req.Params["command_type"].(float64); ok {
			cmdType = uint32(ct)
		}
		params := map[uint32][]byte{cmdType: []byte(fmt.Sprintf("%v", req.Params["value"]))}
		msg := h.commandSender.BuildCommandMessage(1, params)
		err = h.commandSender.SendToDevice(req.Phone, jt808.MsgIDCommand, msg)
	case "get_params":
		// 查询终端参数：param_ids 为要查询的参数ID列表（uint32），为空则查询全部
		var paramIDs []uint32
		if ids, ok := req.Params["param_ids"].([]interface{}); ok {
			for _, id := range ids {
				if v, ok := id.(float64); ok {
					paramIDs = append(paramIDs, uint32(v))
				}
			}
		}
		err = h.commandSender.SendParamQuery(req.Phone, paramIDs)
	case "set_params":
		// AUTO-FIX-2026-06-27: SendParamSet 签名变更为 paramIDs []uint32（0x8106 仅ID列表）
		// 设置终端参数：params 为 {param_id: value} 键值对，提取键作为 paramIDs
		paramIDs := make([]uint32, 0, len(req.Params))
		for k := range req.Params {
			if id, e := strconv.ParseUint(k, 10, 32); e == nil {
				paramIDs = append(paramIDs, uint32(id))
			}
		}
		if len(paramIDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "set_params requires params map with numeric keys"})
			return
		}
		err = h.commandSender.SendParamSet(req.Phone, paramIDs)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": fmt.Sprintf("unsupported command: %s (supported: location_query, text_message, photo, terminal_control)", req.Command)})
		return
	}

	if err != nil {
		h.logger.Error("command send failed",
			zap.String("phone", req.Phone),
			zap.String("command", req.Command),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": fmt.Sprintf("command send failed: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "command sent",
		"data": gin.H{
			"phone":   req.Phone,
			"command": req.Command,
		},
	})
}

type TrackHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

func NewTrackHandler(store storage.Interface, logger *zap.Logger) *TrackHandler {
	return &TrackHandler{store: store, logger: logger}
}

func (h *TrackHandler) GetTrack(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required"})
		return
	}

	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now()
	if s := c.Query("start_time"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			startTime = t
		}
	}
	if s := c.Query("end_time"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			endTime = t
		}
	}

	locations, err := h.store.GetLocationTrack(c.Request.Context(), phone, startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	// 统一返回格式 {track: [...], total: N}，与前端 axios 拦截器契约一致
	c.JSON(http.StatusOK, gin.H{
		"track": locations,
		"total": len(locations),
	})
}

type ReportHandler struct {
	store      storage.Interface
	logger     *zap.Logger
	mu         sync.RWMutex
	reports    map[string]interface{}
	reportTime map[string]time.Time // AUTO-FIX-2026-07-15: 记录报表生成时间，用于淘汰
	maxReports int                  // AUTO-FIX-2026-07-15: 最大报表数，防 OOM
}

func NewReportHandler(store storage.Interface, logger *zap.Logger) *ReportHandler {
	return &ReportHandler{
		store:      store,
		logger:     logger,
		reports:    make(map[string]interface{}),
		reportTime: make(map[string]time.Time),
		maxReports: 1000, // AUTO-FIX-2026-07-15: 上限 1000 条，防 OOM
	}
}

func (h *ReportHandler) Generate(c *gin.Context) {
	var req struct {
		Type      string `json:"type" binding:"required"`
		Period    string `json:"period"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Phone     string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	startTime := time.Now().Add(-24 * time.Hour)
	endTime := time.Now()
	if req.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, req.StartTime); err == nil {
			startTime = t
		}
	}
	if req.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, req.EndTime); err == nil {
			endTime = t
		}
	}

	if endTime.Sub(startTime) > time.Duration(maxReportTimeRangeDays)*24*time.Hour {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "time range must not exceed 31 days"})
		return
	}

	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 校验 Type 防 reportID 注入
	if !isValidIDField(req.Type) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid type: only [A-Za-z0-9_-] allowed, max 64 chars"})
		return
	}

	reportID := fmt.Sprintf("rpt_%s_%d", req.Type, time.Now().Unix())
	var reportData interface{}

	switch req.Type {
	case "mileage":
		phone := req.Phone
		if phone == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required for mileage report"})
			return
		}
		track, err := h.store.GetLocationTrack(c.Request.Context(), phone, startTime, endTime)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
			return
		}
		var totalMileage float64
		for i := 1; i < len(track); i++ {
			dLat := track[i].Latitude - track[i-1].Latitude
			dLng := track[i].Longitude - track[i-1].Longitude
			// AUTO-FIX-2026-07-14 [ConvergeLoop-严重]: 修复经度距离缺少余弦校正
			// 原代码 math.Sqrt(dLat*dLat+dLng*dLng) * 111.32 将经度和纬度等同处理，
			// 但 1° 经度距离 = 111.32 * cos(lat) km，在纬度 40°（北京）处仅 85.3 km。
			// 原实现高估里程 ~31%（北京）/ ~41%（哈尔滨 45°N），直接影响计费和维保周期。
			latRad := track[i].Latitude * math.Pi / 180
			dLngCorrected := dLng * math.Cos(latRad)
			dist := math.Sqrt(dLat*dLat+dLngCorrected*dLngCorrected) * 111.32
			totalMileage += dist
		}
		reportData = gin.H{
			"phone":   phone,
			"mileage": fmt.Sprintf("%.2f", totalMileage),
			"unit":    "km",
			"points":  len(track),
			"start":   startTime.Format(time.RFC3339),
			"end":     endTime.Format(time.RFC3339),
		}

	case "alarm":
		alarmResult, err := h.store.ListAlarms(c.Request.Context(), storage.ListOptions{
			PageSize: 1000,
			Start:    startTime.Format(time.RFC3339),
			End:      endTime.Format(time.RFC3339),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
			return
		}
		typeCount := make(map[string]int)
		levelCount := make(map[int]int)
		total := 0
		if items, ok := alarmResult.Items.([]*storage.AlarmData); ok {
			total = len(items)
			for _, a := range items {
				typeCount[a.Type]++
				levelCount[a.Level]++
			}
		}
		reportData = gin.H{
			"total":       total,
			"by_type":     typeCount,
			"by_level":    levelCount,
			"start":       startTime.Format(time.RFC3339),
			"end":         endTime.Format(time.RFC3339),
		}

	case "online_rate":
		onlineCount, _ := h.store.GetOnlineCount(c.Request.Context())
		offlineCount, _ := h.store.GetOfflineCount(c.Request.Context())
		totalDevices := onlineCount + offlineCount
		rate := 0.0
		if totalDevices > 0 {
			rate = float64(onlineCount) / float64(totalDevices) * 100
		}
		reportData = gin.H{
			"online":       onlineCount,
			"offline":      offlineCount,
			"total":        totalDevices,
			"online_rate":  fmt.Sprintf("%.1f%%", rate),
			"start":        startTime.Format(time.RFC3339),
			"end":          endTime.Format(time.RFC3339),
		}

	case "overspeed":
		phone := req.Phone
		if phone == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required for overspeed report"})
			return
		}
		track, err := h.store.GetLocationTrack(c.Request.Context(), phone, startTime, endTime)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
			return
		}
		var overspeedRecords []*storage.LocationData
		for _, loc := range track {
			if loc.Speed > overspeedThresholdKMH {
				overspeedRecords = append(overspeedRecords, loc)
			}
		}
		reportData = gin.H{
			"phone":          phone,
			"overspeed_count": len(overspeedRecords),
			"records":        overspeedRecords,
			"start":          startTime.Format(time.RFC3339),
			"end":            endTime.Format(time.RFC3339),
		}

	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "unsupported report type: " + req.Type + " (supported: mileage, alarm, online_rate, overspeed)"})
		return
	}

	// AUTO-FIX-2026-07-14 [ConvergeLoop-P0]: 加写锁防止并发 map 读写 panic
	// AUTO-FIX-2026-07-15 [ConvergeLoop-严重]: 超出容量时淘汰最旧报表，防 OOM
	h.mu.Lock()
	if len(h.reports) >= h.maxReports {
		h.evictOldestLocked()
	}
	h.reports[reportID] = reportData
	h.reportTime[reportID] = time.Now()
	h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"report_id": reportID,
			"type":      req.Type,
			"status":    "completed",
			"data":      reportData,
		},
	})
}

// evictOldestLocked 淘汰最旧的报表（调用方必须持有写锁）
// AUTO-FIX-2026-07-15 [ConvergeLoop-严重]: 防 OOM 淘汰机制
func (h *ReportHandler) evictOldestLocked() {
	if len(h.reportTime) == 0 {
		return
	}
	var oldestID string
	var oldestTime time.Time
	first := true
	for id, t := range h.reportTime {
		if first || t.Before(oldestTime) {
			oldestID = id
			oldestTime = t
			first = false
		}
	}
	delete(h.reports, oldestID)
	delete(h.reportTime, oldestID)
}

func (h *ReportHandler) List(c *gin.Context) {
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P0]: 加读锁防止并发 map 读写 panic
	h.mu.RLock()
	defer h.mu.RUnlock()
	items := make([]interface{}, 0, len(h.reports))
	for id, data := range h.reports {
		items = append(items, gin.H{
			"report_id": id,
			"data":      data,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items": items,
			"total": len(items),
		},
	})
}

type CascadeHandler struct {
	store       storage.Interface
	logger      *zap.Logger
	jt809Clients interface {
		ListClients() []JT809ClientInfo
	}
	// AUTO-FIX-2026-07-02 [P1]: 平台配置变更后的热重载回调
	// 上级平台增删改后调用 reloader 通知网关层连接/断开/重连
	platformReloader PlatformReloader
}

// PlatformReloader 平台配置热重载接口（AUTO-FIX-2026-07-02 [P1]）。
// 由网关层实现，API 变更后调用以连接新平台/断开已删除平台/重连已修改平台。
type PlatformReloader interface {
	// OnPlatformCreated 上级平台创建后触发连接
	OnPlatformCreated(platform *storage.Platform) error
	// OnPlatformUpdated 上级平台修改后触发重连
	OnPlatformUpdated(platform *storage.Platform) error
	// OnPlatformDeleted 上级平台删除后触发断开
	OnPlatformDeleted(platformID string) error
}

type JT809ClientInfo struct {
	ID     string `json:"id"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Status string `json:"status"`
}

func NewCascadeHandler(store storage.Interface, logger *zap.Logger) *CascadeHandler {
	return &CascadeHandler{store: store, logger: logger}
}

func (h *CascadeHandler) SetJT809Clients(clients interface {
	ListClients() []JT809ClientInfo
}) {
	h.jt809Clients = clients
}

// SetPlatformReloader 注入平台配置热重载回调（AUTO-FIX-2026-07-02 [P1]）。
func (h *CascadeHandler) SetPlatformReloader(r PlatformReloader) {
	h.platformReloader = r
}

// GetPlatforms 查询平台配置列表（AUTO-FIX-2026-07-02 [P1]: 从存储层读取，替代原 stub）。
// 支持按角色过滤：?role=downstream|upstream，为空时返回全部。
func (h *CascadeHandler) GetPlatforms(c *gin.Context) {
	role := c.Query("role")
	platforms, err := h.store.ListPlatforms(c.Request.Context(), role)
	if err != nil {
		h.logger.Error("list platforms failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to list platforms"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items": platforms,
			"total": len(platforms),
		},
	})
}

// CreatePlatform 创建平台配置（AUTO-FIX-2026-07-02 [P1]: 持久化到存储层 + 热重载）。
func (h *CascadeHandler) CreatePlatform(c *gin.Context) {
	var req struct {
		Name       string `json:"name" binding:"required"`
		UserID     string `json:"user_id" binding:"required"`
		Password   string `json:"password" binding:"required"`
		Role       string `json:"role" binding:"required"` // "downstream" | "upstream"
		Host       string `json:"host"`
		Port       int    `json:"port"`
		LinkType   int    `json:"link_type"`
		DownLinkID string `json:"downlink_id"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Role != "downstream" && req.Role != "upstream" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "role must be 'downstream' or 'upstream'"})
		return
	}
	if req.Role == "upstream" && (req.Host == "" || req.Port == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "upstream platform requires host and port"})
		return
	}

	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 校验 UserID 防 platformID 注入
	if !isValidIDField(req.UserID) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid user_id: only [A-Za-z0-9_-] allowed, max 64 chars"})
		return
	}

	platformID := fmt.Sprintf("plat_%s_%d", req.UserID, time.Now().UnixNano())
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	platform := &storage.Platform{
		ID:         platformID,
		Name:       req.Name,
		UserID:     req.UserID,
		Password:   req.Password,
		Role:       req.Role,
		Host:       req.Host,
		Port:       req.Port,
		LinkType:   req.LinkType,
		DownLinkID: req.DownLinkID,
		Enabled:    enabled,
	}
	if err := h.store.SavePlatform(c.Request.Context(), platform); err != nil {
		h.logger.Error("save platform failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to save platform"})
		return
	}

	// 触发热重载（上级平台需建立连接）
	if h.platformReloader != nil && req.Role == "upstream" && enabled {
		if err := h.platformReloader.OnPlatformCreated(platform); err != nil {
			h.logger.Warn("platform reload after create failed", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "platform created",
		"data":    platform,
	})
}

// UpdatePlatform 更新平台配置（AUTO-FIX-2026-07-02 [P1]: 持久化 + 热重载）。
func (h *CascadeHandler) UpdatePlatform(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "platform id is required"})
		return
	}

	existing, err := h.store.GetPlatform(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "platform not found"})
		return
	}

	var req struct {
		Name       *string `json:"name"`
		Password   *string `json:"password"`
		Host       *string `json:"host"`
		Port       *int    `json:"port"`
		LinkType   *int    `json:"link_type"`
		DownLinkID *string `json:"downlink_id"`
		Enabled    *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 合并更新（仅更新非 nil 字段，支持 PATCH 语义）
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Password != nil {
		existing.Password = *req.Password
	}
	if req.Host != nil {
		existing.Host = *req.Host
	}
	if req.Port != nil {
		existing.Port = *req.Port
	}
	if req.LinkType != nil {
		existing.LinkType = *req.LinkType
	}
	if req.DownLinkID != nil {
		existing.DownLinkID = *req.DownLinkID
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := h.store.SavePlatform(c.Request.Context(), existing); err != nil {
		h.logger.Error("update platform failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to update platform"})
		return
	}

	// 触发热重载（上级平台需重连）
	if h.platformReloader != nil && existing.Role == "upstream" {
		if err := h.platformReloader.OnPlatformUpdated(existing); err != nil {
			h.logger.Warn("platform reload after update failed", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "platform updated",
		"data":    existing,
	})
}

// DeletePlatform 删除平台配置（AUTO-FIX-2026-07-02 [P1]: 持久化 + 热重载）。
func (h *CascadeHandler) DeletePlatform(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "platform id is required"})
		return
	}

	// 先查询以获取角色信息（热重载需要）
	existing, err := h.store.GetPlatform(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "platform not found"})
		return
	}

	if err := h.store.DeletePlatform(c.Request.Context(), id); err != nil {
		h.logger.Error("delete platform failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to delete platform"})
		return
	}

	// 触发热重载（上级平台需断开连接）
	if h.platformReloader != nil && existing.Role == "upstream" {
		if err := h.platformReloader.OnPlatformDeleted(id); err != nil {
			h.logger.Warn("platform reload after delete failed", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "platform deleted",
		"data":    gin.H{"id": id},
	})
}

type chatContext struct {
	LastPhone    string
	LastQuery    string
	LastIntent   string
	LastTimeRange [2]time.Time
	UpdatedAt    time.Time
}

type AIHandler struct {
	store       storage.Interface
	logger      *zap.Logger
	sessions    map[string]*chatContext
	sessMu      sync.RWMutex
	aiModule    interface{ AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error) }
	aiNLPModule interface{ Chat(query, sessionID string) (response string, err error) }
}

func NewAIHandler(store storage.Interface, logger *zap.Logger) *AIHandler {
	return &AIHandler{store: store, logger: logger, sessions: make(map[string]*chatContext)}
}

func (h *AIHandler) SetAIModule(m interface{ AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error) }) {
	h.aiModule = m
}

func (h *AIHandler) SetAINLPModule(m interface{ Chat(query, sessionID string) (response string, err error) }) {
	h.aiNLPModule = m
}

func (h *AIHandler) getOrCreateSession(sessionID string) *chatContext {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	ctx, ok := h.sessions[sessionID]
	if !ok {
		ctx = &chatContext{}
		h.sessions[sessionID] = ctx
	}
	return ctx
}

func (h *AIHandler) cleanupOldSessions() {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, ctx := range h.sessions {
		if ctx.UpdatedAt.Before(cutoff) {
			delete(h.sessions, id)
		}
	}
}

func (h *AIHandler) AnalyzeAlarm(c *gin.Context) {
	var req struct {
		AlarmID   string                 `json:"alarm_id" binding:"required"`
		AlarmType string                 `json:"alarm_type"`
		Data      map[string]interface{} `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if h.aiModule != nil {
		isFalseAlarm, confidence, reason, err := h.aiModule.AnalyzeAlarm(req.AlarmID, req.AlarmType, req.Data)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{
				"code": 0,
				"data": gin.H{
					"alarm_id":       req.AlarmID,
					"is_false_alarm": isFalseAlarm,
					"confidence":     confidence,
					"reason":         reason,
					"model":          "module-ai",
				},
			})
			return
		}
		h.logger.Warn("AI module failed, falling back to rule engine", zap.Error(err))
	}

	alarms, err := h.store.ListAlarms(c.Request.Context(), storage.ListOptions{PageSize: 100})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	isFalseAlarm := false
	confidence := 0.5
	reason := "rule_engine: alarm analyzed"

	alarmItems, ok := alarms.Items.([]*storage.AlarmData)
	if ok {
		var recentSameType int
		var recentSamePhone int
		oneHourAgo := time.Now().Add(-time.Hour)

		for _, a := range alarmItems {
			if a.ID == req.AlarmID {
				continue
			}
			if a.Phone != "" && req.AlarmType != "" && a.Type == req.AlarmType && a.ReceivedAt.After(oneHourAgo) {
				recentSameType++
			}
			if a.ReceivedAt.After(oneHourAgo) {
				recentSamePhone++
			}
		}

		if recentSameType >= 5 {
			isFalseAlarm = true
			confidence = 0.85
			reason = "rule_engine: high frequency same-type alarm in 1h, likely false alarm or device fault"
		} else if recentSamePhone >= 10 {
			confidence = 0.7
			reason = "rule_engine: high frequency alarms from same device, possible device issue"
		} else {
			confidence = 0.6
			reason = "rule_engine: normal alarm pattern"
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"alarm_id":       req.AlarmID,
			"is_false_alarm": isFalseAlarm,
			"confidence":     confidence,
			"reason":         reason,
			"model":          "rule_engine",
		},
	})
}

func (h *AIHandler) CheckFatigue(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required"})
		return
	}

	track, err := h.store.GetLocationTrack(c.Request.Context(), phone, time.Now().Add(-8*time.Hour), time.Now())
	if err != nil || len(track) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"phone":         phone,
				"fatigue_level": "unknown",
				"score":         0,
				"suggestion":    "暂无足够位置数据评估疲劳等级",
			},
		})
		return
	}

	var continuousDrivingMinutes float64
	var isNightDriving bool
	now := time.Now()
	hour := now.Hour()
	if hour >= 22 || hour < 6 {
		isNightDriving = true
	}

	for i := 1; i < len(track); i++ {
		if track[i].Speed > 0 {
			gap := track[i].Time.Sub(track[i-1].Time).Minutes()
			if gap < 30 {
				continuousDrivingMinutes += gap
			} else {
				continuousDrivingMinutes = 0
			}
		} else {
			continuousDrivingMinutes = 0
		}
	}

	score := 0
	level := "normal"
	suggestion := "正常驾驶"

	if continuousDrivingMinutes > 240 {
		level = "high"
		score = 90
		suggestion = "连续驾驶超过4小时，请立即停车休息"
	} else if continuousDrivingMinutes > 120 {
		level = "medium"
		score = 60
		suggestion = "连续驾驶超过2小时，建议适当休息"
	} else {
		score = 15
	}

	if isNightDriving {
		score += 20
		if score > 100 {
			score = 100
		}
		if level == "normal" {
			level = "low"
			suggestion = "夜间驾驶，请注意安全"
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"phone":                   phone,
			"fatigue_level":           level,
			"score":                   score,
			"continuous_driving_min":  continuousDrivingMinutes,
			"night_driving":           isNightDriving,
			"suggestion":              suggestion,
		},
	})
}

func (h *AIHandler) GetRiskScore(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone is required"})
		return
	}

	alarmResult, _ := h.store.ListAlarms(c.Request.Context(), storage.ListOptions{
		Phone:    phone,
		PageSize: 100,
		Start:    time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
		End:      time.Now().Format(time.RFC3339),
	})

	alarmCount := 0
	if alarmResult != nil {
		if items, ok := alarmResult.Items.([]*storage.AlarmData); ok {
			alarmCount = len(items)
		}
	}

	track, _ := h.store.GetLocationTrack(c.Request.Context(), phone, time.Now().Add(-24*time.Hour), time.Now())
	var overspeedCount int
	var maxSpeed float64
	if track != nil {
		for _, loc := range track {
			if loc.Speed > overspeedThresholdKMH {
				overspeedCount++
			}
			if loc.Speed > maxSpeed {
				maxSpeed = loc.Speed
			}
		}
	}

	riskScore := 0.0
	factors := []string{}

	if alarmCount > 0 {
		riskScore += float64(alarmCount) * 5
		factors = append(factors, "alarm_frequency")
	}
	if overspeedCount > 0 {
		riskScore += float64(overspeedCount) * 3
		factors = append(factors, "overspeed")
	}
	if maxSpeed > overspeedThresholdKMH {
		riskScore += 10
		factors = append(factors, "high_speed")
	}

	if riskScore > 100 {
		riskScore = 100
	}

	level := "low"
	if riskScore >= 70 {
		level = "high"
	} else if riskScore >= 40 {
		level = "medium"
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"phone":         phone,
			"risk_score":    int(riskScore),
			"level":         level,
			"factors":       factors,
			"alarm_count":   alarmCount,
			"overspeed_count": overspeedCount,
		},
	})
}

func (h *AIHandler) AnomalyDetect(c *gin.Context) {
	var req struct {
		Phone string `json:"phone" binding:"required"`
		Type  string `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	track, _ := h.store.GetLocationTrack(c.Request.Context(), req.Phone, time.Now().Add(-2*time.Hour), time.Now())

	anomalyDetected := false
	anomalyType := ""
	confidence := 0.0

	if track != nil && len(track) > 1 {
		for i := 1; i < len(track); i++ {
			if track[i].Speed > 200 {
				anomalyDetected = true
				anomalyType = "speed_anomaly"
				confidence = 0.9
				break
			}
			if track[i].Speed < 0 {
				anomalyDetected = true
				anomalyType = "invalid_speed"
				confidence = 0.95
				break
			}
			gap := track[i].Time.Sub(track[i-1].Time).Seconds()
			if gap > 0 && gap < 1 {
				latDiff := track[i].Latitude - track[i-1].Latitude
				lngDiff := track[i].Longitude - track[i-1].Longitude
				// AUTO-FIX-2026-07-14 [ConvergeLoop-严重]: 使用绝对值检测双向跳变
				// 原代码 latDiff > 0.1 只检测北向/东向跳变，南向/西向跳变（负差值）不被检测。
				// 0.1度 ≈ 11km，1秒内移动11km必然是GPS漂移。
				if math.Abs(latDiff) > 0.1 || math.Abs(lngDiff) > 0.1 {
					anomalyDetected = true
					anomalyType = "position_jump"
					confidence = 0.8
					break
				}
			}
		}
	}

	if !anomalyDetected && (track == nil || len(track) == 0) {
		anomalyDetected = true
		anomalyType = "communication_anomaly"
		confidence = 0.6
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"phone":            req.Phone,
			"anomaly_detected": anomalyDetected,
			"anomaly_type":     anomalyType,
			"confidence":       confidence,
		},
	})
}

func (h *AIHandler) Chat(c *gin.Context) {
	var req struct {
		Query     string `json:"query" binding:"required"`
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if req.SessionID == "" {
		req.SessionID = c.ClientIP()
	}

	util.SafeGo(h.logger, "handler.cleanupOldSessions", h.cleanupOldSessions)

	sessCtx := h.getOrCreateSession(req.SessionID)

	if h.aiNLPModule != nil {
		// AUTO-FIX-2026-06-30 [P1-9]: 优先使用 ChatDetailed 返回结构化数据（表格/图表）
		// 通过类型断言检测模块是否实现 ChatDetailed（向后兼容旧模块）
		type chatDetailed interface {
			ChatDetailed(query, sessionID string) (interface{}, error)
		}
		if detailed, ok := h.aiNLPModule.(chatDetailed); ok {
			resp, err := detailed.ChatDetailed(req.Query, req.SessionID)
			if err == nil && resp != nil {
				sessCtx.LastQuery = req.Query
				sessCtx.UpdatedAt = time.Now()
				c.JSON(http.StatusOK, gin.H{
					"code": 0,
					"data": resp,
				})
				return
			}
			if err != nil {
				h.logger.Warn("AI-NLP ChatDetailed failed, falling back", zap.Error(err))
			}
		}

		response, err := h.aiNLPModule.Chat(req.Query, req.SessionID)
		if err == nil {
			sessCtx.LastQuery = req.Query
			sessCtx.UpdatedAt = time.Now()
			c.JSON(http.StatusOK, gin.H{
				"code": 0,
				"data": gin.H{
					"response":   response,
					"model":      "module-ai-nlp",
					"session_id": req.SessionID,
				},
			})
			return
		}
		h.logger.Warn("AI-NLP module failed, falling back to rule engine", zap.Error(err))
	}

	response := ""
	query := strings.ToLower(req.Query)
	resolvedPhone := ""
	intent := ""

	if strings.Contains(query, "它") || strings.Contains(query, "该设备") || strings.Contains(query, "那个") || strings.Contains(query, "这个") {
		if sessCtx.LastPhone != "" {
			resolvedPhone = sessCtx.LastPhone
		}
	}

	if strings.Contains(query, "报警") && !strings.Contains(query, "在线") && !strings.Contains(query, "离线") {
		intent = "alarm"
		phone := extractPhone(query)
		if phone == "" {
			phone = resolvedPhone
		}
		if phone != "" {
			count, err := h.store.GetAlarmCount(c.Request.Context(), time.Now().Add(-24*time.Hour), time.Now())
			if err == nil {
				response = fmt.Sprintf("设备 %s 最近24小时报警 %d 条", phone, count)
			} else {
				response = fmt.Sprintf("无法获取设备 %s 的报警数", phone)
			}
			sessCtx.LastPhone = phone
		} else {
			count, err := h.store.GetAlarmCount(c.Request.Context(), time.Now().Add(-24*time.Hour), time.Now())
			if err == nil {
				response = fmt.Sprintf("最近24小时报警 %d 条", count)
			} else {
				response = "无法获取报警数"
			}
		}
	} else if strings.Contains(query, "在线") && strings.Contains(query, "设备") {
		intent = "online"
		count, err := h.store.GetOnlineCount(c.Request.Context())
		if err == nil {
			response = fmt.Sprintf("当前在线设备 %d 台", count)
		} else {
			response = "无法获取在线设备数"
		}
	} else if strings.Contains(query, "位置") || strings.Contains(query, "定位") {
		intent = "location"
		phone := extractPhone(query)
		if phone == "" {
			phone = resolvedPhone
		}
		if phone != "" {
			loc, err := h.store.GetLatestLocation(c.Request.Context(), phone)
			if err == nil && loc != nil {
				response = fmt.Sprintf("设备 %s 最新位置：纬度%.6f，经度%.6f，速度%.1f km/h", phone, loc.Latitude, loc.Longitude, loc.Speed)
			} else {
				response = fmt.Sprintf("未找到设备 %s 的位置信息", phone)
			}
			sessCtx.LastPhone = phone
		} else {
			response = "请提供设备手机号，例如：设备013912345678的位置"
		}
	} else if strings.Contains(query, "离线") {
		intent = "offline"
		count, err := h.store.GetOfflineCount(c.Request.Context())
		if err == nil {
			response = fmt.Sprintf("当前离线设备 %d 台", count)
		} else {
			response = "无法获取离线设备数"
		}
	} else if strings.Contains(query, "速度") || strings.Contains(query, "超速") {
		intent = "speed"
		phone := extractPhone(query)
		if phone == "" {
			phone = resolvedPhone
		}
		if phone != "" {
			loc, err := h.store.GetLatestLocation(c.Request.Context(), phone)
			if err == nil && loc != nil {
				response = fmt.Sprintf("设备 %s 当前速度 %.1f km/h", phone, loc.Speed)
			} else {
				response = fmt.Sprintf("未找到设备 %s 的速度信息", phone)
			}
			sessCtx.LastPhone = phone
		} else {
			response = "请提供设备手机号，例如：设备013912345678的速度"
		}
	} else if strings.Contains(query, "轨迹") || strings.Contains(query, "历史") {
		intent = "track"
		phone := extractPhone(query)
		if phone == "" {
			phone = resolvedPhone
		}
		if phone != "" {
			track, err := h.store.GetLocationTrack(c.Request.Context(), phone, time.Now().Add(-1*time.Hour), time.Now())
			if err == nil {
				response = fmt.Sprintf("设备 %s 最近1小时有 %d 条轨迹记录", phone, len(track))
			} else {
				response = fmt.Sprintf("无法获取设备 %s 的轨迹", phone)
			}
			sessCtx.LastPhone = phone
		} else {
			response = "请提供设备手机号查询轨迹"
		}
	} else {
		intent = "help"
		response = "我可以帮您查询：在线设备数、离线设备数、报警统计、设备位置、速度、轨迹。支持多轮对话，例如先问\"设备013912345678的位置\"，再问\"它的报警呢\"。"
	}

	sessCtx.LastQuery = req.Query
	sessCtx.LastIntent = intent
	sessCtx.UpdatedAt = time.Now()

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"response":   response,
			"model":      "rule_engine",
			"session_id": req.SessionID,
		},
	})
}

// [商业版] NL2SQL 自然语言转 SQL 查询
func (h *AIHandler) NL2SQL(c *gin.Context) {
	var req struct {
		Query     string `json:"query" binding:"required"`
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.SessionID == "" {
		req.SessionID = c.ClientIP()
	}
	if h.aiNLPModule != nil {
		response, err := h.aiNLPModule.Chat("NL2SQL: "+req.Query, req.SessionID)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"response": response, "model": "module-ai-nlp"}})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"response": "NL2SQL 模块未加载", "model": "fallback"}})
}

// [商业版] GenerateReport AI 辅助报告生成
func (h *AIHandler) GenerateReport(c *gin.Context) {
	var req struct {
		Type      string                 `json:"type"`
		Params    map[string]interface{} `json:"params"`
		SessionID string                 `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.SessionID == "" {
		req.SessionID = c.ClientIP()
	}
	prompt := fmt.Sprintf("生成报告: 类型=%s, 参数=%v", req.Type, req.Params)
	if h.aiNLPModule != nil {
		response, err := h.aiNLPModule.Chat(prompt, req.SessionID)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"report": response, "model": "module-ai-nlp"}})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"report": "报告生成模块未加载", "model": "fallback"}})
}

// [商业版] DebugProtocol AI 协议调试助手
func (h *AIHandler) DebugProtocol(c *gin.Context) {
	var req struct {
		Protocol  string `json:"protocol"`
		Data      string `json:"data"`
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.SessionID == "" {
		req.SessionID = c.ClientIP()
	}
	prompt := fmt.Sprintf("协议调试: 协议=%s, 数据=%s", req.Protocol, req.Data)
	if h.aiNLPModule != nil {
		response, err := h.aiNLPModule.Chat(prompt, req.SessionID)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"analysis": response, "model": "module-ai-nlp"}})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"analysis": "协议调试模块未加载", "model": "fallback"}})
}

// [商业版] QueryKnowledge RAG 知识库查询
func (h *AIHandler) QueryKnowledge(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "missing query parameter 'q'"})
		return
	}
	sessionID := c.ClientIP()
	if h.aiNLPModule != nil {
		response, err := h.aiNLPModule.Chat("知识库查询: "+query, sessionID)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"answer": response, "model": "module-ai-nlp"}})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"answer": "知识库模块未加载", "model": "fallback"}})
}

// AUTO-FIX-2026-06-26: 第六轮遗留修复 - AI 助手 WebSocket 实时对话
// 路由：GET /api/v1/ai/chat/ws
// 协议：客户端发送 {query, session_id?} → 服务端流式返回 {type:"token",content}...{type:"done"}
// 鉴权：路由层通过 middleware.ExtractAndVerifyJWT 完成 JWT 验签（支持 query ?token=<JWT>），
//       handler 内断言 user_id 作为双保险，防止路由误配置导致匿名访问。
// AUTO-FIX-2026-06-29 [P0]: 原实现 CheckOrigin 永远返回 true 且无任何 token 校验，
//       任意匿名客户端可直接连接——已修复为复用共享 upgrader（CORS 白名单）+ 强制 JWT 验签。
func (h *AIHandler) ChatWS(c *gin.Context) {
	if _, exists := c.Get("user_id"); !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "authentication required"})
		return
	}

	conn, err := ws.Upgrader().Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("ChatWS upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	for {
		var msg struct {
			Query     string `json:"query"`
			SessionID string `json:"session_id"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Debug("ChatWS read error", zap.Error(err))
			}
			return
		}
		if msg.Query == "" {
			continue
		}
		if msg.SessionID == "" {
			msg.SessionID = c.ClientIP()
		}

		// 发送"思考中"状态
		_ = conn.WriteJSON(gin.H{"type": "thinking"})

		// 调用 Chat 逻辑获取响应（复用现有 Chat 方法的核心逻辑）
		response := h.processChatQuery(msg.Query, msg.SessionID)

		// 将响应分块作为 token 流式发送（按段落切分，模拟流式体验）
		chunks := splitForStreaming(response)
		for _, chunk := range chunks {
			if err := conn.WriteJSON(gin.H{"type": "token", "content": chunk}); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond) // 轻微延迟，模拟流式
		}

		_ = conn.WriteJSON(gin.H{"type": "done"})
	}
}

// processChatQuery 抽取 Chat 方法核心逻辑，供 HTTP 和 WebSocket 复用
func (h *AIHandler) processChatQuery(query, sessionID string) string {
	util.SafeGo(h.logger, "handler.cleanupOldSessions", h.cleanupOldSessions)
	sessCtx := h.getOrCreateSession(sessionID)

	if h.aiNLPModule != nil {
		if resp, err := h.aiNLPModule.Chat(query, sessionID); err == nil {
			sessCtx.LastQuery = query
			sessCtx.UpdatedAt = time.Now()
			return resp
		}
	}

	// 规则引擎回退（简化版：复用 extractPhone 等辅助函数）
	q := strings.ToLower(query)
	if strings.Contains(q, "报警") {
		return "已识别为报警查询意图。如需详细报警数据，请前往报警中心查看。"
	}
	if strings.Contains(q, "在线") {
		return "已识别为在线状态查询。如需查看在线车辆，请前往概览页。"
	}
	if strings.Contains(q, "位置") || strings.Contains(q, "轨迹") {
		phone := extractPhone(q)
		if phone != "" {
			sessCtx.LastPhone = phone
		}
		return "已识别为位置/轨迹查询。如需查看轨迹，请前往轨迹回放页。"
	}
	return "我是 JTE 智能助手，可以帮您查询报警、在线状态、位置轨迹等信息。请描述您的问题。"
}

// splitForStreaming 将长文本按段落切分为多个 chunk，用于模拟流式 token 推送
func splitForStreaming(text string) []string {
	if text == "" {
		return []string{""}
	}
	// 按句号/换行切分，每段作为一个 token
	var chunks []string
	current := strings.Builder{}
	for _, r := range text {
		current.WriteRune(r)
		if r == '。' || r == '\n' || r == '!' || r == '?' {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 公开官网配置接口（前端购买解锁按钮）
type WebsiteInfoHandler struct {
	purchaseURL string
	logger      *zap.Logger
}

// NewWebsiteInfoHandler 创建官网信息 handler，purchaseURL 来自配置
func NewWebsiteInfoHandler(purchaseURL string, logger *zap.Logger) *WebsiteInfoHandler {
	return &WebsiteInfoHandler{purchaseURL: purchaseURL, logger: logger}
}

func (h *WebsiteInfoHandler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"purchase_url": h.purchaseURL,
		},
	})
}

func extractPhone(query string) string {
	for _, word := range strings.Fields(query) {
		if len(word) >= 6 {
			isDigit := true
			for _, c := range word {
				if c < '0' || c > '9' {
					isDigit = false
					break
				}
			}
			if isDigit {
				return word
			}
		}
	}
	return ""
}

func getIntQuery(c *gin.Context, key string, defaultVal int) int {
	val := defaultVal
	if s := c.Query(key); s != "" {
		fmt.Sscanf(s, "%d", &val)
	}
	return val
}
