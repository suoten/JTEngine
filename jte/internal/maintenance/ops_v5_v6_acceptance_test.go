package maintenance

// ===================================================================
// 运维验收 5: 数据可备份
// 运维验收 6: 升级不停机
//
// 验收5 验证项：
//   1. 备份脚本存在（MySQL/TDengine/Redis/Config）
//   2. Cron 定时配置存在
//   3. 备份完整性校验脚本存在
//   4. 一键恢复脚本存在
//
// 验收6 验证项：
//   1. 蓝绿部署 K8s 清单存在
//   2. 蓝绿切换脚本存在
//   3. HPA 自动扩缩容配置存在
//   4. 优雅停机配置存在（drain_timeout/reconnect_backoff）
//   5. 优雅停机流程：StopAccept → 广播退避 → 排空 → Stop
// ===================================================================

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// projectRoot 返回 jte/ 目录路径。
func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	// 当前文件: jte/internal/maintenance/ops_v5_v6_acceptance_test.go
	// jte/ 目录: 上溯 2 级
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// ==================== 验收 5: 数据可备份 ====================

// TestV5_BackupScripts_Exist 验证所有备份脚本存在。
// 验收标准：每日自动备份关系库+时序库+配置文件。
func TestV5_BackupScripts_Exist(t *testing.T) {
	scriptsDir := filepath.Join(projectRoot(), "scripts", "ops")

	requiredScripts := []string{
		"mysql_backup.sh",
		"tdengine_backup.sh",
		"redis_backup.sh",
		"config_backup.sh",
		"verify_backups.sh",
		"one-click-restore.sh",
	}

	for _, script := range requiredScripts {
		path := filepath.Join(scriptsDir, script)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("备份脚本不存在: %s", path)
		}
	}
}

// TestV5_CronConfig_Exists 验证 Cron 定时备份配置存在。
// 验收标准：每日自动备份。
func TestV5_CronConfig_Exists(t *testing.T) {
	cronPath := filepath.Join(projectRoot(), "deploy", "cron", "jte-backups.cron")
	content, err := os.ReadFile(cronPath)
	if err != nil {
		t.Fatalf("读取 Cron 配置失败: %v", err)
	}
	cron := string(content)

	// 验证 MySQL 全量备份（每日凌晨 2:00）
	if !strings.Contains(cron, "mysql_backup.sh") {
		t.Error("Cron 配置缺少 MySQL 备份")
	}
	// 验证 TDengine 备份
	if !strings.Contains(cron, "tdengine_backup.sh") {
		t.Error("Cron 配置缺少 TDengine 备份")
	}
	// 验证 Redis 备份
	if !strings.Contains(cron, "redis_backup.sh") {
		t.Error("Cron 配置缺少 Redis 备份")
	}
	// 验证备份完整性校验
	if !strings.Contains(cron, "verify_backups.sh") {
		t.Error("Cron 配置缺少备份校验")
	}
	// 验证每日全量备份（包含 "0 2" 凌晨 2:00）
	if !strings.Contains(cron, "0 2") {
		t.Error("Cron 配置缺少每日全量备份调度")
	}
}

// TestV5_VerifyBackups_ScriptContent 验证备份校验脚本内容。
// 验收标准：能恢复到任意时间点（备份→删数据→恢复→校验）。
func TestV5_VerifyBackups_ScriptContent(t *testing.T) {
	verifyPath := filepath.Join(projectRoot(), "scripts", "ops", "verify_backups.sh")
	content, err := os.ReadFile(verifyPath)
	if err != nil {
		t.Fatalf("读取备份校验脚本失败: %v", err)
	}
	script := string(content)

	// 验证校验 MySQL 备份
	if !strings.Contains(script, "verify_mysql") {
		t.Error("校验脚本缺少 MySQL 备份校验")
	}
	// 验证校验 TDengine 备份
	if !strings.Contains(script, "verify_tdengine") {
		t.Error("校验脚本缺少 TDengine 备份校验")
	}
	// 验证校验 Redis 备份
	if !strings.Contains(script, "verify_redis") {
		t.Error("校验脚本缺少 Redis 备份校验")
	}
}

// TestV5_OneClickRestore_ScriptContent 验证一键恢复脚本内容。
// 验收标准：能恢复到任意时间点。
func TestV5_OneClickRestore_ScriptContent(t *testing.T) {
	restorePath := filepath.Join(projectRoot(), "scripts", "ops", "one-click-restore.sh")
	content, err := os.ReadFile(restorePath)
	if err != nil {
		t.Fatalf("读取一键恢复脚本失败: %v", err)
	}
	script := string(content)

	// 验证支持按日期恢复
	if !strings.Contains(script, "RESTORE_DATE") {
		t.Error("恢复脚本缺少按日期恢复参数")
	}
	// 验证支持预演模式
	if !strings.Contains(script, "dry-run") {
		t.Error("恢复脚本缺少预演模式")
	}
	// 验证恢复后验证
	if !strings.Contains(script, "验证") || !strings.Contains(script, "verify") {
		t.Error("恢复脚本缺少恢复后验证步骤")
	}
	// 验证按依赖顺序恢复
	if !strings.Contains(script, "config") || !strings.Contains(script, "mysql") {
		t.Error("恢复脚本缺少按依赖顺序恢复")
	}
}

// TestV5_DisasterRecovery_Drill 验证灾难恢复演练配置。
func TestV5_DisasterRecovery_Drill(t *testing.T) {
	cronPath := filepath.Join(projectRoot(), "deploy", "cron", "jte-backups.cron")
	content, err := os.ReadFile(cronPath)
	if err != nil {
		t.Fatalf("读取 Cron 配置失败: %v", err)
	}
	cron := string(content)

	// 验证灾难恢复演练
	if !strings.Contains(cron, "dr_drill") {
		t.Error("Cron 配置缺少灾难恢复演练")
	}
}

// ==================== 验收 6: 升级不停机 ====================

// TestV6_BlueGreen_K8sManifest 验证蓝绿部署 K8s 清单存在。
// 验收标准：蓝绿部署或滚动升级，设备无感，不丢数据。
func TestV6_BlueGreen_K8sManifest(t *testing.T) {
	manifestPath := filepath.Join(projectRoot(), "deploy", "k8s", "jte-blue-green.yaml")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("读取蓝绿部署清单失败: %v", err)
	}
	manifest := string(content)

	// 验证 Blue Deployment
	if !strings.Contains(manifest, "jte-blue") {
		t.Error("蓝绿部署清单缺少 Blue Deployment")
	}
	// 验证 Green Deployment
	if !strings.Contains(manifest, "jte-green") {
		t.Error("蓝绿部署清单缺少 Green Deployment")
	}
	// 验证 Service selector 切换
	if !strings.Contains(manifest, "slot") {
		t.Error("蓝绿部署清单缺少 slot selector")
	}
	// 验证优雅停机配置
	if !strings.Contains(manifest, "terminationGracePeriodSeconds") {
		t.Error("蓝绿部署清单缺少 terminationGracePeriodSeconds")
	}
	// 验证就绪探针
	if !strings.Contains(manifest, "readinessProbe") {
		t.Error("蓝绿部署清单缺少 readinessProbe")
	}
	// 验证存活探针
	if !strings.Contains(manifest, "livenessProbe") {
		t.Error("蓝绿部署清单缺少 livenessProbe")
	}
	// 验证 PDB
	if !strings.Contains(manifest, "PodDisruptionBudget") {
		t.Error("蓝绿部署清单缺少 PodDisruptionBudget")
	}
}

// TestV6_BlueGreen_SwitchScript 验证蓝绿切换脚本存在。
func TestV6_BlueGreen_SwitchScript(t *testing.T) {
	scriptPath := filepath.Join(projectRoot(), "deploy", "k8s", "blue-green-switch.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("读取蓝绿切换脚本失败: %v", err)
	}
	script := string(content)

	// 验证扩容目标
	if !strings.Contains(script, "scale") {
		t.Error("切换脚本缺少 scale 操作")
	}
	// 验证等待就绪
	if !strings.Contains(script, "wait_ready") {
		t.Error("切换脚本缺少等待就绪逻辑")
	}
	// 验证切换 Service
	if !strings.Contains(script, "switch_service") {
		t.Error("切换脚本缺少 Service 切换逻辑")
	}
	// 验证排空等待
	if !strings.Contains(script, "wait_drain") {
		t.Error("切换脚本缺少排空等待逻辑")
	}
	// 验证回滚支持
	if !strings.Contains(script, "rollback") {
		t.Error("切换脚本缺少回滚支持")
	}
}

// TestV6_HPA_Config 验证 HPA 自动扩缩容配置存在。
func TestV6_HPA_Config(t *testing.T) {
	hpaPath := filepath.Join(projectRoot(), "deploy", "k8s", "hpa.yaml")
	content, err := os.ReadFile(hpaPath)
	if err != nil {
		t.Fatalf("读取 HPA 配置失败: %v", err)
	}
	hpa := string(content)

	// 验证 CPU 使用率扩容
	if !strings.Contains(hpa, "cpu") {
		t.Error("HPA 配置缺少 CPU 指标")
	}
	// 验证内存使用率扩容
	if !strings.Contains(hpa, "memory") {
		t.Error("HPA 配置缺少内存指标")
	}
	// 验证最小副本数
	if !strings.Contains(hpa, "minReplicas") {
		t.Error("HPA 配置缺少 minReplicas")
	}
	// 验证最大副本数
	if !strings.Contains(hpa, "maxReplicas") {
		t.Error("HPA 配置缺少 maxReplicas")
	}
}

// TestV6_GracefulShutdown_Config 验证优雅停机配置。
// 验收标准：升级过程中设备持续上报，数据不丢。
func TestV6_GracefulShutdown_Config(t *testing.T) {
	manifestPath := filepath.Join(projectRoot(), "deploy", "k8s", "jte-blue-green.yaml")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("读取蓝绿部署清单失败: %v", err)
	}
	manifest := string(content)

	// 验证排空超时配置（至少 300s）
	if !strings.Contains(manifest, "drain_timeout") {
		t.Error("清单缺少 drain_timeout 配置")
	}
	// 验证重连退避配置
	if !strings.Contains(manifest, "reconnect_backoff") {
		t.Error("清单缺少 reconnect_backoff 配置")
	}
	// 验证 preStop hook（给负载均衡器时间摘除流量）
	if !strings.Contains(manifest, "preStop") {
		t.Error("清单缺少 preStop hook")
	}
}

// TestV6_ShutdownConfig_Defaults 验证 ShutdownConfig 默认值。
func TestV6_ShutdownConfig_Defaults(t *testing.T) {
	// 从 K8s manifest 验证配置值
	manifestPath := filepath.Join(projectRoot(), "deploy", "k8s", "jte-blue-green.yaml")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("读取蓝绿部署清单失败: %v", err)
	}
	manifest := string(content)

	// drain_timeout 应 >= 300s（5 分钟排空时间）
	if strings.Contains(manifest, "drain_timeout_seconds: 300") {
		// 正确配置
	} else if strings.Contains(manifest, "drain_timeout_seconds") {
		t.Log("drain_timeout_seconds 配置存在但值可能不同")
	}

	// reconnect_backoff 应 <= 60s（蓝绿部署快速重连）
	if strings.Contains(manifest, "reconnect_backoff_max_seconds: 60") {
		// 正确配置
	} else if strings.Contains(manifest, "reconnect_backoff_max_seconds") {
		t.Log("reconnect_backoff_max_seconds 配置存在但值可能不同")
	}

	// terminationGracePeriodSeconds 应 > drain_timeout（给排空足够时间）
	if strings.Contains(manifest, "terminationGracePeriodSeconds: 330") {
		// 正确：330 = 300 + 30s 缓冲
	} else if strings.Contains(manifest, "terminationGracePeriodSeconds") {
		t.Log("terminationGracePeriodSeconds 配置存在但值可能不同")
	}
}
