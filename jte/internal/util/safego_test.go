package util

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestSafeGo_NoPanic_CompletesNormally(t *testing.T) {
	logger := zap.NewNop()
	var ran atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(logger, "test.normal", func() {
		defer wg.Done()
		ran.Store(true)
	})
	wg.Wait()
	if !ran.Load() {
		t.Fatal("SafeGo: fn did not run")
	}
}

func TestSafeGo_PanicRecovered_DoesNotCrashTest(t *testing.T) {
	// 如果 SafeGo 未兜底，testing 框架会因子 goroutine panic 而 fatal 退出测试进程。
	logger := zap.NewNop()
	var panicSeen atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGoWithRecover(logger, "test.panic", func(r interface{}) {
		defer wg.Done()
		if r != nil {
			panicSeen.Store(true)
		}
	}, func() {
		panic("intentional panic for test")
	})
	wg.Wait()
	if !panicSeen.Load() {
		t.Fatal("SafeGoWithRecover: onPanic not invoked on panic")
	}
}

func TestSafeGo_NilLogger_DoesNotPanic(t *testing.T) {
	var ran atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(nil, "test.nil-logger", func() {
		defer wg.Done()
		ran.Store(true)
	})
	wg.Wait()
	if !ran.Load() {
		t.Fatal("SafeGo: fn did not run with nil logger")
	}
}

func TestSafeGo_NilLogger_WithPanic_StillRecovered(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(nil, "test.nil-logger-panic", func() {
		defer wg.Done()
		panic("panic with nil logger")
	})
	// 给 recover 一点时间生效；若未 recover，testing 会直接 fatal
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo: nil-logger panic not recovered in time")
	}
}

func TestSafeGoWithRecover_OnPanicPanics_DoesNotPropagate(t *testing.T) {
	// onPanic 自身 panic 时，二级 recover 应兜底，不应崩溃
	logger := zap.NewNop()
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGoWithRecover(logger, "test.onpanic-panic", func(r interface{}) {
		defer wg.Done()
		panic("onPanic itself panicked")
	}, func() {
		panic("original panic")
	})
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGoWithRecover: onPanic panic not contained")
	}
}
