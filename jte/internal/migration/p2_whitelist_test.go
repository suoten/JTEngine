package migration

import (
	"testing"

	"go.uber.org/zap"
)

// [P2-3] TestAllowedTables_Whitelist 验证白名单包含所有预期的表。
func TestAllowedTables_Whitelist(t *testing.T) {
	expected := []string{"vehicles", "locations", "alarms", "sessions", "protocol_logs"}
	for _, table := range expected {
		if !allowedTables[table] {
			t.Errorf("expected table %q to be in allowedTables whitelist", table)
		}
	}
}

// [P2-3] TestAllowedTables_RejectUnknown 验证白名单拒绝未知表名。
func TestAllowedTables_RejectUnknown(t *testing.T) {
	unknownTables := []string{
		"",
		"users",
		"vehicles; DROP TABLE vehicles; --",
		"vehicles UNION SELECT * FROM users",
		"' OR '1'='1",
		"information_schema.tables",
		"pg_catalog.pg_tables",
	}
	for _, table := range unknownTables {
		if allowedTables[table] {
			t.Errorf("expected table %q to be rejected by allowedTables whitelist", table)
		}
	}
}

// [P2-3] TestMigrateTable_UnknownTableError 验证 migrateTable 对未知表名返回错误。
func TestMigrateTable_UnknownTableError(t *testing.T) {
	logger := zap.NewNop()
	m := NewMigrator(&MigratorConfig{
		SourceDriver: "sqlite3",
		SourceDSN:    ":memory:",
		TargetDriver: "sqlite3",
		TargetDSN:    ":memory:",
	}, logger)

	// 不调用 Connect()，直接测试 migrateTable 的白名单校验
	// migrateTable 在校验白名单时会在访问 m.progress.Tables 之前返回错误
	err := m.migrateTable("malicious_table")
	if err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
	if !contains(err.Error(), "unknown table: malicious_table") {
		t.Errorf("expected 'unknown table' error, got: %v", err)
	}
}

// [P2-3] TestCountRows_UnknownTableError 验证 countRows 对未知表名返回错误。
func TestCountRows_UnknownTableError(t *testing.T) {
	logger := zap.NewNop()
	m := NewMigrator(&MigratorConfig{
		SourceDriver: "sqlite3",
		SourceDSN:    ":memory:",
		TargetDriver: "sqlite3",
		TargetDSN:    ":memory:",
	}, logger)

	// countRows 会先校验白名单，不需要连接数据库
	_, err := m.countRows("evil_table")
	if err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
	if !contains(err.Error(), "unknown table: evil_table") {
		t.Errorf("expected 'unknown table' error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && containsStr(s, substr)))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
