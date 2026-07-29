package security

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestLoginGuard_FailureLockout(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     3,
		LockoutDuration: 1 * time.Hour,
		HistoryLimit:    10,
	}, zap.NewNop())

	// 前两次失败不锁定
	for i := 0; i < 2; i++ {
		locked, _ := g.RecordLoginFailure("user1", "1.2.3.4")
		if locked {
			t.Fatalf("第 %d 次失败不应锁定", i+1)
		}
	}
	// 第三次失败锁定
	locked, until := g.RecordLoginFailure("user1", "1.2.3.4")
	if !locked {
		t.Fatal("第 3 次失败应触发锁定")
	}
	if until.IsZero() || time.Now().After(until) {
		t.Fatal("锁定时间应在未来")
	}

	// 锁定后登录被拒绝
	allowed, reason := g.CheckLogin("user1", "1.2.3.4", "fp1")
	if allowed {
		t.Fatal("锁定后应拒绝登录")
	}
	if reason == "" {
		t.Fatal("应返回锁定原因")
	}
}

func TestLoginGuard_SuccessClearsFailures(t *testing.T) {
	g := NewLoginGuard(DefaultLoginGuardConfig(), zap.NewNop())

	g.RecordLoginFailure("user1", "1.2.3.4")
	g.RecordLoginFailure("user1", "1.2.3.4")

	// 登录成功清除失败计数
	alert := g.RecordLoginSuccess("user1", "1.2.3.4", "Mozilla", "fp1")
	if alert != nil {
		// 首次登录不应告警（无历史记录）
		t.Fatalf("首次登录不应告警，got %+v", alert)
	}

	// 再次失败计数应从 0 开始
	allowed, _ := g.CheckLogin("user1", "1.2.3.4", "fp1")
	if !allowed {
		t.Fatal("成功后应允许登录")
	}
}

func TestLoginGuard_NewDeviceAlert(t *testing.T) {
	g := NewLoginGuard(DefaultLoginGuardConfig(), zap.NewNop())

	// 首次登录（设备 fp1）
	g.RecordLoginSuccess("user1", "1.2.3.4", "UA1", "fp1")

	// 新设备 fp2 登录 → 告警
	alert := g.RecordLoginSuccess("user1", "1.2.3.4", "UA2", "fp2")
	if alert == nil {
		t.Fatal("新设备登录应告警")
	}
	if alert.Type != "new_device" {
		t.Fatalf("告警类型应为 new_device，got %s", alert.Type)
	}
}

func TestLoginGuard_MultiIPAlert(t *testing.T) {
	g := NewLoginGuard(DefaultLoginGuardConfig(), zap.NewNop())

	// 首次登录 IP1
	g.RecordLoginSuccess("user1", "1.2.3.4", "UA", "fp1")

	// 短时间内从不同 IP 登录 → 告警
	alert := g.RecordLoginSuccess("user1", "5.6.7.8", "UA", "fp1")
	if alert == nil {
		t.Fatal("多 IP 登录应告警")
	}
	if alert.Type != "multi_ip" {
		t.Fatalf("告警类型应为 multi_ip，got %s", alert.Type)
	}
}

func TestLoginGuard_ClearFailures(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     2,
		LockoutDuration: 1 * time.Hour,
	}, zap.NewNop())

	g.RecordLoginFailure("user1", "1.2.3.4")
	g.RecordLoginFailure("user1", "1.2.3.4")
	// 管理员解锁
	g.ClearFailures("user1")

	allowed, _ := g.CheckLogin("user1", "1.2.3.4", "fp1")
	if !allowed {
		t.Fatal("解锁后应允许登录")
	}
}

func TestLoginGuard_GetLoginHistory(t *testing.T) {
	g := NewLoginGuard(DefaultLoginGuardConfig(), zap.NewNop())

	g.RecordLoginSuccess("user1", "1.2.3.4", "UA", "fp1")
	g.RecordLoginSuccess("user1", "5.6.7.8", "UA", "fp1")

	history := g.GetLoginHistory("user1", 10)
	if len(history) != 2 {
		t.Fatalf("应返回 2 条历史，got %d", len(history))
	}
	// 倒序，最新在前
	if history[0].IP != "5.6.7.8" {
		t.Fatal("最新记录应在前")
	}
}

func TestGenerateFingerprint(t *testing.T) {
	fp1 := DeviceFingerprint{
		UserAgent:      "Mozilla/5.0",
		Screen:         "1920x1080",
		Timezone:       "Asia/Shanghai",
		CanvasHash:     "abc123",
	}
	fp2 := fp1
	fp3 := DeviceFingerprint{
		UserAgent:  "Mozilla/5.0",
		Screen:     "1280x720", // 不同
		Timezone:   "Asia/Shanghai",
		CanvasHash: "abc123",
	}

	hash1 := GenerateFingerprint(fp1)
	hash2 := GenerateFingerprint(fp2)
	hash3 := GenerateFingerprint(fp3)

	if hash1 != hash2 {
		t.Fatal("相同特征应生成相同指纹")
	}
	if hash1 == hash3 {
		t.Fatal("不同特征应生成不同指纹")
	}
	if len(hash1) != 32 {
		t.Fatalf("指纹长度应为 32，got %d", len(hash1))
	}
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "::1"}
	for _, ip := range privates {
		if !IsPrivateIP(ip) {
			t.Errorf("%s 应为内网 IP", ip)
		}
	}
	publics := []string{"8.8.8.8", "1.1.1.1", "203.0.113.1"}
	for _, ip := range publics {
		if IsPrivateIP(ip) {
			t.Errorf("%s 应为公网 IP", ip)
		}
	}
}

func TestGetClientIP(t *testing.T) {
	// X-Forwarded-For 优先
	ip := GetClientIP("8.8.8.8, 10.0.0.1", "", "")
	if ip != "8.8.8.8" {
		t.Fatalf("应取第一个公网 IP，got %s", ip)
	}
	// 全内网 XFF 取第一个
	ip = GetClientIP("10.0.0.1, 10.0.0.2", "", "")
	if ip != "10.0.0.1" {
		t.Fatalf("全内网应取第一个，got %s", ip)
	}
	// X-Real-IP
	ip = GetClientIP("", "1.2.3.4", "")
	if ip != "1.2.3.4" {
		t.Fatalf("X-Real-IP 应返回，got %s", ip)
	}
	// RemoteAddr
	ip = GetClientIP("", "", "192.168.1.1:12345")
	if ip != "192.168.1.1" {
		t.Fatalf("RemoteAddr 应解析，got %s", ip)
	}
}

// [P0-3] TestLoginGuard_CleanupDoesNotBlockLogins 验证 cleanupExpired 不会长时间阻塞登录请求。
// 向 LoginGuard 填充大量数据后触发 cleanupExpired，同时并发执行 CheckLogin，
// 确保每个 CheckLogin 响应时间不超过 10ms。
func TestLoginGuard_CleanupDoesNotBlockLogins(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     5,
		LockoutDuration: 1 * time.Millisecond, // 极短，使条目快速变为可清理状态
		HistoryLimit:    5,
	}, zap.NewNop())
	defer g.Stop()

	// 填充大量登录失败记录（条目将在锁定过期后变为可清理）
	for i := 0; i < 5000; i++ {
		username := "user" + itoa(i)
		g.RecordLoginFailure(username, "1.2.3.4")
	}

	// 等待锁定过期（lockoutDuration = 1ms）
	time.Sleep(5 * time.Millisecond)

	// 并发：一个 goroutine 调用 cleanupExpired，多个 goroutine 调用 CheckLogin
	const numReaders = 50
	const maxBlockThreshold = 10 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(numReaders + 1)

	// 启动清理 goroutine
	go func() {
		defer wg.Done()
		g.cleanupExpired()
	}()

	// 启动读 goroutine 测量 CheckLogin 延迟
	maxLatency := time.Duration(0)
	var latencyMu sync.Mutex

	for i := 0; i < numReaders; i++ {
		go func(idx int) {
			defer wg.Done()
			username := "user" + itoa(idx)
			start := time.Now()
			g.CheckLogin(username, "1.2.3.4", "fp")
			latency := time.Since(start)

			latencyMu.Lock()
			if latency > maxLatency {
				maxLatency = latency
			}
			latencyMu.Unlock()
		}(i)
	}

	wg.Wait()

	if maxLatency > maxBlockThreshold {
		t.Fatalf("CheckLogin blocked for %v, threshold %v", maxLatency, maxBlockThreshold)
	}
}

// [P0-3] TestLoginGuard_CleanupExpired_RemovesExpiredEntries 验证 cleanupExpired 正确清理过期数据。
func TestLoginGuard_CleanupExpired_RemovesExpiredEntries(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     2,
		LockoutDuration: 1 * time.Millisecond,
		HistoryLimit:    10,
	}, zap.NewNop())
	defer g.Stop()

	// 创建失败记录并锁定
	g.RecordLoginFailure("expireduser", "1.2.3.4")
	g.RecordLoginFailure("expireduser", "1.2.3.4") // 触发锁定

	// 等待锁定过期 + lastFailure 超过 1 小时条件（手动调整 lastFailure）
	g.mu.Lock()
	if state, ok := g.loginFailures["expireduser"]; ok {
		state.lockedUntil = time.Now().Add(-1 * time.Hour)  // 锁定已过期
		state.lastFailure = time.Now().Add(-2 * time.Hour)   // 超过 1 小时无失败
	}
	g.mu.Unlock()

	// 执行清理
	g.cleanupExpired()

	// 验证条目已被清理
	g.mu.RLock()
	_, exists := g.loginFailures["expireduser"]
	g.mu.RUnlock()
	if exists {
		t.Fatal("cleanupExpired should remove expired login failure entry")
	}
}

// [P0-3] TestLoginGuard_CleanupExpired_KeepsActiveEntries 验证 cleanupExpired 不清理活跃条目。
func TestLoginGuard_CleanupExpired_KeepsActiveEntries(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     5,
		LockoutDuration: 1 * time.Hour, // 仍锁定中
		HistoryLimit:    10,
	}, zap.NewNop())
	defer g.Stop()

	// 创建活跃失败记录
	for i := 0; i < 3; i++ {
		g.RecordLoginFailure("activeuser", "1.2.3.4")
	}

	// 执行清理
	g.cleanupExpired()

	// 验证条目仍然存在（锁定未过期）
	g.mu.RLock()
	_, exists := g.loginFailures["activeuser"]
	g.mu.RUnlock()
	if !exists {
		t.Fatal("cleanupExpired should keep active (still-locked) entry")
	}
}

// [P0-3] TestLoginGuard_CleanupBatchSize 验证分批清理不超限。
func TestLoginGuard_CleanupBatchSize(t *testing.T) {
	g := NewLoginGuard(LoginGuardConfig{
		MaxFailures:     1,
		LockoutDuration: 1 * time.Millisecond,
		HistoryLimit:    5,
	}, zap.NewNop())
	defer g.Stop()

	// 填充超过 cleanupBatchSize 的可清理条目
	total := cleanupBatchSize + 500
	for i := 0; i < total; i++ {
		username := "batchuser" + itoa(i)
		g.RecordLoginFailure(username, "1.2.3.4")
	}

	// 使所有条目可清理
	time.Sleep(5 * time.Millisecond)
	g.mu.Lock()
	for _, state := range g.loginFailures {
		state.lockedUntil = time.Now().Add(-1 * time.Hour)
		state.lastFailure = time.Now().Add(-2 * time.Hour)
	}
	g.mu.Unlock()

	// 执行清理（应最多清理 cleanupBatchSize 条）
	g.cleanupExpired()

	g.mu.RLock()
	remaining := len(g.loginFailures)
	g.mu.RUnlock()

	// 应剩余至少 total - cleanupBatchSize 条
	expectedMin := total - cleanupBatchSize
	if remaining < expectedMin {
		t.Fatalf("remaining = %d, expected at least %d (batch limit not respected)", remaining, expectedMin)
	}
}

// itoa 简单整数转字符串（避免引入 strconv 仅为测试）
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
