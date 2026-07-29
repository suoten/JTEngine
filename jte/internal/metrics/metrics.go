package metrics

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: 可观测性 - Prometheus 指标
// AUTO-FIX-2026-07-02 [可观测性完善]: 补全协议/视频/AI/存储四层指标
//
// 轻量级 Prometheus 兼容指标收集器（无外部依赖）。
// 输出标准 Prometheus text exposition format，可被 Prometheus server 直接抓取。
//
// 指标列表（按层分组）：
//
// [协议层]
//   jte_connections_total          counter   设备连接总数
//   jte_messages_received_total    counter   消息接收总数（按协议标签）
//   jte_messages_sent_total        counter   消息发送总数（按协议标签）
//   jte_message_parse_errors_total counter   消息解析错误总数（按协议/错误类型标签）
//   jte_active_connections         gauge     当前活跃连接数
//   jte_online_devices             gauge     在线设备数
//
// [视频层]
//   jte_video_bitrate              gauge     视频码率（kbps，按设备/通道标签）
//   jte_video_framerate_fps        gauge     视频帧率
//   jte_video_packet_loss_percent  gauge     视频丢包率
//   jte_video_latency_ms           gauge     视频端到端延迟（毫秒）
//   jte_video_concurrent_plays     gauge     并发播放数
//   jte_rtp_fallback_total         counter   UDP→TCP fallback 次数
//   jte_rtp_conn_reuse_rate        gauge     RTP 连接复用率
//   jte_rtp_active_conns           gauge     活跃 RTP 转发连接数
//
// [AI 层]
//   jte_ai_inference_duration_ms   histogram AI 推理耗时分布（毫秒）
//   jte_ai_model_hit_rate          gauge     模型命中率（0-1）
//   jte_ai_fallback_total          counter   AI fallback 次数（按层级标签）
//   jte_ai_requests_total          counter   AI 推理请求总数（按模型标签）
//
// [存储层]
//   jte_storage_write_total        counter   存储写入总数（按层标签）
//   jte_storage_write_duration     histogram 存储写入耗时分布
//   jte_storage_write_failures     counter   存储写入失败数
//   jte_tdengine_write_rate        gauge     TDengine 写入速率（点/秒）
//   jte_tdengine_query_duration    histogram TDengine 查询延迟分布
//   jte_archive_progress           gauge     归档进度（0-1）
//   jte_archive_tasks_total        counter   归档任务总数
//   jte_archive_failures_total     counter   归档失败总数
//
// [系统层]
//   jte_module_status              gauge     模块状态（1=running, 0=stopped, -1=failed）
//   jte_license_tier               gauge     当前授权等级
//   jte_module_restart_count       gauge     模块重启次数
// ===================================================================

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MetricType 指标类型
type MetricType int

const (
	TypeCounter MetricType = iota
	TypeGauge
	TypeHistogram
)

// Metric 基础指标接口
type Metric interface {
	Name() string
	Help() string
	Type() MetricType
	WritePrometheus(w io.Writer)
}

// Collector 全局指标收集器
type Collector struct {
	mu      sync.RWMutex
	metrics map[string]Metric
}

var defaultCollector = &Collector{
	metrics: make(map[string]Metric),
}

// DefaultCollector 返回默认全局收集器
func DefaultCollector() *Collector {
	return defaultCollector
}

// Register 注册指标
func (c *Collector) Register(m Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics[m.Name()] = m
}

// WritePrometheus 输出所有指标为 Prometheus text format
func (c *Collector) WritePrometheus(w io.Writer) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0, len(c.metrics))
	for name := range c.metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		c.metrics[name].WritePrometheus(w)
	}
}

// ========== Counter ==========

// Counter 单调递增计数器
type Counter struct {
	name  string
	help  string
	mu    sync.Mutex
	value float64
	// 带标签的计数器值
	labeled map[string]float64 // labelKey → value
}

// NewCounter 创建计数器
func NewCounter(name, help string) *Counter {
	c := &Counter{
		name:    name,
		help:    help,
		labeled: make(map[string]float64),
	}
	defaultCollector.Register(c)
	return c
}

func (c *Counter) Name() string   { return c.name }
func (c *Counter) Help() string   { return c.help }
func (c *Counter) Type() MetricType { return TypeCounter }

// Inc 无标签递增 1
func (c *Counter) Inc() {
	c.IncWithLabels(nil)
}

// IncWithLabels 按标签递增 1
func (c *Counter) IncWithLabels(labels map[string]string) {
	c.AddWithLabels(1, labels)
}

// Add 无标签增加 delta
func (c *Counter) Add(delta float64) {
	c.AddWithLabels(delta, nil)
}

// AddWithLabels 按标签增加 delta
func (c *Counter) AddWithLabels(delta float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := labelKey(labels)
	c.labeled[key] += delta
	if key == "" {
		c.value += delta
	} else {
		// 维护总和
		c.value += delta
	}
}

// Value 返回无标签总和
func (c *Counter) Value() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *Counter) WritePrometheus(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n", c.name, c.help)
	fmt.Fprintf(w, "# TYPE %s counter\n", c.name)
	// 按标签 key 排序输出
	keys := make([]string, 0, len(c.labeled))
	for k := range c.labeled {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" {
			fmt.Fprintf(w, "%s %g\n", c.name, c.labeled[k])
		} else {
			fmt.Fprintf(w, "%s{%s} %g\n", c.name, k, c.labeled[k])
		}
	}
}

// ========== Gauge ==========

// Gauge 可增减仪表
type Gauge struct {
	name    string
	help    string
	mu      sync.Mutex
	labeled map[string]float64
}

func NewGauge(name, help string) *Gauge {
	g := &Gauge{
		name:    name,
		help:    help,
		labeled: make(map[string]float64),
	}
	defaultCollector.Register(g)
	return g
}

func (g *Gauge) Name() string   { return g.name }
func (g *Gauge) Help() string   { return g.help }
func (g *Gauge) Type() MetricType { return TypeGauge }

func (g *Gauge) Set(value float64) {
	g.SetWithLabels(value, nil)
}

func (g *Gauge) SetWithLabels(value float64, labels map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.labeled[labelKey(labels)] = value
}

func (g *Gauge) Inc() {
	g.Add(1)
}

func (g *Gauge) Add(delta float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.labeled[""] += delta
}

func (g *Gauge) Dec() {
	g.Add(-1)
}

func (g *Gauge) WritePrometheus(w io.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n", g.name, g.help)
	fmt.Fprintf(w, "# TYPE %s gauge\n", g.name)
	keys := make([]string, 0, len(g.labeled))
	for k := range g.labeled {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" {
			fmt.Fprintf(w, "%s %g\n", g.name, g.labeled[k])
		} else {
			fmt.Fprintf(w, "%s{%s} %g\n", g.name, k, g.labeled[k])
		}
	}
}

// ========== Histogram ==========

// Histogram 直方图（固定桶）
type Histogram struct {
	name    string
	help    string
	buckets []float64
	mu      sync.Mutex
	counts  []uint64 // 每个桶的累计计数
	sum     float64
	count   uint64
}

func NewHistogram(name, help string, buckets []float64) *Histogram {
	h := &Histogram{
		name:    name,
		help:    help,
		buckets: append([]float64(nil), buckets...),
		counts:  make([]uint64, len(buckets)),
	}
	defaultCollector.Register(h)
	return h
}

func (h *Histogram) Name() string   { return h.name }
func (h *Histogram) Help() string   { return h.help }
func (h *Histogram) Type() MetricType { return TypeHistogram }

func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += value
	h.count++
	for i, bound := range h.buckets {
		if value <= bound {
			h.counts[i]++
		}
	}
}

func (h *Histogram) WritePrometheus(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n", h.name, h.help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", h.name)
	for i, bound := range h.buckets {
		fmt.Fprintf(w, "%s_bucket{le=\"%g\"} %d\n", h.name, bound, h.counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", h.name, h.count)
	fmt.Fprintf(w, "%s_sum %g\n", h.name, h.sum)
	fmt.Fprintf(w, "%s_count %d\n", h.name, h.count)
}

// ========== 辅助函数 ==========

// labelKey 将标签 map 转为 Prometheus label 字符串
func labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, escapeLabelValue(labels[k])))
	}
	return strings.Join(parts, ",")
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "\"", "\\\"")
	v = strings.ReplaceAll(v, "\n", "\\n")
	return v
}

// ========== 预定义指标 ==========

var (
	// ==================== 协议层指标 ====================
	// ConnectionsTotal 设备连接总数
	ConnectionsTotal = NewCounter("jte_connections_total", "Total number of device connections")
	// MessagesReceivedTotal 消息接收总数（按协议标签：jt808/jt809/jt1078/jt1253）
	MessagesReceivedTotal = NewCounter("jte_messages_received_total", "Total number of messages received")
	// MessagesSentTotal 消息发送总数（按协议标签）
	MessagesSentTotal = NewCounter("jte_messages_sent_total", "Total number of messages sent")
	// MessageParseErrorsTotal 消息解析错误总数（按协议/错误类型标签）
	MessageParseErrorsTotal = NewCounter("jte_message_parse_errors_total", "Total number of message parse errors")
	// MessagesTotal 消息处理总数（向后兼容，等价于 MessagesReceivedTotal）
	MessagesTotal = NewCounter("jte_messages_total", "Total number of messages processed (legacy alias)")
	// ActiveConnections 当前活跃连接数（按协议标签）
	ActiveConnections = NewGauge("jte_active_connections", "Number of active connections")
	// OnlineDevices 在线设备数
	OnlineDevices = NewGauge("jte_online_devices", "Number of currently online devices")

	// ==================== 视频层指标 ====================
	// VideoBitrate 视频码率（kbps）
	VideoBitrate = NewGauge("jte_video_bitrate", "Video bitrate in kbps")
	// VideoFramerate 视频帧率
	VideoFramerate = NewGauge("jte_video_framerate_fps", "Video framerate in fps")
	// VideoPacketLoss 视频丢包率
	VideoPacketLoss = NewGauge("jte_video_packet_loss_percent", "Video packet loss percentage")
	// VideoLatencyMs 视频端到端延迟（毫秒）
	VideoLatencyMs = NewGauge("jte_video_latency_ms", "Video end-to-end latency in milliseconds")
	// VideoConcurrentPlays 并发播放数
	VideoConcurrentPlays = NewGauge("jte_video_concurrent_plays", "Number of concurrent video plays")
	// RTPFallbackTotal UDP→TCP 自动降级次数
	RTPFallbackTotal = NewCounter("jte_rtp_fallback_total", "Number of UDP to TCP fallback events")
	// RTPConnReuseRate RTP 长连接复用率（0-1）
	RTPConnReuseRate = NewGauge("jte_rtp_conn_reuse_rate", "RTP connection pool reuse rate (0-1)")
	// RTPActiveConns 当前活跃 RTP 转发连接数
	RTPActiveConns = NewGauge("jte_rtp_active_conns", "Active RTP forwarding connections (udp+tcp)")

	// ==================== AI 层指标 ====================
	// AIInferenceDurationMs AI 推理耗时分布（毫秒）
	AIInferenceDurationMs = NewHistogram("jte_ai_inference_duration_ms",
		"AI inference duration in milliseconds",
		[]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	// AIModelHitRate 模型命中率（0-1，命中=本地 ONNX 推理而非 API 兜底）
	AIModelHitRate = NewGauge("jte_ai_model_hit_rate", "AI model hit rate (0-1, local ONNX vs API fallback)")
	// AIFallbackTotal AI fallback 次数（按层级标签：onnx→deepseek→heuristic）
	AIFallbackTotal = NewCounter("jte_ai_fallback_total", "Number of AI fallback events")
	// AIRequestsTotal AI 推理请求总数（按模型标签）
	AIRequestsTotal = NewCounter("jte_ai_requests_total", "Total number of AI inference requests")

	// ==================== 存储层指标 ====================
	// StorageWriteTotal 存储写入总数
	StorageWriteTotal = NewCounter("jte_storage_write_total", "Total number of storage writes")
	// StorageWriteDuration 存储写入耗时
	StorageWriteDuration = NewHistogram("jte_storage_write_duration_seconds",
		"Storage write duration in seconds",
		[]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5})
	// StorageWriteFailures 存储写入失败数
	StorageWriteFailures = NewCounter("jte_storage_write_failures_total", "Number of storage write failures")
	// TDengineWriteRate TDengine 写入速率（点/秒）
	TDengineWriteRate = NewGauge("jte_tdengine_write_rate", "TDengine write rate in points per second")
	// TDengineQueryDuration TDengine 查询延迟分布（秒）
	TDengineQueryDuration = NewHistogram("jte_tdengine_query_duration_seconds",
		"TDengine query duration in seconds",
		[]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10})
	// ArchiveProgress 归档进度（0-1）
	ArchiveProgress = NewGauge("jte_archive_progress", "Archive task progress (0-1)")
	// ArchiveTasksTotal 归档任务总数
	ArchiveTasksTotal = NewCounter("jte_archive_tasks_total", "Total number of archive tasks")
	// ArchiveFailuresTotal 归档失败总数
	ArchiveFailuresTotal = NewCounter("jte_archive_failures_total", "Total number of archive task failures")

	// ==================== 系统层指标 ====================
	// ModuleStatus 模块状态
	ModuleStatus = NewGauge("jte_module_status", "Module status: 1=running, 0=stopped, -1=failed")
	// ModuleRestartCount 模块重启次数
	ModuleRestartCount = NewGauge("jte_module_restart_count", "Module restart count (monitor instability)")
	// LicenseTier 当前授权等级
	LicenseTier = NewGauge("jte_license_tier", "Current license tier rank (0=free, 1=standard, 2=professional, 3=enterprise)")
	// ==================== 会话层指标（INDUSTRIAL-FIX-2026-07-24） ====================
	// SessionSendQueueDepth 会话发送队列深度（按会话标签）
	// 用于监控慢客户端：队列深度持续高说明客户端写入缓慢，可能需要踢出
	SessionSendQueueDepth = NewGauge("jte_session_send_queue_depth", "Session send queue depth (monitor slow clients)")
	// SessionStateDistribution 会话状态分布（connected/registered/authenticated）
	SessionStateDistribution = NewGauge("jte_session_state_distribution", "Session count by state (connected/registered/authenticated)")
	// MessagesPerSession 每会话消息计数（用于检测消息洪水）
	MessagesPerSession = NewCounter("jte_messages_per_session_total", "Total messages per session (flood detection)")
)

// ========== 原子计数器（高性能场景） ==========

// AtomicCounter 无锁原子计数器（仅支持无标签场景，性能优先）
type AtomicCounter struct {
	value atomic.Int64
}

func (a *AtomicCounter) Inc()    { a.value.Add(1) }
func (a *AtomicCounter) Add(n int64) { a.value.Add(n) }
func (a *AtomicCounter) Load() int64 { return a.value.Load() }
