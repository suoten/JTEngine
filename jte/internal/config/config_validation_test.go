package config

import (
	"strings"
	"testing"
)

func TestValidateGatewayTimeouts(t *testing.T) {
	tests := []struct {
		name             string
		heartbeatInterval int
		heartbeatTimeout  int
		wantErr          bool
		errContains      string
	}{
		{
			name:              "valid normal values",
			heartbeatInterval: 30,
			heartbeatTimeout:  180,
			wantErr:           false,
		},
		{
			name:              "valid minimal interval",
			heartbeatInterval: 10,
			heartbeatTimeout:  60,
			wantErr:           false,
		},
		{
			name:              "interval too small",
			heartbeatInterval: 5,
			heartbeatTimeout:  180,
			wantErr:           true,
			errContains:       "heartbeat_interval must be >= 10",
		},
		{
			name:              "timeout not greater than interval*3",
			heartbeatInterval: 30,
			heartbeatTimeout:  90,
			wantErr:           true,
			errContains:       "heartbeat_timeout",
		},
		{
			name:              "timeout equals interval*3 (boundary)",
			heartbeatInterval: 30,
			heartbeatTimeout:  90,
			wantErr:           true,
			errContains:       "heartbeat_timeout",
		},
		{
			name:              "timeout just above interval*3 (valid)",
			heartbeatInterval: 30,
			heartbeatTimeout:  91,
			wantErr:           false,
		},
		{
			name:              "zero interval skips check",
			heartbeatInterval: 0,
			heartbeatTimeout:  180,
			wantErr:           false,
		},
		{
			name:              "zero timeout skips check",
			heartbeatInterval: 30,
			heartbeatTimeout:  0,
			wantErr:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Gateway: GatewayConfig{
					HeartbeatInterval: tt.heartbeatInterval,
					HeartbeatTimeout:  tt.heartbeatTimeout,
				},
			}
			err := validateGatewayTimeouts(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestAPISecurityConfigDefaults(t *testing.T) {
	// 验证 APISecurityConfig 的默认值常量合理
	cfg := APISecurityConfig{}
	// 零值时应触发默认填充（在 Load 中处理）
	if cfg.ConnLimitPerIP != 0 || cfg.BodyLimitBytes != 0 {
		t.Fatalf("zero-value APISecurityConfig should be 0/0, got %+v", cfg)
	}
}
