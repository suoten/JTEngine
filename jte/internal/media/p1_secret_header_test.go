package media

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// [P1-1] TestZLMediaKit_SecretViaHeader 验证 API secret 通过 Header 传递而非 URL query 参数。
func TestZLMediaKit_SecretViaHeader(t *testing.T) {
	var receivedSecret string
	var receivedURL string

	// 模拟 ZLMediaKit API 服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-API-Secret")
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0}`))
	}))
	defer ts.Close()

	client := NewZLMediaKitClient(&ZLMediaKitConfig{
		APIURL: ts.URL,
		Secret: "my-secret-key",
	}, zap.NewNop())

	_, err := client.apiRequest("GET", "getServerConfig", nil)
	if err != nil {
		t.Fatalf("apiRequest failed: %v", err)
	}

	// 验证 secret 通过 Header 传递
	if receivedSecret != "my-secret-key" {
		t.Fatalf("X-API-Secret header = %q, want 'my-secret-key'", receivedSecret)
	}

	// 验证 URL 中不包含 secret 参数
	if contains(receivedURL, "secret") {
		t.Fatalf("URL should not contain secret parameter, got: %s", receivedURL)
	}
}

// [P1-1] TestZLMediaKit_NoSecretInURL 验证 URL 不泄露 secret。
func TestZLMediaKit_NoSecretInURL(t *testing.T) {
	var receivedURL string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0}`))
	}))
	defer ts.Close()

	client := NewZLMediaKitClient(&ZLMediaKitConfig{
		APIURL: ts.URL,
		Secret: "super-secret-123",
	}, zap.NewNop())

	_, _ = client.apiRequest("GET", "getMediaList", nil)

	if contains(receivedURL, "super-secret-123") {
		t.Fatalf("URL leaked secret: %s", receivedURL)
	}
}

// [P1-1] TestZLMediaKit_ExchangeSDP_SecretViaHeader 验证 WebRTC 接口也通过 Header 传递 secret。
func TestZLMediaKit_ExchangeSDP_SecretViaHeader(t *testing.T) {
	var receivedSecret string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-API-Secret")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"sdp":"v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n"}`))
	}))
	defer ts.Close()

	client := NewZLMediaKitClient(&ZLMediaKitConfig{
		APIURL: ts.URL,
		Secret: "webrtc-secret",
	}, zap.NewNop())

	_, err := client.ExchangeSDP("test", "stream1", "v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\n")
	if err != nil {
		t.Fatalf("ExchangeSDP failed: %v", err)
	}

	if receivedSecret != "webrtc-secret" {
		t.Fatalf("X-API-Secret header = %q, want 'webrtc-secret'", receivedSecret)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
