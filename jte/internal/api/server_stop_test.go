package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/api/websocket"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-29 [P1]: 测试 Server.Stop() 的优雅停机能力。
// 覆盖：nil 安全、HTTP listener 关闭、WS Hub 联动停止、幂等性。

// TestServer_Stop_NilSafe 验证 httpServer 和 wsHub 为 nil 时 Stop 不 panic。
func TestServer_Stop_NilSafe(t *testing.T) {
	s := &Server{logger: zap.NewNop()}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop with nil httpServer/wsHub: unexpected error: %v", err)
	}
}

// TestServer_Stop_ClosesHTTPListener 验证 Stop() 关闭 HTTP listener，新请求被拒绝。
func TestServer_Stop_ClosesHTTPListener(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{
		logger: zap.NewNop(),
		engine: gin.New(),
	}
	s.engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 启动 HTTP server 在随机端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.httpServer = &http.Server{Handler: s.engine}
	go func() { _ = s.httpServer.Serve(ln) }()

	// 等待服务就绪并验证可访问
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if !waitForHTTPReady(url, 2*time.Second) {
		t.Fatal("HTTP server did not become ready")
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("request before Stop failed: %v", err)
	}
	resp.Body.Close()

	// 调用 Stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop returned error: %v", err)
	}

	// 验证新请求被拒绝（listener 已关闭）
	_, err = http.Get(url)
	if err == nil {
		t.Error("expected connection refused after Stop, got nil error")
	}
}

// TestServer_Stop_StopsWSHub 验证 Stop() 同时停止 WS Hub（Run goroutine 退出）。
func TestServer_Stop_StopsWSHub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zap.NewNop()
	hub := websocket.NewHub(logger)
	hubDone := make(chan struct{})
	go func() {
		hub.Run()
		close(hubDone)
	}()

	s := &Server{
		logger: logger,
		engine: gin.New(),
		wsHub:  hub,
	}

	// 启动 HTTP server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	s.httpServer = &http.Server{Handler: s.engine}
	go func() { _ = s.httpServer.Serve(ln) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop returned error: %v", err)
	}

	// 验证 WS Hub Run() 已退出（Server.Stop 调用 wsHub.Stop 触发 stopCh）
	select {
	case <-hubDone:
		// 成功：Hub.Run() 已退出
	case <-time.After(2 * time.Second):
		t.Error("WS Hub Run() did not exit after Server.Stop()")
	}
}

// TestServer_Stop_Idempotent 验证多次调用 Stop 不 panic。
func TestServer_Stop_Idempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zap.NewNop()
	s := &Server{
		logger: logger,
		engine: gin.New(),
		wsHub:  websocket.NewHub(logger),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	s.httpServer = &http.Server{Handler: s.engine}
	go func() { _ = s.httpServer.Serve(ln) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = s.Stop(ctx)
	// 第二次 Stop：httpServer.Shutdown 幂等，wsHub.Stop 用 sync.Once 保护
	_ = s.Stop(ctx)
}

// waitForHTTPReady 轮询检查 HTTP 服务是否就绪。
func waitForHTTPReady(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
