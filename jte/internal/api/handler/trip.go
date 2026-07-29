package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// ===================================================================
// v3.0 琛岀▼鍒嗘瀽 API
// 鎻愪緵琛岀▼妫€娴嬨€佽绋嬭鎯呮煡璇€侀┚椹惰涓哄垎鏋愮瓑鍔熻兘
//
// API 绔偣锛?
//   GET  /api/v1/trips           - 琛岀▼鍒楄〃鏌ヨ
//   GET  /api/v1/trips/:id       - 琛岀▼璇︽儏鏌ヨ
//   GET  /api/v1/trips/analysis  - 琛岀▼鍒嗘瀽锛堥€熷害鍓栭潰/椹鹃┒琛屼负/閲岀▼缁熻锛?
//   POST /api/v1/trips/detect    - 瑙﹀彂琛岀▼妫€娴嬶紙鎸囧畾鏃堕棿鑼冨洿锛?
// ===================================================================

// TripData 琛岀▼鏁版嵁缁撴瀯
type TripData struct {
	ID              string    `json:"id"`
	VehicleID       string    `json:"vehicle_id"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	Duration        int64     `json:"duration_seconds"`
	Distance        float64   `json:"distance"`
	MaxSpeed        float64   `json:"max_speed"`
	AvgSpeed        float64   `json:"avg_speed"`
	StartLat        float64   `json:"start_lat"`
	StartLng        float64   `json:"start_lng"`
	EndLat          float64   `json:"end_lat"`
	EndLng          float64   `json:"end_lng"`
	FuelConsumed    float64   `json:"fuel_consumed"`
	SpeedingCount   int       `json:"speeding_count"`
	HarshBrakeCount int       `json:"harsh_brake_count"`
	HarshAccelCount int       `json:"harsh_accel_count"`
}

// TripHandler 琛岀▼鍒嗘瀽澶勭悊鍣?
type TripHandler struct {
	store  storage.Interface
	logger *zap.Logger
}

// NewTripHandler 鍒涘缓琛岀▼鍒嗘瀽澶勭悊鍣?
func NewTripHandler(store storage.Interface, logger *zap.Logger) *TripHandler {
	return &TripHandler{store: store, logger: logger}
}

// List 琛岀▼鍒楄〃鏌ヨ
// GET /api/v1/trips?vehicle_id=xxx&start_time=xxx&end_time=xxx&page=1&page_size=20
func (h *TripHandler) List(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	startTime, endTime := parseTimeRange(c)
	page, pageSize := parsePagination(c)

	locations, err := h.store.GetLocationTrack(c.Request.Context(), vehicleID, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to list trips", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	trips := h.detectTripsFromLocations(locations, vehicleID)

	// 鎵嬪姩鍒嗛〉
	total := len(trips)
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
			"items":     trips[start:end],
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// Get 琛岀▼璇︽儏鏌ヨ
// GET /api/v1/trips/:id?vehicle_id=xxx
func (h *TripHandler) Get(c *gin.Context) {
	tripID := c.Param("id")
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	startTime, err := parseTripID(tripID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid trip id"})
		return
	}
	endTime := startTime.Add(2 * time.Hour)

	locations, err := h.store.GetLocationTrack(c.Request.Context(), vehicleID, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get trip detail", zap.String("trip_id", tripID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	if len(locations) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "trip not found"})
		return
	}

	trip := h.buildTripFromLocations(locations, vehicleID)
	trip.ID = tripID

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trip,
	})
}

// Analysis 琛岀▼鍒嗘瀽
// GET /api/v1/trips/analysis?vehicle_id=xxx&start_time=xxx&end_time=xxx
func (h *TripHandler) Analysis(c *gin.Context) {
	vehicleID := c.Query("vehicle_id")
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	startTime, endTime := parseTimeRange(c)

	locations, err := h.store.GetLocationTrack(c.Request.Context(), vehicleID, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get trip analysis", zap.String("vehicle_id", vehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	analysis := h.analyzeTrips(locations, vehicleID, startTime, endTime)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": analysis,
	})
}

// Detect 瑙﹀彂琛岀▼妫€娴?
// POST /api/v1/trips/detect
func (h *TripHandler) Detect(c *gin.Context) {
	var req struct {
		VehicleID string `json:"vehicle_id" binding:"required"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "vehicle_id is required"})
		return
	}

	now := time.Now()
	start := now.Add(-24 * time.Hour)
	end := now

	if req.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, req.StartTime); err == nil {
			start = t
		}
	}
	if req.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, req.EndTime); err == nil {
			end = t
		}
	}

	locations, err := h.store.GetLocationTrack(context.Background(), req.VehicleID, start, end)
	if err != nil {
		h.logger.Error("failed to detect trips", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}

	trips := h.detectTripsFromLocations(locations, req.VehicleID)

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": fmt.Sprintf("detected %d trips", len(trips)),
		"data": gin.H{
			"trips":      trips,
			"total":      len(trips),
			"time_range": fmt.Sprintf("%s ~ %s", start.Format(time.RFC3339), end.Format(time.RFC3339)),
		},
	})
}

// detectTripsFromLocations 浠庤建杩规暟鎹腑妫€娴嬭绋?
// 绠楁硶锛氬鏋滀袱涓繛缁建杩圭偣涔嬮棿鐨勬椂闂撮棿闅旇秴杩?5 鍒嗛挓锛屽垯璁や负鏄袱涓笉鍚岀殑琛岀▼
func (h *TripHandler) detectTripsFromLocations(locations []*storage.LocationData, vehicleID string) []TripData {
	if len(locations) == 0 {
		return []TripData{}
	}

	var trips []TripData
	var currentTrip []*storage.LocationData
	var lastTime time.Time
	const tripGapThreshold = 5 * time.Minute

	for _, loc := range locations {
		if loc == nil {
			continue
		}
		if !lastTime.IsZero() && loc.ReceivedAt.Sub(lastTime) > tripGapThreshold {
			if len(currentTrip) > 0 {
				trips = append(trips, h.buildTripFromLocations(currentTrip, vehicleID))
			}
			currentTrip = nil
		}
		currentTrip = append(currentTrip, loc)
		lastTime = loc.ReceivedAt
	}

	if len(currentTrip) > 0 {
		trips = append(trips, h.buildTripFromLocations(currentTrip, vehicleID))
	}

	return trips
}

// buildTripFromLocations 浠庤建杩规暟鎹瀯寤鸿绋嬩俊鎭?
func (h *TripHandler) buildTripFromLocations(locations []*storage.LocationData, vehicleID string) TripData {
	if len(locations) == 0 {
		return TripData{VehicleID: vehicleID}
	}

	first := locations[0]
	last := locations[len(locations)-1]

	var maxSpeed, totalSpeed, distance float64
	var speedingCount, harshBrakeCount, harshAccelCount int
	prevSpeed := first.Speed

	for i, loc := range locations {
		if loc == nil {
			continue
		}
		if loc.Speed > maxSpeed {
			maxSpeed = loc.Speed
		}
		totalSpeed += loc.Speed

		if loc.Speed > overspeedThresholdKMH {
			speedingCount++
		}

		if i > 0 && locations[i-1] != nil {
			speedDiff := loc.Speed - prevSpeed
			if speedDiff < harshBrakeThresholdKMH {
				harshBrakeCount++
			} else if speedDiff > harshAccelThresholdKMH {
				harshAccelCount++
			}
			timeDiff := loc.ReceivedAt.Sub(locations[i-1].ReceivedAt).Hours()
			distance += loc.Speed * timeDiff
		}

		prevSpeed = loc.Speed
	}

	count := len(locations)
	avgSpeed := 0.0
	if count > 0 {
		avgSpeed = totalSpeed / float64(count)
	}

	duration := last.ReceivedAt.Sub(first.ReceivedAt).Seconds()
	fuelConsumed := distance * 0.08

	return TripData{
		ID:              fmt.Sprintf("%s_%d", vehicleID, first.ReceivedAt.Unix()),
		VehicleID:       vehicleID,
		StartTime:       first.ReceivedAt,
		EndTime:         last.ReceivedAt,
		Duration:        int64(duration),
		Distance:        distance,
		MaxSpeed:        maxSpeed,
		AvgSpeed:        avgSpeed,
		StartLat:        first.Latitude,
		StartLng:        first.Longitude,
		EndLat:          last.Latitude,
		EndLng:          last.Longitude,
		FuelConsumed:    fuelConsumed,
		SpeedingCount:   speedingCount,
		HarshBrakeCount: harshBrakeCount,
		HarshAccelCount: harshAccelCount,
	}
}

// analyzeTrips 鍒嗘瀽琛岀▼鏁版嵁
func (h *TripHandler) analyzeTrips(locations []*storage.LocationData, vehicleID string, start, end time.Time) gin.H {
	trips := h.detectTripsFromLocations(locations, vehicleID)

	totalDistance := 0.0
	totalDuration := int64(0)
	totalFuel := 0.0
	maxSpeed := 0.0
	totalSpeeding := 0
	totalHarshBrake := 0
	totalHarshAccel := 0

	for _, trip := range trips {
		totalDistance += trip.Distance
		totalDuration += trip.Duration
		totalFuel += trip.FuelConsumed
		if trip.MaxSpeed > maxSpeed {
			maxSpeed = trip.MaxSpeed
		}
		totalSpeeding += trip.SpeedingCount
		totalHarshBrake += trip.HarshBrakeCount
		totalHarshAccel += trip.HarshAccelCount
	}

	avgSpeed := 0.0
	if totalDuration > 0 {
		avgSpeed = totalDistance / (float64(totalDuration) / 3600.0)
	}

	return gin.H{
		"vehicle_id":      vehicleID,
		"start_time":      start.Format(time.RFC3339),
		"end_time":        end.Format(time.RFC3339),
		"trip_count":      len(trips),
		"total_distance":  fmt.Sprintf("%.2f km", totalDistance),
		"total_duration":  fmt.Sprintf("%d minutes", totalDuration/60),
		"total_fuel":      fmt.Sprintf("%.2f L", totalFuel),
		"max_speed":       fmt.Sprintf("%.1f km/h", maxSpeed),
		"avg_speed":       fmt.Sprintf("%.1f km/h", avgSpeed),
		"speeding_count":  totalSpeeding,
		"harsh_brake":     totalHarshBrake,
		"harsh_accel":     totalHarshAccel,
		"driving_score":   calculateDrivingScore(totalSpeeding, totalHarshBrake, totalHarshAccel, len(trips)),
	}
}

// calculateDrivingScore 璁＄畻椹鹃┒璇勫垎锛?-100锛?
func calculateDrivingScore(speeding, harshBrake, harshAccel, tripCount int) int {
	if tripCount == 0 {
		return 100
	}
	score := 100
	score -= speeding * 5
	score -= harshBrake * 3
	score -= harshAccel * 3
	if score < 0 {
		score = 0
	}
	return score
}

// parseTripID 瑙ｆ瀽琛岀▼ ID锛堟牸寮忥細vehicleID_timestamp锛?
func parseTripID(tripID string) (time.Time, error) {
	idx := -1
	for i := len(tripID) - 1; i >= 0; i-- {
		if tripID[i] == '_' {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(tripID)-1 {
		return time.Time{}, fmt.Errorf("invalid trip id format")
	}
	ts, err := strconv.ParseInt(tripID[idx+1:], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp in trip id")
	}
	return time.Unix(ts, 0), nil
}
