package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// ArchiveRunner 归档器抽象（archive.Archiver 实现该接口）
type ArchiveRunner interface {
	RunOnce(ctx context.Context)
}

// ArchiveProgressProvider 归档进度查询回调（AUTO-FIX-2026-07-02）
// 由于 module-storage 是独立 Go 模块，handler 无法直接 import archive 包，
// 通过此回调将 archiver 的进度数据以 any 类型传回 handler。
// 返回值：(当前进度快照, 上次运行结果, 是否正在运行)
type ArchiveProgressProvider func() (progress any, lastResult any, isRunning bool)

// ArchiveLicenseValidator 归档授权校验接口（AUTO-FIX-2026-06-30 [集成-6]）。
// 由 module.LicenseManager 实现，handler 通过此接口解耦。
// nil 时跳过校验（向后兼容）。
type ArchiveLicenseValidator interface {
	ValidateArchive() error
}

// StorageHandler 存储分层管理 API
type StorageHandler struct {
	cfg              *config.Config
	logger           *zap.Logger
	timeSeries       storage.TimeSeriesStorage // 时序层（可选）
	cache            storage.CacheStorage      // 缓存层（可选）
	object           storage.ObjectStorage     // 对象层（可选）
	archiver         ArchiveRunner             // 归档器（可选）
	licenseValidator ArchiveLicenseValidator  // 归档授权校验（集成-6，可选）
	// AUTO-FIX-2026-07-02: 归档进度查询回调（可选）
	// 由 module.go 注入，通过 SetArchiveProgressProvider 设置
	// 未注入时 /archive/progress 端点返回基本状态信息
	progressProvider ArchiveProgressProvider
	// AUTO-FIX-2026-07-02 [可观测性]: 保留原始 StorageLayers 引用，供健康检查提取 *sql.DB
	layers           *storage.StorageLayers
}

// NewStorageHandler 构造存储管理 handler
func NewStorageHandler(cfg *config.Config, logger *zap.Logger) *StorageHandler {
	return &StorageHandler{cfg: cfg, logger: logger}
}

// SetStorageLayers 注入多层存储实例（按需）
func (h *StorageHandler) SetStorageLayers(layers *storage.StorageLayers) {
	if layers == nil {
		return
	}
	h.layers = layers
	h.timeSeries = layers.TimeSeries
	h.cache = layers.Cache
	h.object = layers.Object
}

// GetStorageLayers 返回注入的多层存储实例（供健康检查提取底层 *sql.DB）
func (h *StorageHandler) GetStorageLayers() *storage.StorageLayers {
	return h.layers
}

// SetArchiver 注入归档器
func (h *StorageHandler) SetArchiver(a ArchiveRunner) {
	h.archiver = a
}

// SetArchiveProgressProvider 注入归档进度查询回调（AUTO-FIX-2026-07-02）
// 由 module.go 在初始化归档器后注入，使 /archive/progress 端点可查询实时进度
func (h *StorageHandler) SetArchiveProgressProvider(p ArchiveProgressProvider) {
	h.progressProvider = p
}

// SetLicenseValidator 注入授权校验器（AUTO-FIX-2026-06-30 [集成-6]）。
// 归档操作前校验授权 features 是否包含 "archive"。
func (h *StorageHandler) SetLicenseValidator(v ArchiveLicenseValidator) {
	h.licenseValidator = v
}

// Stats 返回时序库存储统计
// GET /api/v1/storage/stats
func (h *StorageHandler) Stats(c *gin.Context) {
	if h.timeSeries == nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"enabled":        false,
				"message":        "time series storage not configured",
				"table_count":    0,
				"total_rows":     0,
				"compress_ratio": 0,
				"disk_used_bytes": 0,
			},
		})
		return
	}
	stats, err := h.timeSeries.GetStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"enabled":         true,
			"table_count":     stats.TableCount,
			"total_rows":      stats.TotalRows,
			"compress_ratio":  stats.AvgCompressRatio,
			"disk_used_bytes": stats.DiskUsedBytes,
		},
	})
}

// Metrics Prometheus 指标端点
// GET /metrics
// 输出 Prometheus 文本格式（含 tdengine_write_total / tdengine_query_duration / tdengine_breaker_state 等 20+ 指标）
// 若时序存储未实现 MetricsExporter 接口，返回基础进程指标
func (h *StorageHandler) Metrics(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// 通过类型断言检查是否实现 MetricsExporter 接口
	exporter, ok := h.timeSeries.(storage.MetricsExporter)
	if !ok || exporter == nil {
		// 未实现接口，返回基础信息
		c.String(http.StatusOK, "# time series storage metrics not available\n")
		return
	}

	if err := exporter.WriteMetrics(c.Writer); err != nil {
		h.logger.Warn("write metrics failed", zap.Error(err))
	}
}

// TTLConfig TTL 配置请求
type TTLConfig struct {
	LocationKeepDays int `json:"location_keep_days"`
	ArchiveKeepDays  int `json:"archive_keep_days"`
}

// GetTTL 返回当前存储 TTL 配置
// GET /api/v1/storage/ttl
func (h *StorageHandler) GetTTL(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"location_keep_days": cfg.Storage.TimeSeries.KeepDays,
			"archive_keep_days":  cfg.Storage.Archive.KeepDays,
			"retention_days":     cfg.Storage.RetentionDays,
		},
	})
}

// UpdateTTL 更新存储 TTL 配置（运行时生效，不持久化）
// PUT /api/v1/storage/ttl
func (h *StorageHandler) UpdateTTL(c *gin.Context) {
	var req TTLConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "config not available"})
		return
	}
	applied := []string{}
	if req.LocationKeepDays > 0 {
		cfg.Storage.TimeSeries.KeepDays = req.LocationKeepDays
		applied = append(applied, "location_keep_days")
	}
	if req.ArchiveKeepDays > 0 {
		cfg.Storage.Archive.KeepDays = req.ArchiveKeepDays
		applied = append(applied, "archive_keep_days")
	}
	h.logger.Info("storage ttl updated",
		zap.Int("location_keep_days", cfg.Storage.TimeSeries.KeepDays),
		zap.Int("archive_keep_days", cfg.Storage.Archive.KeepDays))
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "updated",
		"data": gin.H{
			"applied":           applied,
			"location_keep_days": cfg.Storage.TimeSeries.KeepDays,
			"archive_keep_days":  cfg.Storage.Archive.KeepDays,
		},
	})
}

// ArchiveStatus 返回归档任务状态
// GET /api/v1/storage/archive/status
func (h *StorageHandler) ArchiveStatus(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"enabled": false}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"enabled":       cfg.Storage.Archive.Enabled,
			"interval_hours": cfg.Storage.Archive.IntervalHours,
			"keep_days":     cfg.Storage.Archive.KeepDays,
			"batch_days":    cfg.Storage.Archive.BatchDays,
			"dry_run":       cfg.Storage.Archive.DryRun,
			"archiver_loaded": h.archiver != nil,
		},
	})
}

// TriggerArchive 手动触发一次归档
// POST /api/v1/storage/archive/trigger
func (h *StorageHandler) TriggerArchive(c *gin.Context) {
	if h.archiver == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "archiver not configured"})
		return
	}
	// AUTO-FIX-2026-06-30 [集成-6]: 归档功能授权校验
	if h.licenseValidator != nil {
		if err := h.licenseValidator.ValidateArchive(); err != nil {
			h.logger.Warn("archive not licensed, rejecting trigger",
				zap.Error(err))
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "archive feature not licensed: " + err.Error(),
			})
			return
		}
	}
	// 异步执行，避免请求超时
	util.SafeGo(h.logger, "handler.storage.async", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		h.archiver.RunOnce(ctx)
		h.logger.Info("manual archive run completed")
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "archive triggered"})
}

// ArchiveProgress 返回归档任务实时进度和上次运行结果（AUTO-FIX-2026-07-02）
// GET /api/v1/storage/archive/progress
// 响应：
//   - running: 归档任务是否正在执行
//   - progress: 当前进度快照（设备总数/已扫描/当前设备/已归档行数/进度百分比/时间窗口）
//   - last_result: 上次运行结果（扫描设备数/归档行数/删除行数/失败次数/是否成功/时间窗口）
//   - archiver_loaded: 归档器是否已加载
func (h *StorageHandler) ArchiveProgress(c *gin.Context) {
	if h.progressProvider == nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"archiver_loaded": h.archiver != nil,
				"running":         false,
				"message":         "archive progress provider not configured",
			},
		})
		return
	}

	progress, lastResult, isRunning := h.progressProvider()

	data := gin.H{
		"archiver_loaded": h.archiver != nil,
		"running":         isRunning,
	}
	if progress != nil {
		data["progress"] = progress
	}
	if lastResult != nil {
		data["last_result"] = lastResult
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": data,
	})
}

// CacheHitRate 返回缓存层统计
// GET /api/v1/storage/cache/hitrate
// 注：CacheStorage 接口未暴露 hit/miss 计数器，此处返回在线状态条数等基础统计
func (h *StorageHandler) CacheHitRate(c *gin.Context) {
	if h.cache == nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"enabled":       false,
				"message":       "cache storage not configured",
				"online_count":  0,
			},
		})
		return
	}
	onlineCount, err := h.cache.GetOnlineCount(c.Request.Context())
	if err != nil {
		h.logger.Warn("get online count from cache failed", zap.Error(err))
		onlineCount = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"enabled":      true,
			"online_count": onlineCount,
			"note":         "CacheStorage 接口未暴露 hit/miss 计数器，仅返回在线状态条数",
		},
	})
}

// [商业版] ClusterStatus 返回 TDengine 集群状态
// GET /api/v1/storage/cluster/status
func (h *StorageHandler) ClusterStatus(c *gin.Context) {
	layers := h.GetStorageLayers()
	if layers == nil || layers.TimeSeries == nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{"enabled": false, "message": "time series storage not configured"},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"enabled":   true,
			"message":   "TDengine cluster status requires runtime query",
		},
	})
}

// [商业版] TierStats 返回冷热分层数据量统计
// GET /api/v1/storage/tier/stats
func (h *StorageHandler) TierStats(c *gin.Context) {
	layers := h.GetStorageLayers()
	hotEnabled := layers != nil && layers.TimeSeries != nil
	warmEnabled := layers != nil && layers.Cache != nil
	coldEnabled := layers != nil && layers.Object != nil
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"hot_enabled":  hotEnabled,
			"warm_enabled": warmEnabled,
			"cold_enabled": coldEnabled,
			"note":         "Detailed tier statistics require runtime query",
		},
	})
}
