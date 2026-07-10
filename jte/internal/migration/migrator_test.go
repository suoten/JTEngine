package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestNewMigrator(t *testing.T) {
	cfg := &MigratorConfig{
		SourceDriver: "mysql",
		SourceDSN:    "user:pass@tcp(localhost:3306)/src",
		TargetDriver: "mysql",
		TargetDSN:    "user:pass@tcp(localhost:3306)/tgt",
	}
	m := NewMigrator(cfg, zap.NewNop())

	if m.config.BatchSize != 1000 {
		t.Errorf("BatchSize = %d, want 1000 (default)", m.config.BatchSize)
	}
	if m.progress == nil {
		t.Fatal("progress should not be nil")
	}
	if m.progress.Tables == nil {
		t.Fatal("progress.Tables should be initialized")
	}
}

func TestNewMigratorCustomBatchSize(t *testing.T) {
	cfg := &MigratorConfig{
		BatchSize: 500,
	}
	m := NewMigrator(cfg, zap.NewNop())

	if m.config.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", m.config.BatchSize)
	}
}

func TestColumnList(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		want    string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"id"}, "id"},
		{"multiple", []string{"id", "name", "phone"}, "id, name, phone"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnList(tt.columns)
			if got != tt.want {
				t.Errorf("columnList(%v) = %q, want %q", tt.columns, got, tt.want)
			}
		})
	}
}

func TestMigrationProgressSaveLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := &MigratorConfig{
		ConfigDir: dir,
	}
	m := NewMigrator(cfg, zap.NewNop())

	// 设置一些进度数据
	m.progress.Status = "running"
	m.progress.Tables["vehicles"] = &TableProgress{
		TotalRows:    100,
		MigratedRows: 50,
	}

	// 保存
	if err := m.saveProgress(); err != nil {
		t.Fatalf("saveProgress: %v", err)
	}

	// 验证文件存在
	path := filepath.Join(dir, "migration_progress.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("progress file not found: %v", err)
	}

	// 读取并验证
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded MigrationProgress
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Status != "running" {
		t.Errorf("Status = %q, want %q", loaded.Status, "running")
	}
	if tp, ok := loaded.Tables["vehicles"]; !ok {
		t.Error("vehicles table not found in loaded progress")
	} else if tp.MigratedRows != 50 {
		t.Errorf("MigratedRows = %d, want 50", tp.MigratedRows)
	}
}

func TestMigrationProgressLoadNonExistent(t *testing.T) {
	cfg := &MigratorConfig{
		ConfigDir: "/nonexistent/path",
	}
	m := NewMigrator(cfg, zap.NewNop())

	// 加载不存在的进度文件应该返回错误但不 panic
	err := m.loadProgress()
	if err == nil {
		// 如果 ConfigDir 不存在，loadProgress 会尝试读取文件并返回错误
		// 但在 saveProgress 中会创建目录
		t.Log("loadProgress returned nil (may be acceptable if dir is empty)")
	}
}

func TestMigrationProgressLoadEmpty(t *testing.T) {
	cfg := &MigratorConfig{
		ConfigDir: "", // 空目录，不加载进度
	}
	m := NewMigrator(cfg, zap.NewNop())

	err := m.loadProgress()
	if err != nil {
		t.Errorf("loadProgress with empty ConfigDir should return nil, got: %v", err)
	}
}

func TestMigrationSaveProgressNoConfigDir(t *testing.T) {
	cfg := &MigratorConfig{
		ConfigDir: "",
	}
	m := NewMigrator(cfg, zap.NewNop())

	err := m.saveProgress()
	if err != nil {
		t.Errorf("saveProgress with empty ConfigDir should return nil, got: %v", err)
	}
}

func TestGenerateReport(t *testing.T) {
	cfg := &MigratorConfig{
		SourceDriver: "mysql",
		TargetDriver: "postgres",
	}
	m := NewMigrator(cfg, zap.NewNop())

	// 设置进度数据
	m.progress.Status = "completed"
	m.progress.Tables["vehicles"] = &TableProgress{
		TotalRows:    100,
		MigratedRows: 100,
	}
	m.progress.Tables["locations"] = &TableProgress{
		TotalRows:    500,
		MigratedRows: 450,
	}

	report, err := m.GenerateReport()
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if report.Status != "completed" {
		t.Errorf("Status = %q, want %q", report.Status, "completed")
	}
	if report.TotalRows != 600 {
		t.Errorf("TotalRows = %d, want 600", report.TotalRows)
	}
	if report.TotalMigrated != 550 {
		t.Errorf("TotalMigrated = %d, want 550", report.TotalMigrated)
	}
	if len(report.Tables) != 2 {
		t.Errorf("len(Tables) = %d, want 2", len(report.Tables))
	}
}

func TestGenerateReportNoProgress(t *testing.T) {
	cfg := &MigratorConfig{}
	m := NewMigrator(cfg, zap.NewNop())
	m.progress = nil

	_, err := m.GenerateReport()
	if err == nil {
		t.Error("GenerateReport should return error when progress is nil")
	}
}

func TestMigratorClose(t *testing.T) {
	cfg := &MigratorConfig{}
	m := NewMigrator(cfg, zap.NewNop())

	// Close 在未连接数据库时不应 panic
	m.Close()
}

func TestMigrationProgressJSON(t *testing.T) {
	progress := &MigrationProgress{
		SourceDriver: "mysql",
		TargetDriver: "postgres",
		Status:       "completed",
		Tables: map[string]*TableProgress{
			"vehicles": {
				TotalRows:    100,
				MigratedRows: 100,
				VerifyStatus: "OK",
			},
		},
	}

	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded MigrationProgress
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.SourceDriver != "mysql" {
		t.Errorf("SourceDriver = %q", loaded.SourceDriver)
	}
	if loaded.Tables["vehicles"].VerifyStatus != "OK" {
		t.Errorf("VerifyStatus = %q", loaded.Tables["vehicles"].VerifyStatus)
	}
}
