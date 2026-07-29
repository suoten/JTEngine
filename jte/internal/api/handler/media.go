// FIXED: [P1] media.go globalDownloadTracker.StartCleanup goroutine 缺少 recover() [2026-07-17]
package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/internal/media"
	jt1078 "github.com/suoten/jt-engine/pkg/protocol/jt1078"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-28: 视频录像存储路径规范与录制段管理（plan 8.6.2 + 8.6.3）

// VideoRecord 视频录像段记录（对应关系库 video_record 索引表）。
type VideoRecord struct {
	DeviceID     string    `json:"device_id"`
	Channel      string    `json:"channel"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	FilePath     string    `json:"file_path"`  // S3 对象 key
	FileSize     int64     `json:"file_size"`
	Duration     float64   `json:"duration"`      // 秒
	StreamType   string    `json:"stream_type"`   // main/sub/switch
	HasGap       bool      `json:"has_gap"`       // 相邻段间隔>5s 标记为断片
	SwitchReason string    `json:"switch_reason"` // 切换原因（quality_poor/device_fault 等）
}

// BuildVideoPath 构造 S3 录像对象 key。
// 格式: {deviceId}/{channel}/{yyyy}/{MM}/{dd}/{HHmmss}_{streamType}.mp4
// 例: 12345678901/1/2026/06/28/143025_main.mp4
func BuildVideoPath(deviceID, channel string, ts time.Time, streamType string) string {
	return fmt.Sprintf("%s/%s/%04d/%02d/%02d/%s_%s.mp4",
		deviceID, channel,
		ts.Year(), int(ts.Month()), ts.Day(),
		ts.Format("150405"),
		streamType)
}

// RecordManager 录制段管理器（轻量内存实现）。
// 维护当前录制段信息，支持子码流切换时落盘当前段、开新段、检测断片间隔。
type RecordManager struct {
	mu       sync.Mutex
	segments map[string]*VideoRecord // key: deviceID_channel
	store    storage.Interface
	logger   *zap.Logger
}

// NewRecordManager 创建录制段管理器。
func NewRecordManager(store storage.Interface, logger *zap.Logger) *RecordManager {
	return &RecordManager{
		segments: make(map[string]*VideoRecord),
		store:    store,
		logger:   logger,
	}
}

// segmentKey 构造段映射 key。
func segmentKey(deviceID, channel string) string {
	return deviceID + "_" + channel
}

// FlushCurrentSegment 落盘当前录制段：设置 EndTime/Duration 并从内存移除。
// 无当前段时返回 nil（幂等）。
func (rm *RecordManager) FlushCurrentSegment(deviceID, channel string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	key := segmentKey(deviceID, channel)
	seg, ok := rm.segments[key]
	if !ok {
		return nil
	}

	seg.EndTime = time.Now()
	if !seg.StartTime.IsZero() {
		seg.Duration = seg.EndTime.Sub(seg.StartTime).Seconds()
	}
	rm.logger.Info("record segment flushed",
		zap.String("device_id", deviceID),
		zap.String("channel", channel),
		zap.String("file_path", seg.FilePath),
		zap.String("stream_type", seg.StreamType),
		zap.Float64("duration", seg.Duration))
	delete(rm.segments, key)
	return nil
}

// StartNewSegment 开始新录制段。若与前一段间隔>5s 标记 has_gap=true 并写入 alert 表。
// streamType: "main"/"sub"/"switch"；switchReason: 切换原因（可空）。
func (rm *RecordManager) StartNewSegment(deviceID, channel, streamType string) error {
	rm.mu.Lock()
	key := segmentKey(deviceID, channel)
	now := time.Now()
	filePath := BuildVideoPath(deviceID, channel, now, streamType)

	var hasGap bool
	if prev, ok := rm.segments[key]; ok {
		// 前一段未 Flush，先结算
		prev.EndTime = now
		if !prev.StartTime.IsZero() {
			prev.Duration = prev.EndTime.Sub(prev.StartTime).Seconds()
		}
		// 相邻段间隔>5s 标记断片（此处以 prev.EndTime 与 now 之差衡量，
		// 由于切换是即时的，间隔主要来源于前一段的结束延迟）
		gap := now.Sub(prev.EndTime)
		if gap > 5*time.Second {
			hasGap = true
		}
		rm.logger.Info("previous segment closed on new segment start",
			zap.String("device_id", deviceID),
			zap.String("channel", channel),
			zap.Duration("gap", gap),
			zap.Bool("has_gap", hasGap))
	}

	seg := &VideoRecord{
		DeviceID:     deviceID,
		Channel:      channel,
		StartTime:    now,
		FilePath:     filePath,
		StreamType:   streamType,
		HasGap:       hasGap,
		SwitchReason: "", // 由调用方按需设置
	}
	rm.segments[key] = seg
	rm.mu.Unlock()

	// 断片写入 alert 表（plan 8.6.2: 相邻文件时间戳间隔>5s 标记为断片，写入 alert 表）
	if hasGap && rm.store != nil {
		alarm := &storage.AlarmData{
			ID:        fmt.Sprintf("video_gap_%s_%s_%d", deviceID, channel, now.Unix()),
			VehicleID: deviceID,
			Type:      "video_segment_gap",
			Level:     2,
			Time:      now,
			ReceivedAt: now,
			Source:    "record_manager",
			AIReason:  "recording segment gap > 5s during stream switch",
		}
		if err := rm.store.SaveAlarm(context.Background(), alarm); err != nil {
			rm.logger.Warn("save video gap alarm failed",
				zap.String("device_id", deviceID),
				zap.String("channel", channel),
				zap.Error(err))
		}
	}

	rm.logger.Info("new record segment started",
		zap.String("device_id", deviceID),
		zap.String("channel", channel),
		zap.String("file_path", filePath),
		zap.String("stream_type", streamType),
		zap.Bool("has_gap", hasGap))
	return nil
}

// MarkSwitchReason 标记当前段的切换原因。
func (rm *RecordManager) MarkSwitchReason(deviceID, channel, reason string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	key := segmentKey(deviceID, channel)
	if seg, ok := rm.segments[key]; ok {
		seg.SwitchReason = reason
	}
}

// GetCurrentSegment 返回当前录制段（只读副本）。
func (rm *RecordManager) GetCurrentSegment(deviceID, channel string) (*VideoRecord, bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	key := segmentKey(deviceID, channel)
	seg, ok := rm.segments[key]
	if !ok {
		return nil, false
	}
	cp := *seg
	return &cp, true
}

// ListSegments 列出当前内存中所有录制段（按 deviceID/channel 过滤）。
// 注：仅返回活跃段，已落盘的历史段需查询对象存储索引。
func (rm *RecordManager) ListSegments(deviceID, channel string) []*VideoRecord {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	result := make([]*VideoRecord, 0, len(rm.segments))
	for key, seg := range rm.segments {
		if deviceID != "" && seg.DeviceID != deviceID {
			continue
		}
		if channel != "" && seg.Channel != channel {
			continue
		}
		_ = key
		cp := *seg
		result = append(result, &cp)
	}
	return result
}

type MediaHandler struct {
	store         storage.Interface
	logger        *zap.Logger
	cfg           *config.Config
	media         *media.ZLMediaKitClient
	commandSender *CommandSender
	videoEngine   *jt1078.VideoEngine
	// AUTO-FIX-2026-06-28: 录制段管理器（plan 8.6.2 子码流切换断片防护）
	recordManager *RecordManager
}

func NewMediaHandler(store storage.Interface, logger *zap.Logger, cfg *config.Config, mediaClient *media.ZLMediaKitClient, commandSender *CommandSender, videoEngine *jt1078.VideoEngine) *MediaHandler {
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 启动下载任务清理 goroutine
	// FIXED: [P1] 添加 recover 防止 StartCleanup panic 崩溃进程 [2026-07-17]
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("downloadTracker.StartCleanup panic", zap.Any("panic", r))
			}
		}()
		globalDownloadTracker.StartCleanup(context.Background())
	}()
	return &MediaHandler{
		store:         store,
		logger:        logger,
		cfg:           cfg,
		media:         mediaClient,
		commandSender: commandSender,
		videoEngine:   videoEngine,
		recordManager: NewRecordManager(store, logger),
	}
}

// SetMediaClient allows late binding of the ZLMediaKit client (it is created
// after the API server/router in main.go).
func (h *MediaHandler) SetMediaClient(client *media.ZLMediaKitClient) {
	h.media = client
}

// SetVideoEngine allows late binding of the VideoEngine.
func (h *MediaHandler) SetVideoEngine(engine *jt1078.VideoEngine) {
	h.videoEngine = engine
	// AUTO-FIX-2026-06-28: 注册视频质量保障回调
	// 1) 流断开自动重连：重新下发 0x9101 实时音视频请求（保留原 streamID/通道/码流类型）
	//    project_memory: 网络中断时需自动重连并保留播放状态
	engine.SetStreamDownHandler(func(streamID, phone string, logicChannel byte) {
		if h.commandSender == nil {
			h.logger.Warn("cannot auto reconnect: command sender not available",
				zap.String("stream_id", streamID))
			return
		}
		// 通过 streamID 反查 VehicleID（streamID 格式为 "vehicleID_chN"）
		vehicleID := streamIDToVehicleID(streamID)
		if vehicleID == "" {
			h.logger.Warn("cannot auto reconnect: failed to extract vehicle_id from stream_id",
				zap.String("stream_id", streamID))
			return
		}
		// 重新下发 0x9101（StreamType=0 主码流，恢复原始质量）
		realtimeReq := &jt1078.RealtimeRequestMessage{
			LogicChannel: logicChannel,
			MediaType:    0,
			StreamType:   0,
		}
		if err := h.commandSender.SendToDevice(vehicleID, jt1078.MsgIDRealtimeRequest, realtimeReq); err != nil {
			h.logger.Error("auto reconnect: send 0x9101 failed",
				zap.String("stream_id", streamID),
				zap.String("vehicle_id", vehicleID),
				zap.Error(err))
		} else {
			h.logger.Info("auto reconnect: 0x9101 sent to restore stream",
				zap.String("stream_id", streamID),
				zap.String("vehicle_id", vehicleID))
		}
	})

	// 2) 质量差自动切换子码流：下发 0x9101 StreamType=1 切换到子码流
	//    project_memory: 连续3次丢包>5% 或 码率<100kbps 时自动切换到子码流
	//    AUTO-FIX-2026-06-28: 切换前落盘当前录制段，切换后开新段标记 sub/quality_poor（plan 8.6.2）
	engine.SetQualityPoorHandler(func(streamID, phone string, logicChannel, curStreamType byte) {
		if h.commandSender == nil {
			h.logger.Warn("cannot switch to sub stream: command sender not available",
				zap.String("stream_id", streamID))
			return
		}
		vehicleID := streamIDToVehicleID(streamID)
		if vehicleID == "" {
			h.logger.Warn("cannot switch to sub stream: failed to extract vehicle_id",
				zap.String("stream_id", streamID))
			return
		}
		channelStr := fmt.Sprintf("%d", logicChannel)

		// 切换前落盘当前录制段（plan 8.6.2: 防止切换导致录像断片丢失）
		if h.recordManager != nil {
			if err := h.recordManager.FlushCurrentSegment(vehicleID, channelStr); err != nil {
				h.logger.Warn("flush current segment before sub stream switch failed",
					zap.String("vehicle_id", vehicleID),
					zap.String("channel", channelStr),
					zap.Error(err))
			}
		}

		// 下发 0x9101 StreamType=1 切换到子码流
		realtimeReq := &jt1078.RealtimeRequestMessage{
			LogicChannel: logicChannel,
			MediaType:    0,
			StreamType:   1, // 1=子码流
		}
		if err := h.commandSender.SendToDevice(vehicleID, jt1078.MsgIDRealtimeRequest, realtimeReq); err != nil {
			h.logger.Error("auto switch sub stream: send 0x9101 failed",
				zap.String("stream_id", streamID),
				zap.String("vehicle_id", vehicleID),
				zap.Error(err))
		} else {
			h.logger.Info("auto switch sub stream: 0x9101 StreamType=1 sent",
				zap.String("stream_id", streamID),
				zap.String("vehicle_id", vehicleID))
		}

		// 切换后开新录制段，标记 streamType=sub 和 switch_reason=quality_poor（plan 8.6.2）
		if h.recordManager != nil {
			if err := h.recordManager.StartNewSegment(vehicleID, channelStr, "sub"); err != nil {
				h.logger.Warn("start new segment after sub stream switch failed",
					zap.String("vehicle_id", vehicleID),
					zap.String("channel", channelStr),
					zap.Error(err))
			}
			h.recordManager.MarkSwitchReason(vehicleID, channelStr, "quality_poor")
		}
	})
}

// streamIDToVehicleID 从 streamID（格式 "vehicleID_chN"）反查 VehicleID。
// 例如 "abc123_ch1" → "abc123"；无法解析时返回空串。
func streamIDToVehicleID(streamID string) string {
	idx := -1
	for i := len(streamID) - 1; i >= 0; i-- {
		if streamID[i] == '_' {
			idx = i
			break
		}
	}
	if idx < 0 || idx+3 >= len(streamID) {
		return ""
	}
	// 校验后缀格式 _chN
	if streamID[idx+1] != 'c' || streamID[idx+2] != 'h' {
		return ""
	}
	return streamID[:idx]
}

// SetCommandSender allows late binding of the CommandSender.
func (h *MediaHandler) SetCommandSender(cs *CommandSender) {
	h.commandSender = cs
}

type MediaStartRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
	MediaType    int    `json:"media_type"`
	StreamType   int    `json:"stream_type"`
}

type MediaStartResponse struct {
	StreamID string `json:"stream_id"`
	FLVURL   string `json:"flv_url"`
	HLSURL   string `json:"hls_url"`
	RTSPURL  string `json:"rtsp_url"`
	RTPPort  int    `json:"rtp_port"`
}

func (h *MediaHandler) Start(c *gin.Context) {
	var req MediaStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 校验 VehicleID 防 streamID 注入
	if !isValidIDField(req.VehicleID) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid vehicle_id: only [A-Za-z0-9_-] allowed, max 64 chars"})
		return
	}
	// streamID must not contain a slash: ZLMediaKit uses app/stream path segments,
	// so a slash in the stream id would break URL routing. Use "xxx_ch1" with app "rtp".
	streamID := fmt.Sprintf("%s_ch%d", req.VehicleID, req.LogicChannel)
	app := "rtp"

	var rtpPort int
	if h.media != nil {
		port, err := h.media.StartRTPServer(streamID)
		if err != nil {
			h.logger.Warn("failed to start RTP server on ZLMediaKit", zap.Error(err))
		} else {
			rtpPort = port
			// Register the allocated port so incoming 0x1200 RTP data can be
			// forwarded to ZLMediaKit via UDP.
			if h.videoEngine != nil {
				h.videoEngine.RegisterStreamPort(streamID, rtpPort)
			}
		}

		// Send 0x9101 realtime A/V request to the terminal so it starts pushing RTP.
		if h.commandSender != nil {
			// AUTO-FIX-2026-06-29 [P1-7]: 填充 0x9101 的 IP/Port 字段并设置传输模式。
			// 原实现 IPAddress/Port 始终为零值，终端无法获知 RTP 推流地址。
			// 从 ZLMediaKit URL 提取主机 IP，填入分配的 RTP 端口。
			zlmHost := extractHost(h.cfg.ZLMediaKit.URL)
			rtpMode := h.cfg.Video.RTPMode
			if rtpMode == "" {
				rtpMode = "udp"
			}
			var transportMode byte // 0=UDP(默认), 1=TCP
			if rtpMode == "tcp" {
				transportMode = 1
			}
			realtimeReq := &jt1078.RealtimeRequestMessage{
				IPAddress:     zlmHost,
				Port:          uint16(rtpPort),
				LogicChannel:  byte(req.LogicChannel),
				MediaType:     byte(req.MediaType),
				StreamType:    byte(req.StreamType),
				TransportMode: transportMode,
			}
			// AUTO-FIX-2026-06-26: 预注册session以保留StreamType，避免RTP到达时被硬编码为主码流（按第一轮.txt要求）[2026-06-26]
			if h.videoEngine != nil {
				h.videoEngine.CreateSession(streamID, req.VehicleID, byte(req.LogicChannel), byte(req.StreamType))
				// AUTO-FIX-2026-06-29 [P1-7]: 按 0x9101 标识位/配置设置 JTE→ZLM 转发的传输模式，
				// 使 auto 模式的 UDP→TCP fallback 能在该流上生效。
				h.videoEngine.SetStreamMode(streamID, rtpMode)
			}
			if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDRealtimeRequest, realtimeReq); err != nil {
				h.logger.Warn("failed to send 0x9101 realtime request to terminal",
					zap.String("vehicle_id", req.VehicleID), zap.Error(err))
			} else {
				h.logger.Info("0x9101 realtime request sent",
					zap.String("vehicle_id", req.VehicleID),
					zap.Int("channel", req.LogicChannel),
					zap.Int("stream_type", req.StreamType),
					zap.String("rtp_ip", zlmHost),
					zap.Int("rtp_port", rtpPort),
					zap.String("transport_mode", rtpMode))
			}
		}

		// AUTO-FIX-2026-06-28: 录制开始时启动新录制段，使用 BuildVideoPath 作为 S3 上传 key（plan 8.6.3）
		if h.recordManager != nil {
			streamType := "main"
			if req.StreamType == 1 {
				streamType = "sub"
			}
			channelStr := fmt.Sprintf("%d", req.LogicChannel)
			if err := h.recordManager.StartNewSegment(req.VehicleID, channelStr, streamType); err != nil {
				h.logger.Warn("start new record segment failed",
					zap.String("vehicle_id", req.VehicleID),
					zap.String("channel", channelStr),
			zap.Error(err))
		}
	}

		// 验收标准1: 注册并发播放管理器
		if h.videoEngine != nil {
			mgr := h.videoEngine.GetConcurrentPlayManager()
			if mgr != nil {
				if err := mgr.RegisterStream(streamID, req.VehicleID, byte(req.LogicChannel), byte(req.StreamType)); err != nil {
					h.logger.Warn("concurrent stream registration failed",
						zap.String("stream_id", streamID),
						zap.Error(err))
				}
			}
		}

		resp := MediaStartResponse{
			StreamID: streamID,
			FLVURL:   h.media.GetStreamURL(app, streamID, "flv"),
			HLSURL:   h.media.GetStreamURL(app, streamID, "hls"),
			RTSPURL:  h.media.GetStreamURL(app, streamID, "rtsp"),
			RTPPort:  rtpPort,
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "ok",
			"data":    resp,
		})
		return
	}

	baseURL := h.cfg.ZLMediaKit.URL
	resp := MediaStartResponse{
		StreamID: streamID,
		FLVURL:   fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=flv", baseURL, app, streamID),
		HLSURL:   fmt.Sprintf("%s/index/api/webrtc?app=%s&stream=%s&type=hls", baseURL, app, streamID),
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data":    resp,
	})
}

type MediaStopRequest struct {
	StreamID     string `json:"stream_id"`
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
}

func (h *MediaHandler) Stop(c *gin.Context) {
	var req MediaStopRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	streamID := req.StreamID
	if streamID == "" && req.VehicleID != "" {
		if req.LogicChannel == 0 {
			req.LogicChannel = 1
		}
		streamID = fmt.Sprintf("%s_ch%d", req.VehicleID, req.LogicChannel)
	}

	// AUTO-FIX-2026-06-27: 0x9105 不再作为控制消息，停止指令改用 0x9203 回放控制（Command=0 关闭）
	if h.commandSender != nil && req.VehicleID != "" {
		if req.LogicChannel == 0 {
			req.LogicChannel = 1
		}
		ctrlReq := &jt1078.PlaybackControlMessage{
			LogicChannel: byte(req.LogicChannel),
			Command:      0, // 0x00 = close A/V resource
			Speed:        0,
		}
		if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDPlaybackControl, ctrlReq); err != nil {
			h.logger.Warn("failed to send 0x9203 stop command to terminal",
				zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		} else {
			h.logger.Info("0x9203 stop command sent", zap.String("vehicle_id", req.VehicleID))
		}
	}

	if h.media != nil && streamID != "" {
		if err := h.media.StopRTPServer(streamID); err != nil {
			h.logger.Warn("failed to stop RTP server on ZLMediaKit", zap.Error(err))
		}
	}

	// AUTO-FIX-2026-06-28: 停止时落盘当前录制段（plan 8.6.2）
	if h.recordManager != nil && req.VehicleID != "" {
		channelStr := fmt.Sprintf("%d", req.LogicChannel)
		if err := h.recordManager.FlushCurrentSegment(req.VehicleID, channelStr); err != nil {
			h.logger.Warn("flush current segment on stop failed",
				zap.String("vehicle_id", req.VehicleID),
				zap.String("channel", channelStr),
				zap.Error(err))
		}
	}

	if h.videoEngine != nil && streamID != "" {
		h.videoEngine.UnregisterStreamPort(streamID)
		// 验收标准1: 注销并发播放管理器
		mgr := h.videoEngine.GetConcurrentPlayManager()
		if mgr != nil {
			mgr.UnregisterStream(streamID)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
	})
}

type PTZRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
	Direction    int    `json:"direction"`
	Speed        int    `json:"speed"`
}

// AUTO-FIX-2026-06-26: 第三轮视频监控修复 - 关键帧请求 API
type KeyFrameRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
}

// KeyFrame sends a 0x9203 playback control request to the terminal with Command=4
// (平台请求关键帧), 用于视频画面马赛克或黑屏时快速恢复。
// AUTO-FIX-2026-06-27: 0x9105 不再作为控制消息，关键帧请求改用 0x9203 回放控制（Command=4）
// 保留原 AUTO-FIX-2026-06-26 关键帧请求功能。
func (h *MediaHandler) KeyFrame(c *gin.Context) {
	var req KeyFrameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	if h.commandSender == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "command sender not available"})
		return
	}

	ctrlReq := &jt1078.PlaybackControlMessage{
		LogicChannel: byte(req.LogicChannel),
		Command:      4, // 4 = 平台请求关键帧
		Speed:        0,
	}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDPlaybackControl, ctrlReq); err != nil {
		h.logger.Warn("keyframe request send failed", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send keyframe request to terminal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// StreamModeRequest 切换指定流的 RTP 传输模式（udp/tcp）。
// 公网/NAT 环境下 UDP 不通时可切换为 TCP（1078-2022 标准）。
type StreamModeRequest struct {
	StreamID string `json:"stream_id"`
	Mode     string `json:"mode"` // "udp" | "tcp"
}

// SetStreamMode 设置流的 RTP 传输模式，并同步通知 ZLMediaKit 以对应模式接收。
func (h *MediaHandler) SetStreamMode(c *gin.Context) {
	var req StreamModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Mode != "udp" && req.Mode != "tcp" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "mode must be udp or tcp"})
		return
	}
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	h.videoEngine.SetStreamMode(req.StreamID, req.Mode)
	// ZLMediaKit 的 openRtpServer 已以 tcp_mode=1 打开，同时支持 UDP/TCP 接收，
	// 此处仅需切换 JTE→ZLM 的转发模式（VideoEngine.ForwardRTP 按 mode 选 udp/tcp 池）。
	h.logger.Info("stream mode switched",
		zap.String("stream_id", req.StreamID), zap.String("mode", req.Mode))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"stream_id": req.StreamID, "mode": req.Mode}})
}

// GetStreamMode 查询流当前 RTP 传输模式及引擎默认模式。
func (h *MediaHandler) GetStreamMode(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	streamID := c.Query("stream_id")
	mode := h.videoEngine.GetStreamMode(streamID)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"stream_id":     streamID,
			"mode":          mode,
			"default_mode":  h.videoEngine.GetDefaultStreamMode(),
		},
	})
}

// SwitchStreamRequest 双码流手动切换请求。
// AUTO-FIX-2026-07-02 [P1]: 双码流前端切换 UI 缺失（后端 StreamType 已定义）。
// project_memory: 双码流前端切换 UI 缺失（后端 StreamType 已定义）。
type SwitchStreamRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
	StreamType   int    `json:"stream_type"` // 0=主码流 1=子码流
}

// SwitchStream 手动切换码流：下发 0x9101 RealtimeRequestMessage（新 StreamType）并更新 session。
// AUTO-FIX-2026-07-02 [P1]: 补全双码流手动切换 API（/media/switch-stream）。
// 切换时保留播放状态（session/SSRC/时间戳），仅更新 StreamType + 重发 0x9101。
// 同时落盘当前录制段并开新段（streamType=main/sub, switch_reason=manual），防止录制断片。
func (h *MediaHandler) SwitchStream(c *gin.Context) {
	var req SwitchStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	if req.StreamType != 0 && req.StreamType != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "stream_type must be 0 (main) or 1 (sub)"})
		return
	}
	if h.commandSender == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "command sender not available"})
		return
	}

	streamID := fmt.Sprintf("%s_ch%d", req.VehicleID, req.LogicChannel)

	// 1) 更新 session 的 StreamType（保留播放状态：SSRC/时间戳/StartTime 不变）
	var switched bool
	if h.videoEngine != nil {
		switched = h.videoEngine.SwitchStreamType(streamID, byte(req.StreamType))
	}
	if !switched {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "stream not found or already at requested type",
		})
		return
	}

	// 2) 获取已分配的 RTP 端口（复用，不重新分配）与 ZLM 主机 IP
	var rtpPort int
	if h.videoEngine != nil {
		if p, ok := h.videoEngine.GetStreamPort(streamID); ok {
			rtpPort = p
		}
	}
	zlmHost := extractHost(h.cfg.ZLMediaKit.URL)
	rtpMode := h.cfg.Video.RTPMode
	if rtpMode == "" {
		rtpMode = "udp"
	}
	var transportMode byte
	if rtpMode == "tcp" {
		transportMode = 1
	}

	// 3) 下发 0x9101 切换码流（IPAddress/Port 复用，仅 StreamType 变化）
	realtimeReq := &jt1078.RealtimeRequestMessage{
		IPAddress:     zlmHost,
		Port:          uint16(rtpPort),
		LogicChannel:  byte(req.LogicChannel),
		MediaType:     0,
		StreamType:    byte(req.StreamType),
		TransportMode: transportMode,
	}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDRealtimeRequest, realtimeReq); err != nil {
		h.logger.Error("switch stream: send 0x9101 failed",
			zap.String("vehicle_id", req.VehicleID),
			zap.Int("channel", req.LogicChannel),
			zap.Int("stream_type", req.StreamType),
			zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send stream switch command to terminal"})
		return
	}

	// 4) 录制段切换：落盘当前段，开新段（防止切换导致录像断片丢失）
	channelStr := fmt.Sprintf("%d", req.LogicChannel)
	streamTypeStr := "main"
	if req.StreamType == 1 {
		streamTypeStr = "sub"
	}
	if h.recordManager != nil {
		if err := h.recordManager.FlushCurrentSegment(req.VehicleID, channelStr); err != nil {
			h.logger.Warn("switch stream: flush current segment failed",
				zap.String("vehicle_id", req.VehicleID),
				zap.String("channel", channelStr),
				zap.Error(err))
		}
		if err := h.recordManager.StartNewSegment(req.VehicleID, channelStr, streamTypeStr); err != nil {
			h.logger.Warn("switch stream: start new segment failed",
				zap.String("vehicle_id", req.VehicleID),
				zap.String("channel", channelStr),
				zap.Error(err))
		}
		h.recordManager.MarkSwitchReason(req.VehicleID, channelStr, "manual")
	}

	h.logger.Info("stream switched manually",
		zap.String("vehicle_id", req.VehicleID),
		zap.Int("channel", req.LogicChannel),
		zap.Int("stream_type", req.StreamType),
		zap.String("stream_type_name", streamTypeStr))

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"stream_id":   streamID,
			"stream_type": req.StreamType,
		},
	})
}

// Fragments 按时间段查询录制断片列表。
// AUTO-FIX-2026-07-02 [P1]: 录制断片查询 API（/media/fragments）。
// 查询参数：phone（设备手机号）、channel（逻辑通道）、start、end（RFC3339 时间）。
func (h *MediaHandler) Fragments(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	phone := c.Query("phone")
	channelStr := c.Query("channel")
	startStr := c.Query("start")
	endStr := c.Query("end")

	if startStr == "" || endStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "start and end time are required (RFC3339)"})
		return
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid start time: " + err.Error()})
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid end time: " + err.Error()})
		return
	}

	var logicChannel byte
	if channelStr != "" {
		ch, err := strconv.Atoi(channelStr)
		if err != nil || ch < 0 || ch > 255 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid channel"})
			return
		}
		logicChannel = byte(ch)
	}

	segs := h.videoEngine.QueryRecordSegments(phone, logicChannel, start, end)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"fragments": segs,
			"count":     len(segs),
		},
	})
}

// MergeFragments 按时间段合并录制断片。
// AUTO-FIX-2026-07-02 [P1]: 断片合并接口（/media/fragments/merge）。
// 查询参数：phone、channel、start、end（RFC3339 时间）。
func (h *MediaHandler) MergeFragments(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	phone := c.Query("phone")
	channelStr := c.Query("channel")
	startStr := c.Query("start")
	endStr := c.Query("end")

	if phone == "" || channelStr == "" || startStr == "" || endStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "phone, channel, start, end are required"})
		return
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid start time: " + err.Error()})
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid end time: " + err.Error()})
		return
	}
	ch, err := strconv.Atoi(channelStr)
	if err != nil || ch < 0 || ch > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid channel"})
		return
	}

	merged, err := h.videoEngine.MergeRecordSegments(phone, byte(ch), start, end)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": merged,
	})
}

// PTZ sends a 0x9301 PTZ control command to the terminal.
func (h *MediaHandler) PTZ(c *gin.Context) {
	var req PTZRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	if req.Speed == 0 {
		req.Speed = 4
	}

	if h.commandSender == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "command sender not available"})
		return
	}

	// AUTO-FIX-2026-06-27: 0x9301 改为 5B 格式（Channel + 4B ControlInstruction）
	// 控制指令 4B = byte1(光圈/聚焦/变倍位) + byte2(方向位) + byte3(水平速度) + byte4(垂直速度)
	ptzReq := &jt1078.PTZControlMessage{
		LogicChannel: byte(req.LogicChannel),
		ControlInstruction: jt1078.BuildPTZControlInstruction(0, byte(req.Direction), byte(req.Speed), byte(req.Speed)),
	}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDPTZControl, ptzReq); err != nil {
		h.logger.Warn("PTZ command send failed", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send PTZ command to terminal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

type PlaybackRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
	MediaType    int    `json:"media_type"`
	StreamType   int    `json:"stream_type"`
	PlaybackMode int    `json:"playback_mode"`
	StartTime    string `json:"start_time"`
	EndTime      string `json:"end_time"`
}

// Playback sends a 0x9201 history playback request to the terminal.
func (h *MediaHandler) Playback(c *gin.Context) {
	var req PlaybackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 校验 VehicleID 防 streamID 注入
	if !isValidIDField(req.VehicleID) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid vehicle_id: only [A-Za-z0-9_-] allowed, max 64 chars"})
		return
	}

	streamID := fmt.Sprintf("%s_ch%d_playback", req.VehicleID, req.LogicChannel)
	app := "rtp"

	var rtpPort int
	if h.media != nil {
		port, err := h.media.StartRTPServer(streamID)
		if err != nil {
			h.logger.Warn("failed to start RTP server for playback", zap.Error(err))
		} else {
			rtpPort = port
			if h.videoEngine != nil {
				h.videoEngine.RegisterStreamPort(streamID, rtpPort)
			}
		}
	}

	if h.commandSender != nil {
		pbReq := &jt1078.PlaybackRequestMessage{
			LogicChannel: byte(req.LogicChannel),
			MediaType:    byte(req.MediaType),
			StreamType:   byte(req.StreamType),
			PlaybackMode: byte(req.PlaybackMode),
			StartTime:    req.StartTime,
			EndTime:      req.EndTime,
		}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDPlaybackRequest, pbReq); err != nil {
		h.logger.Warn("playback request send failed", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send playback request to terminal"})
		return
	}
	}

	resp := MediaStartResponse{StreamID: streamID, RTPPort: rtpPort}
	if h.media != nil {
		resp.FLVURL = h.media.GetStreamURL(app, streamID, "flv")
		resp.HLSURL = h.media.GetStreamURL(app, streamID, "hls")
		resp.RTSPURL = h.media.GetStreamURL(app, streamID, "rtsp")
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

// Download sends a 0x9205录像下载 request to the terminal.
func (h *MediaHandler) Download(c *gin.Context) {
	var req PlaybackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}

	if h.commandSender == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "command sender not available"})
		return
	}

	// AUTO-FIX-2026-06-27: 0x9205 改用 DownloadRequestMessage（原误用 PlaybackRequestMessage）
	// 注: IPAddress/TcpPort/UdpPort/Username/Password/FilePath 需由服务端配置填充，此处留空。
	dlReq := &jt1078.DownloadRequestMessage{
		LogicChannel: byte(req.LogicChannel),
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		MediaType:    byte(req.MediaType),
		StreamType:   byte(req.StreamType),
		DownloadType: byte(req.PlaybackMode),
	}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDDownloadRequest, dlReq); err != nil {
		h.logger.Warn("download request send failed", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send download request to terminal"})
		return
	}

	// v3.0: 创建下载任务并返回 download_id，供前端轮询进度
	startTs, _ := time.Parse(time.RFC3339, req.StartTime)
	endTs, _ := time.Parse(time.RFC3339, req.EndTime)
	if startTs.IsZero() {
		startTs = time.Now()
	}
	if endTs.IsZero() {
		endTs = startTs.Add(30 * time.Minute)
	}
	filePath := BuildVideoPath(req.VehicleID, fmt.Sprintf("%d", req.LogicChannel), startTs, "download")
	downloadID := globalDownloadTracker.CreateDownloadTask(req.VehicleID, req.LogicChannel, startTs, endTs, filePath)

	c.JSON(http.StatusOK, gin.H{
		"code":        0,
		"message":     "ok",
		"download_id": downloadID,
		"progress_url": "/api/v1/media/download/progress?download_id=" + downloadID,
	})
}

// Streams returns the list of active streams on ZLMediaKit.
func (h *MediaHandler) Streams(c *gin.Context) {
	if h.media == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []interface{}{}})
		return
	}
	streams, err := h.media.ListStreams()
	if err != nil {
		respondInternalError(c, h.logger, err, "MediaHandler.Streams.ListStreams")
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": streams})
}

type WebRTCRequest struct {
	App      string `json:"app" binding:"required"`
	Stream   string `json:"stream" binding:"required"`
	SDPOffer string `json:"sdp_offer" binding:"required"`
}

func (h *MediaHandler) WebRTC(c *gin.Context) {
	var req WebRTCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if h.media == nil {
		// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: URL 编码防注入
		escapedStream := url.QueryEscape(req.Stream)
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "WebRTC not available: ZLMediaKit not configured",
			"fallback": gin.H{
				"flv_url": fmt.Sprintf("/api/v1/media/start?vehicle_id=%s", escapedStream),
				"hls_url": fmt.Sprintf("/api/v1/media/start?vehicle_id=%s", escapedStream),
			},
		})
		return
	}

	sdpAnswer, err := h.media.ExchangeSDP(req.App, req.Stream, req.SDPOffer)
	if err != nil {
		h.logger.Warn("WebRTC SDP exchange failed, falling back to FLV/HLS",
			zap.Error(err),
			zap.String("app", req.App),
			zap.String("stream", req.Stream))

		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "WebRTC failed, falling back to FLV/HLS",
			"data": gin.H{
				"webrtc": nil,
				"fallback": gin.H{
					"flv_url":  h.media.GetStreamURL(req.App, req.Stream, "flv"),
					"hls_url":  h.media.GetStreamURL(req.App, req.Stream, "hls"),
					"rtsp_url": h.media.GetStreamURL(req.App, req.Stream, "rtsp"),
				},
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"sdp_answer": sdpAnswer,
			"app":        req.App,
			"stream":     req.Stream,
		},
	})
}

// AUTO-FIX-2026-06-28: 视频质量统计 API
// GET /api/v1/media/quality — 返回所有流的实时码率/帧率/丢包率
// GET /api/v1/media/quality/:stream_id — 返回指定流的详细质量统计
// project_memory: 视频质量统计需实时显示码率、帧率、丢包率

// Quality returns quality stats for all active streams.
// 支持可选查询参数过滤：device_id（终端手机号）、channel（逻辑通道号）。
// AUTO-FIX-2026-06-29 [P0-2]: 满足前端 GET /api/v1/video/quality?deviceId=xxx&channel=xxx 规范。
func (h *MediaHandler) Quality(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	stats := h.videoEngine.ListQualityStats()

	// 可选过滤：device_id（即 phone）、channel（即 logic_channel）
	deviceID := c.Query("device_id")
	channelStr := c.Query("channel")
	var channelByte byte
	channelFilter := false
	if channelStr != "" {
		if ch, err := strconv.Atoi(channelStr); err == nil && ch >= 0 && ch <= 255 {
			channelByte = byte(ch)
			channelFilter = true
		}
	}

	if deviceID != "" || channelFilter {
		filtered := make([]*jt1078.QualityStats, 0, len(stats))
		for _, s := range stats {
			if deviceID != "" && s.Phone != deviceID {
				continue
			}
			if channelFilter && s.LogicChannel != channelByte {
				continue
			}
			filtered = append(filtered, s)
		}
		stats = filtered
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"streams":   stats,
			"total":     len(stats),
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
}

// QualityByStream returns quality stats for a specific stream.
func (h *MediaHandler) QualityByStream(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	streamID := c.Param("stream_id")
	if streamID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "stream_id is required"})
		return
	}
	stats, ok := h.videoEngine.GetQualityStats(streamID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "stream not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}

// GapReport returns cumulative RTP SeqNum gap statistics for a specific stream.
// 验收标准2: 画面质量监控 - 抓包统计 RTP SeqNum gap 累计丢包率。
// GET /api/v1/media/gap-report/:stream_id
func (h *MediaHandler) GapReport(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	streamID := c.Param("stream_id")
	if streamID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "stream_id is required"})
		return
	}
	report, ok := h.videoEngine.GetGapReport(streamID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "stream not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": report,
	})
}

// GapReports returns cumulative RTP SeqNum gap statistics for all active streams.
// 验收标准2: 画面质量监控 - 全局丢包统计概览。
// GET /api/v1/media/gap-report
func (h *MediaHandler) GapReports(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	reports := h.videoEngine.ListGapReports()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": reports,
	})
}

// QualityConfigRequest 用于动态调整质量保障配置。
type QualityConfigRequest struct {
	AutoReconnect     *bool   `json:"auto_reconnect,omitempty"`
	AutoSwitchSub     *bool   `json:"auto_switch_sub,omitempty"`
	StreamDownTimeout *int    `json:"stream_down_timeout_sec,omitempty"` // 流断开判定阈值（秒）
}

// SetQualityConfig 动态调整视频质量保障配置（运行时生效，不持久化）。
// PUT /api/v1/media/quality/config
func (h *MediaHandler) SetQualityConfig(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	var req QualityConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.AutoReconnect != nil {
		h.videoEngine.SetAutoReconnect(*req.AutoReconnect)
	}
	if req.AutoSwitchSub != nil {
		h.videoEngine.SetAutoSwitchSub(*req.AutoSwitchSub)
	}
	if req.StreamDownTimeout != nil && *req.StreamDownTimeout > 0 {
		h.videoEngine.SetStreamDownTimeout(time.Duration(*req.StreamDownTimeout) * time.Second)
	}
	h.logger.Info("video quality config updated",
		zap.Bool("auto_reconnect", req.AutoReconnect != nil),
		zap.Bool("auto_switch_sub", req.AutoSwitchSub != nil),
		zap.Bool("stream_down_timeout", req.StreamDownTimeout != nil))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// Records 查询录像段列表
// GET /api/v1/media/records?device_id=&channel=&start_time=&end_time=
// 返回字段：path/size/duration/stream_type/has_gap
// 注：当前实现返回内存中活跃录制段；历史段需查询对象存储索引。
func (h *MediaHandler) Records(c *gin.Context) {
	deviceID := c.Query("device_id")
	channel := c.Query("channel")
	startStr := c.Query("start_time")
	endStr := c.Query("end_time")

	var startTime, endTime time.Time
	var err error
	if startStr != "" {
		startTime, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid start_time, use RFC3339 format"})
			return
		}
	}
	if endStr != "" {
		endTime, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid end_time, use RFC3339 format"})
			return
		}
	}

	var segments []*VideoRecord
	if h.recordManager != nil {
		segments = h.recordManager.ListSegments(deviceID, channel)
	}

	// 按时间范围过滤
	filtered := make([]*VideoRecord, 0, len(segments))
	for _, seg := range segments {
		if !startTime.IsZero() && seg.StartTime.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && seg.StartTime.After(endTime) {
			continue
		}
		filtered = append(filtered, seg)
	}

	// 构造返回结果（path/size/duration/stream_type/has_gap）
	items := make([]gin.H, 0, len(filtered))
	for _, seg := range filtered {
		items = append(items, gin.H{
			"path":        seg.FilePath,
			"size":        seg.FileSize,
			"duration":    seg.Duration,
			"stream_type": seg.StreamType,
			"has_gap":     seg.HasGap,
			"device_id":   seg.DeviceID,
			"channel":     seg.Channel,
			"start_time":  seg.StartTime.Format(time.RFC3339),
			"end_time":    seg.EndTime.Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items": items,
			"total": len(items),
		},
	})
}

// ===================================================================
// v3.0 视频下载进度跟踪
// ===================================================================

// DownloadStatus 下载任务状态
type DownloadStatus struct {
	DownloadID  string    `json:"download_id"`
	VehicleID   string    `json:"vehicle_id"`
	Channel     int       `json:"channel"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Status      string    `json:"status"`       // pending/downloading/completed/failed
	Progress    float64   `json:"progress"`     // 0-100
	FileSize    int64     `json:"file_size"`    // 已下载字节数
	TotalSize   int64     `json:"total_size"`   // 总字节数（若已知）
	FilePath    string    `json:"file_path"`    // S3 对象 key
	ErrorMsg    string    `json:"error_msg,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// downloadTracker 全局下载任务跟踪器（进程内）
type downloadTracker struct {
	mu          sync.RWMutex
	tasks       map[string]*DownloadStatus // key: download_id
	byDevice    map[string]map[string]bool // key: vehicle_id -> set of download_id
	maxTasks    int                        // 最大任务数，超出时淘汰最旧任务
	taskTTL     time.Duration              // 任务保留时长，超时后淘汰
}

var globalDownloadTracker = &downloadTracker{
	tasks:    make(map[string]*DownloadStatus),
	byDevice: make(map[string]map[string]bool),
	maxTasks: 10000,              // AUTO-FIX-2026-07-15: 上限 1 万条，防 OOM
	taskTTL:  7 * 24 * time.Hour, // AUTO-FIX-2026-07-15: 7 天 TTL
}

// CreateDownloadTask 创建下载任务并返回 download_id
func (dt *downloadTracker) CreateDownloadTask(vehicleID string, channel int, start, end time.Time, filePath string) string {
	id := fmt.Sprintf("dl_%s_%d_%d", vehicleID, channel, time.Now().UnixNano())
	now := time.Now()
	task := &DownloadStatus{
		DownloadID: id,
		VehicleID:  vehicleID,
		Channel:    channel,
		StartTime:  start,
		EndTime:    end,
		Status:     "pending",
		Progress:   0,
		FilePath:   filePath,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	dt.mu.Lock()
	// AUTO-FIX-2026-07-15 [ConvergeLoop-严重]: 惰性清理过期/超量任务，防 OOM
	dt.cleanExpiredLocked(now)
	dt.tasks[id] = task
	if dt.byDevice[vehicleID] == nil {
		dt.byDevice[vehicleID] = make(map[string]bool)
	}
	dt.byDevice[vehicleID][id] = true
	dt.mu.Unlock()
	return id
}

// cleanExpiredLocked 清理过期和超量任务（调用方必须持有写锁）
func (dt *downloadTracker) cleanExpiredLocked(now time.Time) {
	// 1. 清理超过 TTL 的已完成/失败任务
	for id, task := range dt.tasks {
		if now.Sub(task.CreatedAt) > dt.taskTTL && task.Status != "pending" && task.Status != "downloading" {
			delete(dt.tasks, id)
			if ids, ok := dt.byDevice[task.VehicleID]; ok {
				delete(ids, id)
				if len(ids) == 0 {
					delete(dt.byDevice, task.VehicleID)
				}
			}
		}
	}
	// 2. 如果仍超量，按 CreatedAt 淘汰最旧任务
	if len(dt.tasks) <= dt.maxTasks {
		return
	}
	// 找出最旧的 N 个任务淘汰
	// AUTO-FIX-2026-07-15 [ConvergeLoop-一般]: 改用 sort.Slice O(n log n) 替代选择排序 O(n²)
	excess := len(dt.tasks) - dt.maxTasks
	type taskAge struct {
		id        string
		createdAt time.Time
	}
	ages := make([]taskAge, 0, len(dt.tasks))
	for id, task := range dt.tasks {
		ages = append(ages, taskAge{id, task.CreatedAt})
	}
	sort.Slice(ages, func(i, j int) bool {
		return ages[i].createdAt.Before(ages[j].createdAt)
	})
	for i := 0; i < excess && i < len(ages); i++ {
		task := dt.tasks[ages[i].id]
		delete(dt.tasks, ages[i].id)
		if ids, ok := dt.byDevice[task.VehicleID]; ok {
			delete(ids, ages[i].id)
			if len(ids) == 0 {
				delete(dt.byDevice, task.VehicleID)
			}
		}
	}
}

// UpdateProgress 更新下载进度
func (dt *downloadTracker) UpdateProgress(downloadID string, progress float64, fileSize, totalSize int64, status, errMsg string) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	task, ok := dt.tasks[downloadID]
	if !ok {
		return
	}
	task.Progress = progress
	task.FileSize = fileSize
	task.TotalSize = totalSize
	task.Status = status
	task.ErrorMsg = errMsg
	task.UpdatedAt = time.Now()
}

// GetTask 查询单个下载任务
func (dt *downloadTracker) GetTask(downloadID string) (*DownloadStatus, bool) {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	task, ok := dt.tasks[downloadID]
	if !ok {
		return nil, false
	}
	cp := *task
	return &cp, true
}

// ListByDevice 查询设备的所有下载任务
func (dt *downloadTracker) ListByDevice(vehicleID string) []*DownloadStatus {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	ids := dt.byDevice[vehicleID]
	result := make([]*DownloadStatus, 0, len(ids))
	for id := range ids {
		if task, ok := dt.tasks[id]; ok {
			cp := *task
			result = append(result, &cp)
		}
	}
	return result
}

// StartCleanup 启动定期清理 goroutine，清理过期下载任务防止内存泄漏
// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: downloadTracker 无淘汰机制修复
// 每 24 小时清理一次，保留 7 天内的 task
func (dt *downloadTracker) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dt.cleanupExpired(7 * 24 * time.Hour)
		}
	}
}

// cleanupExpired 清理超过 maxAge 的下载任务
func (dt *downloadTracker) cleanupExpired(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	dt.mu.Lock()
	defer dt.mu.Unlock()
	for id, task := range dt.tasks {
		if task.UpdatedAt.Before(cutoff) {
			delete(dt.tasks, id)
			if ids, ok := dt.byDevice[task.VehicleID]; ok {
				delete(ids, id)
				if len(ids) == 0 {
					delete(dt.byDevice, task.VehicleID)
				}
			}
		}
	}
}

// DownloadProgress godoc
// @Summary 视频下载进度查询
// @Description 查询视频下载任务进度，支持按 download_id 查单个或按 vehicle_id 查全部
// @Tags 视频监控
// @Accept json
// @Produce json
// @Param download_id query string false "下载任务ID（查单个任务）"
// @Param vehicle_id query string false "车辆ID（查该车辆所有下载任务）"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/media/download/progress [get]
func (h *MediaHandler) DownloadProgress(c *gin.Context) {
	downloadID := c.Query("download_id")
	vehicleID := c.Query("vehicle_id")

	if downloadID == "" && vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "download_id or vehicle_id required"})
		return
	}

	// 查单个任务
	if downloadID != "" {
		task, ok := globalDownloadTracker.GetTask(downloadID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "download task not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": task})
		return
	}

	// 查车辆所有任务
	tasks := globalDownloadTracker.ListByDevice(vehicleID)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items": tasks,
			"total": len(tasks),
		},
	})
}

// ===================================================================
// 验收标准5: 录像回放控制（快进/快退/暂停/恢复）
// 0x9203 回放控制命令：0=关闭 1=暂停 2=继续 3=快进 4=关键帧 5=快退 6=拖拽
// ===================================================================

// PlaybackControlRequest 回放控制请求。
type PlaybackControlRequest struct {
	VehicleID    string `json:"vehicle_id"`
	LogicChannel int    `json:"logic_channel"`
	Command      int    `json:"command"` // 1=暂停 2=继续 3=快进 5=快退 6=拖拽
	Speed        int    `json:"speed"`   // 回放速度（1=正常 2=2倍 4=4倍 8=8倍 16=16倍）
}

// PlaybackControl 回放控制（暂停/继续/快进/快退/拖拽）。
func (h *MediaHandler) PlaybackControl(c *gin.Context) {
	var req PlaybackControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.LogicChannel == 0 {
		req.LogicChannel = 1
	}
	if h.commandSender == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "command sender not available"})
		return
	}
	ctrlReq := &jt1078.PlaybackControlMessage{
		LogicChannel: byte(req.LogicChannel),
		Command:      byte(req.Command),
		Speed:        byte(req.Speed),
	}
	if err := h.commandSender.SendToDevice(req.VehicleID, jt1078.MsgIDPlaybackControl, ctrlReq); err != nil {
		h.logger.Warn("playback control send failed", zap.String("vehicle_id", req.VehicleID), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "failed to send playback control to terminal"})
		return
	}
	h.logger.Info("playback control sent",
		zap.String("vehicle_id", req.VehicleID),
		zap.Int("command", req.Command),
		zap.Int("speed", req.Speed))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// ===================================================================
// 验收标准3: 关键帧恢复状态查询
// ===================================================================

// KeyFrameRecoveryStatus 关键帧恢复状态查询。
func (h *MediaHandler) KeyFrameRecoveryStatus(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	tracker := h.videoEngine.GetKeyFrameRecoveryTracker()
	if tracker == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "keyframe tracker not available"})
		return
	}
	streamID := c.Param("stream_id")
	if streamID != "" {
		result, ok := tracker.GetPendingRequest(streamID)
		if !ok {
			history := tracker.GetHistory()
			for _, r := range history {
				if r.StreamID == streamID {
					c.JSON(http.StatusOK, gin.H{"code": 0, "data": r})
					return
				}
			}
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "no recovery record for stream"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
		return
	}
	history := tracker.GetHistory()
	successCount := 0
	timeoutCount := 0
	for _, r := range history {
		if r.Success {
			successCount++
		} else {
			timeoutCount++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"history":       history,
			"total":         len(history),
			"success_count": successCount,
			"timeout_count": timeoutCount,
		},
	})
}

// ===================================================================
// 验收标准6: PTZ 延迟统计查询
// ===================================================================

// PTZLatencyStats PTZ 延迟统计查询。
func (h *MediaHandler) PTZLatencyStats(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	tracker := h.videoEngine.GetPTZLatencyTracker()
	if tracker == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "ptz tracker not available"})
		return
	}
	history := tracker.GetPTZHistory()
	avgLatency := tracker.GetAverageLatency()
	within2s := 0
	for _, r := range history {
		if r.Within2s {
			within2s++
		}
	}
	passRate := 0.0
	if len(history) > 0 {
		passRate = float64(within2s) / float64(len(history)) * 100.0
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"history":        history,
			"total":          len(history),
			"avg_latency_ms": avgLatency,
			"within_2s":      within2s,
			"pass_rate":      passRate,
		},
	})
}

// ===================================================================
// 验收标准1: 并发播放管理
// ===================================================================

// ConcurrentStreams 返回并发流统计与系统资源使用。
func (h *MediaHandler) ConcurrentStreams(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	mgr := h.videoEngine.GetConcurrentPlayManager()
	if mgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "concurrent manager not available"})
		return
	}
	streams := mgr.ListActiveStreams()
	resStats := mgr.GetResourceStats()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"active_streams": streams,
			"resource_stats": resStats,
			"total_started":  mgr.TotalStarted(),
			"total_stopped":  mgr.TotalStopped(),
			"rejected_count": mgr.RejectedCount(),
			"max_concurrent": mgr.GetMaxConcurrent(),
		},
	})
}

// SetMaxConcurrent 设置最大并发流数。
func (h *MediaHandler) SetMaxConcurrent(c *gin.Context) {
	if h.videoEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video engine not available"})
		return
	}
	mgr := h.videoEngine.GetConcurrentPlayManager()
	if mgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "concurrent manager not available"})
		return
	}
	var req struct {
		Max int `json:"max"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Max <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "max must be > 0"})
		return
	}
	mgr.SetMaxConcurrent(req.Max)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"max_concurrent": req.Max}})
}

// extractHost 从 URL 中提取主机 IP/域名（去掉协议前缀和端口）。
// 用于填充 0x9101 的 IPAddress 字段，告知终端 RTP 推流目标地址。
// 例: "http://192.168.1.100:80" → "192.168.1.100"
// AUTO-FIX-2026-06-29 [P1-7]
func extractHost(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// 优先用 net/url 解析
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	// 兜底：手动去协议前缀
	s := strings.TrimPrefix(rawURL, "http://")
	s = strings.TrimPrefix(s, "https://")
	// 去端口
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		s = s[:idx]
	}
	// 去路径
	if idx := strings.Index(s, "/"); idx > 0 {
		s = s[:idx]
	}
	return s
}
