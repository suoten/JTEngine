// Package registry provides the FeatureRegistry and ConfigProvider types shared
// between the main JTE engine and its external modules.
//
// This package lives under pkg/ (not internal/) so that external Go modules
// (jte-modules/module-*) can import it. Go's internal package rule would
// prevent modules whose module path is github.com/jte-engine/jte-modules/*
// from importing github.com/suoten/jt-engine/internal/registry, which caused
// each module to define its own shadow FeatureRegistry type — breaking the
// runtime type assertion app.(JTEApp) in module.Init().
//
// The main engine's internal/registry package re-exports these types via type
// aliases so existing internal callers continue to work without changes.
package registry

import "sync"

// Feature identifies a capability that can be enabled in the engine.
type Feature string

const (
	FeatureJT808       Feature = "jt808"
	FeatureJT1078      Feature = "jt1078"
	FeatureHTTPAPI     Feature = "http_api"
	FeatureWebSocket   Feature = "websocket"
	FeatureMemoryStore Feature = "memory_store"
	FeatureDashboard   Feature = "dashboard"

	FeatureProtocol809   Feature = "protocol_809"
	FeatureProtocol1045  Feature = "protocol_1045"
	FeatureProtocol905   Feature = "protocol_905"
	FeatureProtocol1253  Feature = "protocol_1253"
	FeatureProtocol32960 Feature = "protocol_32960"
	FeatureDBStorage     Feature = "db_storage"
	FeatureCrypto        Feature = "crypto"
	FeatureAdapter       Feature = "adapter"
	FeatureCluster       Feature = "cluster"
	FeatureMonitor       Feature = "monitor"
	FeatureLegacy        Feature = "legacy"
	FeatureAI            Feature = "ai"
	FeatureAINLP         Feature = "ai_nlp"
	FeatureUnlimited     Feature = "unlimited_devices"

	// v2.0 百亿级轨迹数据存储方案 - 多层存储 feature
	FeatureTimeSeriesStorage Feature = "timeseries_storage" // 时序层（TDengine）
	FeatureCacheStorage      Feature = "cache_storage"       // 缓存层（Redis）
	FeatureObjectStorage     Feature = "object_storage"      // 对象存储层（MinIO/S3）
)

// FreeFeatures are enabled by default in NewFeatureRegistry.
var FreeFeatures = []Feature{
	FeatureJT808, FeatureJT1078,
	FeatureHTTPAPI, FeatureWebSocket, FeatureMemoryStore, FeatureDashboard,
}

// FeatureRegistry tracks which features are enabled. It is concurrency-safe.
type FeatureRegistry struct {
	mu       sync.RWMutex
	features map[Feature]bool
}

// NewFeatureRegistry creates a registry with all FreeFeatures pre-enabled.
func NewFeatureRegistry() *FeatureRegistry {
	fr := &FeatureRegistry{
		features: make(map[Feature]bool),
	}
	for _, f := range FreeFeatures {
		fr.features[f] = true
	}
	return fr
}

func (fr *FeatureRegistry) Register(feature Feature) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.features[feature] = true
}

func (fr *FeatureRegistry) Unregister(feature Feature) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	delete(fr.features, feature)
}

func (fr *FeatureRegistry) HasFeature(feature Feature) bool {
	fr.mu.RLock()
	defer fr.mu.RUnlock()
	return fr.features[feature]
}

func (fr *FeatureRegistry) ListFeatures() []Feature {
	fr.mu.RLock()
	defer fr.mu.RUnlock()
	result := make([]Feature, 0, len(fr.features))
	for f := range fr.features {
		result = append(result, f)
	}
	return result
}

func (fr *FeatureRegistry) ListEnabled() map[Feature]bool {
	fr.mu.RLock()
	defer fr.mu.RUnlock()
	result := make(map[Feature]bool, len(fr.features))
	for f, v := range fr.features {
		result[f] = v
	}
	return result
}

// ConfigProvider provides string-based config access for modules that cannot
// import the engine's internal config package (due to Go's internal package
// rule). The main engine's *config.Config satisfies this interface via its
// GetString method, exposed through App.GetConfigProvider().
type ConfigProvider interface {
	GetString(key string) string
}
