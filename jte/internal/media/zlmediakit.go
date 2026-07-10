package media

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/util"
	"go.uber.org/zap"
)

type ZLMediaKitConfig struct {
	APIURL      string `json:"api_url" yaml:"api_url"`
	Secret      string `json:"secret" yaml:"secret"`
	RTSPPort    int    `json:"rtsp_port" yaml:"rtsp_port"`
	RTPPort     int    `json:"rtp_port" yaml:"rtp_port"`
	HTTPPort    int    `json:"http_port" yaml:"http_port"`
	StreamIdle  int    `json:"stream_idle" yaml:"stream_idle"`
	// TcpMode 见 config.ZLMediaKitConfig.TcpMode 注释；0 由 NewZLMediaKitClient 兜底为 1。
	TcpMode     int    `json:"tcp_mode" yaml:"tcp_mode"`
}

type ZLMediaKitClient struct {
	config     *ZLMediaKitConfig
	logger     *zap.Logger
	httpClient *http.Client
	stopCh     chan struct{}
	stopOnce   sync.Once
}

type StreamInfo struct {
	App       string `json:"app"`
	Stream    string `json:"stream"`
	Schema    string `json:"schema"`
	URL       string `json:"url"`
	ReaderCount int  `json:"reader_count"`
}

func NewZLMediaKitClient(cfg *ZLMediaKitConfig, logger *zap.Logger) *ZLMediaKitClient {
	if cfg.RTSPPort == 0 {
		cfg.RTSPPort = 554
	}
	if cfg.RTPPort == 0 {
		cfg.RTPPort = 10000
	}
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.StreamIdle == 0 {
		cfg.StreamIdle = 30
	}
	// TcpMode 缺省兜底为 1（TCP 主动模式），保持修复前的硬编码行为，
	// 避免既有部署因未配置 tcp_mode 而回退到纯 UDP 导致 RTP 不通。
	if cfg.TcpMode == 0 {
		cfg.TcpMode = 1
	}

	return &ZLMediaKitClient{
		config: cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		stopCh: make(chan struct{}),
	}
}

// StartHealthCheck periodically verifies the ZLMediaKit connection.
func (c *ZLMediaKitClient) StartHealthCheck(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	util.SafeGo(c.logger, "media.zlmediakit.healthCheck", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				if c.IsConnected() {
					c.logger.Debug("zlmediakit health check ok")
				} else {
					c.logger.Warn("zlmediakit health check failed")
				}
			}
		}
	})
}

// Stop halts the health check goroutine.
func (c *ZLMediaKitClient) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
}

func (c *ZLMediaKitClient) apiRequest(method, path string, body io.Reader) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/index/api/%s", c.config.APIURL, path)

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, body)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}

	if c.config.Secret != "" {
		q := req.URL.Query()
		q.Set("secret", c.config.Secret)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zlmediakit api request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	code, _ := result["code"].(float64)
	if code != 0 {
		return nil, fmt.Errorf("zlmediakit error: code=%v, msg=%v", result["code"], result["msg"])
	}

	return result, nil
}

func (c *ZLMediaKitClient) StartRTPServer(streamID string) (int, error) {
	// port=0 lets ZLMediaKit auto-allocate a free port, avoiding conflicts.
	// tcp_mode 由配置驱动：0=仅 UDP（不传该参数），1=TCP 主动，2=TCP 被动。
	path := fmt.Sprintf("openRtpServer?port=0&stream_id=%s", streamID)
	if c.config.TcpMode > 0 {
		path = fmt.Sprintf("%s&tcp_mode=%d", path, c.config.TcpMode)
	}
	result, err := c.apiRequest("GET", path, nil)
	if err != nil {
		return 0, err
	}

	port, _ := result["port"].(float64)
	if port == 0 {
		// some versions return the port under data
		if data, ok := result["data"].(map[string]interface{}); ok {
			if p, ok := data["port"].(float64); ok {
				port = p
			}
		}
	}

	c.logger.Info("RTP server started",
		zap.String("stream_id", streamID),
		zap.Int("port", int(port)))

	return int(port), nil
}

func (c *ZLMediaKitClient) StopRTPServer(streamID string) error {
	_, err := c.apiRequest("GET", fmt.Sprintf("closeRtpServer?stream_id=%s", streamID), nil)
	if err != nil {
		return err
	}

	c.logger.Info("RTP server stopped", zap.String("stream_id", streamID))
	return nil
}

func (c *ZLMediaKitClient) GetStreamURL(app, stream string, schema string) string {
	host := strings.TrimPrefix(c.config.APIURL, "http://")
	host = strings.TrimPrefix(host, "https://")
	// 去除 APIURL 中可能包含的端口部分，避免生成 "host:apiport:streamport" 双端口 URL
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		// 确认冒号后是数字（端口），而不是 IPv6 地址的一部分
		portPart := host[idx+1:]
		isPort := true
		for _, ch := range portPart {
			if ch < '0' || ch > '9' {
				isPort = false
				break
			}
		}
		if isPort {
			host = host[:idx]
		}
	}

	switch schema {
	case "rtsp":
		return fmt.Sprintf("rtsp://%s:%d/%s/%s", host, c.config.RTSPPort, app, stream)
	case "rtmp":
		return fmt.Sprintf("rtmp://%s/%s/%s", host, app, stream)
	case "flv":
		return fmt.Sprintf("http://%s:%d/%s/%s.live.flv", host, c.config.HTTPPort, app, stream)
	case "hls":
		return fmt.Sprintf("http://%s:%d/%s/%s/hls.m3u8", host, c.config.HTTPPort, app, stream)
	case "ws-flv":
		return fmt.Sprintf("ws://%s:%d/%s/%s.live.flv", host, c.config.HTTPPort, app, stream)
	default:
		return fmt.Sprintf("http://%s:%d/%s/%s.live.flv", host, c.config.HTTPPort, app, stream)
	}
}

func (c *ZLMediaKitClient) ListStreams() ([]StreamInfo, error) {
	// Query all media list (no app filter) so stream list stays consistent
	// regardless of the app name used when opening the RTP server.
	result, err := c.apiRequest("GET", "getMediaList", nil)
	if err != nil {
		return nil, err
	}

	data, _ := result["data"].([]interface{})
	streams := make([]StreamInfo, 0, len(data))

	for _, item := range data {
		m, _ := item.(map[string]interface{})
		si := StreamInfo{
			App:    fmt.Sprintf("%v", m["app"]),
			Stream: fmt.Sprintf("%v", m["stream"]),
			Schema: fmt.Sprintf("%v", m["schema"]),
		}
		if rc, ok := m["readerCount"].(float64); ok {
			si.ReaderCount = int(rc)
		}
		si.URL = c.GetStreamURL(si.App, si.Stream, si.Schema)
		streams = append(streams, si)
	}

	return streams, nil
}

func (c *ZLMediaKitClient) StartStreamProxy(app, stream, url string) error {
	_, err := c.apiRequest("GET",
		fmt.Sprintf("addStreamProxy?app=%s&stream=%s&url=%s&enable_hls=1&enable_mp4=0", app, stream, url),
		nil)
	if err != nil {
		return err
	}

	c.logger.Info("stream proxy started",
		zap.String("app", app),
		zap.String("stream", stream))
	return nil
}

func (c *ZLMediaKitClient) StopStreamProxy(app, stream string) error {
	_, err := c.apiRequest("GET",
		fmt.Sprintf("delStreamProxy?app=%s&stream=%s", app, stream),
		nil)
	return err
}

func (c *ZLMediaKitClient) IsConnected() bool {
	_, err := c.apiRequest("GET", "getServerConfig", nil)
	return err == nil
}

func (c *ZLMediaKitClient) ExchangeSDP(app, stream, sdpOffer string) (string, error) {
	url := fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=play", c.config.APIURL, app, stream)
	if c.config.Secret != "" {
		url += "&secret=" + c.config.Secret
	}

	reqBody := strings.NewReader(sdpOffer)
	req, err := http.NewRequest("POST", url, reqBody)
	if err != nil {
		return "", fmt.Errorf("create webrtc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/sdp")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("webrtc api request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read webrtc response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse webrtc response: %w", err)
	}

	code, _ := result["code"].(float64)
	if code != 0 {
		return "", fmt.Errorf("webrtc exchange failed: code=%v", result["code"])
	}

	sdpAnswer, _ := result["sdp"].(string)
	if sdpAnswer == "" {
		if sdpRaw, ok := result["data"].(map[string]interface{}); ok {
			sdpAnswer, _ = sdpRaw["sdp"].(string)
		}
	}

	if sdpAnswer == "" {
		return "", fmt.Errorf("webrtc exchange: no SDP answer in response")
	}

	c.logger.Info("WebRTC SDP exchange successful",
		zap.String("app", app),
		zap.String("stream", stream))

	return sdpAnswer, nil
}