package security

import (
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
