package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-4]: 模块崩溃自动重启 supervisor
//
// 功能：
//   1. 后台 goroutine 周期性探活（HealthModule.Health()）
//   2. 模块 Start/Health panic 时自动 recover 并重启
//   3. 限流：每模块最多 3 次重启/小时，超出则停止该模块并告警
//   4. 指数退避：1s → 2s → 4s → 8s → 16s（上限 60s）
//   5. 优雅停止：Stop() 后 supervisor 退出，不再重启
//
// 使用：
//   loader.StartSupervisor()  // 在 StartAll() 之后调用
//   loader.StopSupervisor()   // 在 StopAll() 之前调用（或退出时）
// ===================================================================

import (
	"fmt"
	"sync"
	"time"

	"github.com/jte-engine/jte/internal/metrics"
	"go.uber.org/zap"
)

const (
	// supervisor 探活间隔
	supervisorCheckInterval = 10 * time.Second
	// 每模块每小时最大重启次数
	maxRestartsPerHour = 3
	// 重启时间窗口
	restartWindow = 1 * time.Hour
	// 初始退避
	restartInitialBackoff = 1 * time.Second
	// 最大退避
	restartMaxBackoff = 60 * time.Second
)

// Supervisor 模块崩溃自动重启管理器。
type Supervisor struct {
	loader *Loader
	stopCh chan struct{}
	doneCh chan struct{}

	mu         sync.Mutex
	restarts   map[string][]time.Time // 模块名 → 最近重启时间戳列表（滑动窗口）
	backoffs   map[string]time.Duration
	running    bool
}

// NewSupervisor 创建模块 supervisor。
func NewSupervisor(loader *Loader) *Supervisor {
	return &Supervisor{
		loader:   loader,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		restarts: make(map[string][]time.Time),
		backoffs: make(map[string]time.Duration),
	}
}

// Start 启动 supervisor 后台 goroutine。
// 幂等：重复调用不会启动多个 goroutine。
func (s *Supervisor) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go s.run()
}

// Stop 停止 supervisor（幂等，阻塞等待 goroutine 退出）。
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)
	<-s.doneCh
}

// run supervisor 主循环：周期性探活 + 重启失败模块。
func (s *Supervisor) run() {
	defer close(s.doneCh)

	ticker := time.NewTicker(supervisorCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkAll()
		}
	}
}

// checkAll 遍历所有 running 模块，执行健康检查并重启不健康模块。
func (s *Supervisor) checkAll() {
	s.loader.mu.RLock()
	// 快照当前运行中的模块（避免长时间持锁）
	type snapshot struct {
		name string
		lm   *LoadedModule
	}
	var running []snapshot
	for name, lm := range s.loader.modules {
		if lm.Info.Status == "running" {
			running = append(running, snapshot{name: name, lm: lm})
		}
		// AUTO-FIX-2026-06-30 [集成-7]: 刷新所有模块状态指标（含非 running 的）
		metrics.ModuleStatus.SetWithLabels(moduleStatusValue(lm.Info.Status),
			map[string]string{"module": name})
		metrics.ModuleRestartCount.SetWithLabels(float64(lm.RestartCount),
			map[string]string{"module": name})
	}
	s.loader.mu.RUnlock()

	for _, snap := range running {
		s.checkAndRestart(snap.name, snap.lm)
	}
}

// moduleStatusValue 将模块状态字符串映射为 Prometheus gauge 值。
// 1=running, 0=stopped, -1=failed, 0.5=restarting
func moduleStatusValue(status string) float64 {
	switch status {
	case "running":
		return 1
	case "stopped":
		return 0
	case "failed":
		return -1
	case "restarting":
		return 0.5
	default:
		return 0
	}
}

// checkAndRestart 检查单个模块健康状态，必要时重启。
func (s *Supervisor) checkAndRestart(name string, lm *LoadedModule) {
	// 1. 健康检查（若模块实现 HealthModule）
	unhealthy := false
	if hm, ok := lm.Module.(HealthModule); ok {
		var healthErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					healthErr = fmt.Errorf("health check panic: %v", r)
				}
			}()
			healthErr = hm.Health()
		}()
		if healthErr != nil {
			unhealthy = true
			s.loader.logger.Warn("module health check failed, scheduling restart",
				zap.String("name", name),
				zap.Error(healthErr))
			lm.LastError = healthErr.Error()
		}
	}

	if !unhealthy {
		return
	}

	// 2. 限流检查：1 小时窗口内重启次数
	if !s.canRestart(name) {
		s.loader.logger.Error("module exceeded max restarts per hour, marking as failed",
			zap.String("name", name),
			zap.Int("max_restarts", maxRestartsPerHour),
			zap.Duration("window", restartWindow))
		// 标记为 failed，supervisor 不再探活
		s.loader.mu.Lock()
		lm.Info.Status = "failed"
		lm.Info.Error = "exceeded max restarts per hour"
		lm.LastError = lm.Info.Error
		s.loader.mu.Unlock()
		// AUTO-FIX-2026-06-30 [集成-7]: 更新模块状态指标
		metrics.ModuleStatus.SetWithLabels(-1, map[string]string{"module": name})
		return
	}

	// 3. 执行重启（Stop → 退避 → Start），带 recover 保护
	s.restartModule(name, lm)
}

// canRestart 检查模块在滑动窗口内是否仍可重启。
// 超出限制返回 false。若可重启，记录本次重启时间戳。
func (s *Supervisor) canRestart(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-restartWindow)
	times := s.restarts[name]

	// 淘汰窗口外的记录
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= maxRestartsPerHour {
		s.restarts[name] = valid
		return false
	}

	s.restarts[name] = append(valid, now)
	return true
}

// restartModule 执行模块重启：Stop（容忍错误）→ 指数退避 → Start。
func (s *Supervisor) restartModule(name string, lm *LoadedModule) {
	s.mu.Lock()
	backoff := s.backoffs[name]
	if backoff == 0 {
		backoff = restartInitialBackoff
	} else {
		backoff *= 2
		if backoff > restartMaxBackoff {
			backoff = restartMaxBackoff
		}
	}
	s.backoffs[name] = backoff
	s.mu.Unlock()

	s.loader.logger.Info("restarting module",
		zap.String("name", name),
		zap.Duration("backoff", backoff),
		zap.Int("restart_count", lm.RestartCount))

	// 1. Stop（容忍 panic）
	s.loader.mu.Lock()
	lm.Info.Status = "restarting"
	s.loader.mu.Unlock()
	// AUTO-FIX-2026-06-30 [集成-7]: 更新模块状态指标
	metrics.ModuleStatus.SetWithLabels(0.5, map[string]string{"module": name})

	func() {
		defer func() {
			if r := recover(); r != nil {
				s.loader.logger.Error("module stop panicked during restart",
					zap.String("name", name),
					zap.Any("panic", r))
			}
		}()
		_ = lm.Module.Stop()
	}()

	// 2. 退避等待（可被 Stop 中断）
	select {
	case <-time.After(backoff):
	case <-s.stopCh:
		return
	}

	// 3. Start（带 recover）
	var startErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				startErr = fmt.Errorf("restart start panic: %v", r)
			}
		}()
		startErr = lm.Module.Start()
	}()

	s.loader.mu.Lock()
	defer s.loader.mu.Unlock()
	if startErr != nil {
		lm.Info.Status = "failed"
		lm.Info.Error = startErr.Error()
		lm.LastError = startErr.Error()
		s.loader.logger.Error("module restart failed",
			zap.String("name", name),
			zap.Error(startErr))
		// AUTO-FIX-2026-06-30 [集成-7]: 更新模块状态指标
		metrics.ModuleStatus.SetWithLabels(-1, map[string]string{"module": name})
		metrics.ModuleRestartCount.SetWithLabels(float64(lm.RestartCount),
			map[string]string{"module": name})
		return
	}
	lm.Info.Status = "running"
	lm.Info.Error = ""
	lm.StartTime = time.Now()
	lm.RestartCount++
	s.loader.logger.Info("module restarted successfully",
		zap.String("name", name),
		zap.Int("restart_count", lm.RestartCount))

	// AUTO-FIX-2026-06-30 [集成-7]: 更新模块状态和重启计数指标
	metrics.ModuleStatus.SetWithLabels(1, map[string]string{"module": name})
	metrics.ModuleRestartCount.SetWithLabels(float64(lm.RestartCount),
		map[string]string{"module": name})

	// 重启成功后重置退避（下次故障从初始值开始）
	s.mu.Lock()
	s.backoffs[name] = 0
	s.mu.Unlock()
}

// RestartStats 返回各模块的重启统计（用于监控/调试）。
func (s *Supervisor) RestartStats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := make(map[string]int, len(s.restarts))
	for name, times := range s.restarts {
		stats[name] = len(times)
	}
	return stats
}
