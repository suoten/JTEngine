package gateway

// ===================================================================
// FIXED-2026-07-23 [P2]: 809 熔断器配置注入测试
// ===================================================================

import (
	"testing"

	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// TestP2_SetCircuitBreakerConfig 验证熔断器参数注入
func TestP2_SetCircuitBreakerConfig(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "test-cb"},
		zap.NewNop(),
		nil,
		nil,
	)

	// 验证默认值
	if c.circuitFailThreshold != 10 {
		t.Errorf("expected default circuitFailThreshold=10, got %d", c.circuitFailThreshold)
	}

	// 注入自定义值
	c.SetCircuitBreakerConfig(5, 120, false)

	if c.circuitFailThreshold != 5 {
		t.Errorf("expected circuitFailThreshold=5, got %d", c.circuitFailThreshold)
	}
	if c.circuitResetTimeout.Seconds() != 120 {
		t.Errorf("expected circuitResetTimeout=120s, got %v", c.circuitResetTimeout)
	}
	if c.pendingOverflowAlert != false {
		t.Errorf("expected pendingOverflowAlert=false, got true")
	}
}

// TestP2_SetCircuitBreakerConfig_DefaultsIgnored 零值不覆盖默认值
func TestP2_SetCircuitBreakerConfig_DefaultsIgnored(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "test-cb2"},
		zap.NewNop(),
		nil,
		nil,
	)

	// 传入零值，不应覆盖默认值
	c.SetCircuitBreakerConfig(0, 0, true)

	if c.circuitFailThreshold != 10 {
		t.Errorf("expected circuitFailThreshold=10 (default preserved), got %d", c.circuitFailThreshold)
	}
}
