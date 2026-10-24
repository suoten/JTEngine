package media

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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
	// rtpPortCursor 固定端口池的轮转游标（原子递增），多路并发流分散到不同端口
	rtpPortCursor uint32
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
	// FIXED [2026-10-24]: RTPPort 不再兜底为 10000。10000 是 ZLMediaKit rtp_proxy
	// 的常驻主端口，用作 openRtpServer 固定端口池起始会立即冲突。
	// RTPPort=0 表示不启用固定端口池（由 ZLMediaKit 自动分配，仅适合非容器部署）。
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
	// FIXED [2026-10-24]: 官方 ZLMediaKit（release/master 镜像）的 API 鉴权只认
	// ?secret= 查询参数，不认 X-API-Secret 头——仅传头会得到 code=-100
	// "Please login first"，导致 openRtpServer 等全部失败。
	// 查询参数 + Header 双通道：官方镜像与支持 Header 的自编译版本都兼容。
	if c.config.Secret != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path = fmt.Sprintf("%s%ssecret=%s", path, sep, url.QueryEscape(c.config.Secret))
	}

	reqURL := fmt.Sprintf("%s/index/api/%s", c.config.APIURL, path)

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, reqURL, body)
	} else {
		req, err = http.NewRequest(method, reqURL, nil)
	}
	if err != nil {
		return nil, err
	}

	// 保留 Header 传递（部分自编译版本支持，无此支持的版本会忽略）
	if c.config.Secret != "" {
		req.Header.Set("X-API-Secret", c.config.Secret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zlmediakit api request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // AUTO-FIX-2026-07-25 [P2-R9]: 限制 1MB
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

// rtpPortPoolSize 固定端口池大小：从 rtp_port 起连续本数量个端口，
// 容器化 ZLMediaKit 部署时映射该范围即可承接全部并发流。
const rtpPortPoolSize = 50

// StartRTPServer 在 ZLMediaKit 上为 streamID 开启 RTP 收流端口并返回端口号。
//
// FIXED [2026-10-24]: openRtpServer?port=0 自动分配的是 30000+ 的随机高位端口，
// Docker 端口映射无法提前覆盖，容器化 ZLMediaKit 收不到终端推流/引擎转发。
// 配置 rtp_port>0 时改用固定端口池：从 rtp_port 起按游标轮转取用，
// 端口被占（并发流或残留状态）时依次尝试池内下一个；池耗尽回退自动分配。
func (c *ZLMediaKitClient) StartRTPServer(streamID string) (int, error) {
	if c.config.RTPPort > 0 {
		first := int(atomic.AddUint32(&c.rtpPortCursor, 1))
		var lastErr error
		for i := 0; i < rtpPortPoolSize; i++ {
			port := c.config.RTPPort + ((first-1+i)%rtpPortPoolSize)
			p, err := c.openRtpServer(streamID, port)
			if err == nil {
				return p, nil
			}
			lastErr = err
			// 流已存在等业务性错误，换端口无意义，直接透传
			msg := fmt.Sprint(err)
			if strings.Contains(msg, "exist") || strings.Contains(msg, "存在") {
				return 0, err
			}
			c.logger.Warn("rtp port unavailable, trying next",
				zap.Int("port", port), zap.Error(err))
		}
		c.logger.Warn("rtp port pool exhausted, falling back to auto-allocate",
			zap.Int("pool_start", c.config.RTPPort), zap.Error(lastErr))
	}
	return c.openRtpServer(streamID, 0)
}

func (c *ZLMediaKitClient) openRtpServer(streamID string, port int) (int, error) {
	// port=0 lets ZLMediaKit auto-allocate a free port, avoiding conflicts.
	// tcp_mode 由配置驱动：0=仅 UDP（不传该参数），1=TCP 主动，2=TCP 被动。
	// FIXED [P2]: streamID 需 URL 编码，防止特殊字符（如 #、&、=）破坏查询参数解析
	path := fmt.Sprintf("openRtpServer?port=%d&stream_id=%s", port, url.QueryEscape(streamID))
	if c.config.TcpMode > 0 {
		path = fmt.Sprintf("%s&tcp_mode=%d", path, c.config.TcpMode)
	}
	result, err := c.apiRequest("GET", path, nil)
	if err != nil {
		return 0, err
	}

	allocated, _ := result["port"].(float64)
	if allocated == 0 {
		// some versions return the port under data
		if data, ok := result["data"].(map[string]interface{}); ok {
			if p, ok := data["port"].(float64); ok {
				allocated = p
			}
		}
	}

	c.logger.Info("RTP server started",
		zap.String("stream_id", streamID),
		zap.Int("port", int(allocated)))

	return int(allocated), nil
}

func (c *ZLMediaKitClient) StopRTPServer(streamID string) error {
	// FIXED [P2]: streamID 需 URL 编码
	_, err := c.apiRequest("GET", fmt.Sprintf("closeRtpServer?stream_id=%s", url.QueryEscape(streamID)), nil)
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

func (c *ZLMediaKitClient) StartStreamProxy(app, stream, srcURL string) error {
	// FIXED [P2]: 查询参数需 URL 编码；参数名改为 srcURL 避免遮蔽 net/url 包
	_, err := c.apiRequest("GET",
		fmt.Sprintf("addStreamProxy?app=%s&stream=%s&url=%s&enable_hls=1&enable_mp4=0",
			url.QueryEscape(app), url.QueryEscape(stream), url.QueryEscape(srcURL)),
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
	// FIXED [P2]: 查询参数需 URL 编码
	_, err := c.apiRequest("GET",
		fmt.Sprintf("delStreamProxy?app=%s&stream=%s", url.QueryEscape(app), url.QueryEscape(stream)),
		nil)
	return err
}

func (c *ZLMediaKitClient) IsConnected() bool {
	_, err := c.apiRequest("GET", "getServerConfig", nil)
	return err == nil
}

func (c *ZLMediaKitClient) ExchangeSDP(app, stream, sdpOffer string) (string, error) {
	// FIXED [P2]: 查询参数需 URL 编码
	reqURL := fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=play",
		c.config.APIURL, url.QueryEscape(app), url.QueryEscape(stream))
	// FIXED [2026-10-24]: secret 必须走查询参数（官方 ZLMediaKit 不认 Header，同 apiRequest）
	if c.config.Secret != "" {
		reqURL += "&secret=" + url.QueryEscape(c.config.Secret)
	}
	reqBody := strings.NewReader(sdpOffer)
	req, err := http.NewRequest("POST", reqURL, reqBody)
	if err != nil {
		return "", fmt.Errorf("create webrtc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/sdp")
	// 保留 Header 传递（部分自编译版本支持）
	if c.config.Secret != "" {
		req.Header.Set("X-API-Secret", c.config.Secret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("webrtc api request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // AUTO-FIX-2026-07-25 [P2-R9]: 限制 1MB
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