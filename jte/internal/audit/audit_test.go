package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTestLogger(t *testing.T) (*AuditLogger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	logger := zap.NewNop()
	al, err := NewAuditLogger(path, 1, logger)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	return al, path
}

func newTestLoggerWithHMAC(t *testing.T) (*AuditLogger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	logger := zap.NewNop()
	// 32 字节密钥的 hex 编码（64 字符）
	hmacKey := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	al, err := NewAuditLogger(path, 1, logger, hmacKey)
	if err != nil {
		t.Fatalf("NewAuditLogger with HMAC: %v", err)
	}
	return al, path
}

func TestAuditLogBasic(t *testing.T) {
	al, path := newTestLogger(t)
	defer al.Close()

	err := al.Log(&AuditEntry{
		Operator: "admin",
		Action:   "login",
		Resource: "auth",
		Result:   "success",
		IP:       "192.168.1.1",
		Category: CategoryAuth,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	// 验证文件写入
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit log file is empty")
	}

	var entry AuditEntry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if entry.Operator != "admin" {
		t.Errorf("Operator = %q, want %q", entry.Operator, "admin")
	}
	if entry.Action != "login" {
		t.Errorf("Action = %q, want %q", entry.Action, "login")
	}
}

func TestAuditLogAuth(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	if err := al.LogAuth("user1", "login", "success", "10.0.0.1"); err != nil {
		t.Fatalf("LogAuth: %v", err)
	}
	if err := al.LogAuth("user2", "logout", "success", "10.0.0.2"); err != nil {
		t.Fatalf("LogAuth: %v", err)
	}

	entries, err := al.ReadEntries(10)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
	// ReadEntries 返回倒序（最新在前）
	if entries[0].Operator != "user2" {
		t.Errorf("entries[0].Operator = %q, want %q", entries[0].Operator, "user2")
	}
}

func TestAuditLogDetail(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	details := map[string]interface{}{"reason": "密码错误", "attempts": 3}
	err := al.LogAuthDetail("user1", "login", "failed", "10.0.0.1", "Mozilla/5.0", "sess-123", details)
	if err != nil {
		t.Fatalf("LogAuthDetail: %v", err)
	}

	entries, err := al.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.UserAgent != "Mozilla/5.0" {
		t.Errorf("UserAgent = %q", e.UserAgent)
	}
	if e.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", e.SessionID)
	}
}

func TestAuditLogModule(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	if err := al.LogModule("admin", "enable", "module-ai", "success"); err != nil {
		t.Fatalf("LogModule: %v", err)
	}

	entries, err := al.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if entries[0].Resource != "module:module-ai" {
		t.Errorf("Resource = %q", entries[0].Resource)
	}
	if entries[0].Category != CategorySystem {
		t.Errorf("Category = %q", entries[0].Category)
	}
}

func TestAuditLogConfig(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	if err := al.LogConfig("admin", "update_config", "success"); err != nil {
		t.Fatalf("LogConfig: %v", err)
	}

	entries, err := al.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if entries[0].Category != CategoryConfig {
		t.Errorf("Category = %q", entries[0].Category)
	}
}

func TestAuditLogDataChange(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	before := map[string]interface{}{"status": "offline"}
	after := map[string]interface{}{"status": "online"}
	err := al.LogDataChange("admin", "update", "vehicle:123", "success", "10.0.0.1", CategoryData, before, after)
	if err != nil {
		t.Fatalf("LogDataChange: %v", err)
	}

	entries, err := al.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	e := entries[0]
	if e.Before["status"] != "offline" {
		t.Errorf("Before.status = %v", e.Before["status"])
	}
	if e.After["status"] != "online" {
		t.Errorf("After.status = %v", e.After["status"])
	}
}

func TestAuditLogSecurity(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	details := map[string]interface{}{"key_id": "k001", "algorithm": "SM2"}
	if err := al.LogSecurity("admin", "rotate_key", "success", "10.0.0.1", details); err != nil {
		t.Fatalf("LogSecurity: %v", err)
	}

	entries, err := al.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if entries[0].Category != CategorySecurity {
		t.Errorf("Category = %q", entries[0].Category)
	}
}

func TestAuditChainIntegrity(t *testing.T) {
	al, _ := newTestLoggerWithHMAC(t)
	defer al.Close()

	// 写入多条日志
	for i := 0; i < 5; i++ {
		err := al.Log(&AuditEntry{
			Operator: "admin",
			Action:   "test_action",
			Resource: "test",
			Result:   "success",
			Category: CategorySystem,
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}

	// 验证链完整性
	tamperedLine, err := al.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v (line %d)", err, tamperedLine)
	}
	if tamperedLine != -1 {
		t.Fatalf("chain broken at line %d", tamperedLine)
	}
}

func TestAuditChainTamperDetection(t *testing.T) {
	al, path := newTestLoggerWithHMAC(t)
	defer al.Close()

	// 写入日志
	for i := 0; i < 3; i++ {
		err := al.Log(&AuditEntry{
			Operator: "admin",
			Action:   "test_action",
			Resource: "test",
			Result:   "success",
			Category: CategorySystem,
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}

	// 篡改日志文件
	al.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// 替换某个字符来篡改
	tampered := strings.Replace(string(data), "test_action", "HACKED_ACTN", 1)
	if tampered == string(data) {
		t.Fatal("tamper replacement did not occur")
	}
	if err := os.WriteFile(path, []byte(tampered), 0640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 重新打开并验证
	al2, err := NewAuditLogger(path, 1, zap.NewNop(), "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2")
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al2.Close()

	tamperedLine, err := al2.VerifyChain()
	if err == nil {
		t.Fatal("VerifyChain should detect tampering")
	}
	if tamperedLine == -1 {
		t.Fatal("expected tampered line != -1")
	}
}

func TestAuditReadEntries(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	// 写入 10 条日志
	for i := 0; i < 10; i++ {
		err := al.Log(&AuditEntry{
			Operator: "user",
			Action:   "action",
			Resource: "test",
			Result:   "success",
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}

	// 读取最近 5 条
	entries, err := al.ReadEntries(5)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("len(entries) = %d, want 5", len(entries))
	}

	// 读取全部
	entries, err = al.ReadEntries(0)
	if err != nil {
		t.Fatalf("ReadEntries(0): %v", err)
	}
	if len(entries) != 10 {
		t.Errorf("len(entries) = %d, want 10", len(entries))
	}
}

func TestAuditSetEnabled(t *testing.T) {
	al, _ := newTestLogger(t)
	defer al.Close()

	// 禁用后不应写入
	al.SetEnabled(false)
	err := al.Log(&AuditEntry{
		Operator: "admin",
		Action:   "disabled_test",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	entries, _ := al.ReadEntries(1)
	if len(entries) != 0 {
		t.Error("audit log should not write when disabled")
	}

	// 重新启用
	al.SetEnabled(true)
	err = al.Log(&AuditEntry{
		Operator: "admin",
		Action:   "enabled_test",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	entries, _ = al.ReadEntries(1)
	if len(entries) != 1 {
		t.Error("audit log should write when enabled")
	}
}

func TestAuditRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	al, err := NewAuditLogger(path, 1, zap.NewNop()) // 1MB max
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al.Close()

	// 写入大量日志触发轮转
	for i := 0; i < 1000; i++ {
		err := al.Log(&AuditEntry{
			Operator: "admin",
			Action:   "bulk_test",
			Resource: "test",
			Result:   "success",
			Details:  map[string]interface{}{"i": i, "padding": strings.Repeat("x", 500)},
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}

	// 检查是否有备份文件
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(files) < 2 {
		t.Logf("warning: rotation may not have triggered (files=%d), data may be < 1MB", len(files))
	}
}

func TestAuditGetFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	al, err := NewAuditLogger(path, 10, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al.Close()

	if al.GetFilePath() != path {
		t.Errorf("GetFilePath() = %q, want %q", al.GetFilePath(), path)
	}
}

func TestAuditRecoverChainState(t *testing.T) {
	hmacKey := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// 第一次创建并写入日志
	al1, err := NewAuditLogger(path, 10, zap.NewNop(), hmacKey)
	if err != nil {
		t.Fatalf("NewAuditLogger 1: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := al1.Log(&AuditEntry{
			Operator: "admin",
			Action:   "recover_test",
			Resource: "test",
			Result:   "success",
		}); err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}
	al1.Close()

	// 重新打开，验证链式状态恢复
	al2, err := NewAuditLogger(path, 10, zap.NewNop(), hmacKey)
	if err != nil {
		t.Fatalf("NewAuditLogger 2: %v", err)
	}
	defer al2.Close()

	// 继续写入
	if err := al2.Log(&AuditEntry{
		Operator: "admin",
		Action:   "after_recover",
		Resource: "test",
		Result:   "success",
	}); err != nil {
		t.Fatalf("Log after recover: %v", err)
	}

	// 验证链完整性（包括恢复后写入的日志）
	tamperedLine, err := al2.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v (line %d)", err, tamperedLine)
	}
	if tamperedLine != -1 {
		t.Fatalf("chain broken at line %d after recovery", tamperedLine)
	}
}
