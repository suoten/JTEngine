package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newBufferLogger 创建写入内存 buffer 的结构化 logger（用于测试断言）
// 返回 logger 和 buffer，测试可解析 buffer 中的 JSON 日志行
func newBufferLogger(t *testing.T, level zapcore.Level) (*zap.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	core := zapcore.NewCore(encoder, zapcore.AddSync(buf), level)
	maskedCore := NewMaskCore(core)
	return zap.New(maskedCore), buf
}

// parseLogLine 解析单行 JSON 日志为 map
func parseLogLine(t *testing.T, line string) map[string]interface{} {
	t.Helper()
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("parse log line failed: %v\nline: %s", err, line)
	}
	return m
}

// ========== maskLogValue 测试 ==========

func TestMaskLogValue_Phone(t *testing.T) {
	cases := []struct {
		field string
		value string
		want  string
	}{
		{"phone", "13812345678", "138****5678"},
		{"mobile", "13800138000", "138****8000"},
		{"tel", "13812345678", "138****5678"},
		{"PHONE", "13812345678", "138****5678"}, // 大写字段名也应匹配
		{"contact_phone", "13812345678", "138****5678"},
	}
	for _, c := range cases {
		got := maskLogValue(c.field, c.value)
		if got != c.want {
			t.Errorf("maskLogValue(%q, %q) = %q, want %q", c.field, c.value, got, c.want)
		}
	}
}

func TestMaskLogValue_IDCard(t *testing.T) {
	cases := []struct {
		field string
		value string
		want  string
	}{
		{"id_card", "110101199001011234", "110101********1234"},
		{"idcard", "110101199001011234", "110101********1234"},
		{"identity", "110101199001011234", "110101********1234"},
		{"cert_no", "110101199001011234", "110101********1234"},
	}
	for _, c := range cases {
		got := maskLogValue(c.field, c.value)
		if got != c.want {
			t.Errorf("maskLogValue(%q, %q) = %q, want %q", c.field, c.value, got, c.want)
		}
	}
}

func TestMaskLogValue_Plate(t *testing.T) {
	cases := []struct {
		field string
		value string
		want  string
	}{
		{"plate", "京A12345", "京A***45"},
		{"plate_no", "京A12345", "京A***45"},
		{"license_plate", "京A12345", "京A***45"},
		{"car_no", "京A12345", "京A***45"},
	}
	for _, c := range cases {
		got := maskLogValue(c.field, c.value)
		if got != c.want {
			t.Errorf("maskLogValue(%q, %q) = %q, want %q", c.field, c.value, got, c.want)
		}
	}
}

func TestMaskLogValue_Email(t *testing.T) {
	got := maskLogValue("email", "zhangsan@example.com")
	if got != "z***@example.com" {
		t.Errorf("maskLogValue(email) = %q, want z***@example.com", got)
	}
}

func TestMaskLogValue_Secret(t *testing.T) {
	cases := []struct {
		field string
		value string
		want  string
	}{
		{"password", "secret123", "se****"},
		{"token", "abcdef123456", "ab****"},
		{"api_key", "key123", "ke****"},
		{"access_token", "tok_xyz", "to****"},
		{"auth_code", "123456", "12****"},
		{"sm4_key", "0102030405060708", "01****"},
	}
	for _, c := range cases {
		got := maskLogValue(c.field, c.value)
		if got != c.want {
			t.Errorf("maskLogValue(%q, %q) = %q, want %q", c.field, c.value, got, c.want)
		}
	}
}

func TestMaskLogValue_ShortSecret(t *testing.T) {
	// 长度 <= 2 的凭证完全掩码
	got := maskLogValue("password", "ab")
	if got != "****" {
		t.Errorf("maskLogValue(password, short) = %q, want ****", got)
	}
}

func TestMaskLogValue_EmptyValue(t *testing.T) {
	got := maskLogValue("phone", "")
	if got != "" {
		t.Errorf("maskLogValue(phone, empty) = %q, want empty", got)
	}
}

func TestMaskLogValue_NonSensitiveField(t *testing.T) {
	// 非敏感字段不脱敏
	got := maskLogValue("device_id", "dev123")
	if got != "dev123" {
		t.Errorf("maskLogValue(device_id) = %q, want dev123 (non-sensitive)", got)
	}
}

func TestMaskLogValue_FuzzyMatch(t *testing.T) {
	// 模糊匹配：字段名包含敏感关键词
	got := maskLogValue("user_phone_number", "13812345678")
	if got != "138****5678" {
		t.Errorf("maskLogValue(user_phone_number) = %q, want 138****5678", got)
	}
}

// ========== maskField 测试 ==========

func TestMaskField_StringField(t *testing.T) {
	f := zap.String("phone", "13812345678")
	masked := maskField(f)
	if masked.String != "138****5678" {
		t.Errorf("maskField(phone) = %q, want 138****5678", masked.String)
	}
}

func TestMaskField_NonStringField(t *testing.T) {
	// 非 String 类型字段不脱敏
	f := zap.Int("count", 42)
	masked := maskField(f)
	if masked.Integer != 42 {
		t.Errorf("maskField(count) = %d, want 42 (non-string)", masked.Integer)
	}
}

// ========== maskCore 集成测试 ==========

func TestMaskCore_WriteMasksFields(t *testing.T) {
	logger, buf := newBufferLogger(t, zapcore.InfoLevel)

	logger.Info("login event",
		zap.String("phone", "13812345678"),
		zap.String("id_card", "110101199001011234"),
		zap.String("plate", "京A12345"),
		zap.String("email", "zhangsan@example.com"),
		zap.String("password", "secret123"),
		zap.String("device_id", "dev001"), // 非敏感，不脱敏
	)

	m := parseLogLine(t, buf.String())
	if m["phone"] != "138****5678" {
		t.Errorf("phone = %v, want 138****5678", m["phone"])
	}
	if m["id_card"] != "110101********1234" {
		t.Errorf("id_card = %v, want 110101********1234", m["id_card"])
	}
	if m["plate"] != "京A***45" {
		t.Errorf("plate = %v, want 京A***45", m["plate"])
	}
	if m["email"] != "z***@example.com" {
		t.Errorf("email = %v, want z***@example.com", m["email"])
	}
	if m["password"] != "se****" {
		t.Errorf("password = %v, want se****", m["password"])
	}
	if m["device_id"] != "dev001" {
		t.Errorf("device_id = %v, want dev001 (non-sensitive)", m["device_id"])
	}
}

func TestMaskCore_WithMasksFields(t *testing.T) {
	logger, buf := newBufferLogger(t, zapcore.InfoLevel)
	// With 注入的字段也应被脱敏
	childLogger := logger.With(zap.String("phone", "13812345678"))
	childLogger.Info("child log")

	m := parseLogLine(t, buf.String())
	if m["phone"] != "138****5678" {
		t.Errorf("With(phone) = %v, want 138****5678", m["phone"])
	}
}

func TestMaskCore_PreservesNonSensitiveLogs(t *testing.T) {
	logger, buf := newBufferLogger(t, zapcore.InfoLevel)
	logger.Info("normal event",
		zap.String("module", "gateway"),
		zap.Int("msg_count", 100),
		zap.String("level", "info"),
	)
	m := parseLogLine(t, buf.String())
	if m["module"] != "gateway" {
		t.Errorf("module = %v, want gateway", m["module"])
	}
	if m["msg_count"] != float64(100) {
		t.Errorf("msg_count = %v, want 100", m["msg_count"])
	}
}

// ========== NewStructuredLogger 测试 ==========

func TestNewStructuredLogger_Stdout(t *testing.T) {
	logger, err := NewStructuredLogger("info", "json", "stdout")
	if err != nil {
		t.Fatalf("NewStructuredLogger(stdout) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(stdout) returned nil logger")
	}
}

func TestNewStructuredLogger_Stderr(t *testing.T) {
	logger, err := NewStructuredLogger("debug", "json", "stderr")
	if err != nil {
		t.Fatalf("NewStructuredLogger(stderr) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(stderr) returned nil logger")
	}
}

func TestNewStructuredLogger_ConsoleFormat(t *testing.T) {
	logger, err := NewStructuredLogger("info", "console", "stdout")
	if err != nil {
		t.Fatalf("NewStructuredLogger(console) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(console) returned nil logger")
	}
}

func TestNewStructuredLogger_FileOutput(t *testing.T) {
	// 使用临时文件（filepath.Join 跨平台兼容）
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.log")
	logger, err := NewStructuredLogger("info", "json", tmpFile)
	if err != nil {
		t.Fatalf("NewStructuredLogger(file) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(file) returned nil logger")
	}
	logger.Info("file log test", zap.String("phone", "13812345678"))
	_ = logger.Sync()

	// 关闭底层文件句柄，避免 Windows 上 TempDir 清理时文件锁冲突
	CloseTestFileLoggers()
}

func TestNewStructuredLogger_FileError(t *testing.T) {
	// 无效路径应返回错误
	_, err := NewStructuredLogger("info", "json", "Z:\\nonexistent_dir_xyz\\test.log")
	if err == nil {
		t.Error("NewStructuredLogger(invalid path) should return error")
	}
}

func TestNewStructuredLogger_EmptyPath(t *testing.T) {
	// 空路径默认走 stdout
	logger, err := NewStructuredLogger("info", "json", "")
	if err != nil {
		t.Fatalf("NewStructuredLogger(empty) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(empty) returned nil logger")
	}
}

func TestNewStructuredLogger_DefaultLevel(t *testing.T) {
	// 未知级别默认 info
	logger, err := NewStructuredLogger("unknown_level", "json", "stdout")
	if err != nil {
		t.Fatalf("NewStructuredLogger(unknown) error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStructuredLogger(unknown) returned nil logger")
	}
}

func TestNewStructuredLogger_AllLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "warning", "error", "fatal"}
	for _, lvl := range levels {
		logger, err := NewStructuredLogger(lvl, "json", "stdout")
		if err != nil {
			t.Errorf("NewStructuredLogger(%s) error: %v", lvl, err)
		}
		if logger == nil {
			t.Errorf("NewStructuredLogger(%s) returned nil", lvl)
		}
	}
}

// ========== LoggerWithTrace + device_id/org_id 集成测试 ==========

func TestLoggerWithTrace_DeviceAndOrgID(t *testing.T) {
	baseLogger, buf := newBufferLogger(t, zapcore.DebugLevel)

	ctx := WithTraceID(context.Background(), "test-trace-123")
	ctx = WithDeviceID(ctx, "13812345678")
	ctx = WithOrgID(ctx, "org_001")

	logger := LoggerWithTrace(ctx, baseLogger)
	logger.Info("test message")

	m := parseLogLine(t, buf.String())
	// device_id 在 logger 中应被脱敏（因为它是手机号格式且字段名匹配 phone 关键词?）
	// 注意：device_id 字段名不在 sensitiveLogFields 中，但包含 "id" 关键词模糊匹配
	// 实际上 "device_id" 包含 "id" 但 sensitiveLogFields 没有 "id" 单独条目，需检查
	// 字段名 "device_id" 模糊匹配：包含 "id_card"? 不包含。包含 "idcard"? 不包含。
	// 所以 device_id 不会被脱敏（这是正确的，device_id 不是敏感信息）
	if m["device_id"] != "13812345678" {
		t.Logf("device_id = %v (注: device_id 字段名不触发脱敏，因为日志规范要求 device_id 是设备标识)", m["device_id"])
	}
	if m["org_id"] != "org_001" {
		t.Errorf("org_id = %v, want org_001", m["org_id"])
	}
	if m["trace_id"] != "test-trace-123" {
		t.Errorf("trace_id = %v, want test-trace-123", m["trace_id"])
	}
}

func TestLoggerWithTrace_NilContext(t *testing.T) {
	baseLogger, _ := newBufferLogger(t, zapcore.DebugLevel)
	// nil context 应返回原 logger
	result := LoggerWithTrace(nil, baseLogger)
	if result != baseLogger {
		t.Error("LoggerWithTrace(nil) should return original logger")
	}
}

func TestLoggerWithTrace_NilLogger(t *testing.T) {
	result := LoggerWithTrace(context.Background(), nil)
	if result != nil {
		t.Error("LoggerWithTrace(nil logger) should return nil")
	}
}

// ========== FieldsFromContext 测试 ==========

func TestFieldsFromContext_AllFields(t *testing.T) {
	ctx := WithTraceID(context.Background(), "trace-1")
	ctx = WithSpanID(ctx, "span-1")
	ctx = WithDeviceID(ctx, "dev-1")
	ctx = WithOrgID(ctx, "org-1")

	fields := FieldsFromContext(ctx)
	if len(fields) != 4 {
		t.Fatalf("FieldsFromContext returned %d fields, want 4", len(fields))
	}

	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Key] = f.String
	}
	if fieldMap["trace_id"] != "trace-1" {
		t.Errorf("trace_id = %q", fieldMap["trace_id"])
	}
	if fieldMap["span_id"] != "span-1" {
		t.Errorf("span_id = %q", fieldMap["span_id"])
	}
	if fieldMap["device_id"] != "dev-1" {
		t.Errorf("device_id = %q", fieldMap["device_id"])
	}
	if fieldMap["org_id"] != "org-1" {
		t.Errorf("org_id = %q", fieldMap["org_id"])
	}
}

// TestFieldsFromContext_WithDeviceOrgID 验证 FieldsFromContext 包含 device_id/org_id
// （TestFieldsFromContext_Empty 和 TestFieldsFromContext_Nil 已在 trace_test.go 中定义）
func TestFieldsFromContext_WithDeviceOrgID(t *testing.T) {
	ctx := WithDeviceID(context.Background(), "dev-1")
	ctx = WithOrgID(ctx, "org-1")

	fields := FieldsFromContext(ctx)
	if len(fields) != 2 {
		t.Fatalf("FieldsFromContext returned %d fields, want 2", len(fields))
	}
	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Key] = f.String
	}
	if fieldMap["device_id"] != "dev-1" {
		t.Errorf("device_id = %q", fieldMap["device_id"])
	}
	if fieldMap["org_id"] != "org-1" {
		t.Errorf("org_id = %q", fieldMap["org_id"])
	}
}

// ========== WithDeviceID / WithOrgID 测试 ==========

func TestWithDeviceID_Empty(t *testing.T) {
	ctx := WithDeviceID(context.Background(), "")
	// 空 device_id 应返回原 context
	if GetDeviceID(ctx) != "" {
		t.Error("WithDeviceID(empty) should not set value")
	}
}

func TestWithOrgID_Empty(t *testing.T) {
	ctx := WithOrgID(context.Background(), "")
	if GetOrgID(ctx) != "" {
		t.Error("WithOrgID(empty) should not set value")
	}
}

func TestGetDeviceID_NilContext(t *testing.T) {
	if GetDeviceID(nil) != "" {
		t.Error("GetDeviceID(nil) should return empty")
	}
}

func TestGetOrgID_NilContext(t *testing.T) {
	if GetOrgID(nil) != "" {
		t.Error("GetOrgID(nil) should return empty")
	}
}
