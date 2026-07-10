package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/api/dto"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

type AlarmHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

func NewAlarmHandler(store storage.Interface, logger *zap.Logger) *AlarmHandler {
	return &AlarmHandler{store: store, logger: logger}
}

func (h *AlarmHandler) List(c *gin.Context) {
	h.ListAlarms(c)
}

func (h *AlarmHandler) RegisterRoutes(r *gin.RouterGroup) {
	alarms := r.Group("/alarms")
	{
		alarms.GET("", h.ListAlarms)
		alarms.GET("/stats", h.GetAlarmStats)
		alarms.GET("/:id", h.GetAlarm)
	}
}

// ListAlarms godoc
// @Summary è·åæ¥è­¦åè¡¨
// @Description åé¡µæ¥è¯¢ç»ç«¯æ¥è­¦äºä»¶è®°å½
// @Tags æ¥è­¦
// @Accept json
// @Produce json
// @Param page query int false "é¡µç " default(1)
// @Param page_size query int false "æ¯é¡µæ°é" default(20)
// @Param phone query string false "ææºå·ç­é?
// @Param start query string false "å¼å§æ¶é?
// @Param end query string false "ç»ææ¶é´"
// @Success 200 {object} dto.ListResultDTO
// @Router /api/v1/alarms [get]
func (h *AlarmHandler) ListAlarms(c *gin.Context) {
	var query dto.ListQueryDTO
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	opts := storage.ListOptions{
		Page:     query.Page,
		PageSize: query.PageSize,
		Phone:    query.Phone,
		Start:    query.Start,
		End:      query.End,
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}

	result, err := h.store.ListAlarms(c.Request.Context(), opts)
	if err != nil {
		h.logger.Error("list alarms failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "internal error"})
		return
	}

	alarms := make([]dto.AlarmDTO, 0)
	if items, ok := result.Items.([]*storage.AlarmData); ok {
		for _, a := range items {
			alarms = append(alarms, dto.AlarmDTO{
				ID:         a.ID,
				VehicleID:  a.VehicleID,
				Phone:      a.Phone,
				Type:       a.Type,
				Level:      a.Level,
				AlarmFlag:  a.AlarmFlag,
				Latitude:   a.Latitude,
				Longitude:  a.Longitude,
				Altitude:   a.Altitude,
				Speed:      a.Speed,
				Direction:  a.Direction,
				Time:       a.Time.Format(time.RFC3339),
				ReceivedAt: a.ReceivedAt.Format(time.RFC3339),
				Source:     a.Source,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"alarms": alarms,
		"total":  result.Total,
		"page":   result.Page,
		"size":   result.Size,
	})
}

// GetAlarm godoc
// @Summary è·åæ¥è­¦è¯¦æ
// @Description æ ¹æ®æ¥è­¦IDè·åæ¥è­¦è¯¦ç»ä¿¡æ¯
// @Tags æ¥è­¦
// @Produce json
// @Param id path string true "æ¥è­¦ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/alarms/{id} [get]
func (h *AlarmHandler) GetAlarm(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: "alarm id required"})
		return
	}

	result, err := h.store.ListAlarms(c.Request.Context(), storage.ListOptions{
		Page:     1,
		PageSize: 1,
		Phone:    id,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "internal error"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetAlarmStats godoc
// @Summary è·åæ¥è­¦ç»è®¡
// @Description è·åæå®å¤©æ°åçæ¥è­¦æ°éç»è®¡
// @Tags æ¥è­¦
// @Produce json
// @Param days query int false "ç»è®¡å¤©æ°" default(7)
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/alarms/stats [get]
func (h *AlarmHandler) GetAlarmStats(c *gin.Context) {
	daysStr := c.DefaultQuery("days", "7")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		days = 7
	}

	start := time.Now().AddDate(0, 0, -days)
	count, err := h.store.GetAlarmCount(c.Request.Context(), start, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "internal error"})
		return
	}

	// 今日报警数
	todayStart := time.Now().Truncate(24 * time.Hour)
	todayCount, _ := h.store.GetAlarmCount(c.Request.Context(), todayStart, time.Now())

	// 按来源统计（jt808/jt1045）
	jt808Count, _ := h.store.GetAlarmCountBySource(c.Request.Context(), "jt808", start, time.Now())
	jt1045Count, _ := h.store.GetAlarmCountBySource(c.Request.Context(), "jt1045", start, time.Now())

	c.JSON(http.StatusOK, gin.H{
		"total":  count,
		"today":  todayCount,
		"jt808":  jt808Count,
		"jt1045": jt1045Count,
		"days":   days,
		"start":  start.Format(time.RFC3339),
		"end":    time.Now().Format(time.RFC3339),
	})
}

// ReceiveAlarm godoc
// @Summary 报警 HTTP 接收
// @Description 接收外部系统/级联平台通过 HTTP 上报的报警（对应 JT/T 808 0x0900 / JT/T 1045），写入时序库并触发实时推送
// @Tags 报警
// @Accept json
// @Produce json
// @Param body body object true "报警数据" {vehicle_id=车辆ID, phone=手机号, type=报警类型, level=级别, latitude=纬度, longitude=经度, speed=速度, direction=方向, alarm_flag=报警标志, source=来源}
// @Success 201 {object} map[string]interface{}
// @Router /api/v1/alarms/receive [post]
func (h *AlarmHandler) ReceiveAlarm(c *gin.Context) {
	var req struct {
		VehicleID string  `json:"vehicle_id" binding:"required"`
		Phone     string  `json:"phone"`
		Type      string  `json:"type" binding:"required"`
		Level     int     `json:"level"`
		AlarmFlag uint32  `json:"alarm_flag"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Altitude  float64 `json:"altitude"`
		Speed     float64 `json:"speed"`
		Direction int     `json:"direction"`
		Source    string  `json:"source"`
		Time      string  `json:"time"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 时间解析（允许为空，默认当前时间）
	var ts time.Time
	if req.Time != "" {
		if parsed, err := time.Parse(time.RFC3339, req.Time); err == nil {
			ts = parsed
		} else {
			ts = time.Now()
		}
	} else {
		ts = time.Now()
	}

	// 来源默认根据 protocol 字段推断
	source := req.Source
	if source == "" {
		source = "jt808"
	}

	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("http_%s_%d", req.VehicleID, ts.UnixNano()),
		VehicleID:  req.VehicleID,
		Phone:      req.Phone,
		Type:       req.Type,
		Level:      req.Level,
		AlarmFlag:  req.AlarmFlag,
		Latitude:   req.Latitude,
		Longitude:  req.Longitude,
		Altitude:   req.Altitude,
		Speed:      req.Speed,
		Direction:  req.Direction,
		Time:       ts,
		ReceivedAt: time.Now(),
		Source:     source,
	}

	if err := h.store.SaveAlarm(c.Request.Context(), alarm); err != nil {
		h.logger.Error("save alarm failed",
			zap.String("vehicle_id", req.VehicleID),
			zap.String("type", req.Type),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "save alarm failed: " + err.Error()})
		return
	}

	// 触发全局报警联动（短信/邮件/钉钉通知）
	if globalAlarmLinkage != nil {
		globalAlarmLinkage.Trigger(req.Type, req.Level, fmt.Sprintf("车辆 %s 触发 %s 报警", req.VehicleID, req.Type))
	}

	h.logger.Info("alarm received via HTTP",
		zap.String("vehicle_id", req.VehicleID),
		zap.String("type", req.Type),
		zap.Int("level", req.Level),
		zap.String("source", source))

	c.JSON(http.StatusCreated, gin.H{
		"code":    0,
		"message": "alarm received",
		"id":      alarm.ID,
		"time":    ts.Format(time.RFC3339),
	})
}
