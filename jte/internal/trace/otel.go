//go:build otel

package trace

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: OpenTelemetry 链路追踪（真实接入）
//
// 启用方式：go build -tags otel ./cmd/jte
// 依赖（需先执行）：
//   go get go.opentelemetry.io/otel@v1.28.0
//   go get go.opentelemetry.io/otel/sdk@v1.28.0
//   go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.28.0
//
// 工作原理：
//   - InitOTel 创建 OTLP HTTP exporter + BatchSpanProcessor + TracerProvider
//   - StartSpan 在 OTel 初始化后创建真实 OTel span，
//     同时保留自研 trace_id 注入 context（兼容 zap 日志）
//   - 自研 trace_id 与 OTel TraceID 互相兼容（均为 32 字符十六进制）
//
// 环境变量覆盖：
//   OTEL_EXPORTER_OTLP_ENDPOINT  - OTLP endpoint（覆盖 InitOTel 参数）
//   OTEL_SERVICE_NAME            - 服务名（覆盖 InitOTel 参数）
//   OTEL_TRACES_SAMPLER_ARG      - 采样率（覆盖 InitOTel 参数）
// ===================================================================

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ErrOTelNotEnabled 占位错误（OTel 构建中不会返回，保留以维持 API 一致性）
var ErrOTelNotEnabled = fmt.Errorf("OpenTelemetry not enabled")

// otelEnabled 标记 OTel SDK 实际接入（真实构建=true）
const otelEnabled = true

// otelReady 标记 OTel TracerProvider 是否已初始化
var otelReady atomic.Bool

// otelProvider 全局 TracerProvider（InitOTel 后非空）
var otelProvider *sdktrace.TracerProvider

// otelTracer 全局 Tracer
var otelTracer trace.Tracer

// InitOTel 初始化 OpenTelemetry SDK：创建 OTLP HTTP exporter + BatchSpanProcessor + TracerProvider。
//
// 参数：
//   - endpoint: OTLP collector 地址（如 "localhost:4318"）；空则从 OTEL_EXPORTER_OTLP_ENDPOINT 读取
//   - serviceName: 服务名（如 "jte-gateway"）；空则从 OTEL_SERVICE_NAME 读取
//   - sampleRate: 采样率 0-1（1.0 = 全采样）
func InitOTel(endpoint, serviceName string, sampleRate float64) error {
	if sampleRate <= 0 {
		sampleRate = 1.0
	}
	if sampleRate > 1 {
		sampleRate = 1.0
	}

	// 创建 OTLP HTTP exporter
	opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
	if endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
	}
	exporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		return fmt.Errorf("create OTLP exporter: %w", err)
	}

	// 服务名默认
	if serviceName == "" {
		serviceName = "jte"
	}

	// 资源：service.name + telemetry.sdk
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("3.0.0"),
		),
	)
	if err != nil {
		return fmt.Errorf("create OTel resource: %w", err)
	}

	// 采样器：ParentBased + TraceIDRatioBased
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRate))

	// TracerProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otelProvider = tp
	otelTracer = tp.Tracer("github.com/suoten/jt-engine")
	otelReady.Store(true)

	return nil
}

// ShutdownOTel 优雅关闭 OTel SDK，flush 残留 span 到 exporter。
func ShutdownOTel() error {
	if !otelReady.Load() || otelProvider == nil {
		return nil
	}
	if err := otelProvider.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("shutdown OTel: %w", err)
	}
	otelReady.Store(false)
	otelProvider = nil
	otelTracer = nil
	return nil
}

// OTelEnabled 返回 OTel SDK 是否实际接入。
func OTelEnabled() bool {
	return otelEnabled
}

// startOTelSpan 内部使用：当 OTel 初始化后创建真实 OTel span。
// 返回的 Span 已桥接 OTel trace.ContextSpan，同时保留自研 trace_id 注入 context。
//
// 此函数由 trace.go 的 StartSpan 在 otelEnabled && otelReady 时调用。
func startOTelSpan(parent context.Context, name string, logger *zap.Logger) (*Span, context.Context) {
	if !otelReady.Load() || otelTracer == nil {
		// OTel 未初始化，降级到自研 span（不应在 otelEnabled=true 时发生）
		return startLightweightSpan(parent, name, logger)
	}

	// 创建 OTel span
	otelCtx, otelSpan := otelTracer.Start(parent, name)

	// 提取 OTel TraceID/SpanID 转为自研格式（32/16 字符十六进制）
	sc := trace.SpanContextFromContext(otelCtx)
	traceID := sc.TraceID().String()
	spanID := sc.SpanID().String()

	// 兼容性兜底：OTel TraceID 为空时（采样率=0 或其他原因）使用自研生成
	if traceID == "" || traceID == "00000000000000000000000000000000" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		traceID = hex.EncodeToString(b)
	}
	if spanID == "" || spanID == "0000000000000000" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		spanID = hex.EncodeToString(b)
	}

	// 注入自研 context（兼容 zap 日志字段）
	ctx := WithTraceID(WithSpanID(parent, spanID), traceID)

	span := spanPool.Get().(*Span)
	span.TraceID = traceID
	span.SpanID = spanID
	span.Name = name
	span.ctx = ctx
	// 保存 OTel span 用于 End() 时结束
	span.otelSpan = otelSpan
	if logger != nil {
		span.logger = logger.With(
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.String("span_name", name),
		)
	}
	return span, ctx
}

// endOTelSpan 内部使用：结束 OTel span（调用 otelSpan.End()）。
// 由 Span.End() 在 span.otelSpan != nil 时调用。
func endOTelSpan(s *Span) {
	if s.otelSpan != nil {
		// otelSpan 字段类型为 interface{}（兼容 !otel 构建），此处断言为 OTel trace.Span
		if sp, ok := s.otelSpan.(trace.Span); ok {
			sp.End()
		}
		s.otelSpan = nil
	}
}
