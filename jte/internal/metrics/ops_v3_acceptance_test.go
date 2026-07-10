package metrics

// ===================================================================
// 运维验收 3: 监控可见
//
// 验证项：
//   1. Prometheus 指标覆盖：连接数/消息速率/存储延迟/视频质量
//   2. Prometheus text exposition 格式正确
//   3. 告警规则文件存在且包含 CPU>80%/内存>90%/连接数突降
//   4. Alertmanager 配置存在且 5 分钟内通知
// ===================================================================

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// projectRoot 返回项目根目录（jte/ 的上级目录）。
func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	// 当前文件: jte/internal/metrics/ops_v3_acceptance_test.go
	// jte/ 目录: 上溯 2 级
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// TestV3_Metrics_ConnectionCount 验证连接数指标存在。
// 验收标准：Prometheus 指标包含连接数。
func TestV3_Metrics_ConnectionCount(t *testing.T) {
	var sb strings.Builder
	DefaultCollector().WritePrometheus(&sb)
	output := sb.String()

	requiredMetrics := []string{
		"jte_connections_total",     // 设备连接总数
		"jte_active_connections",    // 当前活跃连接数
		"jte_online_devices",        // 在线设备数
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("缺少连接数指标: %s", m)
		}
	}
}

// TestV3_Metrics_MessageRate 验证消息速率指标存在。
// 验收标准：Prometheus 指标包含消息速率。
func TestV3_Metrics_MessageRate(t *testing.T) {
	var sb strings.Builder
	DefaultCollector().WritePrometheus(&sb)
	output := sb.String()

	requiredMetrics := []string{
		"jte_messages_received_total",    // 消息接收总数
		"jte_messages_sent_total",        // 消息发送总数
		"jte_message_parse_errors_total", // 消息解析错误
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("缺少消息速率指标: %s", m)
		}
	}
}

// TestV3_Metrics_StorageLatency 验证存储延迟指标存在。
// 验收标准：Prometheus 指标包含存储延迟。
func TestV3_Metrics_StorageLatency(t *testing.T) {
	var sb strings.Builder
	DefaultCollector().WritePrometheus(&sb)
	output := sb.String()

	requiredMetrics := []string{
		"jte_storage_write_total",        // 存储写入总数
		"jte_storage_write_duration",     // 存储写入耗时
		"jte_storage_write_failures",     // 存储写入失败
		"jte_tdengine_write_rate",        // TDengine 写入速率
		"jte_tdengine_query_duration",    // TDengine 查询延迟
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("缺少存储延迟指标: %s", m)
		}
	}
}

// TestV3_Metrics_VideoQuality 验证视频质量指标存在。
// 验收标准：Prometheus 指标包含视频质量。
func TestV3_Metrics_VideoQuality(t *testing.T) {
	var sb strings.Builder
	DefaultCollector().WritePrometheus(&sb)
	output := sb.String()

	requiredMetrics := []string{
		"jte_video_bitrate",              // 视频码率
		"jte_video_framerate_fps",        // 视频帧率
		"jte_video_packet_loss_percent",  // 视频丢包率
		"jte_video_latency_ms",           // 视频延迟
		"jte_video_concurrent_plays",     // 并发播放数
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("缺少视频质量指标: %s", m)
		}
	}
}

// TestV3_Metrics_SystemLayer 验证系统层指标存在。
func TestV3_Metrics_SystemLayer(t *testing.T) {
	var sb strings.Builder
	DefaultCollector().WritePrometheus(&sb)
	output := sb.String()

	requiredMetrics := []string{
		"jte_module_status",         // 模块状态
		"jte_license_tier",          // 授权等级
		"jte_module_restart_count",  // 模块重启次数
	}
	for _, m := range requiredMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("缺少系统层指标: %s", m)
		}
	}
}

// TestV3_PrometheusFormat 验证 Prometheus text exposition 格式正确。
func TestV3_PrometheusFormat(t *testing.T) {
	// 测试 Counter 格式
	c := NewCounter("v3_test_counter_total", "test counter for format")
	c.IncWithLabels(map[string]string{"protocol": "jt808"})
	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	if !strings.HasPrefix(output, "# HELP v3_test_counter_total") {
		t.Errorf("缺少 HELP 行:\n%s", output)
	}
	if !strings.Contains(output, "# TYPE v3_test_counter_total counter") {
		t.Errorf("缺少 TYPE 行:\n%s", output)
	}
	if !strings.Contains(output, `v3_test_counter_total{protocol="jt808"} 1`) {
		t.Errorf("缺少指标值行:\n%s", output)
	}
}

// TestV3_AlertRules_FileExists 验证告警规则文件存在。
// 验收标准：告警规则覆盖 CPU>80%/内存>90%/连接数突降。
func TestV3_AlertRules_FileExists(t *testing.T) {
	alertsPath := filepath.Join(projectRoot(), "configs", "alerts.yaml")
	content, err := os.ReadFile(alertsPath)
	if err != nil {
		t.Fatalf("读取告警规则文件失败: %v", err)
	}
	alerts := string(content)

	// 验证连接数突降告警
	if !strings.Contains(alerts, "JTEOnlineDevicesDrop") {
		t.Error("告警规则缺少: 在线设备数突降 (JTEOnlineDevicesDrop)")
	}

	// 验证主机 CPU > 80% 告警
	if !strings.Contains(alerts, "JTEHostCPUHigh") {
		t.Error("告警规则缺少: 主机 CPU > 80% (JTEHostCPUHigh)")
	}

	// 验证主机内存 > 90% 告警
	if !strings.Contains(alerts, "JTEHostMemoryHigh") {
		t.Error("告警规则缺少: 主机内存 > 90% (JTEHostMemoryHigh)")
	}

	// 验证进程内存告警
	if !strings.Contains(alerts, "JTEMemoryHigh") {
		t.Error("告警规则缺少: 进程内存使用过高 (JTEMemoryHigh)")
	}

	// 验证模块崩溃告警
	if !strings.Contains(alerts, "JTEModuleDown") {
		t.Error("告警规则缺少: 模块崩溃 (JTEModuleDown)")
	}

	// 验证存储写入失败告警
	if !strings.Contains(alerts, "JTEStorageWriteFailureRateHigh") {
		t.Error("告警规则缺少: 存储写入失败率 (JTEStorageWriteFailureRateHigh)")
	}

	// 验证告警持续时间为 5 分钟（for: 5m）
	if !strings.Contains(alerts, "for: 5m") {
		t.Error("告警规则缺少 5 分钟持续时间 (for: 5m)")
	}
}

// TestV3_AlertManager_FileExists 验证 Alertmanager 配置文件存在。
// 验收标准：Alertmanager 5 分钟内通知。
func TestV3_AlertManager_FileExists(t *testing.T) {
	amPath := filepath.Join(projectRoot(), "configs", "alertmanager.yml")
	content, err := os.ReadFile(amPath)
	if err != nil {
		t.Fatalf("读取 Alertmanager 配置失败: %v", err)
	}
	am := string(content)

	// 验证 critical 级别立即通知
	if !strings.Contains(am, "severity: critical") {
		t.Error("Alertmanager 缺少 critical 级别路由")
	}
	// 验证 group_wait 配置
	if !strings.Contains(am, "group_wait") {
		t.Error("Alertmanager 缺少 group_wait 配置")
	}
	// 验证钉钉/企业微信 webhook 配置
	if !strings.Contains(am, "dingtalk") && !strings.Contains(am, "webhook") {
		t.Error("Alertmanager 缺少 webhook 通知配置")
	}
}

// TestV3_PrometheusConfig_FileExists 验证 Prometheus 抓取配置存在。
func TestV3_PrometheusConfig_FileExists(t *testing.T) {
	promPath := filepath.Join(projectRoot(), "configs", "prometheus.yml")
	content, err := os.ReadFile(promPath)
	if err != nil {
		t.Fatalf("读取 Prometheus 配置失败: %v", err)
	}
	prom := string(content)

	if !strings.Contains(prom, "jte-engine") {
		t.Error("Prometheus 配置缺少 jte-engine 抓取目标")
	}
	if !strings.Contains(prom, "rule_files") {
		t.Error("Prometheus 配置缺少 rule_files 引用")
	}
	if !strings.Contains(prom, "alertmanager") {
		t.Error("Prometheus 配置缺少 alertmanager 配置")
	}
}
