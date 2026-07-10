package trace

// ===================================================================
// 运维验收 4: 日志可查
//
// 验证项：
//   1. 结构化日志：时间/级别/模块/设备ID/TraceID
//   2. TraceID 生成唯一且格式正确（32 字符十六进制）
//   3. TraceID 通过 context 传播
//   4. LoggerWithTrace 注入 trace_id/device_id/org_id
//   5. 能根据设备 ID 查完整链路（device_id 字段注入）
//   6. Gin 中间件注入 trace_id
// ===================================================================

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestV4_TraceID_Unique 验证 TraceID 唯一性。
// 验收标准：每个请求生成唯一 trace_id。
func TestV4_TraceID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewTraceID()
		if id == "" {
			t.Fatal("TraceID 不应为空")
		}
		if ids[id] {
			t.Fatalf("TraceID 重复: %s", id)
		}
		ids[id] = true
	}
}

// TestV4_TraceID_Format 验证 TraceID 格式。
// 32 字符十六进制，与 OTel TraceID 一致。
func TestV4_TraceID_Format(t *testing.T) {
	id := NewTraceID()
	if len(id) != TraceIDLength {
		t.Errorf("TraceID 长度 = %d, 期望 %d", len(id), TraceIDLength)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("TraceID 包含非十六进制字符: %c", c)
		}
	}
}

// TestV4_SpanID_Format 验证 SpanID 格式。
// 16 字符十六进制，与 OTel SpanID 一致。
func TestV4_SpanID_Format(t *testing.T) {
	id := NewSpanID()
	if len(id) != SpanIDLength {
		t.Errorf("SpanID 长度 = %d, 期望 %d", len(id), SpanIDLength)
	}
}

// TestV4_TraceID_ContextPropagation 验证 TraceID 通过 context 传播。
func TestV4_TraceID_ContextPropagation(t *testing.T) {
	traceID := NewTraceID()
	ctx := WithTraceID(context.Background(), traceID)

	retrieved := GetTraceID(ctx)
	if retrieved != traceID {
		t.Errorf("从 context 获取 TraceID = %s, 期望 %s", retrieved, traceID)
	}
}

// TestV4_TraceID_NilContext 验证 nil context 安全处理。
func TestV4_TraceID_NilContext(t *testing.T) {
	if id := GetTraceID(nil); id != "" {
		t.Errorf("nil context 应返回空字符串, 实际 %s", id)
	}
}

// TestV4_DeviceID_ContextPropagation 验证 device_id 通过 context 传播。
// 验收标准：能根据设备 ID 查完整链路。
func TestV4_DeviceID_ContextPropagation(t *testing.T) {
	deviceID := "13800000000"
	ctx := WithDeviceID(context.Background(), deviceID)

	retrieved := GetDeviceID(ctx)
	if retrieved != deviceID {
		t.Errorf("从 context 获取 device_id = %s, 期望 %s", retrieved, deviceID)
	}
}

// TestV4_OrgID_ContextPropagation 验证 org_id 通过 context 传播。
func TestV4_OrgID_ContextPropagation(t *testing.T) {
	orgID := "org-001"
	ctx := WithOrgID(context.Background(), orgID)

	retrieved := GetOrgID(ctx)
	if retrieved != orgID {
		t.Errorf("从 context 获取 org_id = %s, 期望 %s", retrieved, orgID)
	}
}

// TestV4_LoggerWithTrace_AllFields 验证 LoggerWithTrace 注入所有字段。
// 验收标准：结构化日志包含 trace_id/device_id/org_id。
func TestV4_LoggerWithTrace_AllFields(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	traceID := NewTraceID()
	spanID := NewSpanID()
	deviceID := "13800000000"
	orgID := "org-001"

	ctx := WithTraceID(WithSpanID(WithDeviceID(WithOrgID(context.Background(), orgID), deviceID), spanID), traceID)

	tracedLogger := LoggerWithTrace(ctx, logger)
	tracedLogger.Info("test message")

	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("日志条目数 = %d, 期望 1", len(entries))
	}

	fields := entries[0].ContextMap()
	if fields["trace_id"] != traceID {
		t.Errorf("日志 trace_id = %v, 期望 %s", fields["trace_id"], traceID)
	}
	if fields["span_id"] != spanID {
		t.Errorf("日志 span_id = %v, 期望 %s", fields["span_id"], spanID)
	}
	if fields["device_id"] != deviceID {
		t.Errorf("日志 device_id = %v, 期望 %s", fields["device_id"], deviceID)
	}
	if fields["org_id"] != orgID {
		t.Errorf("日志 org_id = %v, 期望 %s", fields["org_id"], orgID)
	}
}

// TestV4_LoggerWithTrace_NoTraceID 验证无 TraceID 时不注入字段。
func TestV4_LoggerWithTrace_NoTraceID(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	ctx := context.Background()
	tracedLogger := LoggerWithTrace(ctx, logger)
	tracedLogger.Info("test message")

	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("日志条目数 = %d, 期望 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if _, exists := fields["trace_id"]; exists {
		t.Error("无 TraceID 时不应注入 trace_id 字段")
	}
}

// TestV4_GinMiddleware_TraceIDInjection 验证 Gin 中间件注入 trace_id。
// 验收标准：每个 HTTP 请求自动生成 trace_id。
func TestV4_GinMiddleware_TraceIDInjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())

	var capturedTraceID string
	r.GET("/test", func(c *gin.Context) {
		capturedTraceID = GetTraceIDFromGin(c)
		c.JSON(200, gin.H{"trace_id": capturedTraceID})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if capturedTraceID == "" {
		t.Error("Gin 中间件未注入 trace_id")
	}
	if len(capturedTraceID) != TraceIDLength {
		t.Errorf("trace_id 长度 = %d, 期望 %d", len(capturedTraceID), TraceIDLength)
	}
	// 验证响应头部包含 trace_id
	if w.Header().Get(HeaderTraceID) != capturedTraceID {
		t.Error("响应头部未包含 trace_id")
	}
}

// TestV4_GinMiddleware_ExternalTraceID 验证外部传入的 trace_id 被复用。
func TestV4_GinMiddleware_ExternalTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())

	externalTraceID := NewTraceID()
	var capturedTraceID string
	r.GET("/test", func(c *gin.Context) {
		capturedTraceID = GetTraceIDFromGin(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderTraceID, externalTraceID)
	r.ServeHTTP(w, req)

	if capturedTraceID != externalTraceID {
		t.Errorf("外部 trace_id = %s, 实际使用 = %s", externalTraceID, capturedTraceID)
	}
}

// TestV4_GinMiddleware_InvalidTraceID 验证无效 trace_id 被替换。
func TestV4_GinMiddleware_InvalidTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())

	var capturedTraceID string
	r.GET("/test", func(c *gin.Context) {
		capturedTraceID = GetTraceIDFromGin(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderTraceID, "invalid-trace-id")
	r.ServeHTTP(w, req)

	if capturedTraceID == "invalid-trace-id" {
		t.Error("无效 trace_id 应被替换")
	}
	if len(capturedTraceID) != TraceIDLength {
		t.Errorf("替换后 trace_id 长度 = %d, 期望 %d", len(capturedTraceID), TraceIDLength)
	}
}

// TestV4_StartSpan_TraceIDPropagation 验证 span 继承父 context 的 trace_id。
func TestV4_StartSpan_TraceIDPropagation(t *testing.T) {
	logger := zap.NewNop()
	parentTraceID := NewTraceID()
	parentCtx := WithTraceID(context.Background(), parentTraceID)

	span, spanCtx := StartSpan(parentCtx, "test-span", logger)
	defer span.End()

	if span.TraceID != parentTraceID {
		t.Errorf("span TraceID = %s, 期望继承父 %s", span.TraceID, parentTraceID)
	}
	if GetTraceID(spanCtx) != parentTraceID {
		t.Error("span context 中 trace_id 应与父一致")
	}
}

// TestV4_StartSpan_NewTraceID 验证无父 trace_id 时生成新的。
func TestV4_StartSpan_NewTraceID(t *testing.T) {
	logger := zap.NewNop()
	span, _ := StartSpan(context.Background(), "test-span", logger)
	defer span.End()

	if span.TraceID == "" {
		t.Error("无父 context 时应生成新的 trace_id")
	}
	if len(span.TraceID) != TraceIDLength {
		t.Errorf("新 trace_id 长度 = %d, 期望 %d", len(span.TraceID), TraceIDLength)
	}
}

// TestV4_FieldsFromContext 验证 FieldsFromContext 提取所有字段。
func TestV4_FieldsFromContext(t *testing.T) {
	traceID := NewTraceID()
	spanID := NewSpanID()
	deviceID := "13800000000"
	orgID := "org-001"

	ctx := WithTraceID(WithSpanID(WithDeviceID(WithOrgID(context.Background(), orgID), deviceID), spanID), traceID)

	fields := FieldsFromContext(ctx)
	if len(fields) != 4 {
		t.Fatalf("字段数 = %d, 期望 4", len(fields))
	}

	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Key] = f.String
	}

	if fieldMap["trace_id"] != traceID {
		t.Errorf("trace_id = %s, 期望 %s", fieldMap["trace_id"], traceID)
	}
	if fieldMap["device_id"] != deviceID {
		t.Errorf("device_id = %s, 期望 %s", fieldMap["device_id"], deviceID)
	}
}

// TestV4_NetHTTPMiddleware 验证标准 http.Handler 中间件。
func TestV4_NetHTTPMiddleware(t *testing.T) {
	var capturedTraceID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTraceID = GetTraceID(r.Context())
		w.WriteHeader(200)
	})

	wrapped := NetHTTPMiddleware(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	wrapped.ServeHTTP(w, req)

	if capturedTraceID == "" {
		t.Error("标准 HTTP 中间件未注入 trace_id")
	}
	if w.Header().Get(HeaderTraceID) != capturedTraceID {
		t.Error("响应头部未包含 trace_id")
	}
}

// TestV4_DeviceLogger_FullChain 验证按设备 ID 查完整链路。
// 模拟：注册→鉴权→上报→报警全流程，所有日志包含同一 device_id 和 trace_id。
func TestV4_DeviceLogger_FullChain(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	baseLogger := zap.New(core)

	traceID := NewTraceID()
	deviceID := "13800000000"

	// 模拟全流程共享同一 trace_id 和 device_id
	ctx := WithTraceID(WithDeviceID(context.Background(), deviceID), traceID)
	logger := LoggerWithTrace(ctx, baseLogger)

	// 模拟注册→鉴权→上报→报警全流程
	logger.Info("设备注册")
	logger.Info("设备鉴权")
	logger.Info("位置上报")
	logger.Info("报警触发")

	entries := recorded.All()
	if len(entries) != 4 {
		t.Fatalf("日志条目数 = %d, 期望 4", len(entries))
	}

	// 验证所有日志包含相同的 trace_id 和 device_id
	for i, entry := range entries {
		fields := entry.ContextMap()
		if fields["trace_id"] != traceID {
			t.Errorf("日志[%d] trace_id 不一致", i)
		}
		if fields["device_id"] != deviceID {
			t.Errorf("日志[%d] device_id 不一致", i)
		}
	}

	// 验证可以通过 device_id 过滤完整链路
	var chainLogs []string
	for _, entry := range entries {
		if entry.ContextMap()["device_id"] == deviceID {
			chainLogs = append(chainLogs, entry.Message)
		}
	}
	if len(chainLogs) != 4 {
		t.Errorf("按 device_id 过滤的链路日志数 = %d, 期望 4", len(chainLogs))
	}
	expected := []string{"设备注册", "设备鉴权", "位置上报", "报警触发"}
	if strings.Join(chainLogs, ",") != strings.Join(expected, ",") {
		t.Errorf("链路顺序不匹配: %v", chainLogs)
	}
}
