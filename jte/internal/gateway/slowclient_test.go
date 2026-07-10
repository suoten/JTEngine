package gateway

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestSessionStatusTransitions 验证会话状态转换覆盖慢连接检测的三阶段：
// connected（连接建立，5s 内须注册）→ registered（已注册，10s 内须鉴权）
// → authenticated（已鉴权，30s 周期超时）。
func TestSessionStatusTransitions(t *testing.T) {
	session, _, peer := newTestSession(t)
	defer session.Close()
	defer peer.Close()

	// 新会话默认状态
	if got := session.GetStatus(); got != "" && got != "connected" {
		// SessionManager.Create 可能未设置初始状态，手动设置后验证转换
		session.SetStatus("connected")
	}

	// 阶段 1: connected → registered（收到 0x0100 注册后）
	session.SetStatus("registered")
	if got := session.GetStatus(); got != "registered" {
		t.Fatalf("after SetStatus(registered): got %q, want registered", got)
	}

	// 阶段 2: registered → authenticated（收到 0x0102 鉴权后）
	session.SetStatus("authenticated")
	if got := session.GetStatus(); got != "authenticated" {
		t.Fatalf("after SetStatus(authenticated): got %q, want authenticated", got)
	}

	// 禁止非法状态回退（业务层应限制，但 SetStatus 本身不做限制）
	session.SetStatus("registered")
	if got := session.GetStatus(); got != "registered" {
		t.Fatalf("SetStatus should allow any value: got %q", got)
	}
}

// TestSessionActivityTracking 验证会话活跃时间更新，
// 慢连接检测依赖 GetLastActive 判断超时。
func TestSessionActivityTracking(t *testing.T) {
	session, _, peer := newTestSession(t)
	defer session.Close()
	defer peer.Close()

	before := session.GetLastActive()
	time.Sleep(10 * time.Millisecond)
	session.UpdateActivity()
	after := session.GetLastActive()

	if !after.After(before) {
		t.Fatalf("UpdateActivity should advance lastActive: before=%v after=%v", before, after)
	}

	// 再次读取应返回相同值（无更新时不变）
	again := session.GetLastActive()
	if !again.Equal(after) {
		t.Fatalf("GetLastActive without UpdateActivity should be stable: after=%v again=%v", after, again)
	}
}

// TestHeartbeatCheckerTimeoutHook 验证心跳超时后资源清理回调被调用。
// 这是慢连接/空闲连接资源清理链的核心：超时 → onTimeout hook → 关闭连接。
func TestHeartbeatCheckerTimeoutHook(t *testing.T) {
	sm := NewSessionManager(zap.NewNop())
	// 用一对 TCP 连接创建 session，模拟真实设备
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	peer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer peer.Close()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	session := sm.Create("timeout-test-session", conn)
	session.SetPhone("13900000000")
	session.SetStatus("authenticated")

	// 超时回调计数器
	var hookCalled int32
	checker := NewHeartbeatChecker(50*time.Millisecond, 100*time.Millisecond, sm, zap.NewNop())
	checker.SetTimeoutHook(func(s *Session) {
		atomic.StoreInt32(&hookCalled, 1)
		if s.ID != session.ID {
			t.Errorf("hook called with wrong session: got %q, want %q", s.ID, session.ID)
		}
		if s.GetPhone() != "13900000000" {
			t.Errorf("hook called with wrong phone: got %q", s.GetPhone())
		}
	})

	checker.Start()
	defer checker.Stop()

	// 等待超时触发（100ms 超时 + 50ms 检查间隔 + 余量）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hookCalled) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if atomic.LoadInt32(&hookCalled) != 1 {
		t.Fatal("timeout hook was not called within 2s")
	}
}

// TestHeartbeatCheckerActiveSessionNotKicked 验证活跃会话不会被误判超时。
func TestHeartbeatCheckerActiveSessionNotKicked(t *testing.T) {
	sm := NewSessionManager(zap.NewNop())
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	peer, _ := net.Dial("tcp", ln.Addr().String())
	defer peer.Close()
	conn, _ := ln.Accept()

	session := sm.Create("active-session", conn)
	session.SetPhone("13700000000")
	session.SetStatus("authenticated")

	var hookCalled int32
	checker := NewHeartbeatChecker(30*time.Millisecond, 200*time.Millisecond, sm, zap.NewNop())
	checker.SetTimeoutHook(func(s *Session) {
		atomic.StoreInt32(&hookCalled, 1)
	})

	checker.Start()
	defer checker.Stop()

	// 每 50ms 更新活跃时间，确保不超时（200ms 超时）
	end := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(end) {
		session.UpdateActivity()
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&hookCalled) != 0 {
		t.Fatal("active session should not be timed out")
	}
}

// TestHeartbeatCheckerHookPanicRecovered 验证超时回调 panic 不会导致检查器崩溃。
func TestHeartbeatCheckerHookPanicRecovered(t *testing.T) {
	sm := NewSessionManager(zap.NewNop())
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	peer, _ := net.Dial("tcp", ln.Addr().String())
	defer peer.Close()
	conn, _ := ln.Accept()

	session := sm.Create("panic-test-session", conn)
	session.SetPhone("13600000000")
	session.SetStatus("authenticated")

	checker := NewHeartbeatChecker(30*time.Millisecond, 80*time.Millisecond, sm, zap.NewNop())
	checker.SetTimeoutHook(func(s *Session) {
		panic("intentional panic for test")
	})

	checker.Start()
	defer checker.Stop()

	// 等待足够时间确认 checker 没有崩溃（仍能继续检查）
	time.Sleep(300 * time.Millisecond)

	// checker 仍在运行（stopCh 未关闭）即说明未崩溃
	// 再创建一个新 session，验证 checker 仍能工作
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln2.Close()
	peer2, _ := net.Dial("tcp", ln2.Addr().String())
	defer peer2.Close()
	conn2, _ := ln2.Accept()
	session2 := sm.Create("second-session", conn2)
	session2.SetPhone("13500000000")
	session2.SetStatus("authenticated")

	var secondHookCalled int32
	checker.SetTimeoutHook(func(s *Session) {
		if s.ID == "second-session" {
			atomic.StoreInt32(&secondHookCalled, 1)
		}
	})

	end := time.Now().Add(1 * time.Second)
	for time.Now().Before(end) {
		if atomic.LoadInt32(&secondHookCalled) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if atomic.LoadInt32(&secondHookCalled) != 1 {
		t.Fatal("checker should still work after a panic in hook")
	}
}
