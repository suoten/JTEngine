package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// ===================================================================
// v3.0 OBD 数据管理 API
// 提供车辆 OBD 诊断数据的查询、历史记录和统计分析功能
//
// API 端点
//   GET  /api/v1/obd/data        - 获取最新 OBD 数据
//   GET  /api/v1/obd/history     - 获取 OBD 历史数据
//   GET  /api/v1/obd/stats       - 获取 OBD 统计信息
//   GET  /api/v1/obd/fault-codes - 获取故障码列表
// ===================================================================

// OBDData OBD 诊断数据结构
type OBDData struct {
	VehicleID      string    `json:"vehicle_id"`
	Phone          string    `json:"phone"`
	Timestamp      time.Time `json:"timestamp"`
	Speed          float64   `json:"speed"`
	Mileage        float64   `json:"mileage"`
	Fuel           float64   `json:"fuel"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	EngineTemp     float64   `json:"engine_temp,omitempty"`
	BatteryVoltage float64   `json:"battery_voltage,omitempty"`
	FaultCodes     []string  `json:"fault_codes,omitempty"`
}

// OBDHandler OBD 数据处理器
type OBDHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

// NewOBDHandler 创建 OBD 数据处理器
func NewOBDHandler(store storage.Interface, logger *zap.Logger) *OBDHandler {
	return &OBDHandler{store: store, logger: logger}
}

// GetData 获取车辆最新 OBD 数据
// GET /api/v1/obd/data?vehicle_id=xxx
func (h *OBDHandler) GetData(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	loc, err := h.store.GetLatestLocation(context.Background(), vehicleID)
	if err != nil {
		h.logger.Warn("failed to get OBD data", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": nil})
		return
	}

	obd := OBDData{
		VehicleID: loc.VehicleID,
		Phone:     loc.Phone,
		Timestamp: loc.ReceivedAt,
		Speed:     loc.Speed,
		Mileage:   loc.Mileage,
		Fuel:      loc.Fuel,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": obd,
	})
}

// GetHistory 获取 OBD 历史数据
// GET /api/v1/obd/history?vehicle_id=xxx&start_time=xxx&end_time=xxx&page=1&page_size=20
func (h *OBDHandler) GetHistory(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	startTime, endTime := parseTimeRange(c)
	page, pageSize := parsePagination(c)

	locations, err := h.store.GetLocationTrack(c.Request.Context(), vehicleID, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get OBD history", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	total := len(locations)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items":     locations[start:end],
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetStats 获取 OBD 统计信息
// GET /api/v1/obd/stats?vehicle_id=xxx&start_time=xxx&end_time=xxx
func (h *OBDHandler) GetStats(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	startTime, endTime := parseTimeRange(c)

	locations, err := h.store.GetLocationTrack(c.Request.Context(), vehicleID, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get OBD stats", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	totalDistance := 0.0
	maxSpeed := 0.0
	totalFuel := 0.0
	for _, loc := range locations {
		if loc != nil {
			if loc.Speed > maxSpeed {
				maxSpeed = loc.Speed
			}
			totalFuel += loc.Fuel
			totalDistance += loc.Mileage
		}
	}

	stats := gin.H{
		"vehicle_id":    vehicleID,
		"start_time":    startTime.Format(time.RFC3339),
		"end_time":      endTime.Format(time.RFC3339),
		"total_points":  len(locations),
		"total_mileage": fmt.Sprintf("%.2f km", totalDistance),
		"total_fuel":    fmt.Sprintf("%.2f L", totalFuel),
		"max_speed":     fmt.Sprintf("%.1f km/h", maxSpeed),
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}

// GetFaultCodes 获取车辆故障码列表
// GET /api/v1/obd/fault-codes?vehicle_id=xxx
func (h *OBDHandler) GetFaultCodes(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	alarms, err := h.store.ListAlarms(context.Background(), storage.ListOptions{
		Phone:    vehicleID,
		Page:     1,
		PageSize: 50,
	})
	if err != nil {
		h.logger.Error("failed to get fault codes", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	faultCodes := make([]gin.H, 0)
	if items, ok := alarms.Items.([]*storage.AlarmData); ok {
		for _, alarm := range items {
			if alarm.Source == "obd" || alarm.Type == "obd_fault" {
				faultCodes = append(faultCodes, gin.H{
					"code":      alarm.Type,
					"level":     alarm.Level,
					"timestamp": alarm.ReceivedAt.Format(time.RFC3339),
					"status":    "active",
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"vehicle_id":  vehicleID,
			"fault_codes": faultCodes,
			"total":       len(faultCodes),
		},
	})
}
