package config

// ===================================================================
// FIXED-2026-07-23 [P2]: 生产部署配置加固测试
// 1. 生产环境禁止 SQLite
// 2. 生产环境必须配置时序库
// 3. 809 熔断器配置默认值
// 4. 网关安全配置默认值
// ===================================================================

import (
	"os"
	"strings"
	"testing"
)

// TestP2_ValidateForProduction_SqliteRejected 生产环境禁止 SQLite
func TestP2_ValidateForProduction_SqliteRejected(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type: "sqlite",
			DSN:  "./data/jte.db",
			// 不配置时序库
		},
		API: APIConfig{
			JWTSecret:   strings.Repeat("a", 32),
			JWTExpireHours: 24,
			JWT: &JWTConfig{
				Secrets:   map[string]string{"kid-2026-06": strings.Repeat("b", 32)},
				ActiveKid: "kid-2026-06",
			},
		},
		Auth: AuthConfig{
			OfflineUnbindSecret: strings.Repeat("c", 32),
		},
	}

	err := cfg.ValidateForProduction()
	if err == nil {
		t.Fatal("expected error when storage.type=sqlite in production")
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Errorf("error should mention sqlite, got: %v", err)
	}
}

// TestP2_ValidateForProduction_NoTimeSeries 生产环境必须配置时序库
func TestP2_ValidateForProduction_NoTimeSeries(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type: "postgres",
			DSN:  "postgres://user:pass@localhost/db",
			// 不配置时序库
		},
		API: APIConfig{
			JWTSecret:   strings.Repeat("a", 32),
			JWTExpireHours: 24,
			JWT: &JWTConfig{
				Secrets:   map[string]string{"kid-2026-06": strings.Repeat("b", 32)},
				ActiveKid: "kid-2026-06",
			},
		},
		Auth: AuthConfig{
			OfflineUnbindSecret: strings.Repeat("c", 32),
		},
	}

	err := cfg.ValidateForProduction()
	if err == nil {
		t.Fatal("expected error when time_series.driver is empty in production")
	}
	if !strings.Contains(err.Error(), "time_series.driver") {
		t.Errorf("error should mention time_series.driver, got: %v", err)
	}
}

// TestP2_ValidateForProduction_PassWithProperConfig 生产配置校验通过
func TestP2_ValidateForProduction_PassWithProperConfig(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type: "postgres",
			DSN:  "postgres://user:pass@localhost/db",
			TimeSeries: TimeSeriesConfig{
				Driver:   "tdengine",
				Host:     "127.0.0.1",
				Port:     6030,
				User:     "root",
				Password: "secure_password_16chars",
			},
		},
		API: APIConfig{
			JWTSecret:   strings.Repeat("a", 32),
			JWTExpireHours: 24,
			JWT: &JWTConfig{
				Secrets:   map[string]string{"kid-2026-06": strings.Repeat("b", 32)},
				ActiveKid: "kid-2026-06",
			},
		},
		Auth: AuthConfig{
			OfflineUnbindSecret: strings.Repeat("c", 32),
		},
	}

	err := cfg.ValidateForProduction()
	if err != nil {
		t.Errorf("expected no error with proper production config, got: %v", err)
	}
}

// TestP2_Load_CircuitBreakerDefaults 809 熔断器默认值
func TestP2_Load_CircuitBreakerDefaults(t *testing.T) {
	os.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	defer os.Unsetenv("JTE_ALLOW_INSECURE_JWT")

	// 创建临时配置文件
	tmpDir := t.TempDir()
	configContent := `
server:
  host: "0.0.0.0"
  port: 7611
gateway:
  tcp_port: 7611
  udp_port: 7612
api:
  enabled: false
storage:
  type: "sqlite"
  dsn: "./data/test.db"
jt809:
  platforms: []
  server_port: 0
`
	configPath := tmpDir + "/jte.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	// 验证熔断器默认值
	if cfg.JT809.CircuitBreaker.FailThreshold != 10 {
		t.Errorf("expected circuit_breaker.fail_threshold=10, got %d", cfg.JT809.CircuitBreaker.FailThreshold)
	}
	if cfg.JT809.CircuitBreaker.ResetTimeout != 300 {
		t.Errorf("expected circuit_breaker.reset_timeout=300, got %d", cfg.JT809.CircuitBreaker.ResetTimeout)
	}
}

// TestP2_Load_GatewaySecurityDefaults 网关安全配置默认值
func TestP2_Load_GatewaySecurityDefaults(t *testing.T) {
	os.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	defer os.Unsetenv("JTE_ALLOW_INSECURE_JWT")

	tmpDir := t.TempDir()
	configContent := `
server:
  host: "0.0.0.0"
  port: 7611
gateway:
  tcp_port: 7611
  udp_port: 7612
api:
  enabled: false
storage:
  type: "sqlite"
  dsn: "./data/test.db"
`
	configPath := tmpDir + "/jte.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	if cfg.Gateway.InitialAuthTimeout != 30 {
		t.Errorf("expected initial_auth_timeout=30, got %d", cfg.Gateway.InitialAuthTimeout)
	}
	if cfg.Gateway.MaxConnsPerIP != 100 {
		t.Errorf("expected max_conns_per_ip=100, got %d", cfg.Gateway.MaxConnsPerIP)
	}
	if cfg.Gateway.MaxConnRatePerIP != 50 {
		t.Errorf("expected max_conn_rate_per_ip=50, got %d", cfg.Gateway.MaxConnRatePerIP)
	}
}

// TestP2_Load_JTE_ENV_Prod_LoadsProdConfig JTE_ENV=prod 加载 jte-prod.yaml
func TestP2_Load_JTE_ENV_Prod_LoadsProdConfig(t *testing.T) {
	os.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	defer os.Unsetenv("JTE_ALLOW_INSECURE_JWT")

	tmpDir := t.TempDir()
	// 主配置
	mainConfig := `
server:
  host: "0.0.0.0"
  port: 7611
gateway:
  tcp_port: 7611
api:
  enabled: false
storage:
  type: "sqlite"
  dsn: "./data/test.db"
`
	if err := os.WriteFile(tmpDir+"/jte.yaml", []byte(mainConfig), 0644); err != nil {
		t.Fatalf("write main config: %v", err)
	}

	// 生产环境覆盖配置
	prodConfig := `
storage:
  type: "postgres"
  dsn: "postgres://user:pass@localhost/db"
`
	if err := os.WriteFile(tmpDir+"/jte-prod.yaml", []byte(prodConfig), 0644); err != nil {
		t.Fatalf("write prod config: %v", err)
	}

	os.Setenv("JTE_ENV", "prod")
	os.Setenv("JTE_ALLOW_INSECURE_JWT", "1")
	defer os.Unsetenv("JTE_ENV")
	defer os.Unsetenv("JTE_ALLOW_INSECURE_JWT")

	// 使用空 configPath + chdir 方式加载，使 MergeInConfig 能搜索到 jte-prod.yaml
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	if cfg.Storage.Type != "postgres" {
		t.Errorf("expected storage.type=postgres (from jte-prod.yaml), got %s", cfg.Storage.Type)
	}
}
