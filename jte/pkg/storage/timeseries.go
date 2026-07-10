package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound 哨兵错误：查询的目标数据不存在
// CacheStorage / TimeSeriesStorage / ObjectStorage 在未命中时统一返回该错误，
// 便于上层判断是否需要回源到关系库或时序库
var ErrNotFound = errors.New("storage: not found")

// ===================================================================
// v2.0 百亿级轨迹数据存储方案 - 时序/缓存/对象存储分层接口
// 文档第七章 7.3：与现有 Interface 并存，按配置按需启用
// ===================================================================

// TimeSeriesRow 时序数据通用行（用于批量写入/查询的统一抽象）
type TimeSeriesRow struct {
	// Timestamp 时间戳（时序主键，自动索引）
	Timestamp time.Time
	// Tags 标签（对应 TDengine 子表 TAG，如 device_id/vehicle_id）
	Tags map[string]string
	// Fields 指标字段（如 longitude/latitude/speed）
	Fields map[string]interface{}
}

// AggregateRow 聚合查询结果行
type AggregateRow struct {
	// Timestamp 聚合窗口起始时间（对应 TDengine _irowts）
	Timestamp time.Time
	// Values 聚合结果，key 为字段名（如 avg_speed/max_speed/daily_mileage）
	Values map[string]float64
}

// LocationAgg 位置聚合统计（轨迹回放/报表专用）
type LocationAgg struct {
	Timestamp time.Time // 聚合窗口起始时间
	AvgSpeed  float64   // 平均速度
	MaxSpeed  float64   // 最高速度
	MinSpeed  float64   // 最低速度
	Mileage   float64   // 窗口内行驶里程（MAX(mileage)-MIN(mileage)）
}

// StorageStats 时序库存储统计
type StorageStats struct {
	TableCount       int64   // 子表数量
	TotalRows        int64   // 总行数
	AvgCompressRatio float64 // 平均压缩比（TDengine 通常 30:1）
	DiskUsedBytes    int64   // 磁盘已用字节
}

// DeviceOnlineState 设备在线状态（Redis 缓存热数据）
type DeviceOnlineState struct {
	DeviceID    string    `json:"device_id"`
	VehicleID   string    `json:"vehicle_id"`
	Phone       string    `json:"phone"`
	Online      bool      `json:"online"`
	RemoteAddr  string    `json:"remote_addr"`
	Protocol    string    `json:"protocol"`
	LastActive  time.Time `json:"last_active"`
	ReceivedAt  time.Time `json:"received_at"`
}

// AlarmStats 报警聚合统计
type AlarmStats struct {
	Total      int64            `json:"total"`
	ByType     map[string]int64 `json:"by_type"`
	ByLevel    map[int]int64    `json:"by_level"`
	BySource   map[string]int64 `json:"by_source"`
	FalseAlarm int64            `json:"false_alarm"`
}

// AlarmFilter 报警查询过滤条件
type AlarmFilter struct {
	DeviceID  string    `json:"device_id,omitempty"`
	VehicleID string    `json:"vehicle_id,omitempty"`
	Phone     string    `json:"phone,omitempty"`
	Source    string    `json:"source,omitempty"`
	Level     int       `json:"level,omitempty"`
	Type      string    `json:"type,omitempty"`
	Start     time.Time `json:"start,omitempty"`
	End       time.Time `json:"end,omitempty"`
}

// ===================================================================
// TimeSeriesStorage 时序存储专用接口（文档 7.3）
// TDengineStorage / IoTDBStorage / KingbaseTSStorage 实现
// ===================================================================

type TimeSeriesStorage interface {
	// 写入（批量，高并发，支撑千万点/秒）
	BatchWrite(ctx context.Context, table string, rows []TimeSeriesRow) error

	// 时间范围查询（核心，轨迹回放，<100ms）
	QueryRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) ([]TimeSeriesRow, error)

	// 最新值查询（实时监控，<10ms）
	QueryLast(ctx context.Context, table string, tags map[string]string) (*TimeSeriesRow, error)

	// 聚合查询（报表统计，<500ms）
	QueryAggregate(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration, aggFunc string) ([]AggregateRow, error)

	// 降采样查询（历史趋势）
	QueryDownsample(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration) ([]TimeSeriesRow, error)

	// 删除（按时间范围，配合 TTL）
	DeleteRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) error

	// 创建子表（按设备，对应 TDengine 一车一子表）
	CreateSubTable(ctx context.Context, stable string, subTable string, tags map[string]string) error

	// 获取存储统计
	GetStats(ctx context.Context) (*StorageStats, error)

	// 健康检查
	HealthCheck(ctx context.Context) error

	// 关闭
	Close() error
}

// ===================================================================
// 高频业务专用接口（基于 TimeSeriesStorage 的语义化封装）
// 文档 7.3：WriteLocation / QueryLocation / QueryLocationLatest 等
// ===================================================================

// LocationTimeSeries 位置轨迹时序操作（语义化封装）
type LocationTimeSeries interface {
	// 单条写入（入队异步处理）
	WriteLocation(ctx context.Context, loc *LocationData) error
	// 批量写入（直接批量 flush）
	WriteLocations(ctx context.Context, locs []*LocationData) error
	// 时间范围查询（轨迹回放）
	QueryLocation(ctx context.Context, deviceID string, start, end time.Time) ([]*LocationData, error)
	// 最新位置查询（实时监控）
	QueryLocationLatest(ctx context.Context, deviceID string) (*LocationData, error)
	// 聚合查询（报表统计）
	QueryLocationAggregate(ctx context.Context, deviceID string, start, end time.Time, interval time.Duration) ([]*LocationAgg, error)
}

// AlarmTimeSeries 报警时序操作
type AlarmTimeSeries interface {
	WriteAlarm(ctx context.Context, alarm *AlarmData) error
	QueryAlarm(ctx context.Context, filter AlarmFilter) ([]*AlarmData, error)
	QueryAlarmStats(ctx context.Context, filter AlarmFilter) (*AlarmStats, error)
}

// ===================================================================
// CANTimeSeries CAN 总线时序操作（文档 3.3 高频数据，1 秒 1 条）
// vehicle_can 超级表采用 can_id + can_value 的通用设计，兼容各类 CAN 报文
// ===================================================================

// CANData CAN 总线数据行
type CANData struct {
	DeviceID  string    `json:"device_id"`
	VehicleID string    `json:"vehicle_id"`
	CanID     int64     `json:"can_id"`     // CAN 报文 ID
	CanValue  []byte    `json:"can_value"`  // CAN 报文负载（原始字节）
	Time      time.Time `json:"time"`      // 采集时间
	ReceivedAt time.Time `json:"received_at"`
}

// CANTimeSeries CAN 总线时序操作接口
type CANTimeSeries interface {
	// 单条写入（入队异步处理）
	WriteCAN(ctx context.Context, data *CANData) error
	// 批量写入（直接 flush）
	WriteCANs(ctx context.Context, datas []*CANData) error
	// 时间范围查询（CAN 数据回放）
	QueryCAN(ctx context.Context, deviceID string, start, end time.Time) ([]*CANData, error)
}

// ===================================================================
// CacheStorage 缓存层接口（文档 7.3 CacheSet/CacheGet/CacheDelete）
// RedisStorage 实现
// ===================================================================

type CacheStorage interface {
	// 通用 K/V
	CacheSet(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	CacheGet(ctx context.Context, key string, out interface{}) error
	CacheDelete(ctx context.Context, key string) error

	// 设备在线状态（热点）
	SetOnlineState(ctx context.Context, state *DeviceOnlineState) error
	GetOnlineState(ctx context.Context, deviceID string) (*DeviceOnlineState, error)
	DeleteOnlineState(ctx context.Context, deviceID string) error
	ListOnlineStates(ctx context.Context) ([]*DeviceOnlineState, error)
	GetOnlineCount(ctx context.Context) (int64, error)

	// 最新位置（热点）
	SetLatestLocation(ctx context.Context, loc *LocationData) error
	GetLatestLocation(ctx context.Context, vehicleID string) (*LocationData, error)

	// 健康检查
	HealthCheck(ctx context.Context) error

	// 关闭
	Close() error
}

// ===================================================================
// ObjectStorage 对象存储接口（文档 7.3 ObjectPut/Get/Delete）
// MinIOStorage / S3Storage 实现
// ===================================================================

type ObjectStorage interface {
	// 通用对象操作
	ObjectPut(ctx context.Context, bucket, key string, data io.Reader) error
	ObjectGet(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	ObjectDelete(ctx context.Context, bucket, key string) error
	ObjectExists(ctx context.Context, bucket, key string) (bool, error)

	// Bucket 管理
	EnsureBucket(ctx context.Context, bucket string) error

	// 归档轨迹（Parquet/JSONL）
	ArchiveLocation(ctx context.Context, deviceID string, start, end time.Time, data io.Reader) (string, error)
	// GetArchivedLocation 按 archive_key 下载已归档的轨迹数据（AUTO-FIX-2026-07-02）
	// 内部使用 ArchiveBucket，修复 QueryArchivedLocation 传空 bucket 的 bug
	GetArchivedLocation(ctx context.Context, archiveKey string) (io.ReadCloser, error)

	// 视频文件（1078 原始录像）
	PutVideo(ctx context.Context, deviceID string, channelID int, key string, data io.Reader) (string, error)
	GetVideo(ctx context.Context, bucket, key string) (io.ReadCloser, error)

	// 健康检查
	HealthCheck(ctx context.Context) error

	// 关闭
	Close() error
}

// ===================================================================
// StorageV2 复合接口（按需启用，module.go 通过类型断言路由）
// ===================================================================

// StorageV2 v2.0 存储层聚合接口
// 关系数据走 Interface（DBStore），时序数据走 TimeSeriesStorage，
// 缓存走 CacheStorage，对象存储走 ObjectStorage。
// module.go 按配置按需初始化，未启用的层为 nil。
type StorageV2 interface {
	Interface // 嵌入 v1.x 关系存储接口（DBStore 实现）
}

// StorageLayers v2.0 多层存储容器（module.go 注入）
type StorageLayers struct {
	Relational Interface            // 关系层（必选，DBStore）
	TimeSeries TimeSeriesStorage    // 时序层（可选，TDengineStorage）
	Location   LocationTimeSeries   // 位置时序业务接口（可选）
	Alarm      AlarmTimeSeries      // 报警时序业务接口（可选）
	CAN        CANTimeSeries        // CAN 时序业务接口（可选）
	Cache      CacheStorage         // 缓存层（可选，RedisStorage）
	Object     ObjectStorage        // 对象存储层（可选，MinIOStorage）
}

// ===================================================================
// LocationArchiveFallback 归档数据查询 fallback 接口（AUTO-FIX-2026-07-02）
// 当 TDengine/关系层数据已被归档删除时，通过此接口从 MinIO 查询已归档的历史轨迹。
// archive.Archiver 实现此接口，注入到 TDengine.Storage 和 DBStore 中。
// ===================================================================
type LocationArchiveFallback interface {
	// IsArchived 检查某设备某日数据是否已归档完成
	IsArchived(ctx context.Context, deviceID string, date time.Time) bool
	// QueryArchivedLocation 从归档存储（MinIO）查询历史轨迹
	QueryArchivedLocation(ctx context.Context, deviceID string, start, end time.Time) ([]*LocationData, error)
}

// ===================================================================
// v3.0 P2 #14：Prometheus 指标暴露（可选接口）
// 时序存储实现该接口后，API 层 /metrics 端点会调用 WriteMetrics 输出 Prometheus 文本格式
// 未实现该接口的存储层不影响其他功能
// ===================================================================

// MetricsExporter Prometheus 指标导出接口（可选实现）
// 实现：tdengine.Storage（通过 WritePrometheus 输出 20+ 指标）
type MetricsExporter interface {
	// WriteMetrics 将 Prometheus 文本格式指标写入 w
	// 调用方：API /metrics 端点
	WriteMetrics(w io.Writer) error
}
