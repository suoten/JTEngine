package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewZLMediaKitClientDefaults(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL: "http://localhost:8080",
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if cfg.RTSPPort != 554 {
		t.Errorf("RTSPPort = %d, want 554", cfg.RTSPPort)
	}
	if cfg.RTPPort != 10000 {
		t.Errorf("RTPPort = %d, want 10000", cfg.RTPPort)
	}
	if cfg.HTTPPort != 80 {
		t.Errorf("HTTPPort = %d, want 80", cfg.HTTPPort)
	}
	if cfg.StreamIdle != 30 {
		t.Errorf("StreamIdle = %d, want 30", cfg.StreamIdle)
	}
	if cfg.TcpMode != 1 {
		t.Errorf("TcpMode = %d, want 1", cfg.TcpMode)
	}
	if client.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
	if client.stopCh == nil {
		t.Error("stopCh should not be nil")
	}
}

func TestNewZLMediaKitClientCustomValues(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL:     "http://media.example.com:8888",
		Secret:     "test-secret",
		RTSPPort:   8554,
		RTPPort:    20000,
		HTTPPort:   8080,
		StreamIdle: 60,
		TcpMode:    2,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if cfg.RTSPPort != 8554 {
		t.Errorf("RTSPPort = %d, want 8554", cfg.RTSPPort)
	}
	if cfg.TcpMode != 2 {
		t.Errorf("TcpMode = %d, want 2", cfg.TcpMode)
	}
	if client.config.Secret != "test-secret" {
		t.Errorf("Secret = %q", client.config.Secret)
	}
}

func TestGetStreamURL(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL:    "http://media.example.com:8080",
		RTSPPort:  554,
		HTTPPort:  80,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	tests := []struct {
		name   string
		app    string
		stream string
		schema string
		want   string
	}{
		{
			name:   "rtsp",
			app:    "rtp",
			stream: "test001",
			schema: "rtsp",
			want:   "rtsp://media.example.com:554/rtp/test001",
		},
		{
			name:   "rtmp",
			app:    "live",
			stream: "stream1",
			schema: "rtmp",
			want:   "rtmp://media.example.com/live/stream1",
		},
		{
			name:   "flv",
			app:    "rtp",
			stream: "test001",
			schema: "flv",
			want:   "http://media.example.com:80/rtp/test001.live.flv",
		},
		{
			name:   "hls",
			app:    "rtp",
			stream: "test001",
			schema: "hls",
			want:   "http://media.example.com:80/rtp/test001/hls.m3u8",
		},
		{
			name:   "ws-flv",
			app:    "rtp",
			stream: "test001",
			schema: "ws-flv",
			want:   "ws://media.example.com:80/rtp/test001.live.flv",
		},
		{
			name:   "default",
			app:    "rtp",
			stream: "test001",
			schema: "unknown",
			want:   "http://media.example.com:80/rtp/test001.live.flv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.GetStreamURL(tt.app, tt.stream, tt.schema)
			if got != tt.want {
				t.Errorf("GetStreamURL(%q, %q, %q) = %q, want %q",
					tt.app, tt.stream, tt.schema, got, tt.want)
			}
		})
	}
}

func TestGetStreamURLHTTPS(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL:   "https://media.secure.com",
		HTTPPort: 443,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	url := client.GetStreamURL("app", "stream", "flv")
	// HTTPS prefix should be stripped
	if url != "http://media.secure.com:443/app/stream.live.flv" {
		t.Errorf("GetStreamURL with HTTPS APIURL = %q", url)
	}
}

func TestStop(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL: "http://localhost:8080",
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	// Stop should not panic
	client.Stop()

	// Double stop should not panic (sync.Once)
	client.Stop()
}

func TestStartHealthCheck(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL: "http://localhost:1", // unreachable, will fail
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())
	defer client.Stop()

	// Start with short interval
	client.StartHealthCheck(100 * time.Millisecond)

	// Wait a bit to let health check run
	time.Sleep(300 * time.Millisecond)

	// Stop should be safe
	client.Stop()
}

func TestStartRTPServer(t *testing.T) {
	// Mock ZLMediaKit API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"code": 0,
			"port": float64(30000),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL:  server.URL,
		Secret:  "test-secret",
		TcpMode: 1,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	port, err := client.StartRTPServer("stream-001")
	if err != nil {
		t.Fatalf("StartRTPServer: %v", err)
	}
	if port != 30000 {
		t.Errorf("port = %d, want 30000", port)
	}
}

func TestStartRTPServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"code": 1,
			"msg":  "port already in use",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL: server.URL,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	_, err := client.StartRTPServer("stream-001")
	if err == nil {
		t.Error("StartRTPServer should return error when API returns non-zero code")
	}
}

func TestStopRTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{"code": 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL: server.URL,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if err := client.StopRTPServer("stream-001"); err != nil {
		t.Fatalf("StopRTPServer: %v", err)
	}
}

func TestListStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"code": 0,
			"data": []interface{}{
				map[string]interface{}{
					"app":         "rtp",
					"stream":      "stream1",
					"schema":      "rtsp",
					"readerCount": float64(3),
				},
				map[string]interface{}{
					"app":         "rtp",
					"stream":      "stream2",
					"schema":      "flv",
					"readerCount": float64(0),
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL:   server.URL,
		HTTPPort: 80,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	streams, err := client.ListStreams()
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("len(streams) = %d, want 2", len(streams))
	}
	if streams[0].Stream != "stream1" {
		t.Errorf("streams[0].Stream = %q, want %q", streams[0].Stream, "stream1")
	}
	if streams[0].ReaderCount != 3 {
		t.Errorf("streams[0].ReaderCount = %d, want 3", streams[0].ReaderCount)
	}
}

func TestIsConnected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{"code": 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL: server.URL,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if !client.IsConnected() {
		t.Error("IsConnected should return true for reachable server")
	}
}

func TestIsConnectedUnreachable(t *testing.T) {
	cfg := &ZLMediaKitConfig{
		APIURL: "http://127.0.0.1:1", // unreachable port
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if client.IsConnected() {
		t.Error("IsConnected should return false for unreachable server")
	}
}

func TestStartStreamProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{"code": 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL: server.URL,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if err := client.StartStreamProxy("rtp", "proxy1", "rtsp://source/stream"); err != nil {
		t.Fatalf("StartStreamProxy: %v", err)
	}
}

func TestStopStreamProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{"code": 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &ZLMediaKitConfig{
		APIURL: server.URL,
	}
	client := NewZLMediaKitClient(cfg, zap.NewNop())

	if err := client.StopStreamProxy("rtp", "proxy1"); err != nil {
		t.Fatalf("StopStreamProxy: %v", err)
	}
}

func TestStreamInfoJSON(t *testing.T) {
	si := StreamInfo{
		App:         "rtp",
		Stream:      "test",
		Schema:      "flv",
		URL:         "http://localhost/test.live.flv",
		ReaderCount: 5,
	}

	data, err := json.Marshal(si)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded StreamInfo
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.App != "rtp" || loaded.Stream != "test" || loaded.ReaderCount != 5 {
		t.Errorf("StreamInfo round-trip failed: %+v", loaded)
	}
}
