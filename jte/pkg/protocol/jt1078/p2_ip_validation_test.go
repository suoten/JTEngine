package jt1078

import (
	"strings"
	"testing"
)

// TestP2_DownloadRequestMessage_IPFormatValidation [P2-3]
// 验证 DownloadRequestMessage.Marshal 对 IP 地址格式校验。
func TestP2_DownloadRequestMessage_IPFormatValidation(t *testing.T) {
	// 有效 IPv4 应通过
	msg := &DownloadRequestMessage{
		LogicChannel: 1,
		StartTime:    "260723100000",
		EndTime:      "260723110000",
		IPAddress:    "192.168.1.1",
		TcpPort:      8080,
		UdpPort:      8081,
		Username:     "admin",
		Password:     "pass",
	}
	if _, err := msg.Marshal(); err != nil {
		t.Fatalf("valid IPv4 should pass: %v", err)
	}

	// 无效 IP 应失败
	invalidIPs := []string{
		"not.an.ip.address",
		"192.168.1",
		"192.168.1.256",
		"::1",           // IPv6, not IPv4
		"192.168.1.1.1", // too many octets
		"abc.def.ghi.jkl",
	}
	for _, ip := range invalidIPs {
		msg.IPAddress = ip
		_, err := msg.Marshal()
		if err == nil {
			t.Fatalf("invalid IP %q should fail", ip)
		}
		if !strings.Contains(err.Error(), "invalid IPv4 address") {
			t.Fatalf("error should mention 'invalid IPv4 address', got: %v", err)
		}
	}
}

// TestP2_DownloadRequestMessage_EmptyIP [P2-3]
// 空 IP 地址应通过校验（某些场景 IP 可选）
func TestP2_DownloadRequestMessage_EmptyIP(t *testing.T) {
	msg := &DownloadRequestMessage{
		LogicChannel: 1,
		StartTime:    "260723100000",
		EndTime:      "260723110000",
		IPAddress:    "",
		TcpPort:      8080,
		UdpPort:      8081,
		Username:     "admin",
		Password:     "pass",
	}
	if _, err := msg.Marshal(); err != nil {
		t.Fatalf("empty IP should be valid: %v", err)
	}
}

// TestP2_DownloadRequestMessage_ValidIPv4RoundTrip [P2-3]
// 验证有效 IPv4 的 Marshal/Unmarshal 往返一致。
func TestP2_DownloadRequestMessage_ValidIPv4RoundTrip(t *testing.T) {
	orig := &DownloadRequestMessage{
		LogicChannel: 1,
		StartTime:    "260723100000",
		EndTime:      "260723110000",
		IPAddress:    "10.0.0.1",
		TcpPort:      554,
		UdpPort:      554,
		Username:     "user",
		Password:     "pwd",
		FilePath:     "/record/2026-07-23/001.mp4",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &DownloadRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.IPAddress != orig.IPAddress {
		t.Errorf("IPAddress round-trip: got %q, want %q", parsed.IPAddress, orig.IPAddress)
	}
	if parsed.TcpPort != orig.TcpPort {
		t.Errorf("TcpPort round-trip: got %d, want %d", parsed.TcpPort, orig.TcpPort)
	}
}

// TestP2_TerminalLogUploadMessage_IPFormatValidation [P2-3]
// 验证 TerminalLogUploadMessage.Marshal 对 IP 地址格式校验。
func TestP2_TerminalLogUploadMessage_IPFormatValidation(t *testing.T) {
	// 有效 IPv4 应通过
	msg := &TerminalLogUploadMessage{
		LogicChannel: 1,
		IPAddress:    "172.16.0.1",
		Port:         8080,
		Username:     "admin",
		Password:     "pass",
		StartTime:    "260723100000",
		EndTime:      "260723110000",
	}
	if _, err := msg.Marshal(); err != nil {
		t.Fatalf("valid IPv4 should pass: %v", err)
	}

	// 无效 IP 应失败
	msg.IPAddress = "not.an.ip"
	_, err := msg.Marshal()
	if err == nil {
		t.Fatal("invalid IP should fail")
	}
	if !strings.Contains(err.Error(), "invalid IPv4 address") {
		t.Fatalf("error should mention 'invalid IPv4 address', got: %v", err)
	}
}

// TestP2_TerminalLogUploadMessage_EmptyIP [P2-3]
// 空 IP 应通过校验
func TestP2_TerminalLogUploadMessage_EmptyIP(t *testing.T) {
	msg := &TerminalLogUploadMessage{
		LogicChannel: 1,
		IPAddress:    "",
		Port:         8080,
		Username:     "admin",
		Password:     "pass",
		StartTime:    "260723100000",
		EndTime:      "260723110000",
	}
	if _, err := msg.Marshal(); err != nil {
		t.Fatalf("empty IP should be valid: %v", err)
	}
}

// TestP2_TerminalLogUploadMessage_ValidIPv4RoundTrip [P2-3]
// 验证有效 IPv4 的 Marshal/Unmarshal 往返一致。
func TestP2_TerminalLogUploadMessage_ValidIPv4RoundTrip(t *testing.T) {
	orig := &TerminalLogUploadMessage{
		LogicChannel: 1,
		IPAddress:    "10.20.30.40",
		Port:         5060,
		Username:     "user",
		Password:     "pwd",
		StartTime:    "260723100000",
		EndTime:      "260723110000",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	parsed := &TerminalLogUploadMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.IPAddress != orig.IPAddress {
		t.Errorf("IPAddress round-trip: got %q, want %q", parsed.IPAddress, orig.IPAddress)
	}
	if parsed.Port != orig.Port {
		t.Errorf("Port round-trip: got %d, want %d", parsed.Port, orig.Port)
	}
}

// TestP2_DownloadRequestMessage_EdgeCaseIPs [P2-3]
// 验证边界 IP 格式。
func TestP2_DownloadRequestMessage_EdgeCaseIPs(t *testing.T) {
	validIPs := []string{
		"0.0.0.0",
		"255.255.255.255",
		"127.0.0.1",
		"10.0.0.1",
		"192.168.0.1",
	}
	for _, ip := range validIPs {
		msg := &DownloadRequestMessage{
			StartTime: "260723100000",
			EndTime:   "260723110000",
			IPAddress: ip,
		}
		if _, err := msg.Marshal(); err != nil {
			t.Errorf("valid IP %q should pass: %v", ip, err)
		}
	}
}
