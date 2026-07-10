package maintenance

import (

	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestMode_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zap.NewNop()
	m := NewMode(tmpDir, logger)

	if m.IsActive() {
		t.Fatal("expected mode to be inactive initially")
	}

	if err := m.Start("test reason", false); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !m.IsActive() {
		t.Fatal("expected mode to be active after Start")
	}

	status := m.GetStatus()
	if !status.Active || status.Reason != "test reason" {
		t.Errorf("GetStatus = %+v, want active=true, reason='test reason'", status)
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if m.IsActive() {
		t.Fatal("expected mode to be inactive after Stop")
	}
}

func TestMode_DoubleStart(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewMode(tmpDir, zap.NewNop())

	if err := m.Start("first", false); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}

	if err := m.Start("second", false); err == nil {
		t.Fatal("expected error on double Start")
	}
}

func TestMode_StopWhenNotActive(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewMode(tmpDir, zap.NewNop())

	if err := m.Stop(); err == nil {
		t.Fatal("expected error on Stop when not active")
	}
}

func TestMode_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	m1 := NewMode(tmpDir, zap.NewNop())

	if err := m1.Start("persistent test", false); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	m2 := NewMode(tmpDir, zap.NewNop())
	status := m2.GetStatus()
	if !status.Active {
		t.Fatal("expected mode to be loaded from persisted state")
	}
	if status.Reason != "persistent test" {
		t.Errorf("Reason = %s, want 'persistent test'", status.Reason)
	}
}

func TestMode_EmptyConfigDir(t *testing.T) {
	m := NewMode("", zap.NewNop())

	if err := m.Start("test", false); err != nil {
		t.Fatalf("Start with empty dir failed: %v", err)
	}

	if !m.IsActive() {
		t.Fatal("expected mode to be active")
	}
}

// AUTO-FIX-2026-06-30 [P2-6]: 停止写入模式 + 0x8103 通知测试

func TestMode_StopWrites_BufferEnabled(t *testing.T) {
	m := NewMode(t.TempDir(), zap.NewNop())

	if err := m.Start("db upgrade", true); err != nil {
		t.Fatalf("Start stopWrites failed: %v", err)
	}

	if !m.ShouldBuffer() {
		t.Fatal("stopWrites=true 时 ShouldBuffer 应为 true")
	}
	if m.Buffer() == nil {
		t.Fatal("stopWrites=true 时应创建缓冲队列")
	}

	status := m.GetStatus()
	if !status.StopWrites {
		t.Fatal("status.StopWrites 应为 true")
	}
}

func TestMode_QueryOnly_NoBuffer(t *testing.T) {
	m := NewMode(t.TempDir(), zap.NewNop())

	// stopWrites=false：仅停止查询，不缓冲
	if err := m.Start("add index", false); err != nil {
		t.Fatalf("Start query-only failed: %v", err)
	}

	if m.ShouldBuffer() {
		t.Fatal("stopWrites=false 时 ShouldBuffer 应为 false（写入继续）")
	}
	if m.Buffer() != nil {
		t.Fatal("stopWrites=false 时不应创建缓冲队列")
	}
}

func TestMode_NotifyCallbacks_StopWrites(t *testing.T) {
	m := NewMode(t.TempDir(), zap.NewNop())

	var startCalled int32
	var stopCalled int32
	var startReason string

	m.SetNotifyCallbacks(
		func(reason string) {
			atomic.StoreInt32(&startCalled, 1)
			startReason = reason
		},
		func() {
			atomic.StoreInt32(&stopCalled, 1)
		},
	)

	// stopWrites=true：应触发 start 回调（0x8103 暂停上报）
	if err := m.Start("migration", true); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 等待异步回调执行
	for i := 0; i < 100 && atomic.LoadInt32(&startCalled) == 0; i++ {
		<-time.After(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&startCalled) == 0 {
		t.Fatal("stopWrites=true 时应调用 notifyStartCallback（0x8103 暂停上报）")
	}
	if startReason != "migration" {
		t.Errorf("start reason = %q, want 'migration'", startReason)
	}

	// 停止维护：应触发 stop 回调（0x8103 恢复上报）
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	for i := 0; i < 100 && atomic.LoadInt32(&stopCalled) == 0; i++ {
		<-time.After(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&stopCalled) == 0 {
		t.Fatal("stopWrites 维护停止时应调用 notifyStopCallback（0x8103 恢复上报）")
	}
}

func TestMode_NotifyCallbacks_QueryOnly_NotCalled(t *testing.T) {
	m := NewMode(t.TempDir(), zap.NewNop())

	var startCalled int32
	m.SetNotifyCallbacks(func(reason string) { atomic.StoreInt32(&startCalled, 1) }, nil)

	// stopWrites=false：不应触发 start 回调（查询维护不影响终端上报）
	if err := m.Start("add index", false); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	<-time.After(100 * time.Millisecond)
	if atomic.LoadInt32(&startCalled) != 0 {
		t.Fatal("stopWrites=false 时不应调用 notifyStartCallback（查询维护不影响终端上报）")
	}
}

func TestMode_StopWrites_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	m1 := NewMode(tmpDir, zap.NewNop())

	if err := m1.Start("db migration", true); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 重启后应恢复 stopWrites 状态 + 重建缓冲队列
	m2 := NewMode(tmpDir, zap.NewNop())
	status := m2.GetStatus()
	if !status.StopWrites {
		t.Fatal("重启后 stopWrites 应持久化恢复")
	}
	if m2.Buffer() == nil {
		t.Fatal("重启后处于 stopWrites 维护模式应重建缓冲队列")
	}
}