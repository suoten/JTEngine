package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-1/2/3]: 模块接口扩展
//
// 模块接口分层设计（向后兼容，全部为可选接口）：
//   Module              —— 必需基础接口（Name/Version/Init/Start/Stop）
//   DependentModule     —— 声明依赖（依赖图 + 循环检测 + 拓扑加载）
//   VersionedModule     —— 声明宿主 API 版本（已有）
//   CoreVersionedModule —— 声明核心版本兼容范围 [MinCoreVersion, MaxCoreVersion]
//   GRPCModule          —— gRPC 进程模式注册（独立进程模块实现）
//   HTTPModule          —— HTTP 路由注册（正式化已有约定）
//   HealthModule        —— 健康检查（supervisor 探活）
//   PrioritizedModule   —— 加载优先级（拓扑同层稳定排序）
//
// 加载顺序（优先级 + 依赖图拓扑）：
//   核心(10) → 存储(20) → 协议扩展(30) → 业务(40) → 安全/运维(50)
// ===================================================================

// 模块加载优先级分层常量（数值越小越先加载）
// 用于拓扑排序同层内的稳定排序，保证基础服务先于业务模块启动。
const (
	PriorityCore     = 10 // 核心模块（module-cluster 等基础设施）
	PriorityStorage  = 20 // 存储层（module-storage）
	PriorityProtocol = 30 // 协议扩展（module-protocol-809/1045/1253/32960/905）
	PriorityBusiness = 40 // 业务模块（module-ai/module-ai-nlp/module-crypto/module-adapter）
	PriorityOps      = 50 // 安全/运维（module-monitor/module-legacy）
)

// DependentModule 可选接口：模块声明其依赖的其他模块名。
// ModuleLoader 启动时构建依赖图，拓扑排序后按序 Init/Start；
// 检测到循环依赖则拒绝加载并告警。
// 依赖必须已被加载（否则该模块跳过加载并记录错误）。
type DependentModule interface {
	Depends() []string
}

// CoreVersionedModule 可选接口：模块声明其兼容的核心（宿主）版本范围。
// 加载时校验：HostCoreVersion 必须落在 [MinCoreVersion, MaxCoreVersion] 区间。
// MaxCoreVersion 为空表示无上限（兼容所有未来主版本，慎用）。
// 不兼容则拒绝加载并提示升级。
type CoreVersionedModule interface {
	MinCoreVersion() string
	MaxCoreVersion() string
}

// GRPCModule 可选接口：gRPC 独立进程模式注册。
// 进程模式下，模块作为独立子进程运行，宿主通过 gRPC client 调用。
// 模块实现此接口在子进程侧注册 gRPC service。
type GRPCModule interface {
	RegisterGRPC(server interface{}) error
}

// HTTPModule 可选接口：HTTP 路由注册（正式化已有约定）。
// 模块实现此接口由 ModuleLoader 在 Start 后统一注入 gin.Engine。
// 未实现此接口的模块仍可在 Start() 内通过 JTEApp.GetRouter() 自助注册（向后兼容）。
type HTTPModule interface {
	RegisterHTTP(router interface{}) error
}

// HealthModule 可选接口：模块健康检查。
// supervisor goroutine 周期性调用 Health()，返回 error 视为不健康，触发重启。
// 未实现此接口的模块，supervisor 仅在 panic 时重启（无法检测卡死）。
type HealthModule interface {
	Health() error
}

// PrioritizedModule 可选接口：声明加载优先级。
// 拓扑排序同层内按 Priority 升序加载（数值小先加载）。
// 未实现此接口的模块按名称推断分类（见 classifyPriority）。
type PrioritizedModule interface {
	Priority() int
}

// HostCoreVersion 宿主核心版本（语义化版本 MAJOR.MINOR.PATCH）。
// 模块通过 CoreVersionedModule 声明兼容范围，加载时校验。
// 主版本号变更代表破坏性变更，永久授权版本锁定以此为粒度。
const HostCoreVersion = "3.0.0"

// classifyPriority 按模块名推断加载优先级（未实现 PrioritizedModule 时使用）。
// 命名约定：module-<category>-<name> 或 module-<category>
func classifyPriority(name string) int {
	switch {
	case stringsContain(name, "cluster"):
		return PriorityCore
	case stringsContain(name, "storage"):
		return PriorityStorage
	case stringsContain(name, "protocol"):
		return PriorityProtocol
	case stringsContain(name, "monitor") || stringsContain(name, "legacy"):
		return PriorityOps
	default:
		// ai / ai-nlp / crypto / adapter 等业务模块
		return PriorityBusiness
	}
}

// stringsContain 简单子串包含（避免在接口文件引入 strings 包，保持独立）。
func stringsContain(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
