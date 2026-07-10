package trace

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: 可观测性 - trace_id 链路追踪
//
// 轻量级 trace_id 传播机制（无外部依赖）。
// 为每个请求/会话生成唯一 trace_id，通过 context 传播，
// zap 日志自动包含 trace_id 字段，实现端到端链路追踪。
//
// 与 OpenTelemetry 兼容设计：
//   - trace_id 格式：32 字符十六进制（与 OTel TraceID 一致）
//   - span_id 格式：16 字符十六进制（与 OTel SpanID 一致）
//   - 未来可平滑迁移到 go.opentelemetry.io/otel
//
// 使用方式：
//   ctx = trace.WithTraceID(ctx, trace.NewTraceID())
//   logger = trace.LoggerWithTrace(ctx, baseLogger)
//   logger.Info("message") // 自动包含 trace_id 字段
// ===================================================================

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// traceIDKey context key for trace_id
type traceIDKey struct{}

// spanIDKey context key for span_id
type spanIDKey struct{}

// AUTO-FIX-2026-07-02 [可观测性/日志规范]: device_id/org_id context key
// 日志规范要求所有日志包含 trace_id、device_id、org_id 字段
type deviceIDKey struct{}
type orgIDKey struct{}

// TraceIDLength trace_id 长度（32 字符十六进制 = 16 字节，与 OTel 一致）
const TraceIDLength = 32

// SpanIDLength span_id 长度（16 字符十六进制 = 8 字节，与 OTel 一致）
const SpanIDLength = 16

// NewTraceID 生成新的 trace_id（32 字符十六进制）
func NewTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand 失败时使用时间戳兜底（不应发生）
		return fmt.Sprintf("%032x", 0)
	}
	return hex.EncodeToString(b)
}

// NewSpanID 生成新的 span_id（16 字符十六进制）
func NewSpanID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", 0)
	}
	return hex.EncodeToString(b)
}

// WithTraceID 将 trace_id 存入 context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// GetTraceID 从 context 获取 trace_id
func GetTraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithSpanID 将 span_id 存入 context
func WithSpanID(ctx context.Context, spanID string) context.Context {
	if spanID == "" {
		return ctx
	}
	return context.WithValue(ctx, spanIDKey{}, spanID)
}

// GetSpanID 从 context 获取 span_id
func GetSpanID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(spanIDKey{}).(string); ok {
		return v
	}
	return ""
}

// NewContext 创建带新 trace_id 的 context
func NewContext(parent context.Context) (context.Context, string) {
	traceID := NewTraceID()
	return WithTraceID(parent, traceID), traceID
}

// ContinueContext 使用已有 trace_id 继续传播（用于跨服务调用）
func ContinueContext(parent context.Context, traceID string) context.Context {
	return WithTraceID(parent, traceID)
}

// ========== AUTO-FIX-2026-07-02 [日志规范]: device_id / org_id 传播 ==========

// WithDeviceID 将 device_id（终端手机号/设备ID）存入 context
// 日志规范要求所有日志包含 device_id 字段，便于按设备筛选链路
func WithDeviceID(ctx context.Context, deviceID string) context.Context {
	if deviceID == "" {
		return ctx
	}
	return context.WithValue(ctx, deviceIDKey{}, deviceID)
}

// GetDeviceID 从 context 获取 device_id
func GetDeviceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(deviceIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithOrgID 将 org_id（组织ID）存入 context
// 多租户场景下日志需包含 org_id，便于按组织筛选和审计
func WithOrgID(ctx context.Context, orgID string) context.Context {
	if orgID == "" {
		return ctx
	}
	return context.WithValue(ctx, orgIDKey{}, orgID)
}

// GetOrgID 从 context 获取 org_id
func GetOrgID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(orgIDKey{}).(string); ok {
		return v
	}
	return ""
}

// LoggerWithTrace 从 context 提取 trace_id/device_id/org_id 并注入 zap logger
// AUTO-FIX-2026-07-02 [日志规范]: 所有日志包含 trace_id、device_id、org_id 字段
func LoggerWithTrace(ctx context.Context, logger *zap.Logger) *zap.Logger {
	if ctx == nil || logger == nil {
		return logger
	}
	traceID := GetTraceID(ctx)
	if traceID == "" {
		return logger
	}
	spanID := GetSpanID(ctx)
	fields := []zap.Field{zap.String("trace_id", traceID)}
	if spanID != "" {
		fields = append(fields, zap.String("span_id", spanID))
	}
	// AUTO-FIX-2026-07-02 [日志规范]: 注入 device_id 和 org_id
	if deviceID := GetDeviceID(ctx); deviceID != "" {
		fields = append(fields, zap.String("device_id", deviceID))
	}
	if orgID := GetOrgID(ctx); orgID != "" {
		fields = append(fields, zap.String("org_id", orgID))
	}
	return logger.With(fields...)
}

// ========== Trace Span（轻量级 span 管理） ==========

// Span 表示一个追踪跨度。
// 默认轻量级（不依赖 OpenTelemetry）；用 -tags otel 编译后 otelSpan 非 nil 时桥接真实 OTel span。
type Span struct {
	TraceID   string
	SpanID    string
	Name      string
	ctx       context.Context
	logger    *zap.Logger
	otelSpan  interface{} // AUTO-FIX-2026-06-30 [集成-7]: OTel span（仅 -tags otel 构建时非 nil）
}

var spanPool = sync.Pool{
	New: func() interface{} {
		return &Span{}
	},
}

// StartSpan 启动一个新的 span。
// 若 parent context 中已有 trace_id，则复用（同一链路）；
// 否则生成新的 trace_id。
//
// AUTO-FIX-2026-06-30 [集成-7]: 当 OTel SDK 实际接入并初始化后（-tags otel + InitOTel），
// 创建真实 OTel span；否则使用轻量级自研 span。
func StartSpan(parent context.Context, name string, logger *zap.Logger) (*Span, context.Context) {
	if otelEnabled && otelReady.Load() {
		return startOTelSpan(parent, name, logger)
	}
	return startLightweightSpan(parent, name, logger)
}

// startLightweightSpan 创建轻量级自研 span（无 OTel 依赖）。
// 提取为独立函数便于 otel.go 在 OTel 未初始化时降级调用。
func startLightweightSpan(parent context.Context, name string, logger *zap.Logger) (*Span, context.Context) {
	traceID := GetTraceID(parent)
	if traceID == "" {
		traceID = NewTraceID()
	}
	spanID := NewSpanID()

	ctx := WithTraceID(WithSpanID(parent, spanID), traceID)

	span := spanPool.Get().(*Span)
	span.TraceID = traceID
	span.SpanID = spanID
	span.Name = name
	span.ctx = ctx
	if logger != nil {
		span.logger = logger.With(
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.String("span_name", name),
		)
	}
	return span, ctx
}

// Context 返回 span 的 context
func (s *Span) Context() context.Context {
	return s.ctx
}

// Logger 返回带 trace_id 的 logger
func (s *Span) Logger() *zap.Logger {
	return s.logger
}

// End 结束 span（记录日志，归还到 pool）
// AUTO-FIX-2026-06-30 [集成-7]: 若 otelSpan 非 nil（-tags otel 构建），调用 OTel span.End() 上报。
func (s *Span) End() {
	if s.otelSpan != nil {
		endOTelSpan(s)
	}
	if s.logger != nil {
		s.logger.Debug("span ended", zap.String("span_name", s.Name))
	}
	s.TraceID = ""
	s.SpanID = ""
	s.Name = ""
	s.ctx = nil
	s.logger = nil
	s.otelSpan = nil
	spanPool.Put(s)
}

// ========== Context 辅助 ==========

// ContextWithModule 创建带 trace_id 和 module 字段的 context
func ContextWithModule(parent context.Context, moduleName string) (context.Context, string) {
	ctx, traceID := NewContext(parent)
	// module 字段通过 logger 注入，不存入 context
	return ctx, traceID
}

// FieldsFromContext 从 context 提取标准日志字段
// AUTO-FIX-2026-07-02 [日志规范]: 包含 trace_id/span_id/device_id/org_id
func FieldsFromContext(ctx context.Context) []zap.Field {
	if ctx == nil {
		return nil
	}
	var fields []zap.Field
	if traceID := GetTraceID(ctx); traceID != "" {
		fields = append(fields, zap.String("trace_id", traceID))
	}
	if spanID := GetSpanID(ctx); spanID != "" {
		fields = append(fields, zap.String("span_id", spanID))
	}
	if deviceID := GetDeviceID(ctx); deviceID != "" {
		fields = append(fields, zap.String("device_id", deviceID))
	}
	if orgID := GetOrgID(ctx); orgID != "" {
		fields = append(fields, zap.String("org_id", orgID))
	}
	return fields
}
