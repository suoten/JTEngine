// Package registry provides the FeatureRegistry and ConfigProvider types shared
// between the main JTE engine and its external modules.
//
// This package lives under pkg/ (not internal/) so that external Go modules
// (jte-modules/module-*) can import it. Go's internal package rule would
// prevent modules whose module path is github.com/suoten/jt-engine-modules/*
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

// PluginRegistry 是插件模块的全局注册表。
//
// 设计目的：当使用 Garble 等代码混淆工具编译模块 .so 文件时，
// plugin.Lookup("Module") 会因变量名被混淆而失效。
// PluginRegistry 通过 init() 函数注册机制提供可靠的回退查找路径：
// 模块在 init() 中调用 RegisterPluginModule 注册自身，
// 宿主加载器在 plugin.Lookup 失败时从注册表中查找。
//
// 线程安全：所有方法均通过互斥锁保护，可在并发环境中安全调用。
//
// 工作原理：
//  1. 宿主和插件共享同一个 registry 包实例（Go 运行时自动去重）
//  2. plugin.Open() 触发插件的 init() 函数执行
//  3. init() 调用 RegisterPluginModule 将模块实例写入共享注册表
//  4. 宿主加载器从注册表中获取模块实例
//
// 使用示例（模块侧）：
//
//	func init() {
//	    registry.RegisterPluginModule("module-ai", Module)
//	}
//
// 使用示例（宿主侧）：
//
//	if m, ok := registry.GetPluginModule("module-ai"); ok {
//	    mod := m.(module.Module)
//	    // ... 使用 mod
//	}
var pluginRegistry = struct {
	mu      sync.Mutex
	modules map[string]interface{}
}{
	modules: make(map[string]interface{}),
}

// RegisterPluginModule 将插件模块实例注册到全局注册表。
// key 通常为模块的 Name() 返回值（如 "module-ai"）。
// m 为模块实例，宿主加载器会通过类型断言将其转换为 module.Module 接口。
//
// 此函数设计为在插件模块的 init() 函数中调用，确保在 plugin.Open() 时自动执行。
// 重复注册同一 key 会覆盖之前的值（用于模块重新加载场景）。
func RegisterPluginModule(key string, m interface{}) {
	pluginRegistry.mu.Lock()
	defer pluginRegistry.mu.Unlock()
	pluginRegistry.modules[key] = m
}

// GetPluginModule 从全局注册表查找插件模块实例。
// key 通常为模块的 Name() 返回值或 .so 文件名（去掉扩展名）。
// 返回的 interface{} 需要由调用方进行类型断言。
func GetPluginModule(key string) (interface{}, bool) {
	pluginRegistry.mu.Lock()
	defer pluginRegistry.mu.Unlock()
	m, ok := pluginRegistry.modules[key]
	return m, ok
}

// UnregisterPluginModule 从全局注册表移除指定模块。
// 用于模块卸载场景，避免注册表中残留无效引用。
func UnregisterPluginModule(key string) {
	pluginRegistry.mu.Lock()
	defer pluginRegistry.mu.Unlock()
	delete(pluginRegistry.modules, key)
}

// ListPluginModules 返回当前注册表中所有模块的 key 列表。
// 用于调试和诊断。
func ListPluginModules() []string {
	pluginRegistry.mu.Lock()
	defer pluginRegistry.mu.Unlock()
	keys := make([]string, 0, len(pluginRegistry.modules))
	for k := range pluginRegistry.modules {
		keys = append(keys, k)
	}
	return keys
}

// ClearPluginModules 清空注册表中的所有模块。
// 主要用于测试场景，生产环境不应调用。
func ClearPluginModules() {
	pluginRegistry.mu.Lock()
	defer pluginRegistry.mu.Unlock()
	pluginRegistry.modules = make(map[string]interface{})
}
