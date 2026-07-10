//go:build !otel

package trace

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: OpenTelemetry 链路追踪（默认 stub）
//
// 默认构建（无 -tags otel）使用此 stub，不引入 OTel SDK 依赖，
// 保持 Go 1.22+ 兼容性与零外部依赖。
//
// 启用真正 OTel 集成：
//   1. go get go.opentelemetry.io/otel@v1.28.0 \
//        go.opentelemetry.io/otel/sdk@v1.28.0 \
//        go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.28.0
//   2. go build -tags otel ./cmd/jte
//
// 默认 stub 提供 InitOTel 返回 ErrOTelNotEnabled，
// StartSpan 在 OTel 未初始化时仍使用轻量级自研 trace_id 机制（见 trace.go）。
// ===================================================================

import (
	"context"
	"errors"
	"sync/atomic"

	"go.uber.org/zap"
)

// ErrOTelNotEnabled OTel 未启用（需用 -tags otel 重新编译）
var ErrOTelNotEnabled = errors.New("OpenTelemetry not enabled: rebuild with -tags otel")

// otelEnabled 标记 OTel SDK 是否实际接入（stub=false）
const otelEnabled = false

// otelReady 标记 OTel TracerProvider 是否已初始化（stub 构建永远为 false）。
// 提供 atomic.Bool 类型以保持与 otel.go 编译兼容，StartSpan 的短路逻辑会跳过此变量。
var otelReady atomic.Bool

// InitOTel 初始化 OpenTelemetry SDK。
// 默认构建返回 ErrOTelNotEnabled；用 -tags otel 编译后实际接入。
//
// 参数：
//   - endpoint: OTLP collector 地址（如 "localhost:4318"）
//   - serviceName: 服务名（如 "jte-gateway"）
//   - sampleRate: 采样率 0-1（1.0 = 全采样）
func InitOTel(endpoint, serviceName string, sampleRate float64) error {
	return ErrOTelNotEnabled
}

// ShutdownOTel 优雅关闭 OTel SDK，flush 残留 span。
// 默认构建为 no-op。
func ShutdownOTel() error {
	return nil
}

// OTelEnabled 返回 OTel SDK 是否实际接入。
func OTelEnabled() bool {
	return otelEnabled
}

// startOTelSpan stub 占位（默认构建永远不会被调用，因 otelEnabled=false 短路）。
// 提供此签名仅为让 trace.go 在 !otel 构建下编译通过。
func startOTelSpan(parent context.Context, name string, logger *zap.Logger) (*Span, context.Context) {
	return startLightweightSpan(parent, name, logger)
}

// endOTelSpan stub 占位（默认构建永远不会被调用）。
func endOTelSpan(s *Span) {
	// no-op
}

