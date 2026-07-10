package gateway

// ===================================================================
// 运维验收 2: 重启自动恢复
//
// 验证项：
//   1. 重连退避时间 0-60s 随机（蓝绿部署场景）/ 10-120s 或 60-300s（重启场景）
//   2. 鉴权限流令牌桶防止鉴权风暴
//   3. 重连退避应答帧（0x8001）构造正确
//   4. 会话管理器支持踢旧建新（设备重连场景）
//   5. 心跳检测器超时清理
// ===================================================================

import (
	"testing"
	"time"

	"github.com/jte-engine/jte/internal/config"
	"go.uber.org/zap"
)

// TestV2_AuthBackoffSec_Range 验证鉴权退避时间在合理范围内。
// 验收标准：重连退避 0-60s 随机，避免鉴权风暴。
// 重启窗口期内 60-300s，正常运行期 10-120s。
func TestV2_AuthBackoffSec_Range(t *testing.T) {
	cfg := &config.GatewayConfig{MaxConnections: 120000}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)

	// 模拟重启窗口期内（startupTime 刚设置）
	server.startupTime = time.Now()
	server.backoffWindow = 5 * time.Minute

	for i := 0; i < 100; i++ {
		backoff := server.authBackoffSec()
		if backoff < 60 || backoff >= 300 {
			t.Errorf("重启窗口期退避时间 = %d, 期望 [60, 300)", backoff)
		}
	}

	// 模拟正常运行期（startupTime 已超过 backoffWindow）
	server.startupTime = time.Now().Add(-10 * time.Minute)

	for i := 0; i < 100; i++ {
		backoff := server.authBackoffSec()
		if backoff < 10 || backoff >= 120 {
			t.Errorf("正常运行期退避时间 = %d, 期望 [10, 120)", backoff)
		}
	}
}

// TestV2_AuthBackoffSec_Randomness 验证退避时间的随机性。
// 连续调用应产生不同的值（避免可预测性）。
func TestV2_AuthBackoffSec_Randomness(t *testing.T) {
	cfg := &config.GatewayConfig{MaxConnections: 120000}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)
	server.startupTime = time.Now().Add(-10 * time.Minute)

	seen := make(map[int]bool)
	for i := 0; i < 50; i++ {
		seen[server.authBackoffSec()] = true
	}
	if len(seen) < 10 {
		t.Errorf("50 次调用仅产生 %d 个不同值，随机性不足", len(seen))
	}
}

// TestV2_BuildReconnectBackoffResp 验证重连退避应答帧构造。
// 应答帧应为合法的 808 帧，以 0x7e 开头和结尾。
func TestV2_BuildReconnectBackoffResp(t *testing.T) {
	phone := "13800000000"
	seq := uint16(42)
	backoffSec := 60

	frame := BuildReconnectBackoffResp(phone, seq, backoffSec)

	if len(frame) < 10 {
		t.Fatalf("应答帧长度 = %d, 期望 >= 10", len(frame))
	}
	if frame[0] != 0x7e {
		t.Errorf("帧首字节 = 0x%02x, 期望 0x7e", frame[0])
	}
	if frame[len(frame)-1] != 0x7e {
		t.Errorf("帧尾字节 = 0x%02x, 期望 0x7e", frame[len(frame)-1])
	}
}

// TestV2_SessionManager_KickOldOnReconnect 验证设备重连时踢出旧会话。
// 验收标准：重启后设备自动重连，同一设备仅允许一个活跃会话。
func TestV2_SessionManager_KickOldOnReconnect(t *testing.T) {
	logger := zap.NewNop()
	sm := NewSessionManager(logger)

	// 创建旧会话
	conn1a, conn1b := createPipeConn(t)
	defer conn1a.Close()
	defer conn1b.Close()
	oldSession := sm.Create("old-session", conn1a)
	sm.Register(oldSession, "13800000000")

	// 验证旧会话已注册
	if _, ok := sm.GetByPhone("13800000000"); !ok {
		t.Fatal("旧会话应已注册")
	}

	// 创建新会话（设备重连）
	conn2a, conn2b := createPipeConn(t)
	defer conn2a.Close()
	defer conn2b.Close()
	newSession := sm.Create("new-session", conn2a)
	sm.Register(newSession, "13800000000")

	// 验证：byPhone 指向新会话
	current, ok := sm.GetByPhone("13800000000")
	if !ok {
		t.Fatal("重连后应能查到会话")
	}
	if current.ID != "new-session" {
		t.Errorf("当前会话 = %s, 期望 new-session", current.ID)
	}
}

// TestV2_HeartbeatChecker_Timeout 验证心跳超时检测。
// 验收标准：kill 进程→重启→设备自动重连，超时会话被清理。
func TestV2_HeartbeatChecker_Timeout(t *testing.T) {
	logger := zap.NewNop()
	sm := NewSessionManager(logger)

	// 创建一个会话，设置很久以前的 LastActive
	conn1, conn2 := createPipeConn(t)
	defer conn1.Close()
	defer conn2.Close()
	session := sm.Create("timeout-test", conn1)
	session.SetPhone("13800000001")
	session.SetStatus("authenticated")

	// 设置 LastActive 为 10 分钟前
	session.mu.Lock()
	session.LastActive = time.Now().Add(-10 * time.Minute)
	session.mu.Unlock()

	// 创建心跳检测器：1s 间隔，5s 超时
	checker := NewHeartbeatChecker(100*time.Millisecond, 5*time.Second, sm, logger)

	cleaned := false
	checker.SetTimeoutHook(func(s *Session) {
		if s.ID == "timeout-test" {
			cleaned = true
		}
	})

	checker.Start()
	defer checker.Stop()

	// 等待心跳检测器执行
	time.Sleep(500 * time.Millisecond)

	if !cleaned {
		t.Error("心跳超时会话未被清理")
	}
}

// TestV2_TokenBucket_AuthStormProtection 验证令牌桶防止鉴权风暴。
// 重启后大量设备同时鉴权时，令牌桶应限流。
// 令牌桶会按时间补充令牌，因此验证大批量请求时被拒绝的比例。
func TestV2_TokenBucket_AuthStormProtection(t *testing.T) {
	tb := newTokenBucket(1000, 1000)

	// 模拟 10000 个设备同时鉴权
	allowed := 0
	rejected := 0
	for i := 0; i < 10000; i++ {
		if tb.Allow() {
			allowed++
		} else {
			rejected++
		}
	}

	// 应有大量请求被拒绝（令牌耗尽后仅按速率补充）
	if rejected == 0 {
		t.Error("10000 个请求应有部分被拒绝，但全部通过")
	}
	// 允许数应在合理范围（初始 1000 + 时间补充，但远小于 10000）
	if allowed >= 10000 {
		t.Error("不应全部通过")
	}
	t.Logf("允许 %d, 拒绝 %d (总计 10000)", allowed, rejected)
}
