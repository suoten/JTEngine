package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/gateway"
	"github.com/jte-engine/jte/internal/registry"
	"github.com/jte-engine/jte/pkg/storage"
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
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
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
		modules = append(modules, moduleInfo{
			Name:    string(f),
			Enabled: h.registry.HasFeature(f),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data":    modules,
	})
}