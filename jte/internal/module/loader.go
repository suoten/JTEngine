package module

import (
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/registry"
	"go.uber.org/zap"
)

// HostAPIVersion 宿主 API 版本（MAJOR.MINOR）。
// 模块通过 HostAPIVersion() 声明其编译时所针对的宿主 API 版本。
// 兼容规则：主版本号必须一致，且模块次版本号 <= 宿主次版本号。
// 主版本号变更代表破坏性 API 变更，模块必须重新编译。
const HostAPIVersion = "1.0"

type Module interface {
	Name() string
	Version() string
	Init(app interface{}) error
	Start() error
	Stop() error
}

// VersionedModule 可选接口：模块实现 HostAPIVersion() 声明其编译时所针对的宿主 API 版本。
// 未实现此接口的旧模块将跳过版本校验（仅打印警告），保持向后兼容。
type VersionedModule interface {
	HostAPIVersion() string
}

type ModuleInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	HostAPIVersion string `json:"host_api_version,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type Loader struct {
	mu         sync.RWMutex
	dir        string
	modules    map[string]*LoadedModule
	registry   *registry.FeatureRegistry
	logger     *zap.Logger
	verify     bool
	loadOrder  []string    // 拓扑排序后的加载顺序（Init/Start 正序，Stop 逆序）
	supervisor *Supervisor // AUTO-FIX-2026-06-30 [集成-4]: 模块崩溃自动重启
	loadMode   ModuleLoadMode // AUTO-FIX-2026-06-30 [集成-5]: 加载模式（plugin/process）
	modeCfg    LoadModeConfig // 进程模式配置
}

type LoadedModule struct {
	Info         ModuleInfo
	Module       Module
	StartTime    time.Time // 最近一次 Start 时间（supervisor 健康检查用）
	RestartCount int       // supervisor 自动重启次数（最近 1 小时窗口）
	LastError    string    // 最近一次错误（panic/recover 或 Start/Health 失败）
}

func NewLoader(dir string, reg *registry.FeatureRegistry, logger *zap.Logger, verify bool) *Loader {
	return &Loader{
		dir:      dir,
		modules:  make(map[string]*LoadedModule),
		registry: reg,
		logger:   logger,
		verify:   verify,
	}
}

func (l *Loader) SetLogger(logger *zap.Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger = logger
}

// SetLoadMode 设置模块加载模式（AUTO-FIX-2026-06-30 [集成-5]）。
// 在 LoadAll 之前调用。未调用时默认 Auto（根据 OS 和环境自动选择）。
func (l *Loader) SetLoadMode(mode ModuleLoadMode, cfg LoadModeConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadMode = mode
	l.modeCfg = cfg
}

func (l *Loader) LoadAll() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// AUTO-FIX-2026-06-30 [集成-5]: 根据加载模式选择 plugin 或 process
	mode := SelectLoadMode(l.loadMode)
	if mode == LoadModeProcess {
		l.logger.Info("loading modules in process mode",
			zap.String("mode", mode.String()),
			zap.String("bin_dir", l.modeCfg.ModuleBinDir))
		return l.loadProcessModulesLocked()
	}

	// plugin 模式（仅 Linux）
	if !IsPluginSupported() {
		l.logger.Warn("plugin mode not supported on this OS, skipping",
			zap.String("os", runtime.GOOS),
			zap.String("hint", "build modules as separate binaries and use process mode"))
		return nil
	}

	l.logger.Info("loading modules in plugin mode", zap.String("mode", mode.String()))
	return l.loadPluginModulesLocked()
}

// loadPluginModulesLocked 原有 plugin 模式加载逻辑（持有写锁）。
func (l *Loader) loadPluginModulesLocked() error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			l.logger.Info("modules directory not found, skipping", zap.String("dir", l.dir))
			return nil
		}
		return fmt.Errorf("read modules dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".so" && ext != ".dll" {
			continue
		}

		modulePath := filepath.Join(l.dir, name)
		if err := l.loadModule(modulePath); err != nil {
			l.logger.Error("load module failed",
				zap.String("path", modulePath),
				zap.Error(err))
			continue
		}
	}

	return nil
}

// loadProcessModulesLocked 进程模式加载（持有写锁）。
func (l *Loader) loadProcessModulesLocked() error {
	cfg := l.modeCfg
	if cfg.ModuleBinDir == "" {
		cfg.ModuleBinDir = filepath.Join(l.dir, "bin")
	}
	if cfg.SocketDir == "" {
		cfg.SocketDir = os.TempDir()
	}

	binaries, err := discoverModuleBinaries(cfg.ModuleBinDir)
	if err != nil {
		if os.IsNotExist(err) {
			l.logger.Info("module binaries directory not found, skipping",
				zap.String("dir", cfg.ModuleBinDir))
			return nil
		}
		return fmt.Errorf("discover module binaries: %w", err)
	}

	if len(binaries) == 0 {
		l.logger.Info("no module binaries found", zap.String("dir", cfg.ModuleBinDir))
		return nil
	}

	for name, binPath := range binaries {
		pm := NewProcessModule(name, binPath, cfg, l.logger)
		info := ModuleInfo{
			Name:    name,
			Version: "(process mode)",
			Status:  "loaded",
		}
		l.modules[name] = &LoadedModule{
			Info:   info,
			Module: pm,
		}
		l.logger.Info("module loaded (process mode)",
			zap.String("name", name),
			zap.String("binary", binPath))
	}

	return nil
}

func (l *Loader) loadModule(path string) error {
	sigPath := path + ".sig"

	if l.verify {
		if _, err := os.Stat(sigPath); os.IsNotExist(err) {
			return fmt.Errorf("signature file missing: %s (signature_verify is enabled)", sigPath)
		}
		if err := VerifySignature(path, sigPath); err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}
	} else {
		if _, err := os.Stat(sigPath); os.IsNotExist(err) {
			l.logger.Warn("module has no signature file (signature_verify disabled)",
				zap.String("path", path))
		}
	}

	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("plugin open: %w", err)
	}

	sym, err := p.Lookup("Module")
	if err != nil {
		return fmt.Errorf("lookup Module symbol: %w", err)
	}

	mod, ok := sym.(Module)
	if !ok {
		return fmt.Errorf("symbol does not implement Module interface")
	}

	// 模块版本绑定校验：防止模块针对不兼容的宿主 API 版本编译导致运行时崩溃
	modAPIVersion := ""
	if vm, ok := mod.(VersionedModule); ok {
		modAPIVersion = vm.HostAPIVersion()
		if err := checkAPIVersionCompatible(modAPIVersion, mod.Name()); err != nil {
			return err
		}
	} else {
		l.logger.Warn("module does not declare HostAPIVersion, skipping version check",
			zap.String("name", mod.Name()),
			zap.String("host_api_version", HostAPIVersion),
			zap.String("hint", "implement HostAPIVersion() string on the Module to enable version binding validation"))
	}

	// AUTO-FIX-2026-06-30 [集成-3]: 核心版本兼容性矩阵校验
	// 模块通过 CoreVersionedModule 声明 [MinCoreVersion, MaxCoreVersion] 兼容范围。
	// HostCoreVersion 必须落在该区间，否则拒绝加载并提示升级。
	if cv, ok := mod.(CoreVersionedModule); ok {
		if err := checkCoreVersionCompatible(cv.MinCoreVersion(), cv.MaxCoreVersion(), mod.Name()); err != nil {
			return err
		}
	} else {
		l.logger.Debug("module does not declare CoreVersion range, skipping core version check",
			zap.String("name", mod.Name()),
			zap.String("host_core_version", HostCoreVersion))
	}

	info := ModuleInfo{
		Name:           mod.Name(),
		Version:        mod.Version(),
		HostAPIVersion: modAPIVersion,
		Status:         "loaded",
	}

	l.modules[mod.Name()] = &LoadedModule{
		Info:   info,
		Module: mod,
	}

	l.logger.Info("module loaded",
		zap.String("name", mod.Name()),
		zap.String("version", mod.Version()))

	return nil
}

// computeLoadOrder 构建依赖图并拓扑排序，返回加载顺序。
// AUTO-FIX-2026-06-30 [集成-2]: 模块依赖图与循环依赖检测。
// 检测到循环依赖 → 返回 error，调用方应拒绝继续加载。
// 缺失依赖 → 该模块标记为 failed，但不阻断其他模块加载。
// 调用方需持有 l.mu 写锁。
func (l *Loader) computeLoadOrder() error {
	g, missingDeps := l.buildDepGraph()

	// 1. 标记缺失依赖的模块为 failed
	if len(missingDeps) > 0 {
		l.logger.Error(formatMissingDeps(missingDeps))
		for name, deps := range missingDeps {
			if lm, ok := l.modules[name]; ok {
				lm.Info.Status = "failed"
				lm.Info.Error = "missing dependencies: " + strings.Join(deps, ", ")
				lm.LastError = lm.Info.Error
			}
		}
	}

	// 2. 拓扑排序 + 循环检测
	order, cycle := g.topologicalOrder()
	if len(cycle) > 0 {
		// 循环依赖：拒绝加载相关模块并告警
		l.logger.Error(formatCycle(cycle))
		// 将环内所有模块标记为 failed
		for _, name := range cycle {
			if lm, ok := l.modules[name]; ok {
				lm.Info.Status = "failed"
				lm.Info.Error = "involved in circular dependency"
				lm.LastError = lm.Info.Error
			}
		}
		// 环外模块仍可按拓扑序加载（order 已排除环内节点）
	}

	l.loadOrder = order
	return nil
}

func (l *Loader) InitAll(app interface{}) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 构建依赖图并拓扑排序
	if err := l.computeLoadOrder(); err != nil {
		return err
	}

	// AUTO-FIX-2026-06-30 [集成-2]: 按拓扑顺序初始化（核心→存储→协议→业务→运维）
	for _, name := range l.loadOrder {
		lm := l.modules[name]
		if lm.Info.Status == "failed" {
			continue
		}
		// recover 保护：模块 Init panic 不应崩溃整个宿主
		var initErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					initErr = fmt.Errorf("module init panic: %v", r)
					l.logger.Error("module init panicked (recovered)",
						zap.String("name", name),
						zap.Any("panic", r),
						zap.Stack("stack"))
				}
			}()
			initErr = lm.Module.Init(app)
		}()
		if initErr != nil {
			lm.Info.Status = "failed"
			lm.Info.Error = initErr.Error()
			lm.LastError = initErr.Error()
			l.logger.Error("module init failed",
				zap.String("name", name),
				zap.Error(initErr),
				zap.String("hint", "ensure App implements the module's AppProvider interface (ProtocolHub/HandlerRegistry/Storage/MergeEngine/Logger)"))
			continue
		}
		l.logger.Info("module initialized", zap.String("name", name))
	}

	return nil
}

func (l *Loader) StartAll() error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// 按拓扑顺序启动（依赖先启动）
	for _, name := range l.loadOrder {
		lm := l.modules[name]
		if lm.Info.Status == "failed" {
			continue
		}
		// recover 保护：模块 Start panic 不应崩溃整个宿主
		var startErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					startErr = fmt.Errorf("module start panic: %v", r)
					l.logger.Error("module start panicked (recovered)",
						zap.String("name", name),
						zap.Any("panic", r),
						zap.Stack("stack"))
				}
			}()
			startErr = lm.Module.Start()
		}()
		if startErr != nil {
			lm.Info.Status = "failed"
			lm.Info.Error = startErr.Error()
			lm.LastError = startErr.Error()
			l.logger.Error("module start failed",
				zap.String("name", name),
				zap.Error(startErr))
			continue
		}
		lm.Info.Status = "running"
		lm.StartTime = time.Now()
		l.logger.Info("module started", zap.String("name", name))
	}

	return nil
}

func (l *Loader) StopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()

	// AUTO-FIX-2026-06-30 [集成-2]: 按拓扑逆序停止（业务→运维→协议→存储→核心）
	for i := len(l.loadOrder) - 1; i >= 0; i-- {
		name := l.loadOrder[i]
		lm := l.modules[name]
		if lm.Info.Status == "running" {
			func() {
				defer func() {
					if r := recover(); r != nil {
						l.logger.Error("module stop panicked (recovered)",
							zap.String("name", name),
							zap.Any("panic", r),
							zap.Stack("stack"))
						lm.Info.Status = "failed"
						lm.Info.Error = fmt.Sprintf("stop panic: %v", r)
						lm.LastError = lm.Info.Error
					}
				}()
				lm.Module.Stop()
				lm.Info.Status = "stopped"
				l.logger.Info("module stopped", zap.String("name", name))
			}()
		}
	}
}

func (l *Loader) List() []ModuleInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]ModuleInfo, 0, len(l.modules))
	for _, lm := range l.modules {
		result = append(result, lm.Info)
	}
	return result
}

// Stop 停止单个已加载模块（不卸载）。用于授权移除时联动停止对应模块。
func (l *Loader) Stop(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	lm, ok := l.modules[name]
	if !ok {
		return fmt.Errorf("module %s not found", name)
	}

	if lm.Info.Status == "running" {
		if err := lm.Module.Stop(); err != nil {
			lm.Info.Status = "failed"
			lm.Info.Error = err.Error()
			l.logger.Error("module stop failed",
				zap.String("name", name),
				zap.Error(err))
			return fmt.Errorf("stop module %s: %w", name, err)
		}
		lm.Info.Status = "stopped"
		l.logger.Info("module stopped (license removed)",
			zap.String("name", name))
	}

	return nil
}

// IsLoaded 检查模块是否已加载。
func (l *Loader) IsLoaded(name string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.modules[name]
	return ok
}

func (l *Loader) Unload(name string, cleanData bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	lm, ok := l.modules[name]
	if !ok {
		return fmt.Errorf("module %s not found", name)
	}

	if lm.Info.Status == "running" {
		return fmt.Errorf("module %s is still running, stop it first", name)
	}

	if cleanData {
		dataDir := filepath.Join(l.dir, "data", name)
		if _, err := os.Stat(dataDir); err == nil {
			if err := os.RemoveAll(dataDir); err != nil {
				l.logger.Warn("failed to clean module data",
					zap.String("module", name),
					zap.Error(err))
			} else {
				l.logger.Info("module data cleaned",
					zap.String("module", name),
					zap.String("data_dir", dataDir))
			}
		}
	}

	delete(l.modules, name)
	l.logger.Info("module unloaded",
		zap.String("name", name),
		zap.Bool("data_cleaned", cleanData))

	return nil
}

func (l *Loader) GetModuleDataDir(name string) string {
	return filepath.Join(l.dir, "data", name)
}

// checkAPIVersionCompatible 校验模块声明的宿主 API 版本与当前宿主版本是否兼容。
// 兼容规则：主版本号必须一致，且模块次版本号 <= 宿主次版本号。
// 版本格式：MAJOR.MINOR（如 "1.0"、"2.3"）。
func checkAPIVersionCompatible(modVersion, modName string) error {
	if modVersion == "" {
		return nil
	}
	hostMajor, hostMinor, ok := parseAPIVersion(HostAPIVersion)
	if !ok {
		return fmt.Errorf("invalid host API version: %s", HostAPIVersion)
	}
	modMajor, modMinor, ok := parseAPIVersion(modVersion)
	if !ok {
		return fmt.Errorf("module %s declares invalid HostAPIVersion: %s", modName, modVersion)
	}
	if modMajor != hostMajor {
		return fmt.Errorf("module %s requires host API v%d but host is v%d (major version mismatch, module must be recompiled)",
			modName, modMajor, hostMajor)
	}
	if modMinor > hostMinor {
		return fmt.Errorf("module %s requires host API v%d.%d but host is v%d.%d (module uses newer API than host provides)",
			modName, modMajor, modMinor, hostMajor, hostMinor)
	}
	return nil
}

// parseAPIVersion 解析 "MAJOR.MINOR" 格式的版本字符串
func parseAPIVersion(v string) (major, minor int, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 1 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	if len(parts) >= 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, false
		}
	}
	return major, minor, true
}

// checkCoreVersionCompatible 校验核心（宿主）版本是否落在模块声明的兼容区间内。
// AUTO-FIX-2026-06-30 [集成-3]: 模块版本兼容性矩阵。
// 版本格式：语义化版本 MAJOR.MINOR.PATCH（如 "3.0.0"、"3.1.5"）。
//   - minVersion 非空时，HostCoreVersion 必须 >= minVersion
//   - maxVersion 非空时，HostCoreVersion 必须 <= maxVersion
//   - maxVersion 为空表示无上限（兼容所有未来版本）
// 不兼容则返回 error，调用方拒绝加载并提示升级。
func checkCoreVersionCompatible(minVersion, maxVersion, modName string) error {
	host := HostCoreVersion
	hostMajor, hostMinor, hostPatch, ok := parseSemVer(host)
	if !ok {
		return fmt.Errorf("invalid host core version: %s", host)
	}

	if minVersion != "" {
		minMaj, minMin, minPatch, ok := parseSemVer(minVersion)
		if !ok {
			return fmt.Errorf("module %s declares invalid MinCoreVersion: %s", modName, minVersion)
		}
		if compareSemVer(hostMajor, hostMinor, hostPatch, minMaj, minMin, minPatch) < 0 {
			return fmt.Errorf("module %s requires core >= %s but host is %s (please upgrade host core)",
				modName, minVersion, host)
		}
	}

	if maxVersion != "" {
		maxMaj, maxMin, maxPatch, ok := parseSemVer(maxVersion)
		if !ok {
			return fmt.Errorf("module %s declares invalid MaxCoreVersion: %s", modName, maxVersion)
		}
		if compareSemVer(hostMajor, hostMinor, hostPatch, maxMaj, maxMin, maxPatch) > 0 {
			return fmt.Errorf("module %s requires core <= %s but host is %s (please upgrade module to support newer core)",
				modName, maxVersion, host)
		}
	}

	return nil
}

// parseSemVer 解析 "MAJOR.MINOR.PATCH" 格式的语义化版本。
func parseSemVer(v string) (major, minor, patch int, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 1 {
		return 0, 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	if len(parts) >= 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, 0, false
		}
	}
	if len(parts) >= 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			// patch 段可能含预发布后缀（如 "0-rc1"），取数字部分
			patchStr := parts[2]
			i := 0
			for i < len(patchStr) && patchStr[i] >= '0' && patchStr[i] <= '9' {
				i++
			}
			if i == 0 {
				return 0, 0, 0, false
			}
			patch, err = strconv.Atoi(patchStr[:i])
			if err != nil {
				return 0, 0, 0, false
			}
		}
	}
	return major, minor, patch, true
}

// compareSemVer 比较两个语义化版本：a < b → -1, a == b → 0, a > b → 1。
func compareSemVer(aMaj, aMin, aPatch, bMaj, bMin, bPatch int) int {
	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	if aPatch != bPatch {
		if aPatch < bPatch {
			return -1
		}
		return 1
	}
	return 0
}

func (l *Loader) ListModuleData() map[string]string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make(map[string]string)
	for name := range l.modules {
		dataDir := l.GetModuleDataDir(name)
		if _, err := os.Stat(dataDir); err == nil {
			result[name] = dataDir
		}
	}
	return result
}

// StartSupervisor 启动模块崩溃自动重启 supervisor。
// AUTO-FIX-2026-06-30 [集成-4]: 在 StartAll() 之后调用。
// supervisor 后台周期性探活（HealthModule.Health()），崩溃模块自动重启（最多 3 次/小时）。
// 幂等：重复调用不会启动多个 supervisor。
func (l *Loader) StartSupervisor() {
	l.mu.Lock()
	if l.supervisor != nil {
		l.mu.Unlock()
		return
	}
	l.supervisor = NewSupervisor(l)
	l.mu.Unlock()
	l.supervisor.Start()
	l.logger.Info("module supervisor started", zap.Duration("check_interval", supervisorCheckInterval))
}

// StopSupervisor 停止模块 supervisor（幂等）。
// 应在 StopAll() 之前调用，避免 supervisor 试图重启正在停止的模块。
func (l *Loader) StopSupervisor() {
	l.mu.Lock()
	sup := l.supervisor
	l.supervisor = nil
	l.mu.Unlock()
	if sup != nil {
		sup.Stop()
		l.logger.Info("module supervisor stopped")
	}
}

// GetSupervisor 返回当前 supervisor 实例（可能为 nil）。
func (l *Loader) GetSupervisor() *Supervisor {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.supervisor
}
