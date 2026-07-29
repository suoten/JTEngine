package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/internal/registry"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type SystemHandler struct {
	store    storage.Interface
	sessions *gateway.SessionManager
	registry *registry.FeatureRegistry
	logger   *zap.Logger
}

func NewSystemHandler(store storage.Interface, sessions *gateway.SessionManager, reg *registry.FeatureRegistry, logger *zap.Logger) *SystemHandler {
	return &SystemHandler{store: store, sessions: sessions, registry: reg, logger: logger}
}

// Status godoc
// @Summary è·åç³»ç»ç¶æ?
// @Description è·åå¨çº¿è®¾å¤æ°ãä¼è¯æ°ãå·²å¯ç¨åè½ç­ç³»ç»ç¶æ?
// @Tags ç³»ç»
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/system/status [get]
func (h *SystemHandler) Status(c *gin.Context) {
	onlineCount, _ := h.store.GetOnlineCount(context.Background())

	features := h.registry.ListFeatures()
	featureNames := make([]string, 0, len(features))
	for _, f := range features {
		featureNames = append(featureNames, string(f))
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"online_devices": onlineCount,
			"total_sessions": len(h.sessions.List()),
			"features":       featureNames,
			"version":        "1.0.0",
		},
	})
}

// Modules godoc
// @Summary è·åæ¨¡ååè¡¨
// @Description è·åææåè½æ¨¡ååå¶å¯ç¨ç¶æ?
// @Tags ç³»ç»
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/system/modules [get]
func (h *SystemHandler) Modules(c *gin.Context) {
	_ = h.registry.ListFeatures()
	type moduleInfo struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Category    string `json:"category"` // core=核心免费 / licensed=授权模块 / infrastructure=基础设施
		Description string `json:"description"`
		Enabled     bool   `json:"enabled"`
	}

	// FIXED-2026-07-24: 模块分类+中文名+描述，让用户一目了然哪些免费、哪些需授权
	type moduleMeta struct {
		Display     string
		Category    string
		Description string
	}
	moduleMap := map[string]moduleMeta{
		"jt808":          {"JT/T 808 终端通信", "core", "JT/T 808 终端通信协议，车辆基础通信，开源版免费"},
		"jt1078":         {"JT/T 1078 音视频", "core", "JT/T 1078 音视频传输，实时视频监控，开源版免费"},
		"http_api":       {"HTTP API 接口", "core", "RESTful API 接口，前后端数据交互，开源版免费"},
		"websocket":      {"WebSocket 实时推送", "core", "WebSocket 实时推送，报警/事件订阅，开源版免费"},
		"memory_store":   {"内存存储", "core", "内存存储引擎，开发环境默认存储，开源版免费"},
		"dashboard":      {"仪表盘", "core", "监控仪表盘，统计概览，开源版免费"},
		"protocol_809":   {"JT/T 809 平台级联", "licensed", "JT/T 809 下级平台级联，跨平台数据转发，需授权"},
		"protocol_1045":  {"JT/T 1045 主动安全", "licensed", "JT/T 1045 主动安全协议，ADAS驾驶行为分析，需授权"},
		"protocol_905":   {"JT/T 905 北斗出租", "licensed", "JT/T 905 北斗导航出租车专用协议，需授权"},
		"db_storage":     {"数据库存储", "licensed", "TDengine时序数据库+Redis缓存+MinIO对象存储，需授权"},
		"ai":             {"AI 智能分析", "licensed", "AI智能分析引擎，报警过滤/疲劳驾驶/风险评分，需授权"},
		"ai_nlp":         {"AI 对话助手", "licensed", "AI自然语言对话助手，智能查询车辆/报警/轨迹，需授权"},
		"protocol_1253":  {"JT/T 1253 道路运输", "infrastructure", "JT/T 1253 道路运输车辆卫星定位协议"},
		"protocol_32960": {"GB/T 32960 新能源车", "infrastructure", "GB/T 32960 电动汽车远程监控协议"},
		"crypto":         {"国密加密", "infrastructure", "SM2/SM3/SM4国密算法，TLCP双证书协议"},
		"adapter":        {"协议适配器", "infrastructure", "多协议适配框架，对接非标设备"},
		"cluster":        {"集群管理", "infrastructure", "多节点集群管理，Gossip发现/负载均衡/故障转移"},
		"monitor":        {"监控告警", "infrastructure", "系统监控告警，指标采集/阈值告警/Prometheus"},
		"legacy":         {"兼容旧版协议", "infrastructure", "JT/T 808-2011/2013旧版协议兼容，各省份地方标准"},
	}

	var modules []moduleInfo
	allFeatures := []registry.Feature{
		registry.FeatureJT808, registry.FeatureJT1078,
		registry.FeatureProtocol809, registry.FeatureProtocol1045,
		registry.FeatureProtocol905, registry.FeatureProtocol1253, registry.FeatureProtocol32960,
		registry.FeatureHTTPAPI, registry.FeatureWebSocket, registry.FeatureMemoryStore, registry.FeatureDashboard,
		registry.FeatureDBStorage, registry.FeatureCrypto,
		registry.FeatureAdapter, registry.FeatureCluster, registry.FeatureMonitor,
		registry.FeatureLegacy, registry.FeatureAI, registry.FeatureAINLP,
	}

	for _, f := range allFeatures {
		name := string(f)
		meta, ok := moduleMap[name]
		if !ok {
			meta = moduleMeta{name, "infrastructure", ""}
		}
		modules = append(modules, moduleInfo{
			Name:        name,
			DisplayName: meta.Display,
			Category:    meta.Category,
			Description: meta.Description,
			Enabled:     h.registry.HasFeature(f),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data":    modules,
	})
}