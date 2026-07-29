package jt1078

// ====================================================================
// [P1-修复] PlatformNegotiateMessage / PlatformNegotiateResponse Port 解析测试
// FIXED-2026-07-22 [P1]: len(data)==offset+2 时 Port 未解析，将 > 改为 >=
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// TestP1_PlatformNegotiateMessage_PortOnlyNoIP 验证恰好 offset+2（无 IP，仅 Port）时 Port 被正确解析。
// FIXED-2026-07-22 [P1]: 原 len(data) > offset+2 条件在恰好等于时跳过 Port 解析。
func TestP1_PlatformNegotiateMessage_PortOnlyNoIP(t *testing.T) {
	// 构造恰好 12 字节数据：Phone(6B) + LogicChannel(1B) + AVType(1B) + StreamType(1B) + ProtocolType(1B) + Port(2B)
	// offset = 10, len(data) = 12 = offset + 2
	data := make([]byte, 12)
	// Phone BCD: "13800138000"
	copy(data[0:6], []byte{0x01, 0x38, 0x00, 0x13, 0x80, 0x00})
	data[6] = 1 // LogicChannel
	data[7] = 0 // AVType
	data[8] = 0 // StreamType
	data[9] = 1 // ProtocolType
	// Port = 10000 at data[10:12]
	binary.BigEndian.PutUint16(data[10:12], 10000)

	m := &PlatformNegotiateMessage{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Port != 10000 {
		t.Errorf("Port = %d, want 10000 (should be parsed when len==offset+2)", m.Port)
	}
	if m.IPAddress != "" {
		t.Errorf("IPAddress = %q, want empty (no IP in this data)", m.IPAddress)
	}
	if m.LogicChannel != 1 {
		t.Errorf("LogicChannel = %d, want 1", m.LogicChannel)
	}
}

// TestP1_PlatformNegotiateMessage_WithIPAndPort 验证包含 IP 和 Port 的完整报文。
func TestP1_PlatformNegotiateMessage_WithIPAndPort(t *testing.T) {
	// Phone(6B) + LogicChannel(1B) + AVType(1B) + StreamType(1B) + ProtocolType(1B) + IP("192.168.1.1") + Port(2B)
	ipStr := "192.168.1.1"
	data := make([]byte, 0, 10+len(ipStr)+2)
	data = append(data, 0x01, 0x38, 0x00, 0x13, 0x80, 0x00) // Phone
	data = append(data, 1)                                   // LogicChannel
	data = append(data, 0)                                   // AVType
	data = append(data, 0)                                   // StreamType
	data = append(data, 1)                                   // ProtocolType
	data = append(data, []byte(ipStr)...)                    // IP
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 8080)
	data = append(data, portBytes...) // Port

	m := &PlatformNegotiateMessage{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Port != 8080 {
		t.Errorf("Port = %d, want 8080", m.Port)
	}
	if m.IPAddress != ipStr {
		t.Errorf("IPAddress = %q, want %q", m.IPAddress, ipStr)
	}
}

// TestP1_PlatformNegotiateMessage_MarshalUnmarshal_RoundTrip 验证往返一致。
func TestP1_PlatformNegotiateMessage_MarshalUnmarshal_RoundTrip(t *testing.T) {
	original := &PlatformNegotiateMessage{
		Phone:        "13800138000",
		LogicChannel: 1,
		AVType:       0,
		StreamType:   0,
		ProtocolType: 1,
		IPAddress:    "10.0.0.1",
		Port:         65535,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed := &PlatformNegotiateMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.Port != original.Port {
		t.Errorf("Port = %d, want %d", parsed.Port, original.Port)
	}
	if parsed.IPAddress != original.IPAddress {
		t.Errorf("IPAddress = %q, want %q", parsed.IPAddress, original.IPAddress)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
}

// TestP1_PlatformNegotiateResponse_PortOnlyNoIP 验证 Response 恰好 offset+2 时 Port 被解析。
// FIXED-2026-07-22 [P1]: 同 PlatformNegotiateMessage 修复。
func TestP1_PlatformNegotiateResponse_PortOnlyNoIP(t *testing.T) {
	// Phone(6B) + LogicChannel(1B) + Result(1B) + Port(2B) = 10 bytes
	// offset = 8, len(data) = 10 = offset + 2
	data := make([]byte, 10)
	copy(data[0:6], []byte{0x01, 0x38, 0x00, 0x13, 0x80, 0x00})
	data[6] = 1 // LogicChannel
	data[7] = 0 // Result
	binary.BigEndian.PutUint16(data[8:10], 5060)

	m := &PlatformNegotiateResponse{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Port != 5060 {
		t.Errorf("Port = %d, want 5060 (should be parsed when len==offset+2)", m.Port)
	}
	if m.Result != 0 {
		t.Errorf("Result = %d, want 0", m.Result)
	}
	if m.IPAddress != "" {
		t.Errorf("IPAddress = %q, want empty", m.IPAddress)
	}
}

// TestP1_PlatformNegotiateResponse_WithIPAndPort 验证 Response 含 IP+Port 的完整报文。
func TestP1_PlatformNegotiateResponse_WithIPAndPort(t *testing.T) {
	ipStr := "172.16.0.1"
	data := make([]byte, 0, 8+len(ipStr)+2)
	data = append(data, 0x01, 0x38, 0x00, 0x13, 0x80, 0x00) // Phone
	data = append(data, 1)                                   // LogicChannel
	data = append(data, 0)                                   // Result
	data = append(data, []byte(ipStr)...)                    // IP
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 9000)
	data = append(data, portBytes...) // Port

	m := &PlatformNegotiateResponse{}
	if err := m.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Port != 9000 {
		t.Errorf("Port = %d, want 9000", m.Port)
	}
	if m.IPAddress != ipStr {
		t.Errorf("IPAddress = %q, want %q", m.IPAddress, ipStr)
	}
}

// TestP1_PlatformNegotiateResponse_MarshalUnmarshal_RoundTrip 验证往返一致。
func TestP1_PlatformNegotiateResponse_MarshalUnmarshal_RoundTrip(t *testing.T) {
	original := &PlatformNegotiateResponse{
		Phone:        "13800138000",
		LogicChannel: 1,
		Result:       0,
		IPAddress:    "10.0.0.1",
		Port:         8080,
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed := &PlatformNegotiateResponse{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.Port != original.Port {
		t.Errorf("Port = %d, want %d", parsed.Port, original.Port)
	}
	if parsed.Result != original.Result {
		t.Errorf("Result = %d, want %d", parsed.Result, original.Result)
	}
}
