package gateway

import (
	"sync"
	"testing"
)

// AUTO-FIX-2026-06-29 [P1]: 测试 SeqDedup 环形缓冲区去重器。
// 覆盖：首次见、重复检测、环形淘汰、并发安全、Session 集成。

// TestSeqDedup_FirstSeen 验证首次见到的 SeqNum 不算重复。
func TestSeqDedup_FirstSeen(t *testing.T) {
	d := NewSeqDedup(100)
	if d.IsDuplicate(1) {
		t.Error("seq 1 should not be duplicate on first call")
	}
	if d.IsDuplicate(2) {
		t.Error("seq 2 should not be duplicate on first call")
	}
}

// TestSeqDedup_Duplicate 验证重复的 SeqNum 被正确检测。
func TestSeqDedup_Duplicate(t *testing.T) {
	d := NewSeqDedup(100)
	d.IsDuplicate(42)
	if !d.IsDuplicate(42) {
		t.Error("seq 42 should be duplicate on second call")
	}
}

// TestSeqDedup_Eviction 验证环形缓冲区满后淘汰旧值，被淘汰的 SeqNum 不再算重复。
//
// 注意：IsDuplicate 对"非重复"的 SeqNum 有副作用（会写入并淘汰旧值），
// 因此验证缓冲区状态使用纯读取方法 Contains，避免检查动作本身改变状态。
func TestSeqDedup_Eviction(t *testing.T) {
	d := NewSeqDedup(3) // 小容量便于测试淘汰

	d.IsDuplicate(1)
	d.IsDuplicate(2)
	d.IsDuplicate(3)

	// 缓冲区已满（容量 3），写入 4 淘汰 1
	d.IsDuplicate(4)

	// 用 Contains 纯读取验证缓冲区状态（IsDuplicate 对非重复值有记录副作用）
	if d.Contains(1) {
		t.Error("seq 1 should have been evicted")
	}
	if !d.Contains(2) {
		t.Error("seq 2 should still be in buffer")
	}
	if !d.Contains(3) {
		t.Error("seq 3 should still be in buffer")
	}
	if !d.Contains(4) {
		t.Error("seq 4 should still be in buffer")
	}
	if d.Size() != 3 {
		t.Errorf("Size = %d, want 3", d.Size())
	}
}

// TestSeqDedup_EvictionWrapAround 验证环形缓冲区 head 回绕后的淘汰逻辑。
//
// 注意：用 Contains 验证最终缓冲区状态，避免 IsDuplicate 的记录副作用干扰。
func TestSeqDedup_EvictionWrapAround(t *testing.T) {
	d := NewSeqDedup(3)

	// 填满并覆写多轮，验证 head 回绕
	for i := 1; i <= 10; i++ {
		d.IsDuplicate(uint16(i))
	}

	// 只有最后 3 个（8, 9, 10）在缓冲区
	for _, seq := range []uint16{8, 9, 10} {
		if !d.Contains(seq) {
			t.Errorf("seq %d should still be in buffer", seq)
		}
	}
	// 1-7 应已被淘汰
	for _, seq := range []uint16{1, 2, 3, 4, 5, 6, 7} {
		if d.Contains(seq) {
			t.Errorf("seq %d should have been evicted", seq)
		}
	}
	if d.Size() != 3 {
		t.Errorf("Size = %d, want 3", d.Size())
	}
}

// TestSeqDedup_Concurrent 验证并发调用线程安全（不会 panic 或数据竞争）。
func TestSeqDedup_Concurrent(t *testing.T) {
	d := NewSeqDedup(1000)
	var wg sync.WaitGroup

	// 100 个 goroutine 各发 100 个 SeqNum（有重叠）
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				d.IsDuplicate(uint16(offset*100 + i))
			}
		}(g)
	}
	wg.Wait()

	// 验证最终状态一致（10000 个唯一 SeqNum，但缓冲区只保留 1000 个）
	if d.Size() > 1000 {
		t.Errorf("Size = %d, should not exceed 1000", d.Size())
	}
}

// TestSeqDedup_DefaultSize 验证 size <= 0 时使用默认值 200。
func TestSeqDedup_DefaultSize(t *testing.T) {
	d := NewSeqDedup(0)
	for i := 0; i < 300; i++ {
		d.IsDuplicate(uint16(i))
	}
	// 默认容量 200，所以 Size 不应超过 200
	if d.Size() > 200 {
		t.Errorf("Size = %d, should not exceed default 200", d.Size())
	}
}

// TestSession_CheckDuplicate 验证 Session.CheckDuplicate 的懒初始化和去重逻辑。
func TestSession_CheckDuplicate(t *testing.T) {
	s := &Session{}

	// 首次调用触发懒初始化
	if s.CheckDuplicate(100) {
		t.Error("seq 100 should not be duplicate on first call")
	}

	// 第二次调用相同 SeqNum 应为重复
	if !s.CheckDuplicate(100) {
		t.Error("seq 100 should be duplicate on second call")
	}

	// 不同 SeqNum 不算重复
	if s.CheckDuplicate(200) {
		t.Error("seq 200 should not be duplicate")
	}
}

// TestSession_CheckDuplicate_Concurrent 验证多个 goroutine 并发调用 CheckDuplicate 的线程安全。
func TestSession_CheckDuplicate_Concurrent(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup

	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.CheckDuplicate(uint16(offset*50 + i))
			}
		}(g)
	}
	wg.Wait()

	// 不应 panic 或 data race（go test -race 会检测）
}
