package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-5]: 模块加载架构统一
//
// 加载模式选择：
//   - Linux 生产环境：强制 gRPC/RPC 独立进程模式（进程隔离 + 崩溃隔离）
//   - Windows/macOS：强制 RPC 进程模式（Go plugin 不支持非 Linux 平台）
//   - 开发环境（Linux dev）：可选 Go plugin（.so）模式，便于调试
//
// 模块接口统一：
//   - RegisterGRPC：gRPC 服务注册（进程模式下由子进程实现）
//   - RegisterHTTP：HTTP 路由注册（由 Loader 统一注入 router）
//
// 进程模式架构：
//   宿主进程                        子进程（每个模块独立）
//   ┌──────────┐    net/rpc     ┌──────────────┐
//   │ Loader   │ ◄────────────► │ Module binary│
//   │          │  Unix socket   │ (rpc server) │
//   └──────────┘                └──────────────┘
//   ProcessModule 实现 Module 接口，代理所有调用到子进程
// ===================================================================

import (
	"os"
	"runtime"
	"strings"
)

// ModuleLoadMode 模块加载模式
type ModuleLoadMode int

const (
	// LoadModeAuto 自动选择（根据 OS 和环境）
	LoadModeAuto ModuleLoadMode = iota
	// LoadModePlugin Go plugin 模式（.so，仅 Linux 开发环境）
	LoadModePlugin
	// LoadModeProcess 独立进程模式（RPC，所有平台支持，生产推荐）
	LoadModeProcess
)

// String 返回加载模式名称
func (m ModuleLoadMode) String() string {
	switch m {
	case LoadModePlugin:
		return "plugin"
	case LoadModeProcess:
		return "process"
	default:
		return "auto"
	}
}

// ParseLoadMode 从字符串解析加载模式
func ParseLoadMode(s string) ModuleLoadMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "plugin", "so":
		return LoadModePlugin
	case "process", "rpc", "grpc":
		return LoadModeProcess
	default:
		return LoadModeAuto
	}
}

// SelectLoadMode 根据环境和配置选择加载模式。
// AUTO 模式规则：
//   - Windows/macOS → Process（plugin 不支持）
//   - Linux + 生产环境（JTE_ENV=production 或 非 dev） → Process
//   - Linux + 开发环境（JTE_ENV=dev 或 GO_PLUGIN=1） → Plugin
func SelectLoadMode(configured ModuleLoadMode) ModuleLoadMode {
	if configured != LoadModeAuto {
		return configured
	}

	// 非 Linux 平台：强制进程模式（Go plugin 仅支持 Linux）
	if runtime.GOOS != "linux" {
		return LoadModeProcess
	}

	// Linux 平台：根据环境判断
	env := strings.ToLower(os.Getenv("JTE_ENV"))
	if env == "production" || env == "prod" {
		return LoadModeProcess
	}

	// 开发环境：默认 plugin 模式（便于调试），可通过环境变量强制 process
	if os.Getenv("JTE_MODULE_PROCESS") != "" {
		return LoadModeProcess
	}

	return LoadModePlugin
}

// IsPluginSupported 当前平台是否支持 Go plugin
func IsPluginSupported() bool {
	return runtime.GOOS == "linux"
}

// LoadModeConfig 模块加载配置
type LoadModeConfig struct {
	Mode            ModuleLoadMode // 加载模式（auto/plugin/process）
	ModuleBinDir    string         // 进程模式下模块二进制目录
	SocketDir       string         // 进程模式下 Unix socket / 命名管道目录
	StartTimeoutSec int            // 子进程启动超时（秒）
	StopTimeoutSec  int            // 子进程停止超时（秒）
}

// DefaultLoadModeConfig 默认加载配置
func DefaultLoadModeConfig() LoadModeConfig {
	return LoadModeConfig{
		Mode:            LoadModeAuto,
		ModuleBinDir:    "modules/bin",
		SocketDir:       os.TempDir(),
		StartTimeoutSec: 10,
		StopTimeoutSec:  5,
	}
}
