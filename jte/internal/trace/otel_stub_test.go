//go:build !otel

package trace

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-7]: OpenTelemetry 集成测试
//
// 此测试在默认构建（无 -tags otel）下验证：
//   - OTelEnabled() 返回 false
//   - InitOTel 返回 ErrOTelNotEnabled
//   - StartSpan 仍可正常工作（降级到轻量级 span）
//   - ShutdownOTel 无错误
//
// 启用 -tags otel 后应补充真实 OTel SDK 集成测试（需要本地 OTLP collector）。
// ===================================================================

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestOTel_DefaultBuild_NotEnabled(t *testing.T) {
	if OTelEnabled() {
		t.Fatal("default build should have OTel disabled, got enabled")
	}
}

func TestOTel_InitReturnsError_WhenNotEnabled(t *testing.T) {
	err := InitOTel("localhost:4318", "jte-test", 1.0)
	if err == nil {
		t.Fatal("expected ErrOTelNotEnabled, got nil")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("expected error mentioning 'not enabled', got: %v", err)
	}
}

func TestOTel_Shutdown_NoError_WhenNotEnabled(t *testing.T) {
	if err := ShutdownOTel(); err != nil {
		t.Fatalf("ShutdownOTel should be no-op in default build, got: %v", err)
	}
}

func TestOTel_StartSpan_StillWorksWithoutOTel(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	// 即使 OTel 未启用，StartSpan 仍应正常工作（降级到轻量级 span）
	span, spanCtx := StartSpan(ctx, "test-operation", logger)
	if span == nil {
		t.Fatal("StartSpan returned nil span")
	}
	if spanCtx == nil {
		t.Fatal("StartSpan returned nil context")
	}

	// 验证 trace_id 被生成并注入 context
	traceID := GetTraceID(spanCtx)
	if traceID == "" {
		t.Fatal("trace_id should be non-empty even without OTel")
	}
	if len(traceID) != TraceIDLength {
		t.Fatalf("trace_id length = %d, want %d", len(traceID), TraceIDLength)
	}

	// 验证 span 携带 otelSpan=nil（默认构建）
	if span.otelSpan != nil {
		t.Fatal("otelSpan should be nil in default build")
	}

	// End 不应 panic
	span.End()
}

func TestOTel_StartSpan_PreservesParentTraceID(t *testing.T) {
	logger := zap.NewNop()
	parentTraceID := NewTraceID()
	ctx := WithTraceID(context.Background(), parentTraceID)

	span, spanCtx := StartSpan(ctx, "child-op", logger)
	defer span.End()

	// 子 span 应继承父 trace_id（同一链路）
	gotTraceID := GetTraceID(spanCtx)
	if gotTraceID != parentTraceID {
		t.Fatalf("child span trace_id = %s, want parent %s", gotTraceID, parentTraceID)
	}

	// span_id 应新生成
	gotSpanID := GetSpanID(spanCtx)
	if gotSpanID == "" {
		t.Fatal("span_id should be non-empty")
	}
	if len(gotSpanID) != SpanIDLength {
		t.Fatalf("span_id length = %d, want %d", len(gotSpanID), SpanIDLength)
	}
}
