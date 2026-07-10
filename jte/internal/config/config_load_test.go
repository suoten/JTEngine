package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestConfig 写入临时 YAML 配置文件并返回路径。
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "jte.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad_DefaultAPISecurity(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	// 重置全局配置，避免受其他测试影响
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
`
	path := writeTestConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// ConnLimitPerIP 默认 100
	if cfg.API.Security.ConnLimitPerIP != 100 {
		t.Errorf("ConnLimitPerIP = %d, want 100", cfg.API.Security.ConnLimitPerIP)
	}
	// BodyLimitBytes 默认 10MB
	if cfg.API.Security.BodyLimitBytes != 10*1024*1024 {
		t.Errorf("BodyLimitBytes = %d, want %d", cfg.API.Security.BodyLimitBytes, 10*1024*1024)
	}
}

func TestLoad_ExplicitAPISecurity(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
  security:
    conn_limit_per_ip: 50
    body_limit_bytes: 5242880
gateway:
  port: 7611
`
	path := writeTestConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.API.Security.ConnLimitPerIP != 50 {
		t.Errorf("ConnLimitPerIP = %d, want 50", cfg.API.Security.ConnLimitPerIP)
	}
	if cfg.API.Security.BodyLimitBytes != 5242880 {
		t.Errorf("BodyLimitBytes = %d, want 5242880", cfg.API.Security.BodyLimitBytes)
	}
}

func TestLoad_ArchiveDefaults(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
`
	path := writeTestConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 归档默认启用
	if !cfg.Storage.Archive.Enabled {
		t.Error("Archive.Enabled should default to true")
	}
	if cfg.Storage.Archive.IntervalHours != 24 {
		t.Errorf("IntervalHours = %d, want 24", cfg.Storage.Archive.IntervalHours)
	}
	if cfg.Storage.Archive.ScheduleHour != 2 {
		t.Errorf("ScheduleHour = %d, want 2", cfg.Storage.Archive.ScheduleHour)
	}
	if cfg.Storage.Archive.DeleteDelayDays != 7 {
		t.Errorf("DeleteDelayDays = %d, want 7", cfg.Storage.Archive.DeleteDelayDays)
	}
}

func TestLoad_GatewayDefaults(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
`
	path := writeTestConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.MaxDevices != 20 {
		t.Errorf("MaxDevices = %d, want 20", cfg.Gateway.MaxDevices)
	}
	if cfg.Gateway.MaxConnections != 120000 {
		t.Errorf("MaxConnections = %d, want 120000", cfg.Gateway.MaxConnections)
	}
}

func TestLoad_InvalidHeartbeatInterval(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
  heartbeat_interval: 5
`
	path := writeTestConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for heartbeat_interval < 10")
	}
}

func TestLoad_InvalidHeartbeatTimeout(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
  heartbeat_interval: 30
  heartbeat_timeout: 60
`
	path := writeTestConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for heartbeat_timeout <= heartbeat_interval*3")
	}
}

func TestLoad_ValidHeartbeatConfig(t *testing.T) {
	t.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "test"
gateway:
  port: 7611
  heartbeat_interval: 30
  heartbeat_timeout: 120
`
	path := writeTestConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.HeartbeatInterval != 30 {
		t.Errorf("HeartbeatInterval = %d, want 30", cfg.Gateway.HeartbeatInterval)
	}
}

func TestLoad_InsecureJWTRejected(t *testing.T) {
	// 不设置 JTE_ALLOW_INSECURE_JWT，使用弱密钥应被拒绝
	os.Unsetenv("JTE_ALLOW_INSECURE_JWT")
	globalConfig = nil

	content := `
api:
  port: 8080
  jwt_secret: "weak"
gateway:
  port: 7611
`
	path := writeTestConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for insecure JWT secret")
	}
}
