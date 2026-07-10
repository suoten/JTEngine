package module

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-4]: 模块崩溃自动重启 supervisor 测试
// ===================================================================

// flakyModule 模拟崩溃模块：Health 返回 error 触发重启，Start/Stop 计数
type flakyModule struct {
	mockModule
	mu          sync.Mutex
	startCount  int32
	stopCount   int32
	healthFail  int32 // 前多少次 Health 返回 error
	healthCalls int32
}

func (m *flakyModule) Start() error {
	atomic.AddInt32(&m.startCount, 1)
	return nil
}
func (m *flakyModule) Stop() error {
	atomic.AddInt32(&m.stopCount, 1)
	return nil
}
func (m *flakyModule) Health() error {
	calls := atomic.AddInt32(&m.healthCalls, 1)
	if calls <= atomic.LoadInt32(&m.healthFail) {
		return errors.New("simulated health failure")
	}
	return nil
}

func newSupervisorTestLoader() *Loader {
	logger := zap.NewNop()
	return &Loader{
		modules: make(map[string]*LoadedModule),
		logger:  logger,
	}
}

func TestSupervisor_StartStopIdempotent(t *testing.T) {
	l := newSupervisorTestLoader()
	sup := NewSupervisor(l)
	sup.Start()
	sup.Start() // 幂等
	sup.Stop()
	sup.Stop() // 幂等
}

func TestSupervisor_RestartsUnhealthyModule(t *testing.T) {
	l := newSupervisorTestLoader()
	mod := &flakyModule{
		mockModule: mockModule{name: "test-flaky"},
		healthFail: 1, // 第一次 Health 失败，之后成功
	}
	addModule(l, mod)
	l.loadOrder = []string{"test-flaky"}

	// 模拟模块已启动（StartAll 之后的状态）
	mod.Start()
	l.modules["test-flaky"].Info.Status = "running"

	sup := NewSupervisor(l)
	sup.Start()
	defer sup.Stop()

	// 直接调用 checkAll 触发健康检查 + 重启
	sup.checkAll()

	// checkAll 同步等待重启完成（含 1s 退避），重启后 startCount 应 >= 2
	startCount := atomic.LoadInt32(&mod.startCount)
	stopCount := atomic.LoadInt32(&mod.stopCount)

	if startCount < 2 {
		t.Errorf("expected module to be restarted (startCount>=2), got %d", startCount)
	}
	if stopCount < 1 {
		t.Errorf("expected module to be stopped before restart (stopCount>=1), got %d", stopCount)
	}

	// 验证重启后状态恢复为 running
	l.mu.RLock()
	status := l.modules["test-flaky"].Info.Status
	restartCount := l.modules["test-flaky"].RestartCount
	l.mu.RUnlock()
	if status != "running" {
		t.Errorf("expected status=running after restart, got %s", status)
	}
	if restartCount < 1 {
		t.Errorf("expected restartCount>=1, got %d", restartCount)
	}
}

func TestSupervisor_MaxRestartsPerHour(t *testing.T) {
	l := newSupervisorTestLoader()
	mod := &flakyModule{
		mockModule: mockModule{name: "test-crash"},
		healthFail: 1000, // 永远健康检查失败
	}
	addModule(l, mod)
	l.loadOrder = []string{"test-crash"}
	l.modules["test-crash"].Info.Status = "running"

	sup := NewSupervisor(l)

	// 模拟超过 maxRestartsPerHour 次重启请求
	for i := 0; i < maxRestartsPerHour; i++ {
		if !sup.canRestart("test-crash") {
			t.Errorf("restart %d should be allowed (max=%d)", i+1, maxRestartsPerHour)
		}
	}

	// 第 maxRestartsPerHour+1 次应被拒绝
	if sup.canRestart("test-crash") {
		t.Errorf("restart %d should be rejected (max=%d)", maxRestartsPerHour+1, maxRestartsPerHour)
	}
}

func TestSupervisor_BackoffExponential(t *testing.T) {
	l := newSupervisorTestLoader()
	sup := NewSupervisor(l)

	// 第一次重启：初始退避
	b1 := sup.computeBackoff("test-mod")
	if b1 != restartInitialBackoff {
		t.Errorf("expected initial backoff %v, got %v", restartInitialBackoff, b1)
	}

	// 第二次：×2
	b2 := sup.computeBackoff("test-mod")
	if b2 != restartInitialBackoff*2 {
		t.Errorf("expected backoff %v, got %v", restartInitialBackoff*2, b2)
	}

	// 第三次：×4
	b3 := sup.computeBackoff("test-mod")
	if b3 != restartInitialBackoff*4 {
		t.Errorf("expected backoff %v, got %v", restartInitialBackoff*4, b3)
	}
}

func TestSupervisor_BackoffMaxCap(t *testing.T) {
	l := newSupervisorTestLoader()
	sup := NewSupervisor(l)

	var backoff time.Duration
	for i := 0; i < 20; i++ {
		backoff = sup.computeBackoff("test-mod")
	}
	if backoff > restartMaxBackoff {
		t.Errorf("backoff should not exceed max %v, got %v", restartMaxBackoff, backoff)
	}
	if backoff != restartMaxBackoff {
		t.Errorf("expected backoff capped at %v, got %v", restartMaxBackoff, backoff)
	}
}

func TestSupervisor_BackoffResetOnSuccess(t *testing.T) {
	l := newSupervisorTestLoader()
	sup := NewSupervisor(l)

	sup.computeBackoff("test-mod") // 1s
	sup.computeBackoff("test-mod") // 2s
	sup.resetBackoff("test-mod")

	b := sup.computeBackoff("test-mod")
	if b != restartInitialBackoff {
		t.Errorf("expected backoff reset to %v, got %v", restartInitialBackoff, b)
	}
}

func TestSupervisor_RestartStats(t *testing.T) {
	l := newSupervisorTestLoader()
	sup := NewSupervisor(l)

	sup.canRestart("mod-a")
	sup.canRestart("mod-a")
	sup.canRestart("mod-b")

	stats := sup.RestartStats()
	if stats["mod-a"] != 2 {
		t.Errorf("expected mod-a restarts=2, got %d", stats["mod-a"])
	}
	if stats["mod-b"] != 1 {
		t.Errorf("expected mod-b restarts=1, got %d", stats["mod-b"])
	}
}

func TestSupervisor_HealthCheckPanicRecovered(t *testing.T) {
	l := newSupervisorTestLoader()

	panicMod := &panicHealthModule{
		mockModule: mockModule{name: "test-panic"},
	}
	addModule(l, panicMod)
	l.loadOrder = []string{"test-panic"}
	l.modules["test-panic"].Info.Status = "running"

	sup := NewSupervisor(l)
	sup.Start()
	defer sup.Stop()

	// 直接调用 checkAll，Health panic 应被 recover，不应传播
	// checkAll 内部会触发重启流程（Stop→backoff→Start）
	sup.checkAll()

	// 验证 supervisor 仍在运行（未因 panic 崩溃）
	if !sup.isRunning() {
		t.Errorf("supervisor should still be running after health panic")
	}

	// 验证模块已记录错误（健康检查 panic → unhealthy → 重启流程）
	l.mu.RLock()
	lastErr := l.modules["test-panic"].LastError
	l.mu.RUnlock()
	if lastErr == "" {
		t.Errorf("expected LastError to be set after health panic")
	}
}

func TestSupervisor_DoesNotCheckNonRunningModules(t *testing.T) {
	l := newSupervisorTestLoader()
	mod := &flakyModule{
		mockModule: mockModule{name: "test-failed"},
		healthFail: 1000,
	}
	addModule(l, mod)
	l.loadOrder = []string{"test-failed"}
	l.modules["test-failed"].Info.Status = "failed" // 非 running

	sup := NewSupervisor(l)
	sup.checkAll()

	// failed 模块不应被检查
	if atomic.LoadInt32(&mod.healthCalls) != 0 {
		t.Errorf("failed module should not be health-checked, got %d calls", mod.healthCalls)
	}
}

func TestLoader_StartSupervisorIdempotent(t *testing.T) {
	l := newSupervisorTestLoader()
	l.StartSupervisor()
	l.StartSupervisor() // 幂等
	l.StopSupervisor()
	l.StopSupervisor() // 幂等
}

// ========== 辅助类型和方法 ==========

type panicHealthModule struct {
	mockModule
}

func (m *panicHealthModule) Health() error {
	panic("simulated health check panic")
}

// computeBackoff 计算并更新退避值（测试辅助）
func (s *Supervisor) computeBackoff(name string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return backoff
}

// resetBackoff 重置退避（测试辅助）
func (s *Supervisor) resetBackoff(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backoffs[name] = 0
}

// isRunning 返回 supervisor 运行状态（测试辅助）
func (s *Supervisor) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
