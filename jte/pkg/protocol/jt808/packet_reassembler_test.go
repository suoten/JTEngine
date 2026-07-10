package jt808

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/protocol"
)

// makeFragHeader 构造分片消息头辅助函数
func makeFragHeader(phone string, msgID, seqNum, packTotal, packIndex uint16) *protocol.MessageHeader {
	return &protocol.MessageHeader{
		MsgID:     msgID,
		Phone:     phone,
		SeqNum:    seqNum,
		PackTotal: packTotal,
		PackIndex: packIndex,
		HasPack:   true,
	}
}

// TestPacketReassembler_NonFragmentedMessage 非分片消息应直接返回原始 body
func TestPacketReassembler_NonFragmentedMessage(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	body := []byte{0x01, 0x02, 0x03}
	header := &protocol.MessageHeader{
		MsgID:   0x0200,
		Phone:   "13800138000",
		SeqNum:  1,
		HasPack: false,
	}

	got, complete, err := r.Feed(header, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("non-fragmented message should be complete immediately")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("got %v, want %v", got, body)
	}
	if r.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d, want 0", r.PendingCount())
	}
}

// TestPacketReassembler_InOrderFragments 按顺序投递分片应正确重组
func TestPacketReassembler_InOrderFragments(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13800138000"
	const msgID uint16 = 0x0704 // 批量位置上报
	const seqNum uint16 = 100
	const total uint16 = 3

	fragments := [][]byte{
		{0x01, 0x02},
		{0x03, 0x04, 0x05},
		{0x06},
	}
	var expected []byte
	for _, f := range fragments {
		expected = append(expected, f...)
	}

	for i := uint16(0); i < total; i++ {
		got, complete, err := r.Feed(makeFragHeader(phone, msgID, seqNum, total, i), fragments[i])
		if err != nil {
			t.Fatalf("fragment %d: unexpected error: %v", i, err)
		}
		if i < total-1 {
			if complete {
				t.Fatalf("fragment %d: should not be complete yet", i)
			}
			if got != nil {
				t.Fatalf("fragment %d: got non-nil body before completion", i)
			}
		} else {
			if !complete {
				t.Fatal("last fragment: should be complete")
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("reassembled body = %v, want %v", got, expected)
			}
		}
	}

	// 收齐后组应被清理
	if r.PendingCount() != 0 {
		t.Fatalf("PendingCount after completion = %d, want 0", r.PendingCount())
	}
}

// TestPacketReassembler_OutOfOrderFragments 乱序投递分片仍应正确按 PackIndex 重组
func TestPacketReassembler_OutOfOrderFragments(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13900139000"
	const msgID uint16 = 0x0704
	const seqNum uint16 = 200
	const total uint16 = 4

	// 按 2,0,3,1 顺序投递
	fragments := map[uint16][]byte{
		0: {0xAA},
		1: {0xBB, 0xBB},
		2: {0xCC, 0xCC, 0xCC},
		3: {0xDD},
	}
	order := []uint16{2, 0, 3, 1}
	expected := []byte{0xAA, 0xBB, 0xBB, 0xCC, 0xCC, 0xCC, 0xDD}

	var lastComplete bool
	var lastBody []byte
	for _, idx := range order {
		var err error
		lastBody, lastComplete, err = r.Feed(makeFragHeader(phone, msgID, seqNum, total, idx), fragments[idx])
		if err != nil {
			t.Fatalf("fragment %d: unexpected error: %v", idx, err)
		}
	}

	if !lastComplete {
		t.Fatal("should be complete after all fragments")
	}
	if !bytes.Equal(lastBody, expected) {
		t.Fatalf("reassembled body = %v, want %v", lastBody, expected)
	}
}

// TestPacketReassembler_DuplicateFragmentIgnored 重复分片应被忽略，不影响重组
func TestPacketReassembler_DuplicateFragmentIgnored(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13700137000"
	const msgID uint16 = 0x0704
	const seqNum uint16 = 300
	const total uint16 = 2

	// 投递分片 0 两次
	body0 := []byte{0x01}
	if _, complete, err := r.Feed(makeFragHeader(phone, msgID, seqNum, total, 0), body0); err != nil || complete {
		t.Fatalf("first fragment 0: err=%v complete=%v", err, complete)
	}
	if _, complete, err := r.Feed(makeFragHeader(phone, msgID, seqNum, total, 0), body0); err != nil || complete {
		t.Fatalf("duplicate fragment 0: err=%v complete=%v", err, complete)
	}

	// 投递分片 1，应收齐
	body1 := []byte{0x02}
	got, complete, err := r.Feed(makeFragHeader(phone, msgID, seqNum, total, 1), body1)
	if err != nil {
		t.Fatalf("fragment 1: unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("should be complete after fragment 1")
	}
	expected := []byte{0x01, 0x02}
	if !bytes.Equal(got, expected) {
		t.Fatalf("reassembled body = %v, want %v", got, expected)
	}
}

// TestPacketReassembler_MultipleGroupsConcurrent 多组分片并发投递互不干扰
func TestPacketReassembler_MultipleGroupsConcurrent(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)

	// 两组：设备 A 的消息 X 和设备 B 的消息 Y
	const total uint16 = 2
	groups := []struct {
		phone  string
		msgID  uint16
		seqNum uint16
		data   [][]byte
	}{
		{"13800000001", 0x0704, 1, [][]byte{{0xA1}, {0xA2}}},
		{"13800000002", 0x0704, 1, [][]byte{{0xB1}, {0xB2}}},
	}

	// 交替投递两组的分片 0
	for _, g := range groups {
		if _, _, err := r.Feed(makeFragHeader(g.phone, g.msgID, g.seqNum, total, 0), g.data[0]); err != nil {
			t.Fatalf("group %s: fragment 0 error: %v", g.phone, err)
		}
	}
	if r.PendingCount() != 2 {
		t.Fatalf("PendingCount = %d, want 2", r.PendingCount())
	}

	// 交替投递两组的分片 1
	results := make(map[string][]byte)
	for _, g := range groups {
		got, complete, err := r.Feed(makeFragHeader(g.phone, g.msgID, g.seqNum, total, 1), g.data[1])
		if err != nil {
			t.Fatalf("group %s: fragment 1 error: %v", g.phone, err)
		}
		if !complete {
			t.Fatalf("group %s: should be complete", g.phone)
		}
		results[g.phone] = got
	}

	expected := map[string][]byte{
		"13800000001": {0xA1, 0xA2},
		"13800000002": {0xB1, 0xB2},
	}
	for phone, want := range expected {
		if !bytes.Equal(results[phone], want) {
			t.Fatalf("phone %s: got %v, want %v", phone, results[phone], want)
		}
	}
	if r.PendingCount() != 0 {
		t.Fatalf("PendingCount after all complete = %d, want 0", r.PendingCount())
	}
}

// TestPacketReassembler_InvalidPackTotalZero PackTotal=0 应返回错误
func TestPacketReassembler_InvalidPackTotalZero(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	header := makeFragHeader("13800138000", 0x0704, 1, 0, 0)
	_, _, err := r.Feed(header, []byte{0x01})
	if err == nil {
		t.Fatal("expected error for PackTotal=0, got nil")
	}
}

// TestPacketReassembler_PackIndexOutOfRange PackIndex >= PackTotal 应返回错误
func TestPacketReassembler_PackIndexOutOfRange(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	header := makeFragHeader("13800138000", 0x0704, 1, 2, 5) // index 5 >= total 2
	_, _, err := r.Feed(header, []byte{0x01})
	if err == nil {
		t.Fatal("expected error for PackIndex out of range, got nil")
	}
}

// TestPacketReassembler_PartialFragmentsNotComplete 部分分片投递后未收齐应返回未完成
func TestPacketReassembler_PartialFragmentsNotComplete(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	const total uint16 = 5

	// 仅投递 3/5 个分片
	for i := uint16(0); i < 3; i++ {
		got, complete, err := r.Feed(makeFragHeader("13800138000", 0x0704, 1, total, i), []byte{byte(i)})
		if err != nil {
			t.Fatalf("fragment %d: error: %v", i, err)
		}
		if complete {
			t.Fatalf("fragment %d: should not be complete", i)
		}
		if got != nil {
			t.Fatalf("fragment %d: got non-nil body", i)
		}
	}
	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1", r.PendingCount())
	}
}

// TestPacketReassembler_BodyDeepCopy 投递后修改原 body slice 不应影响重组结果
func TestPacketReassembler_BodyDeepCopy(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	const total uint16 = 2

	// 投递分片 0
	body0 := []byte{0x01, 0x02}
	if _, _, err := r.Feed(makeFragHeader("13800138000", 0x0704, 1, total, 0), body0); err != nil {
		t.Fatalf("fragment 0: error: %v", err)
	}

	// 修改原 body0
	body0[0] = 0xFF
	body0[1] = 0xFF

	// 投递分片 1 并检查重组结果（分片 0 应保持原值）
	got, complete, err := r.Feed(makeFragHeader("13800138000", 0x0704, 1, total, 1), []byte{0x03})
	if err != nil {
		t.Fatalf("fragment 1: error: %v", err)
	}
	if !complete {
		t.Fatal("should be complete")
	}
	expected := []byte{0x01, 0x02, 0x03}
	if !bytes.Equal(got, expected) {
		t.Fatalf("reassembled body = %v, want %v (deep copy failed)", got, expected)
	}
}

// TestPacketReassembler_ExpiryCleanup 过期分片组应被自动清理
func TestPacketReassembler_ExpiryCleanup(t *testing.T) {
	// 使用短 expiry 加速测试
	r := NewPacketReassembler(200 * time.Millisecond)
	const total uint16 = 3

	// 投递 1/3 个分片
	if _, _, err := r.Feed(makeFragHeader("13800138000", 0x0704, 1, total, 0), []byte{0x01}); err != nil {
		t.Fatalf("fragment 0: error: %v", err)
	}
	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1", r.PendingCount())
	}

	// 立即调用 Cleanup，组尚未过期（createdAt 刚刚），不应被清理
	if removed := r.Cleanup(); removed != 0 {
		t.Fatalf("immediate Cleanup removed = %d, want 0 (group not yet expired)", removed)
	}
	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount after immediate cleanup = %d, want 1", r.PendingCount())
	}

	// 等待过期（expiry=200ms）
	time.Sleep(250 * time.Millisecond)

	// 手动触发清理，避免依赖 cleanupLoop 的 1s ticker 时序（短 expiry 下 ticker=1s）
	if removed := r.Cleanup(); removed != 1 {
		t.Fatalf("Cleanup after expiry removed = %d, want 1", removed)
	}
	if r.PendingCount() != 0 {
		t.Fatalf("PendingCount after expiry = %d, want 0 (group should be cleaned up)", r.PendingCount())
	}
}

// TestPacketReassembler_SamePhoneDifferentMsgID 同一设备不同消息ID的分片应分组独立
func TestPacketReassembler_SamePhoneDifferentMsgID(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13800138000"
	const total uint16 = 2

	// 消息 A 的分片 0
	if _, _, err := r.Feed(makeFragHeader(phone, 0x0704, 1, total, 0), []byte{0xA1}); err != nil {
		t.Fatalf("msg A frag 0: error: %v", err)
	}
	// 消息 B 的分片 0（同设备同流水号，不同消息ID）
	if _, _, err := r.Feed(makeFragHeader(phone, 0x0900, 1, total, 0), []byte{0xB1}); err != nil {
		t.Fatalf("msg B frag 0: error: %v", err)
	}
	if r.PendingCount() != 2 {
		t.Fatalf("PendingCount = %d, want 2 (different msgID = different groups)", r.PendingCount())
	}

	// 完成消息 A
	gotA, completeA, err := r.Feed(makeFragHeader(phone, 0x0704, 1, total, 1), []byte{0xA2})
	if err != nil || !completeA {
		t.Fatalf("msg A frag 1: err=%v complete=%v", err, completeA)
	}
	if !bytes.Equal(gotA, []byte{0xA1, 0xA2}) {
		t.Fatalf("msg A body = %v, want [A1 A2]", gotA)
	}

	// 消息 B 仍 pending
	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1", r.PendingCount())
	}
}

// TestPacketReassembler_SamePhoneDifferentSeqNum 同一设备同消息ID不同流水号的分片应分组独立
func TestPacketReassembler_SamePhoneDifferentSeqNum(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13800138000"
	const msgID uint16 = 0x0704
	const total uint16 = 2

	// 流水号 1 的分片 0
	if _, _, err := r.Feed(makeFragHeader(phone, msgID, 1, total, 0), []byte{0x01}); err != nil {
		t.Fatalf("seq 1 frag 0: error: %v", err)
	}
	// 流水号 2 的分片 0（同设备同消息ID，不同流水号）
	if _, _, err := r.Feed(makeFragHeader(phone, msgID, 2, total, 0), []byte{0x02}); err != nil {
		t.Fatalf("seq 2 frag 0: error: %v", err)
	}
	if r.PendingCount() != 2 {
		t.Fatalf("PendingCount = %d, want 2 (different seqNum = different groups)", r.PendingCount())
	}
}

// TestPacketReassembler_ConcurrentFeed 并发投递不同组的分片应线程安全
func TestPacketReassembler_ConcurrentFeed(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	const total uint16 = 3
	const numGoroutines = 50

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)
	completions := make(chan []byte, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			phone := fmt.Sprintf("138%08d", idx)
			for j := uint16(0); j < total; j++ {
				got, complete, err := r.Feed(
					makeFragHeader(phone, 0x0704, 1, total, j),
					[]byte{byte(idx), byte(j)},
				)
				if err != nil {
					errors <- fmt.Errorf("goroutine %d frag %d: %w", idx, j, err)
					return
				}
				if complete {
					completions <- got
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	close(completions)

	for err := range errors {
		t.Errorf("concurrent error: %v", err)
	}

	completedCount := 0
	for range completions {
		completedCount++
	}
	if completedCount != numGoroutines {
		t.Fatalf("completed count = %d, want %d", completedCount, numGoroutines)
	}
	if r.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d, want 0 after all complete", r.PendingCount())
	}
}

// TestPacketReassembler_LargeFragmentCount 大分片数（0x0704 批量位置场景）应正确重组
func TestPacketReassembler_LargeFragmentCount(t *testing.T) {
	r := NewPacketReassembler(60 * time.Second)
	phone := "13800138000"
	const msgID uint16 = 0x0704
	const seqNum uint16 = 500
	const total uint16 = 100 // 100 个分片

	// 每个分片 1 字节，值为分片索引
	expected := make([]byte, total)
	for i := uint16(0); i < total; i++ {
		expected[i] = byte(i % 256)
	}

	// 随机顺序投递（倒序）
	for i := total - 1; i < total; i-- {
		got, complete, err := r.Feed(
			makeFragHeader(phone, msgID, seqNum, total, i),
			[]byte{byte(i % 256)},
		)
		if err != nil {
			t.Fatalf("fragment %d: error: %v", i, err)
		}
		if i > 0 && complete {
			t.Fatalf("fragment %d: should not be complete yet", i)
		}
		if i == 0 {
			if !complete {
				t.Fatal("last fragment: should be complete")
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("reassembled body length = %d, want %d", len(got), len(expected))
			}
		}
	}
}

// TestPacketReassembler_GroupKeyFormat 验证 groupKey 格式
func TestPacketReassembler_GroupKeyFormat(t *testing.T) {
	key := groupKey("13800138000", 0x0704, 42)
	// 格式: phone_MSGID(4位十六进制大写)_seqNum
	expected := "13800138000_0704_42"
	if key != expected {
		t.Fatalf("groupKey = %q, want %q", key, expected)
	}
}
