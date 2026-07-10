package trace

// ===================================================================
// AUTO-FIX-2026-07-02 [可观测性/日志规范]: 日志脱敏 + 结构化 JSON 日志
//
// 功能：
//   1. LogMasker — zap.Core 包装器，对日志字段值自动脱敏
//      敏感字段：phone/mobile/tel/id_card/idcard/identity/plate/license/email/password/secret/token
//   2. NewStructuredLogger — 创建结构化 JSON 日志器（等保2.0 要求）
//   3. 敏感信息脱敏规则与 pkg/masking 一致（手机号/身份证/车牌/邮箱）
//
// 用法：
//   logger := trace.NewStructuredLogger(level, format, outputPath)
//   logger.Info("login",
//     zap.String("phone", "13812345678"),  // 自动脱敏为 138****5678
//     zap.String("id_card", "110101199001011234"))  // 自动脱敏为 110101********1234
// ===================================================================

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jte-engine/jte/pkg/masking"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ========== 输出目标辅助函数 ==========

// osStdout 返回标准输出的 WriteSyncer（并发安全）
func osStdout() zapcore.WriteSyncer {
	return zapcore.Lock(zapcore.AddSync(os.Stdout))
}

// osStderr 返回标准错误的 WriteSyncer（并发安全）
func osStderr() zapcore.WriteSyncer {
	return zapcore.Lock(zapcore.AddSync(os.Stderr))
}

// newFileWriteSyncer 创建文件输出 WriteSyncer
// 文件以 append 模式打开，权限 0640（等保2.0 要求日志文件权限最小化）
// 注：生产环境建议对接 lumberjack 实现按大小/时间轮转，此处保持轻量无外部依赖
// fileWriteSyncer 包装文件 WriteSyncer，支持 Close（解决 Windows 文件锁问题）
type fileWriteSyncer struct {
	zapcore.WriteSyncer
	file *os.File
}

func (f *fileWriteSyncer) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

// openFiles 追踪所有打开的日志文件句柄（用于测试环境清理）
var (
	openFiles   []*os.File
	openFileMu  sync.Mutex
)

// CloseTestFileLoggers 关闭所有通过 NewStructuredLogger 打开的文件句柄（仅供测试调用）
func CloseTestFileLoggers() {
	openFileMu.Lock()
	defer openFileMu.Unlock()
	for _, f := range openFiles {
		_ = f.Close()
	}
	openFiles = nil
}

func newFileWriteSyncer(path string) (zapcore.WriteSyncer, error) {
	if path == "" {
		return nil, fmt.Errorf("log file path is empty")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", path, err)
	}
	openFileMu.Lock()
	openFiles = append(openFiles, f)
	openFileMu.Unlock()
	return &fileWriteSyncer{
		WriteSyncer: zapcore.Lock(zapcore.AddSync(f)),
		file:        f,
	}, nil
}

// sensitiveLogFields 需要脱敏的日志字段名（小写匹配）
var sensitiveLogFields = map[string]string{
	// 手机号类
	"phone": "phone", "mobile": "phone", "tel": "phone",
	"phone_no": "phone", "mobile_no": "phone", "contact_phone": "phone",
	// 身份证类
	"id_card": "id_card", "idcard": "id_card", "id_number": "id_card",
	"identity": "id_card", "identity_no": "id_card", "cert_no": "id_card",
	// 车牌类
	"plate": "plate", "plate_no": "plate", "license": "plate",
	"license_plate": "plate", "car_no": "plate", "vehicle_no": "plate",
	// 邮箱类
	"email": "email", "mail": "email", "user_email": "email",
	// 凭证类（完全掩码）
	"password": "secret", "passwd": "secret", "pwd": "secret",
	"secret": "secret", "token": "secret", "access_token": "secret",
	"refresh_token": "secret", "api_key": "secret", "apikey": "secret",
	"auth_code": "secret", "authcode": "secret", "secret_key": "secret",
	"private_key": "secret", "sm4_key": "secret",
}

// maskLogValue 对日志字段值进行脱敏
// field: 字段名（不区分大小写）
// value: 字段值
// 返回脱敏后的值
func maskLogValue(field, value string) string {
	if value == "" {
		return value
	}
	lower := strings.ToLower(field)
	maskType, ok := sensitiveLogFields[lower]
	if !ok {
		// 模糊匹配：字段名包含敏感关键词
		for keyword, mType := range sensitiveLogFields {
			if strings.Contains(lower, keyword) {
				maskType = mType
				ok = true
				break
			}
		}
		if !ok {
			return value
		}
	}

	switch maskType {
	case "phone":
		return masking.MaskPhone(value)
	case "id_card":
		return masking.MaskIDCard(value)
	case "plate":
		return masking.MaskPlate(value)
	case "email":
		return masking.MaskEmail(value)
	case "secret":
		// 凭证类完全掩码，仅保留前 2 字符
		if len(value) <= 2 {
			return "****"
		}
		return value[:2] + "****"
	default:
		return value
	}
}

// maskCore 包装 zapcore.Core，对敏感字段值自动脱敏
type maskCore struct {
	zapcore.Core
}

// NewMaskCore 创建带脱敏功能的 zapcore.Core 包装器
func NewMaskCore(core zapcore.Core) zapcore.Core {
	return &maskCore{Core: core}
}

// Check 实现 zapcore.Core 接口，注册 maskCore 自身（而非底层 Core）到 CheckedEntry
// 这样 ce.Write(fields) 会调用 maskCore.Write 进行字段脱敏
// AUTO-FIX: 原实现委托给 m.Core.Check，导致底层 ioCore 被注册，maskCore.Write 被绕过
func (m *maskCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if m.Core.Enabled(ent.Level) {
		return ce.AddCore(ent, m)
	}
	return ce
}

// With 实现 zapcore.Core 接口，包装子 Core
func (m *maskCore) With(fields []zap.Field) zapcore.Core {
	masked := make([]zap.Field, len(fields))
	for i, f := range fields {
		masked[i] = maskField(f)
	}
	return &maskCore{Core: m.Core.With(masked)}
}

// maskField 对单个 zap.Field 进行脱敏
// 仅对 String 类型字段脱敏，其他类型（Int/Bool/Duration 等）原样返回
func maskField(f zap.Field) zap.Field {
	if f.Type != zapcore.StringType {
		return f
	}
	masked := maskLogValue(f.Key, f.String)
	if masked == f.String {
		return f
	}
	return zap.String(f.Key, masked)
}

// maskWriteSyncer 包装 zapcore.WriteSyncer，在写入前对 Encoder 的字段脱敏
// 由于 zap 的 Encoder 在 Write 前已完成编码，我们改为在 Field 层面脱敏
// （即 With/Write 时对 fields 脱敏），因此无需包装 WriteSyncer

// maskEntry 对 zapcore.Entry 的字段脱敏后写入
// 用于 Write 方法
func (m *maskCore) Write(ent zapcore.Entry, fields []zap.Field) error {
	masked := make([]zap.Field, len(fields))
	for i, f := range fields {
		masked[i] = maskField(f)
	}
	return m.Core.Write(ent, masked)
}

// NewStructuredLogger 创建结构化日志器（等保2.0 合规）
//
// 参数：
//   - level: 日志级别（"debug"/"info"/"warn"/"error"）
//   - format: 输出格式（"json" 结构化 JSON / "console" 人类可读）
//   - outputPath: 输出路径（"stdout"/"stderr"/文件路径）
//
// 特性：
//   - JSON 格式输出（等保2.0 要求结构化日志，便于 ELK 采集）
//   - 敏感字段自动脱敏（手机号/身份证/车牌/邮箱/密码/Token）
//   - 日志分级：DEBUG/INFO/WARN/ERROR/FATAL
//   - 通过 NewMaskCore 包装实现透明脱敏，业务代码无需修改
func NewStructuredLogger(level, format, outputPath string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn", "warning":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	case "fatal":
		zapLevel = zapcore.FatalLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	// Encoder 配置
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder   // ISO8601 时间格式（含时区）
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder  // INFO/WARN/ERROR（大写）
	encoderConfig.EncodeDuration = zapcore.MillisDurationEncoder // 毫秒

	var encoder zapcore.Encoder
	switch strings.ToLower(format) {
	case "json", "":
		encoder = zapcore.NewJSONEncoder(encoderConfig) // 结构化 JSON（默认，等保2.0 推荐）
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderConfig) // 人类可读（开发环境）
	default:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// 输出目标
	var writeSyncer zapcore.WriteSyncer
	switch strings.ToLower(outputPath) {
	case "", "stdout":
		writeSyncer = osStdout()
	case "stderr":
		writeSyncer = osStderr()
	default:
		ws, err := newFileWriteSyncer(outputPath)
		if err != nil {
			return nil, err
		}
		writeSyncer = ws
	}

	// 创建带脱敏的 Core
	core := zapcore.NewCore(encoder, writeSyncer, zapLevel)
	maskedCore := NewMaskCore(core)

	// 创建 logger
	logger := zap.New(maskedCore,
		zap.AddCaller(),                    // 添加调用者信息（文件:行号）
		zap.AddStacktrace(zapcore.ErrorLevel), // Error 及以上记录堆栈
	)

	return logger, nil
}
