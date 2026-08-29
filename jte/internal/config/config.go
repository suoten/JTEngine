package config

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type GatewayConfig struct {
	TCPPort           int `mapstructure:"tcp_port"`
	UDPPort           int `mapstructure:"udp_port"`
	ReadTimeout       int `mapstructure:"read_timeout"`
	WriteTimeout      int `mapstructure:"write_timeout"`
	HeartbeatInterval int `mapstructure:"heartbeat_interval"`
	HeartbeatTimeout  int `mapstructure:"heartbeat_timeout"`
	MaxConnections    int `mapstructure:"max_connections"`
	MaxDevices        int `mapstructure:"max_devices"`
	// FIXED-2026-07-22 [P0]: 初始认证超时（秒），连接建立后在此时间内必须完成注册+鉴权。
	// 默认 30s，超时未认证的连接主动关闭并记录日志。
	InitialAuthTimeout int `mapstructure:"initial_auth_timeout"`
	// FIXED-2026-07-22 [P0]: 单 IP 最大并发连接数，默认 100。
	MaxConnsPerIP int `mapstructure:"max_conns_per_ip"`
	// FIXED-2026-07-22 [P0]: 单 IP 连接速率限制（每秒新建连接数），默认 50。
	MaxConnRatePerIP int `mapstructure:"max_conn_rate_per_ip"`
	// FIXED-2026-07-23 [P2]: JT/T 协议是否启用 TLS（非标准，部分客户要求时开启）
	TLSEnabled bool `mapstructure:"tls_enabled"`
	// OOM 内存防护配置（按文档第9章存储/稳定性要求）
	OOMProtect OOMProtectConfig `mapstructure:"oom_protect"`
}

// OOMProtectConfig 内存 OOM 防护配置
type OOMProtectConfig struct {
	// 是否启用 OOM 防护
	Enabled bool `mapstructure:"enabled"`
	// 内存告警阈值（MB）
	WarnMB int `mapstructure:"warn_mb"`
	// 内存危险阈值（MB）
	CriticalMB int `mapstructure:"critical_mb"`
	// 内存致命阈值（MB），触发主动熔断
	FatalMB int `mapstructure:"fatal_mb"`
	// 内存检查间隔（秒，默认 5）
	CheckIntervalSeconds int `mapstructure:"check_interval_seconds"`
}

type APIConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	Port           int      `mapstructure:"port"`
	CORSOrigins    []string `mapstructure:"cors_origins"`
	RateLimit      int      `mapstructure:"rate_limit"`
	JWTSecret      string   `mapstructure:"jwt_secret"`
	JWTExpireHours int      `mapstructure:"jwt_expire_hours"`
	// JWT 多密钥轮换配置（A.2.1 安全防护），为空则回退到 JWTSecret
	JWT *JWTConfig `mapstructure:"jwt"`
	// AUTO-FIX-2026-07-02 [等保2.0 传输安全]: TLS/HTTPS 配置
	TLS *TLSConfig `mapstructure:"tls"`
	// AUTO-FIX-2026-07-02 [等保2.0 传输安全]: 强制 HTTPS，true 时 HTTP 请求返回 426
	RequireTLS bool `mapstructure:"require_tls"`
	// Security API 安全配置（连接限制/请求体大小限制）
	Security *APISecurityConfig `mapstructure:"security"`
}

// APISecurityConfig API 安全配置（连接限制/请求体大小限制）
type APISecurityConfig struct {
	// ConnLimitPerIP 每 IP 并发连接限制（默认 100）
	ConnLimitPerIP int `mapstructure:"conn_limit_per_ip"`
	// BodyLimitBytes 请求体最大字节数（默认 10MB）
	BodyLimitBytes int `mapstructure:"body_limit_bytes"`
}

// TLSConfig TLS/HTTPS 传输安全配置（等保2.0 三级 - 通信完整性 & 机密性）
type TLSConfig struct {
	// Enabled 是否启用 TLS
	Enabled bool `mapstructure:"enabled"`
	// CertFile 证书文件路径（PEM 格式）
	CertFile string `mapstructure:"cert_file"`
	// KeyFile 私钥文件路径（PEM 格式）
	KeyFile string `mapstructure:"key_file"`
	// AutoRenew 是否启用证书自动续期（Let's Encrypt / 自签 CA）
	AutoRenew bool `mapstructure:"auto_renew"`
	// MinVersion 最小 TLS 版本（"1.2" / "1.3"，默认 "1.2"）
	MinVersion string `mapstructure:"min_version"`
	// ACME 启用 Let's Encrypt 自动证书（需域名 + 80 端口可用）
	ACME bool `mapstructure:"acme"`
	// ACMEDir ACME 目录（缓存证书）
	ACMEDir string `mapstructure:"acme_dir"`
	// ACMEDomains ACME 域名列表
	ACMEDomains []string `mapstructure:"acme_domains"`
}

// JWTConfig JWT 密钥轮换配置，支持多 kid 并存以便平滑过渡
type JWTConfig struct {
	Secrets     map[string]string `mapstructure:"secrets"`     // kid -> secret，支持多密钥
	ActiveKid   string            `mapstructure:"active_kid"`  // 当前签发用的 kid
	RotateDays  int               `mapstructure:"rotate_days"` // 轮换天数，默认 90
	// AUTO-FIX-2026-06-30 [P1-6]: KMS 密钥来源，禁止主配置文件明文存储。
	//   env  — 从环境变量 JTE_JWT_SECRET_<KID> 加载（推荐 K8s Secret 注入）
	//   file — 从独立文件 kms_file_path 加载（推荐 Vault/KMS 挂载）
	//   config/空 — 回退到 secrets 字段（向后兼容，生产不推荐）
	KMSSource   string `mapstructure:"kms_source"`
	KMSFilePath string `mapstructure:"kms_file_path"`

	// 运行时维护，不参与序列化
	kidCreatedAt map[string]time.Time `mapstructure:"-"`
	lastCleanup  time.Time            `mapstructure:"-"`
	mu           sync.RWMutex         `mapstructure:"-"`
}

// GetSecret 根据 kid 获取密钥（线程安全）
func (c *JWTConfig) GetSecret(kid string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	secret, ok := c.Secrets[kid]
	return secret, ok
}

// GetActiveSecret 获取当前签发用的 kid 和 secret
func (c *JWTConfig) GetActiveSecret() (kid, secret string, ok bool) {
	if c == nil {
		return "", "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ActiveKid == "" {
		return "", "", false
	}
	secret, ok = c.Secrets[c.ActiveKid]
	return c.ActiveKid, secret, ok
}

// RotateJWTKey 轮换到新密钥，旧 kid 保留 7 天仅供验签
func (c *JWTConfig) RotateJWTKey(newKid, newSecret string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Secrets == nil {
		c.Secrets = make(map[string]string)
	}
	c.Secrets[newKid] = newSecret
	if c.kidCreatedAt == nil {
		c.kidCreatedAt = make(map[string]time.Time)
	}
	c.kidCreatedAt[newKid] = time.Now()
	c.ActiveKid = newKid
}

// CleanupExpiredKids 清理超过 7 天的旧 kid（仅验签用的旧密钥）
func (c *JWTConfig) CleanupExpiredKids() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// 每小时最多执行一次
	if time.Since(c.lastCleanup) < time.Hour {
		return
	}
	c.lastCleanup = time.Now()
	if c.kidCreatedAt == nil || len(c.Secrets) == 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	for kid, createdAt := range c.kidCreatedAt {
		if kid == c.ActiveKid {
			continue
		}
		if createdAt.Before(cutoff) {
			delete(c.Secrets, kid)
			delete(c.kidCreatedAt, kid)
		}
	}
}

// GetActiveKidCreatedAt 返回当前 active kid 的创建时间（P1-6 轮换调度用）。
// 若 active kid 无创建记录，返回零值和 false。
func (c *JWTConfig) GetActiveKidCreatedAt() (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.kidCreatedAt[c.ActiveKid]
	return t, ok
}

// EnsureKidCreatedAt 为所有无创建记录的 kid 补记创建时间（P1-6 KMS 加载后调用）。
func (c *JWTConfig) EnsureKidCreatedAt() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kidCreatedAt == nil {
		c.kidCreatedAt = make(map[string]time.Time)
	}
	for kid := range c.Secrets {
		if _, ok := c.kidCreatedAt[kid]; !ok {
			c.kidCreatedAt[kid] = time.Now()
		}
	}
}

// SetSecretsFromKMS 用 KMS 加载的密钥替换 Secrets（P1-6）。
// activeKid 非空时同时设置 ActiveKid。会为所有新 kid 初始化创建时间。
func (c *JWTConfig) SetSecretsFromKMS(secrets map[string]string, activeKid string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Secrets = secrets
	if activeKid != "" {
		c.ActiveKid = activeKid
	}
	if c.kidCreatedAt == nil {
		c.kidCreatedAt = make(map[string]time.Time)
	}
	for kid := range secrets {
		if _, ok := c.kidCreatedAt[kid]; !ok {
			c.kidCreatedAt[kid] = time.Now()
		}
	}
}

// SetTestKidCreatedAt 设置指定 kid 的创建时间（仅供测试使用，模拟密钥到期场景）。
func (c *JWTConfig) SetTestKidCreatedAt(kid string, t time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kidCreatedAt == nil {
		c.kidCreatedAt = make(map[string]time.Time)
	}
	c.kidCreatedAt[kid] = t
}

type WebSocketConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	Path           string `mapstructure:"path"`
	WriteBufferSize int   `mapstructure:"write_buffer_size"`
	ReadBufferSize  int   `mapstructure:"read_buffer_size"`
}

type StorageConfig struct {
	Type string `mapstructure:"type"`
	DSN  string `mapstructure:"dsn"`
	// AUTO-FIX-2026-06-26: 第五轮存储修复 - 数据归档保留时长（天）
	// 设置后定时清理超过保留时长的历史数据，0 表示不清理（默认 90 天）
	RetentionDays int `mapstructure:"retention_days"`

	// v2.0 百亿级轨迹数据存储方案 - 时序库/缓存/对象存储分离配置
	// 关系数据仍由 Type/DSN 指向（MySQL/达梦/金仓/高斯/SQLite），
	// 时序数据写入 TimeSeries，热点数据走 Cache，归档数据入 Object。
	TimeSeries TimeSeriesConfig `mapstructure:"time_series"`
	Cache      CacheConfig       `mapstructure:"cache"`
	Object     ObjectConfig      `mapstructure:"object"`
	// AUTO-FIX-2026-06-28: 离线归档任务配置（TDengine→MinIO jsonl）
	Archive ArchiveConfig `mapstructure:"archive"`
}

// ArchiveConfig 离线归档任务配置
// AUTO-FIX-2026-06-28: 自动将 TDengine 中超过 KeepDays 的轨迹数据
// 按 BatchDays 时间窗口导出为 JSONL 上传到 MinIO ArchiveBucket，并从 TDengine 删除
// AUTO-FIX-2026-07-02: Enabled 默认 true（确保 3 年以上历史轨迹归档自动生效）；
// 新增 ScheduleHour（每日定点调度）、DeleteDelayDays（延迟删除）、MaxDeviceScan、Alert 告警配置
type ArchiveConfig struct {
	// 是否启用归档任务，默认 true（AUTO-FIX-2026-07-02: 从 false 改为 true）
	// 确保历史轨迹数据自动迁移到 MinIO，避免 TDengine 数据膨胀
	Enabled bool `mapstructure:"enabled"`
	// 调度间隔（小时，默认 24），即多久执行一次归档
	// 仅当 ScheduleHour <= 0 时生效；ScheduleHour > 0 时按每日定点调度
	IntervalHours int `mapstructure:"interval_hours"`
	// 保留天数（与 TDengine KEEP 对齐），归档此天数之前的数据
	// 留空则使用 time_series.keep_days；都为 0 时不归档
	KeepDays int `mapstructure:"keep_days"`
	// TTL 保留缓冲天数，TTL = KeepDays + TTLBufferDays
	TTLBufferDays int `mapstructure:"ttl_buffer_days"`
	// 单次归档时间窗口（天，默认 1），过大可能导致单批数据量过高
	BatchDays int `mapstructure:"batch_days"`
	// DryRun 仅上传不删除，用于首次验证归档链路
	DryRun bool `mapstructure:"dry_run"`
	// ScheduleHour 每日定点调度小时（0-23，默认 2 = 凌晨 2 点）
	// 大于 0 时按每日定点调度（每天凌晨 ScheduleHour 点执行）；
	// 等于 0 时回退到 IntervalHours 间隔调度（兼容旧行为）
	// AUTO-FIX-2026-07-02: 新增字段
	ScheduleHour int `mapstructure:"schedule_hour"`
	// MaxDeviceScan 单次任务最多扫描的设备数，0 表示不限制（默认 0）
	// 大规模车队场景下用于限制单次归档时长，避免影响在线业务
	// AUTO-FIX-2026-07-02: 新增字段
	MaxDeviceScan int `mapstructure:"max_device_scan"`
	// DeleteDelayDays 归档完成后延迟删除天数（默认 7）
	// 归档标记写入后等待此天数再从 TDengine 物理删除，确保查询 fallback 窗口
	// AUTO-FIX-2026-07-02: 新增字段
	DeleteDelayDays int `mapstructure:"delete_delay_days"`
	// Alert 归档失败告警配置（钉钉/企业微信/webhook）
	// AUTO-FIX-2026-07-02: 新增字段
	Alert ArchiveAlertConfig `mapstructure:"alert"`
	// MarkerStoreDriver 归档标记存储驱动（sqlite3/mysql），默认 sqlite3
	// 集群部署时应配置 mysql 以共享标记数据
	// AUTO-FIX-2026-07-02: 新增字段
	MarkerStoreDriver string `mapstructure:"marker_store_driver"`
	// MarkerStoreDSN 归档标记存储 DSN
	// sqlite3: ./archive_markers.db  mysql: user:pass@tcp(host:3306)/db
	// AUTO-FIX-2026-07-02: 新增字段
	MarkerStoreDSN string `mapstructure:"marker_store_dsn"`
}

// ArchiveAlertConfig 归档失败告警配置（AUTO-FIX-2026-07-02）
// 支持钉钉机器人、企业微信机器人、通用 webhook 三种格式（按 URL 自动识别）
type ArchiveAlertConfig struct {
	// WebhookURL 告警 webhook 地址
	// 钉钉: https://oapi.dingtalk.com/robot/send?access_token=xxx
	// 企业微信: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx
	// 通用 webhook: https://your-alert.example.com/api/notify
	// 留空则不告警
	WebhookURL string `mapstructure:"webhook_url"`
	// AlertOnUploadFail 上传 MinIO 失败时告警（默认 true）
	AlertOnUploadFail bool `mapstructure:"alert_on_upload_fail"`
	// AlertOnDeleteFail 从 TDengine 删除失败时告警（默认 true）
	AlertOnDeleteFail bool `mapstructure:"alert_on_delete_fail"`
	// AlertThreshold 连续失败次数达到阈值才告警（默认 1，即首次失败即告警）
	AlertThreshold int `mapstructure:"alert_threshold"`
}

// TimeSeriesConfig 时序数据库配置（TDengine 主推，IoTDB/金仓时序备选）
// 文档第三章：超级表+子表+TTL+异步批量写入
type TimeSeriesConfig struct {
	// 驱动类型：tdengine / iotdb / kingbase_ts，空则不启用时序层
	Driver string `mapstructure:"driver"`
	// 主机（TDengine 默认 6030，IoTDB 默认 6667）
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// 用户/密码（TDengine 默认 root/<参见官方文档>）
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	// 数据库名（默认 jte_ts）
	Database string `mapstructure:"database"`
	// 数据保留时长（天），对应 TDengine KEEP 参数，0 表示用驱动默认
	// 文档第五章：位置 365d、报警 1095d、CAN 90d，此处为全局默认
	KeepDays int `mapstructure:"keep_days"`
	// 异步批量写入大小（文档默认 1000）
	BatchSize int `mapstructure:"batch_size"`
	// 异步写入缓冲通道大小（默认 10000）
	BufferSize int `mapstructure:"buffer_size"`
	// 异步 flush 间隔（毫秒，默认 100ms）
	FlushIntervalMs int `mapstructure:"flush_interval_ms"`

	// AUTO-FIX-2026-06-28: 集群部署参数（百亿级场景分片策略）
	// 单节点部署全部留空使用默认值即可；集群部署按规模调整
	// 副本数（1/3，默认 1；生产环境建议 3 保证高可用）
	Replica int `mapstructure:"replica"`
	// 超级表初始 VGroup 数（分片数，默认 10；百亿级场景建议 100+，按设备数*频率估算）
	VGroups int `mapstructure:"vgroups"`
	// 数据文件存储周期（默认 10；与 KEEP 配合决定冷热分级）
	Days int `mapstructure:"days"`
	// 每个 VGroup 内存块数（默认 6；写入吞吐不足时调大）
	Blocks int `mapstructure:"blocks"`

	// v3.0 P0 #5：ws/unified WebSocket 连接（跨平台，无需安装客户端）
	// 启用后优先走 Stmt2 写入路径（千万点/秒）+ LAST_ROW 查询（<10ms）
	// 不启用则回退 database/sql + REST/原生驱动
	WSEnabled bool `mapstructure:"ws_enabled"`
	// WSDSN WebSocket DSN，支持多节点故障切换（v3.8.0+）：
	//   单节点：root:<password>@ws(127.0.0.1:6041)/jte_ts
	//   多节点：root:<password>@ws(node1:6041,node2:6041,node3:6041)/jte_ts?autoReconnect=true&reconnectRetryCount=10
	// 留空则根据 Host/Port 自动构造单节点 DSN
	WSDSN string `mapstructure:"ws_dsn"`
	// WSNodes 多节点列表（与 WSDSN 二选一），如 ["node1:6041","node2:6041","node3:6041"]
	// 配置后自动构造多节点故障切换 DSN
	WSNodes []string `mapstructure:"ws_nodes"`
	// WSSkipVerify TLS 跳过证书校验（wss 时生效，默认 false=严格校验）
	// 生产环境必须 false；仅开发/测试环境可设 true
	WSSkipVerify bool `mapstructure:"ws_skip_verify"`
	// WSAutoReconnect 自动重连（默认 true）
	WSAutoReconnect bool `mapstructure:"ws_auto_reconnect"`
	// WSReconnectRetryCount 重连次数（默认 10）
	WSReconnectRetryCount int `mapstructure:"ws_reconnect_retry_count"`
	// WSReadTimeout 读超时（毫秒，0=驱动默认）
	WSReadTimeoutMs int `mapstructure:"ws_read_timeout_ms"`
	// WSWriteTimeout 写超时（毫秒，0=驱动默认）
	WSWriteTimeoutMs int `mapstructure:"ws_write_timeout_ms"`

	// v3.0 P2 #11：TLS 安全连接
	// 启用后使用 wss 协议；证书校验由 WSSkipVerify 控制
	WSTLSEnabled bool `mapstructure:"ws_tls_enabled"`

	// v3.0 P2 #12：Worker Pool 并行批量写入
	// 启用后 writeChan 不再由单 asyncWriter 消费，而是按 deviceID 哈希分发到多 worker
	// 适用于高并发写入场景（>1万点/秒）
	WPEnabled bool `mapstructure:"wp_enabled"`
	// WPWorkerCount worker 数量（默认 runtime.NumCPU()，上限 16）
	WPWorkerCount int `mapstructure:"wp_worker_count"`
	// WPBatchSize 每个 worker 的批次大小（默认与 batch_size 对齐）
	WPBatchSize int `mapstructure:"wp_batch_size"`
	// WPFlushIntervalMs worker flush 间隔（默认与 flush_interval_ms 对齐）
	WPFlushIntervalMs int `mapstructure:"wp_flush_interval_ms"`
	// WPQueueSize 每个 worker 的本地队列大小（默认 10000）
	WPQueueSize int `mapstructure:"wp_queue_size"`

	// v3.0 P0 #3 补充：SchemalessInsert 无模式写入
	// 启用后支持动态字段扩展（车型/传感器差异、第三方设备自定义字段）
	// 与 Stmt2 路径互补：Stmt2 主力高吞吐，Schemaless 灵活扩展
	SchemalessEnabled bool `mapstructure:"schemaless_enabled"`
	// SchemalessProtocol 写入协议（1=InfluxDB Line, 2=OpenTSDB Telnet, 3=OpenTSDB JSON）
	// 默认 1（InfluxDB Line，生态最广）
	SchemalessProtocol int `mapstructure:"schemaless_protocol"`
	// SchemalessPrecision 时间精度（ms/us/ns/s，默认 ms）
	SchemalessPrecision string `mapstructure:"schemaless_precision"`
	// SchemalessTTL 数据保留时长（天，0=使用数据库默认 KEEP）
	SchemalessTTL int `mapstructure:"schemaless_ttl"`
	// SchemalessTableNameKey 子表名提取键（默认 device_id）
	SchemalessTableNameKey string `mapstructure:"schemaless_table_name_key"`
	// SchemalessBatchSize 单批次最大行数（默认 1000）
	SchemalessBatchSize int `mapstructure:"schemaless_batch_size"`
	// SchemalessFlushIntervalMs flush 间隔（默认 100ms）
	SchemalessFlushIntervalMs int `mapstructure:"schemaless_flush_interval_ms"`

	// v3.0 P0 #5 补充：TMQ 数据订阅（实时报警推送）
	// 启用后创建 TMQ 消费者订阅指定 topic，新数据实时推送到 handler
	// 需先在 TDengine 中执行 CREATE TOPIC（见 schema.sql）
	TMQEnabled bool `mapstructure:"tmq_enabled"`
	// TMQTopics 订阅主题列表
	// 示例：["topic_vehicle_alarm","topic_vehicle_location"]
	TMQTopics []string `mapstructure:"tmq_topics"`
	// TMQGroupID 消费者组（同组共同消费，不同组独立消费）
	TMQGroupID string `mapstructure:"tmq_group_id"`
	// TMQClientID 消费者客户端 ID
	TMQClientID string `mapstructure:"tmq_client_id"`
	// TMQAutoCommit 自动提交 offset（默认 true）
	TMQAutoCommit bool `mapstructure:"tmq_auto_commit"`
	// TMQAutoCommitIntervalMs 自动提交间隔（毫秒，默认 5000）
	TMQAutoCommitIntervalMs int `mapstructure:"tmq_auto_commit_interval_ms"`
	// TMQPollTimeoutMs 单次 Poll 阻塞时长（毫秒，默认 500）
	TMQPollTimeoutMs int `mapstructure:"tmq_poll_timeout_ms"`
}

// CacheConfig Redis 缓存层配置
// 文档第二章：在线设备状态、最新位置、会话缓存、限流计数
type CacheConfig struct {
	// 驱动：redis，空则不启用缓存层
	Driver string `mapstructure:"driver"`
	// 部署模式：single | sentinel | cluster，默认 single
	Mode string `mapstructure:"mode"`
	// Redis 地址 host:port（默认 127.0.0.1:6379）
	Addr string `mapstructure:"addr"`
	// Sentinel 模式：主节点名称（master name）
	MasterName string `mapstructure:"master_name"`
	// Sentinel 模式：哨兵节点地址列表
	SentinelAddrs []string `mapstructure:"sentinel_addrs"`
	// Cluster 模式：集群节点地址列表
	ClusterAddrs []string `mapstructure:"cluster_addrs"`
	// 密码（无密码则留空）
	Password string `mapstructure:"password"`
	// DB 编号（默认 0）
	DB int `mapstructure:"db"`
	// 连接池大小（默认 10）
	PoolSize int `mapstructure:"pool_size"`
	// Key 前缀，多租户/多实例隔离
	KeyPrefix string `mapstructure:"key_prefix"`
	// 最新位置缓存 TTL（秒，默认 300=5 分钟）
	LatestLocationTTL int `mapstructure:"latest_location_ttl"`
	// 在线状态缓存 TTL（秒，默认 120=2 分钟，需小于网关心跳超时）
	OnlineStatusTTL int `mapstructure:"online_status_ttl"`
}

// ObjectConfig 对象存储配置（MinIO/S3 兼容）
// 文档第二章/第五章：3 年以上历史轨迹归档、原始视频文件存储
type ObjectConfig struct {
	// 驱动：minio / s3，空则不启用对象存储
	Driver string `mapstructure:"driver"`
	// Endpoint（MinIO 默认 127.0.0.1:9000；S3 形如 s3.cn-north-1.amazonaws.com.cn）
	Endpoint string `mapstructure:"endpoint"`
	// AccessKey / SecretKey
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	// 默认 Bucket（如 jte-archive）
	Bucket string `mapstructure:"bucket"`
	// Region（MinIO 可留空，S3 必填）
	Region string `mapstructure:"region"`
	// 是否启用 SSL/TLS
	UseSSL bool `mapstructure:"use_ssl"`
	// 归档 Bucket（用于历史轨迹 Parquet/JSONL 归档，可与 Bucket 不同）
	ArchiveBucket string `mapstructure:"archive_bucket"`
	// 视频 Bucket（用于 1078 原始录像存储）
	VideoBucket string `mapstructure:"video_bucket"`
}

type LoggingConfig struct {
	Level    string `mapstructure:"level"`
	Format   string `mapstructure:"format"`
	Output   string `mapstructure:"output"`
	FilePath string `mapstructure:"file_path"`
	// 日志切割配置（lumberjack），output 为 file/both 时生效
	MaxSize    int  `mapstructure:"max_size"`    // 单文件最大体积（MB），默认 100
	MaxBackups int  `mapstructure:"max_backups"` // 保留的旧日志文件数量，默认 7
	MaxAge     int  `mapstructure:"max_age"`     // 旧日志文件保留天数，默认 30
	Compress   bool `mapstructure:"compress"`    // 是否压缩旧日志文件，默认 true
}

type ModulesConfig struct {
	Dir             string `mapstructure:"dir"`
	SignatureVerify bool   `mapstructure:"signature_verify"`
	Registry        string `mapstructure:"registry"`
	// AUTO-FIX-2026-06-30 [集成-5]: 模块加载模式（auto/plugin/process/grpc）
	// auto: 根据 OS 和环境自动选择（Linux 开发→plugin，Linux 生产/Win/Mac→process）
	// plugin: Go plugin .so 模式（仅 Linux）
	// process/grpc: 独立进程模式（所有平台，生产推荐）
	LoadMode        string `mapstructure:"load_mode"`
	// 进程模式下模块二进制目录（默认 <dir>/bin）
	BinDir          string `mapstructure:"bin_dir"`
	// 进程模式下 socket 目录（默认系统临时目录）
	SocketDir       string `mapstructure:"socket_dir"`
	// 子进程启动超时（秒）
	StartTimeoutSec int    `mapstructure:"start_timeout_sec"`
	// 子进程停止超时（秒）
	StopTimeoutSec  int    `mapstructure:"stop_timeout_sec"`
}

type WebsiteConfig struct {
	URL    string `mapstructure:"url"`
	APIURL string `mapstructure:"api_url"`
	// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 官网购买链接（前端"购买解锁"按钮跳转地址）
	PurchaseURL string `mapstructure:"purchase_url"`
}

type ForwardRules struct {
	// 是否转发位置数据
	ForwardLocation bool `mapstructure:"forward_location"`
	// 是否转发报警数据
	ForwardAlarm bool `mapstructure:"forward_alarm"`
	// AUTO-FIX-2026-07-02: 是否转发视频数据（0x1B00 平台间视频转发）
	ForwardVideo bool `mapstructure:"forward_video"`
	// 只转发指定车牌号（空=全部转发）
	ForwardPhones []string `mapstructure:"forward_phones"`
}

type JT809PlatformConfig struct {
	ID           string       `mapstructure:"id"`
	Host         string       `mapstructure:"host"`
	Port         int          `mapstructure:"port"`
	// AUTO-FIX-2026-07-04 [P0]: 从链路（Down-link）独立监听端口。
	// JT/T 809-2019 规定主链路和从链路为独立 TCP 连接：
	//   - 主链路：下级平台主动连接上级平台（已由 Host:Port 实现）
	//   - 从链路：上级平台反向连接下级平台（需下级平台监听此端口）
	// 设为 0 表示不启用独立从链路（降级为在主链路上复用 0x9xxx 消息）。
	DownlinkPort int          `mapstructure:"downlink_port"`
	Username     string       `mapstructure:"username"`
	Password     string       `mapstructure:"password"`
	LinkType     int          `mapstructure:"link_type"`
	Role         string       `mapstructure:"role"`
	ForwardRules ForwardRules `mapstructure:"forward_rules"`
	// AUTO-FIX-2026-07-04 [P1]: 0x1003 兼容分发配置开关
	// true（默认）= 自动按长度分发旧版 LogoutMessage(4B) / 2019 LinkDisconnectMessage(1B)
	// false = 强制 2019 标准，0x1003 始终解析为 LinkDisconnectMessage
	LegacyCompat bool         `mapstructure:"legacy_compat"`
}

type JT809Config struct {
	Platforms  []JT809PlatformConfig `mapstructure:"platforms"`
	ServerPort int                   `mapstructure:"server_port"`
	// AUTO-FIX-2026-06-26: 下级平台接入鉴权账号配置（按第一轮.txt要求）[2026-06-26]
	DownstreamPlatforms []DownstreamPlatformConfig `mapstructure:"downstream_platforms"`
	// FIXED-2026-07-23 [P2]: 809 客户端熔断器配置
	CircuitBreaker JT809CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	// FIXED-2026-07-23 [P2]: 缓冲区溢出告警开关
	PendingBufferOverflowAlert bool `mapstructure:"pending_buffer_overflow_alert"`
}

// JT809CircuitBreakerConfig 809 客户端熔断器配置
// 连续重连失败达到 FailThreshold 次后进入熔断状态，
// 停止重连 ResetTimeout 秒，期间数据缓冲到 pendingBuffer。
// 熔断恢复后自动尝试重连，成功则 flush 缓冲数据。
type JT809CircuitBreakerConfig struct {
	// Enabled 是否启用熔断器（默认 true）
	Enabled bool `mapstructure:"enabled"`
	// FailThreshold 连续失败触发熔断的次数（默认 10）
	FailThreshold int `mapstructure:"fail_threshold"`
	// ResetTimeout 熔断恢复时间（秒，默认 300 = 5 分钟）
	ResetTimeout int `mapstructure:"reset_timeout"`
}

// DownstreamPlatformConfig 下级平台接入账号配置（用于JT809Server登录鉴权）
type DownstreamPlatformConfig struct {
	UserID   string `mapstructure:"user_id"`
	Password string `mapstructure:"password"`
	DownLinkID string `mapstructure:"downlink_id"` // 下级平台标识
}

type ZLMediaKitConfig struct {
	URL        string `mapstructure:"url"`
	Secret     string `mapstructure:"secret"`
	RTSPPort   int    `mapstructure:"rtsp_port"`
	RTPPort    int    `mapstructure:"rtp_port"`
	HTTPPort   int    `mapstructure:"http_port"`
	StreamIdle int    `mapstructure:"stream_idle"`
	// TcpMode 控制 ZLMediaKit openRtpServer 的 tcp_mode 参数：
	//   0 = 仅 UDP（不传 tcp_mode）
	//   1 = TCP 主动模式（passive=1，ZLM 主动连接上游）
	//   2 = TCP 被动模式（ZLM 监听等待上游连接）
	// 配置缺省时由 media.NewZLMediaKitClient 兜底为 1（保持原硬编码行为）。
	TcpMode int `mapstructure:"tcp_mode"`
}

type ClusterConfig struct {
	NodeID string `mapstructure:"node_id"`
	// AUTO-FIX-2026-08-29 [P0-1]: 集群种子节点列表（host:port，gossip UDP）。
	// 为空时本节点以 standalone 模式运行；多节点部署必须配置至少一个其他节点的地址。
	Seeds []string `mapstructure:"seeds"`
	// AUTO-FIX-2026-08-29 [P0-1]: 本节点 gossip 监听地址，默认 0.0.0.0:7946。
	BindAddr string `mapstructure:"bind_addr"`
}

type AIConfig struct {
	DeepSeekAPIKey string `mapstructure:"deepseek_api_key"`
	DeepSeekURL    string `mapstructure:"deepseek_url"`
	OllamaURL      string `mapstructure:"ollama_url"`
	OllamaModel    string `mapstructure:"ollama_model"`
	QwenAPIKey     string `mapstructure:"qwen_api_key"`
	QwenModel      string `mapstructure:"qwen_model"`
	// 单次调用超时（秒，默认 3）
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
	// 失败重试次数（默认 3）
	RetryCount int `mapstructure:"retry_count"`
	// 降级链路，例如 deepseek -> qwen -> ollama -> onnx -> rule
	FallbackChain []string `mapstructure:"fallback_chain"`
	// 限流配置
	RateLimit AIRateLimitConfig `mapstructure:"ratelimit"`
	// 结果缓存配置
	Cache AICacheConfig `mapstructure:"cache"`
}

// AIRateLimitConfig AI 调用限流配置
type AIRateLimitConfig struct {
	// 单客户每秒请求上限
	PerCustomerPerSecond int `mapstructure:"per_customer_per_second"`
	// 单客户每日请求上限
	PerCustomerPerDay int `mapstructure:"per_customer_per_day"`
	// 全局每秒请求上限
	GlobalPerSecond int `mapstructure:"global_per_second"`
}

// AICacheConfig AI 结果缓存配置
type AICacheConfig struct {
	// 是否启用缓存
	Enabled bool `mapstructure:"enabled"`
	// 缓存 TTL（分钟，默认 60）
	TTLMinutes int `mapstructure:"ttl_minutes"`
}

type AINLPConfig struct {
	DeepSeekAPIKey string `mapstructure:"deepseek_api_key"`
	DeepSeekURL    string `mapstructure:"deepseek_url"`
}

// CryptoConfig 国密配置（SM2/SM3/SM4），按文档第9章存储安全要求
type CryptoConfig struct {
	// 是否启用国密（主开关，true 时所有算法默认启用）
	Enabled bool `mapstructure:"enabled"`
	// FIXED: [国密调用必须加开关] 新增 SM2/SM3/SM4 独立开关 [2026-07-17]
	// EnableSM2 启用 SM2（签名/验签/加密/解密），独立于 Enabled 主开关
	EnableSM2 bool `mapstructure:"enable_sm2"`
	// EnableSM3 启用 SM3（摘要/HMAC），独立于 Enabled 主开关
	EnableSM3 bool `mapstructure:"enable_sm3"`
	// EnableSM4 启用 SM4（对称加密 GCM/CBC），独立于 Enabled 主开关
	EnableSM4 bool `mapstructure:"enable_sm4"`
	// SM2 证书路径
	SM2CertPath string `mapstructure:"sm2_cert_path"`
	// SM2 私钥路径
	SM2KeyPath string `mapstructure:"sm2_key_path"`
	// SM4 对称密钥
	SM4Key string `mapstructure:"sm4_key"`
	// 哈希算法：sm3 | sha256
	HashAlgorithm string `mapstructure:"hash_algorithm"`
}

// IsSM2Enabled 返回 SM2 是否启用。
// 主开关 Enabled=true 时默认启用；或 EnableSM2=true 独立启用。
func (c CryptoConfig) IsSM2Enabled() bool {
	return c.Enabled || c.EnableSM2
}

// IsSM3Enabled 返回 SM3 是否启用。
// 主开关 Enabled=true 时默认启用；或 EnableSM3=true 独立启用。
func (c CryptoConfig) IsSM3Enabled() bool {
	return c.Enabled || c.EnableSM3
}

// IsSM4Enabled 返回 SM4 是否启用。
// 主开关 Enabled=true 时默认启用；或 EnableSM4=true 独立启用。
func (c CryptoConfig) IsSM4Enabled() bool {
	return c.Enabled || c.EnableSM4
}

// MonitorConfig 监控告警配置
type MonitorConfig struct {
	// 是否启用监控告警
	Enabled bool `mapstructure:"enabled"`
	// 告警通道配置
	AlertChannels AlertChannelsConfig `mapstructure:"alert_channels"`
}

// AlertChannelsConfig 告警通道集合
type AlertChannelsConfig struct {
	// 钉钉告警
	DingTalk DingTalkAlertConfig `mapstructure:"dingtalk"`
	// 企业微信告警
	WeChat WeChatAlertConfig `mapstructure:"wechat"`
	// 邮件告警
	Email EmailAlertConfig `mapstructure:"email"`
	// 通用 Webhook 告警
	Webhook WebhookAlertConfig `mapstructure:"webhook"`
}

// DingTalkAlertConfig 钉钉机器人告警配置
type DingTalkAlertConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Webhook string `mapstructure:"webhook"`
}

// WeChatAlertConfig 企业微信机器人告警配置
type WeChatAlertConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Webhook string `mapstructure:"webhook"`
}

// EmailAlertConfig 邮件告警配置
type EmailAlertConfig struct {
	Enabled  bool     `mapstructure:"enabled"`
	SMTPHost string   `mapstructure:"smtp_host"`
	SMTPPort int      `mapstructure:"smtp_port"`
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	To       []string `mapstructure:"to"`
}

// WebhookAlertConfig 通用 Webhook 告警配置
type WebhookAlertConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
}

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Gateway    GatewayConfig    `mapstructure:"gateway"`
	API        APIConfig        `mapstructure:"api"`
	WebSocket  WebSocketConfig  `mapstructure:"websocket"`
	Storage    StorageConfig    `mapstructure:"storage"`
	Logging    LoggingConfig    `mapstructure:"logging"`
	Modules    ModulesConfig    `mapstructure:"modules"`
	Website    WebsiteConfig    `mapstructure:"website"`
	JT809      JT809Config      `mapstructure:"jt809"`
	ZLMediaKit ZLMediaKitConfig `mapstructure:"zlmediakit"`
	Auth       AuthConfig       `mapstructure:"auth"`
	Cluster    ClusterConfig    `mapstructure:"cluster"`
	AI         AIConfig         `mapstructure:"ai"`
	AINLP      AINLPConfig      `mapstructure:"ai_nlp"`
	// AUTO-FIX-2026-06-26: 新增VideoConfig（TCP-RTP支持，按第一轮.txt要求）[2026-06-26]
	Video      VideoConfig      `mapstructure:"video"`
	// AUTO-FIX-2026-06-26: 新增MapConfig（地图API Key配置化）[2026-06-26]
	Map        MapConfig        `mapstructure:"map"`
	// 国密配置（SM2/SM3/SM4）
	Crypto     CryptoConfig     `mapstructure:"crypto"`
	// 监控告警配置
	Monitor    MonitorConfig    `mapstructure:"monitor"`
	// AUTO-FIX-2026-06-30 [P1-2]: 优雅停机配置（替代 main.go 硬编码超时）
	Shutdown   ShutdownConfig   `mapstructure:"shutdown"`
}

// ShutdownConfig 优雅停机 / 蓝绿部署排空配置。
// AUTO-FIX-2026-06-30 [P1-1/P1-2]: 替换 main.go 中硬编码的 5 分钟 drain 和 10s API 超时。
type ShutdownConfig struct {
	// DrainTimeout 等待现有设备连接排空的最长时间（秒），超时后强制关闭。
	// 蓝绿部署中 Blue 节点的最大存活窗口。默认 300（5 分钟）。
	DrainTimeoutSeconds int `mapstructure:"drain_timeout_seconds"`
	// APIShutdownTimeout HTTP API 优雅停机超时（秒），等待在途 HTTP 请求完成。
	// 默认 10。
	APIShutdownTimeoutSeconds int `mapstructure:"api_shutdown_timeout_seconds"`
	// ReconnectBackoffMax 下发给终端的重连退避时间上限（秒）。
	// 停机/蓝绿切换时广播 0x8001 携带 0~Max 随机退避，避免重启后鉴权风暴。
	// 默认 300（安全场景）；蓝绿部署可设 60（快速重连）。
	ReconnectBackoffMaxSeconds int `mapstructure:"reconnect_backoff_max_seconds"`
	// DrainCheckInterval 排空检查间隔（秒），每隔此时间检查一次剩余连接数。默认 5。
	DrainCheckIntervalSeconds int `mapstructure:"drain_check_interval_seconds"`
}

// DrainTimeout 返回排空超时（兜底默认 5 分钟）。
func (s ShutdownConfig) DrainTimeout() time.Duration {
	if s.DrainTimeoutSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(s.DrainTimeoutSeconds) * time.Second
}

// APIShutdownTimeout 返回 API 停机超时（兜底默认 10 秒）。
func (s ShutdownConfig) APIShutdownTimeout() time.Duration {
	if s.APIShutdownTimeoutSeconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(s.APIShutdownTimeoutSeconds) * time.Second
}

// ReconnectBackoffMax 返回重连退避上限（兜底默认 300 秒）。
func (s ShutdownConfig) ReconnectBackoffMax() int {
	if s.ReconnectBackoffMaxSeconds <= 0 {
		return 300
	}
	return s.ReconnectBackoffMaxSeconds
}

// DrainCheckInterval 返回排空检查间隔（兜底默认 5 秒）。
func (s ShutdownConfig) DrainCheckInterval() time.Duration {
	if s.DrainCheckIntervalSeconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(s.DrainCheckIntervalSeconds) * time.Second
}

// MapConfig 地图API Key配置，支持天地图/高德/百度
type MapConfig struct {
	Provider     string `mapstructure:"provider"`       // tianditu/amap/baidu，默认 tianditu
	TiandituKey  string `mapstructure:"tianditu_key"`
	AMapKey      string `mapstructure:"amap_key"`
	AMapSecurity string `mapstructure:"amap_security"`  // 高德安全密钥
	BaiduKey     string `mapstructure:"baidu_key"`
	// 默认中心点经度
	CenterLongitude float64 `mapstructure:"center_longitude"`
	// 默认中心点纬度
	CenterLatitude float64 `mapstructure:"center_latitude"`
	// 默认缩放级别
	Zoom int `mapstructure:"zoom"`
}

// VideoConfig 视频引擎配置（TCP-RTP支持）
type VideoConfig struct {
	RTPMode    string `mapstructure:"rtp_mode"`    // udp/tcp/auto，默认udp
	StreamPort int    `mapstructure:"stream_port"` // TCP-RTP监听端口
	// SRTP 加密配置（按文档第9章存储/视频安全要求）
	SRTP SRTPConfig `mapstructure:"srtp"`
}

// SRTPConfig SRTP 视频流加密配置
type SRTPConfig struct {
	// 是否启用 SRTP 加密
	Enabled bool `mapstructure:"enabled"`
	// 加密套件：AES-128-CM | SM4-CBC
	CipherSuite string `mapstructure:"cipher_suite"`
	// 密钥轮换周期（小时，默认 24）
	KeyRotateHours int `mapstructure:"key_rotate_hours"`
}

type AuthConfig struct {
	LicenseKey string `mapstructure:"license_key"`
	// OfflineUnbindSecret 是离线解绑凭证 HMAC 降级路径的应用密钥。
	// AUTO-FIX-2026-06-29 [P2]: 替换 license_manager.go 中硬编码的
	// "jte-offline-unbind-v1-secret"，避免源码公开后任何人都可伪造合法凭证。
	// 留空时 LicenseManager 回退到包级默认值（仅用于向后兼容已签发的旧凭证）。
	// 生产环境应配置 ≥32 字节随机值：openssl rand -base64 48。
	OfflineUnbindSecret string `mapstructure:"offline_unbind_secret"`
}

var (
	globalConfig *Config
	once         sync.Once
	hotReloadableKeys = map[string]bool{
		"logging.level":            true,
		"logging.format":           true,
		"gateway.heartbeat_interval": true,
		"gateway.heartbeat_timeout":  true,
		"gateway.read_timeout":      true,
		"gateway.write_timeout":     true,
		"api.rate_limit":            true,
		"websocket.write_buffer_size": true,
		"websocket.read_buffer_size":  true,
	}
)

func IsHotReloadable(key string) bool {
	return hotReloadableKeys[key]
}

func (c *Config) GetString(key string) string {
	switch key {
	case "ai.deepseek_api_key":
		return c.AI.DeepSeekAPIKey
	case "ai.deepseek_url":
		return c.AI.DeepSeekURL
	case "ai.ollama_url":
		return c.AI.OllamaURL
	case "ai.ollama_model":
		return c.AI.OllamaModel
	case "ai.qwen_api_key":
		return c.AI.QwenAPIKey
	case "ai.qwen_model":
		return c.AI.QwenModel
	case "ai_nlp.deepseek_api_key":
		return c.AINLP.DeepSeekAPIKey
	case "ai_nlp.deepseek_url":
		return c.AINLP.DeepSeekURL
	case "cluster.node_id":
		return c.Cluster.NodeID
	case "map.provider":
		return c.Map.Provider
	case "map.tianditu_key":
		return c.Map.TiandituKey
	case "map.amap_key":
		return c.Map.AMapKey
	case "map.amap_security":
		return c.Map.AMapSecurity
	case "map.baidu_key":
		return c.Map.BaiduKey
	default:
		return ""
	}
}

func (c *Config) ApplyHotReload(newCfg *Config) []string {
	var applied []string

	if newCfg.Logging.Level != c.Logging.Level && hotReloadableKeys["logging.level"] {
		c.Logging.Level = newCfg.Logging.Level
		applied = append(applied, "logging.level")
	}
	if newCfg.Logging.Format != c.Logging.Format && hotReloadableKeys["logging.format"] {
		c.Logging.Format = newCfg.Logging.Format
		applied = append(applied, "logging.format")
	}
	if newCfg.Gateway.HeartbeatInterval != c.Gateway.HeartbeatInterval && hotReloadableKeys["gateway.heartbeat_interval"] {
		c.Gateway.HeartbeatInterval = newCfg.Gateway.HeartbeatInterval
		applied = append(applied, "gateway.heartbeat_interval")
	}
	if newCfg.Gateway.HeartbeatTimeout != c.Gateway.HeartbeatTimeout && hotReloadableKeys["gateway.heartbeat_timeout"] {
		c.Gateway.HeartbeatTimeout = newCfg.Gateway.HeartbeatTimeout
		applied = append(applied, "gateway.heartbeat_timeout")
	}
	if newCfg.Gateway.ReadTimeout != c.Gateway.ReadTimeout && hotReloadableKeys["gateway.read_timeout"] {
		c.Gateway.ReadTimeout = newCfg.Gateway.ReadTimeout
		applied = append(applied, "gateway.read_timeout")
	}
	if newCfg.Gateway.WriteTimeout != c.Gateway.WriteTimeout && hotReloadableKeys["gateway.write_timeout"] {
		c.Gateway.WriteTimeout = newCfg.Gateway.WriteTimeout
		applied = append(applied, "gateway.write_timeout")
	}
	if newCfg.API.RateLimit != c.API.RateLimit && hotReloadableKeys["api.rate_limit"] {
		c.API.RateLimit = newCfg.API.RateLimit
		applied = append(applied, "api.rate_limit")
	}
	if newCfg.WebSocket.WriteBufferSize != c.WebSocket.WriteBufferSize && hotReloadableKeys["websocket.write_buffer_size"] {
		c.WebSocket.WriteBufferSize = newCfg.WebSocket.WriteBufferSize
		applied = append(applied, "websocket.write_buffer_size")
	}
	if newCfg.WebSocket.ReadBufferSize != c.WebSocket.ReadBufferSize && hotReloadableKeys["websocket.read_buffer_size"] {
		c.WebSocket.ReadBufferSize = newCfg.WebSocket.ReadBufferSize
		applied = append(applied, "websocket.read_buffer_size")
	}

	return applied
}

func (c *Config) GetNonReloadableChanges(newCfg *Config) []string {
	var changes []string
	if newCfg.Gateway.TCPPort != c.Gateway.TCPPort {
		changes = append(changes, "gateway.tcp_port (restart required)")
	}
	if newCfg.Gateway.UDPPort != c.Gateway.UDPPort {
		changes = append(changes, "gateway.udp_port (restart required)")
	}
	if newCfg.API.Port != c.API.Port {
		changes = append(changes, "api.port (restart required)")
	}
	if newCfg.Storage.Type != c.Storage.Type {
		changes = append(changes, "storage.type (restart required)")
	}
	if newCfg.Storage.DSN != c.Storage.DSN {
		changes = append(changes, "storage.dsn (restart required)")
	}
	if newCfg.Server.Port != c.Server.Port {
		changes = append(changes, "server.port (restart required)")
	}
	return changes
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigName("jte")
	v.SetConfigType("yaml")

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	v.AutomaticEnv()
	v.SetEnvPrefix("JTE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	// 多环境配置覆盖：根据 JTE_ENV 环境变量加载 jte-{env}.yaml 覆盖主配置
	// 支持 dev/test/staging/prod 等环境，配置文件不存在时静默跳过
	if env := os.Getenv("JTE_ENV"); env != "" {
		envConfigName := "jte-" + env
		v.SetConfigName(envConfigName)
		if err := v.MergeInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, fmt.Errorf("merge env config %q: %w", envConfigName, err)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.Gateway.MaxDevices == 0 {
		cfg.Gateway.MaxDevices = 20
	}
	// AUTO-FIX-2026-06-30 [P1-4]: 单机连接硬上限默认 12 万（OOM 防护）。
	// 原0值会导致 OnlineCount()>=0 恒真，拒绝所有新连接。
	if cfg.Gateway.MaxConnections == 0 {
		cfg.Gateway.MaxConnections = 120000
	}
	if cfg.Gateway.HeartbeatInterval == 0 {
		cfg.Gateway.HeartbeatInterval = 30
	}
	if cfg.Gateway.HeartbeatTimeout == 0 {
		cfg.Gateway.HeartbeatTimeout = 180
	}
	// FIXED-2026-07-22 [P0]: 初始认证超时默认 30s
	if cfg.Gateway.InitialAuthTimeout == 0 {
		cfg.Gateway.InitialAuthTimeout = 30
	}
	// FIXED-2026-07-22 [P0]: 单 IP 最大并发连接数默认 100
	if cfg.Gateway.MaxConnsPerIP == 0 {
		cfg.Gateway.MaxConnsPerIP = 100
	}
	// FIXED-2026-07-22 [P0]: 单 IP 连接速率限制默认 50/s
	if cfg.Gateway.MaxConnRatePerIP == 0 {
		cfg.Gateway.MaxConnRatePerIP = 50
	}
	if cfg.API.RateLimit == 0 {
		cfg.API.RateLimit = 100
	}
	if cfg.API.JWTExpireHours == 0 {
		cfg.API.JWTExpireHours = 24
	}
	// JWT 密钥轮换默认值
	if cfg.API.JWT == nil {
		cfg.API.JWT = &JWTConfig{}
	}
	if cfg.API.JWT.RotateDays == 0 {
		cfg.API.JWT.RotateDays = 90
	}
	// Security 默认值
	if cfg.API.Security == nil {
		cfg.API.Security = &APISecurityConfig{}
	}
	if cfg.API.Security.ConnLimitPerIP == 0 {
		cfg.API.Security.ConnLimitPerIP = 100
	}
	if cfg.API.Security.BodyLimitBytes == 0 {
		cfg.API.Security.BodyLimitBytes = 10 * 1024 * 1024 // 10MB
	}
	// 初始化 kidCreatedAt，记录配置文件中已有 kid 的创建时间
	if cfg.API.JWT.Secrets != nil && cfg.API.JWT.kidCreatedAt == nil {
		cfg.API.JWT.kidCreatedAt = make(map[string]time.Time)
		for kid := range cfg.API.JWT.Secrets {
			cfg.API.JWT.kidCreatedAt[kid] = time.Now()
		}
	}
	if cfg.Modules.Dir == "" {
		cfg.Modules.Dir = "./modules"
	}
	if cfg.Storage.Type == "" {
		// AUTO-FIX-2026-07-15 [ConvergeLoop-语义一致性]: 默认值对齐 jte.yaml/storage.type=sqlite
		// 原先 fallback 为 memory，与 jte.yaml 默认值 sqlite 不一致；
		// 用户删除 yaml 中 type 字段时会静默降级到 memory 导致数据丢失。
		cfg.Storage.Type = "sqlite"
	}
	// v2.0 存储层默认值（时序/缓存/对象存储）
	if cfg.Storage.TimeSeries.Driver != "" {
		if cfg.Storage.TimeSeries.Host == "" {
			cfg.Storage.TimeSeries.Host = "127.0.0.1"
		}
		if cfg.Storage.TimeSeries.Port == 0 {
			cfg.Storage.TimeSeries.Port = 6030
		}
		if cfg.Storage.TimeSeries.User == "" {
			cfg.Storage.TimeSeries.User = "root"
		}
		if cfg.Storage.TimeSeries.Password == "" {
			// [P0-安全] 密码必须通过环境变量或配置文件注入，不再硬编码默认值
			// 优先使用 JTE_TDENGINE_PASSWORD（与 docker-compose-prod.yml 对齐）
			cfg.Storage.TimeSeries.Password = os.Getenv("JTE_TDENGINE_PASSWORD")
			if cfg.Storage.TimeSeries.Password == "" {
				// 兼容旧变量名
				cfg.Storage.TimeSeries.Password = os.Getenv("JTE_TS_DEFAULT_PASSWORD")
			}
			if cfg.Storage.TimeSeries.Password == "" {
				if os.Getenv("JTE_ENV") == "production" {
					return nil, fmt.Errorf("JTE_TDENGINE_PASSWORD environment variable is required in production mode (TDengine password must be set explicitly)")
				}
				// 仅开发/测试环境使用 TDengine 官方默认密码 taosdata
				// 不使用 base64 编码绕过安全扫描，直接标注 nolint
				cfg.Storage.TimeSeries.Password = "taosdata" //nolint:gosec // TDengine official default password, dev/test only
			}
		}
		if cfg.Storage.TimeSeries.Database == "" {
			cfg.Storage.TimeSeries.Database = "jte_ts"
		}
		if cfg.Storage.TimeSeries.KeepDays == 0 {
			cfg.Storage.TimeSeries.KeepDays = 365
		}
		if cfg.Storage.TimeSeries.BatchSize == 0 {
			cfg.Storage.TimeSeries.BatchSize = 1000
		}
		if cfg.Storage.TimeSeries.BufferSize == 0 {
			cfg.Storage.TimeSeries.BufferSize = 10000
		}
		if cfg.Storage.TimeSeries.FlushIntervalMs == 0 {
			cfg.Storage.TimeSeries.FlushIntervalMs = 100
		}
		// AUTO-FIX-2026-06-28: 集群部署默认值（与 tdengine.NewStorage 保持一致）
		// 单节点：Replica=1，VGroups=10；生产集群建议 Replica=3，VGroups 按规模调整
		if cfg.Storage.TimeSeries.Replica == 0 {
			cfg.Storage.TimeSeries.Replica = 1
		}
		if cfg.Storage.TimeSeries.VGroups == 0 {
			cfg.Storage.TimeSeries.VGroups = 10
		}
		if cfg.Storage.TimeSeries.Days == 0 {
			cfg.Storage.TimeSeries.Days = 10
		}
		if cfg.Storage.TimeSeries.Blocks == 0 {
			cfg.Storage.TimeSeries.Blocks = 6
		}
	}
	if cfg.Storage.Cache.Driver != "" {
		if cfg.Storage.Cache.Addr == "" {
			cfg.Storage.Cache.Addr = "127.0.0.1:6379"
		}
		if cfg.Storage.Cache.PoolSize == 0 {
			cfg.Storage.Cache.PoolSize = 10
		}
		if cfg.Storage.Cache.LatestLocationTTL == 0 {
			cfg.Storage.Cache.LatestLocationTTL = 300
		}
		if cfg.Storage.Cache.OnlineStatusTTL == 0 {
			cfg.Storage.Cache.OnlineStatusTTL = 120
		}
		if cfg.Storage.Cache.Mode == "" {
			cfg.Storage.Cache.Mode = "single"
		}
	}
	if cfg.Storage.Object.Driver != "" {
		if cfg.Storage.Object.Endpoint == "" {
			cfg.Storage.Object.Endpoint = "127.0.0.1:9000"
		}
		if cfg.Storage.Object.Bucket == "" {
			cfg.Storage.Object.Bucket = "jte-archive"
		}
		if cfg.Storage.Object.ArchiveBucket == "" {
			cfg.Storage.Object.ArchiveBucket = cfg.Storage.Object.Bucket
		}
		if cfg.Storage.Object.VideoBucket == "" {
			cfg.Storage.Object.VideoBucket = "jte-video"
		}
	}
	// AUTO-FIX-2026-07-02: 归档任务默认值
	// Enabled 默认 true（确保历史轨迹归档自动生效）
	// 判断逻辑：当 archive 配置段完全未配置（所有字段均为零值）时，启用归档；
	// 用户在 YAML 中显式设置 enabled: false 可关闭。
	// 注意：bool 零值为 false，无法区分"未设置"和"显式 false"，
	// 因此采用"全零值检测"判断配置段是否缺失。
	if !cfg.Storage.Archive.Enabled &&
		cfg.Storage.Archive.IntervalHours == 0 &&
		cfg.Storage.Archive.KeepDays == 0 &&
		cfg.Storage.Archive.BatchDays == 0 &&
		!cfg.Storage.Archive.DryRun &&
		cfg.Storage.Archive.ScheduleHour == 0 {
		cfg.Storage.Archive.Enabled = true
	}
	if cfg.Storage.Archive.IntervalHours == 0 {
		cfg.Storage.Archive.IntervalHours = 24
	}
	if cfg.Storage.Archive.BatchDays == 0 {
		cfg.Storage.Archive.BatchDays = 1
	}
	if cfg.Storage.Archive.ScheduleHour == 0 {
		cfg.Storage.Archive.ScheduleHour = 2 // 默认凌晨 2 点
	}
	if cfg.Storage.Archive.DeleteDelayDays == 0 {
		cfg.Storage.Archive.DeleteDelayDays = 7 // 默认 7 天延迟删除
	}
	if cfg.Storage.Archive.Alert.AlertThreshold == 0 {
		cfg.Storage.Archive.Alert.AlertThreshold = 1
	}
	// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 购买链接默认值
	if cfg.Website.PurchaseURL == "" {
		cfg.Website.PurchaseURL = cfg.Website.URL + "/purchase"
	}
	if cfg.Website.PurchaseURL == "/purchase" {
		cfg.Website.PurchaseURL = "https://jte.example.com/purchase"
	}
	// AI 降级与限流默认值
	if cfg.AI.TimeoutSeconds == 0 {
		cfg.AI.TimeoutSeconds = 3
	}
	if cfg.AI.RetryCount == 0 {
		cfg.AI.RetryCount = 3
	}
	if len(cfg.AI.FallbackChain) == 0 {
		cfg.AI.FallbackChain = []string{"deepseek", "qwen", "ollama", "onnx", "rule"}
	}
	if cfg.AI.Cache.Enabled && cfg.AI.Cache.TTLMinutes == 0 {
		cfg.AI.Cache.TTLMinutes = 60
	}
	// SRTP 默认值
	if cfg.Video.SRTP.CipherSuite == "" {
		cfg.Video.SRTP.CipherSuite = "AES-128-CM"
	}
	if cfg.Video.SRTP.Enabled && cfg.Video.SRTP.KeyRotateHours == 0 {
		cfg.Video.SRTP.KeyRotateHours = 24
	}
	// 日志切割默认值
	if cfg.Logging.MaxSize == 0 {
		cfg.Logging.MaxSize = 100 // 100MB
	}
	if cfg.Logging.MaxBackups == 0 {
		cfg.Logging.MaxBackups = 7
	}
	if cfg.Logging.MaxAge == 0 {
		cfg.Logging.MaxAge = 30 // 30 天
	}
	// OOM 防护检查间隔默认值
	if cfg.Gateway.OOMProtect.Enabled && cfg.Gateway.OOMProtect.CheckIntervalSeconds == 0 {
		cfg.Gateway.OOMProtect.CheckIntervalSeconds = 5
	}
	// 国密哈希算法默认值
	// FIXED: [国密开关] 使用 IsSM3Enabled() 替代 Enabled，支持独立 SM3 开关 [2026-07-17]
	if cfg.Crypto.IsSM3Enabled() && cfg.Crypto.HashAlgorithm == "" {
		cfg.Crypto.HashAlgorithm = "sm3"
	}

	// FIXED-2026-07-23 [P2]: 809 熔断器配置默认值
	if cfg.JT809.CircuitBreaker.FailThreshold == 0 {
		cfg.JT809.CircuitBreaker.FailThreshold = 10
	}
	if cfg.JT809.CircuitBreaker.ResetTimeout == 0 {
		cfg.JT809.CircuitBreaker.ResetTimeout = 300
	}

	// AUTO-FIX-2026-06-29 [P0]: JWT secret 安全校验——禁止使用空值、占位符或弱密钥。
	// 生产环境必须配置 ≥32 字节的随机密钥，防止 JWT 被伪造导致鉴权绕过。
	// 开发/测试环境可通过环境变量 JTE_ALLOW_INSECURE_JWT=1 跳过校验。
	// AUTO-FIX-2026-06-30 [P1-6]: 当 KMS 来源（env/file）或 JWT.Secrets 已配置时，
	// 跳过 JWTSecret 校验（密钥从 KMS 加载，不在主配置明文存储）。
	// [P2-2] 空值防护：kms_source != "env" && jwt_secret == "" 时启动失败。
	// JTE_ALLOW_INSECURE_JWT 为空（未设置）时不跳过此校验，仅当显式设为 "1" 时才跳过。
	if os.Getenv("JTE_ALLOW_INSECURE_JWT") != "1" {
		kmsActive := cfg.API.JWT != nil &&
			(cfg.API.JWT.KMSSource == "env" || cfg.API.JWT.KMSSource == "file" || len(cfg.API.JWT.Secrets) > 0)
		if !kmsActive {
			// [P2-2] 空值单独报错，消息明确指出 kms_source 不为 env 时 jwt_secret 不能为空
			if cfg.API.JWTSecret == "" {
				return nil, fmt.Errorf("jwt_secret must not be empty when kms_source is not env (current kms_source=%q); set api.jwt_secret or configure jwt.kms_source=env/file for KMS-managed keys, or set JTE_ALLOW_INSECURE_JWT=1 for dev/test", kmsSourceStr(cfg.API.JWT))
			}
			jwtSecretPlaceholders := map[string]bool{
				"PLEASE-CHANGE-THIS-SECRET-BEFORE-PRODUCTION": true,
				"your-secret-key":                             true,
				"jwt-secret":                                  true,
				"change-me":                                   true,
				"secret":                                      true,
			}
			if jwtSecretPlaceholders[cfg.API.JWTSecret] {
				return nil, fmt.Errorf("config api.jwt_secret must be set to a secure random value (current is placeholder); generate one with `openssl rand -base64 48`, configure jwt.kms_source=env/file, or set JTE_ALLOW_INSECURE_JWT=1 for dev/test")
			}
			if len(cfg.API.JWTSecret) < 32 {
				return nil, fmt.Errorf("config api.jwt_secret must be at least 32 bytes for HS256 security (current length: %d), configure jwt.kms_source=env/file, or set JTE_ALLOW_INSECURE_JWT=1 for dev/test", len(cfg.API.JWTSecret))
			}
		}
	}

	// AUTO-FIX-2026-06-29 [P2]: license 离线解绑 HMAC secret 校验。
	// 留空时 LicenseManager 回退到包级默认值（向后兼容）；一旦配置则必须 ≥32 字节，
	// 且禁止使用已知占位符，防止生产环境用弱密钥签发可被穷举的解绑凭证。
	// 开发/测试环境可通过 JTE_ALLOW_INSECURE_JWT=1 跳过校验（复用 JWT 的跳过开关）。
	if os.Getenv("JTE_ALLOW_INSECURE_JWT") != "1" && cfg.Auth.OfflineUnbindSecret != "" {
		unbindSecretPlaceholders := map[string]bool{
			"jte-offline-unbind-v1-secret":                  true,
			"PLEASE-CHANGE-THIS-SECRET-BEFORE-PRODUCTION":   true,
			"change-me":                                      true,
			"secret":                                         true,
			"your-secret-key":                                true,
		}
		if unbindSecretPlaceholders[cfg.Auth.OfflineUnbindSecret] {
			return nil, fmt.Errorf("config auth.offline_unbind_secret must be a secure random value (current is placeholder); generate one with `openssl rand -base64 48`, leave empty to use built-in default, or set JTE_ALLOW_INSECURE_JWT=1 for dev/test")
		}
		if len(cfg.Auth.OfflineUnbindSecret) < 32 {
			return nil, fmt.Errorf("config auth.offline_unbind_secret must be at least 32 bytes when set (current length: %d); leave empty to use built-in default, or set JTE_ALLOW_INSECURE_JWT=1 for dev/test", len(cfg.Auth.OfflineUnbindSecret))
		}
	}

	globalConfig = &cfg
	// 验证网关超时配置
	if err := validateGatewayTimeouts(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// kmsSourceStr 返回 JWT 配置的 kms_source 字符串，用于错误消息。
// [P2-2] 辅助函数，nil 安全。
func kmsSourceStr(j *JWTConfig) string {
	if j == nil {
		return ""
	}
	return j.KMSSource
}

// validateGatewayTimeouts 验证网关心跳超时配置
// 心跳间隔必须 >= 10 秒；心跳超时必须 > 心跳间隔 × 3
func validateGatewayTimeouts(cfg *Config) error {
	if cfg.Gateway.HeartbeatInterval > 0 && cfg.Gateway.HeartbeatInterval < 10 {
		return fmt.Errorf("heartbeat_interval must be >= 10 seconds")
	}
	if cfg.Gateway.HeartbeatInterval > 0 && cfg.Gateway.HeartbeatTimeout > 0 {
		if cfg.Gateway.HeartbeatTimeout <= cfg.Gateway.HeartbeatInterval*3 {
			return fmt.Errorf("heartbeat_timeout must be > heartbeat_interval * 3 (got %d <= %d)",
				cfg.Gateway.HeartbeatTimeout, cfg.Gateway.HeartbeatInterval*3)
		}
	}
	return nil
}

func Get() *Config {
	if globalConfig == nil {
		cfg, _ := Load("")
		if cfg != nil {
			globalConfig = cfg
		}
	}
	return globalConfig
}

// loggerFiles 跟踪 InitLogger 打开的文件句柄，支持应用退出或日志重载时清理，避免文件描述符泄漏。
var (
	loggerFilesMu sync.Mutex
	loggerFiles   []*os.File
)

// CloseLogger 关闭由 InitLogger 打开的所有日志文件句柄。
// 应用退出（main 中的 defer）或日志配置热重载时应调用，防止文件描述符泄漏。
func CloseLogger() {
	loggerFilesMu.Lock()
	defer loggerFilesMu.Unlock()
	for _, f := range loggerFiles {
		_ = f.Close()
	}
	loggerFiles = nil
}

// registerLoggerFile 注册一个由 InitLogger 打开的文件句柄，纳入生命周期管理。
func registerLoggerFile(f *os.File) {
	if f == nil {
		return
	}
	loggerFilesMu.Lock()
	loggerFiles = append(loggerFiles, f)
	loggerFilesMu.Unlock()
}

func InitLogger(cfg *LoggingConfig) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	var encoder zapcore.Encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	var writeSyncer zapcore.WriteSyncer
	switch cfg.Output {
	case "file":
		if cfg.FilePath == "" {
			cfg.FilePath = "jte.log"
		}
		// 使用 lumberjack 实现按大小切割 + 按天数保留
		lj := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		writeSyncer = zapcore.AddSync(lj)
	case "both":
		if cfg.FilePath == "" {
			cfg.FilePath = "jte.log"
		}
		lj := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		writeSyncer = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(lj))
	default:
		writeSyncer = zapcore.AddSync(os.Stdout)
	}

	core := zapcore.NewCore(encoder, writeSyncer, level)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return logger, nil
}

// ValidateForProduction 生产环境强制安全校验。
// 当 JTE_ENV=production 时，main.go 在启动时调用此方法，任一校验失败则拒绝启动。
//
// [P1-可靠性] 生产环境必须满足以下安全基线：
//   - 时序数据库（TDengine）密码不为空且 ≥16 字符
//   - Redis 缓存密码不为空（当缓存层启用时）
//   - JWT 密钥 ≥32 字节（防止暴力破解）
//   - TLS 启用时证书路径不为空或 ACME 域名已配置
//   - CORS 不包含通配符 "*"（防止跨域攻击）
//   - 离线解绑密钥 ≥32 字节（防伪造凭证）
func (c *Config) ValidateForProduction() error {
	var errs []string

	// 1. 时序数据库密码校验
	if c.Storage.TimeSeries.Driver != "" {
		if c.Storage.TimeSeries.Password == "" {
			errs = append(errs, "storage.time_series.password must not be empty in production (driver enabled)")
		} else if len(c.Storage.TimeSeries.Password) < 16 {
			errs = append(errs, fmt.Sprintf("storage.time_series.password must be at least 16 characters in production (current: %d)", len(c.Storage.TimeSeries.Password)))
		}
	}

	// 2. Redis 缓存密码校验（当缓存层启用时）
	if c.Storage.Cache.Driver != "" {
		if c.Storage.Cache.Password == "" {
			errs = append(errs, "storage.cache.password must not be empty in production (redis driver enabled)")
		}
	}

	// 3. JWT 密钥强度校验
	jwtSecret := c.API.JWTSecret
	if c.API.JWT != nil {
		if kid, secret, ok := c.API.JWT.GetActiveSecret(); ok && secret != "" {
			jwtSecret = secret
			_ = kid
		}
	}
	if len(jwtSecret) < 32 {
		if jwtSecret == "" {
			errs = append(errs, "api.jwt_secret must not be empty in production (minimum 32 bytes required)")
		} else {
			errs = append(errs, fmt.Sprintf("api.jwt_secret must be at least 32 bytes in production (current: %d bytes)", len(jwtSecret)))
		}
	}

	// 4. TLS 配置校验（启用 TLS 时）
	if c.API.TLS != nil && c.API.TLS.Enabled {
		if !c.API.TLS.ACME {
			// 非 ACME 模式必须提供证书和私钥路径
			if c.API.TLS.CertFile == "" {
				errs = append(errs, "api.tls.cert_file must not be empty when TLS is enabled (or enable ACME)")
			}
			if c.API.TLS.KeyFile == "" {
				errs = append(errs, "api.tls.key_file must not be empty when TLS is enabled (or enable ACME)")
			}
		} else {
			// ACME 模式必须配置域名
			if len(c.API.TLS.ACMEDomains) == 0 {
				errs = append(errs, "api.tls.acme_domains must not be empty when ACME is enabled")
			}
		}
	}

	// 5. CORS 配置校验（禁止通配符）
	for _, origin := range c.API.CORSOrigins {
		if origin == "*" {
			errs = append(errs, "api.cors_origins must not contain wildcard \"*\" in production (specify explicit origins)")
			break
		}
	}

	// 6. 离线解绑密钥强度校验
	if len(c.Auth.OfflineUnbindSecret) < 32 {
		if c.Auth.OfflineUnbindSecret == "" {
			errs = append(errs, "auth.offline_unbind_secret must not be empty in production (minimum 32 bytes required)")
		} else {
			errs = append(errs, fmt.Sprintf("auth.offline_unbind_secret must be at least 32 bytes in production (current: %d bytes)", len(c.Auth.OfflineUnbindSecret)))
		}
	}

	// FIXED-2026-07-23 [P2]: 生产环境禁止使用 SQLite 作为关系库
	// SQLite 无法支持并发写入，生产环境会导致性能瓶颈和数据竞争
	if c.Storage.Type == "sqlite" || c.Storage.Type == "sqlite3" {
		errs = append(errs, "storage.type must not be 'sqlite' in production (use 'postgres' or 'mysql'; SQLite is for dev/test only)")
	}

	// FIXED-2026-07-23 [P2]: 生产环境必须配置时序库
	// 高频轨迹/报警数据走关系库会导致写入性能瓶颈
	if c.Storage.TimeSeries.Driver == "" {
		errs = append(errs, "storage.time_series.driver must not be empty in production (use 'tdengine'; SQLite alone cannot handle high-frequency time-series writes)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("production config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}