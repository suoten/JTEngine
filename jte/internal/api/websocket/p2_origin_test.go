package websocket

import (
	"sync"
	"testing"
)

// [P2-1] TestOriginSet_ConcurrentReadWrite 验证 originSet 在并发 set/isAllowed 下不 panic。
// 使用 sync.WaitGroup 启动多个 goroutine 同时读写，确保 RWMutex 保护生效。
func TestOriginSet_ConcurrentReadWrite(t *testing.T) {
	o := newOriginSet()

	const numWriters = 10
	const numReaders = 50
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	// Writers: 并发调用 set()
	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			origins := []string{
				"http://localhost:3000",
				"http://example.com",
				"https://jte.example.com",
			}
			for j := 0; j < iterations; j++ {
				o.set(origins)
			}
		}(i)
	}

	// Readers: 并发调用 isAllowed()
	for i := 0; i < numReaders; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Should never panic with concurrent map access
				o.isAllowed("http://localhost:3000")
				o.isAllowed("http://evil.com")
				o.isAllowed("https://jte.example.com")
			}
		}(i)
	}

	wg.Wait()
	// If we reach here without panic, the test passes
}

// [P2-1] TestOriginSet_SetAndIsAllowed_Basic 验证基本功能正确性。
func TestOriginSet_SetAndIsAllowed_Basic(t *testing.T) {
	o := newOriginSet()

	// Before set: empty origins → allow all
	if !o.isAllowed("http://anything.com") {
		t.Fatal("empty originSet should allow all")
	}

	// Set specific origins
	o.set([]string{"http://localhost:3000", "https://jte.example.com"})

	if !o.isAllowed("http://localhost:3000") {
		t.Fatal("allowed origin should return true")
	}
	if !o.isAllowed("https://jte.example.com") {
		t.Fatal("allowed origin should return true")
	}
	if o.isAllowed("http://evil.com") {
		t.Fatal("disallowed origin should return false")
	}
}

// [P2-1] TestOriginSet_Wildcard 验证通配符 * 允许所有来源。
func TestOriginSet_Wildcard(t *testing.T) {
	o := newOriginSet()
	o.set([]string{"*"})

	if !o.isAllowed("http://anything.com") {
		t.Fatal("wildcard should allow all origins")
	}
	if !o.isAllowed("http://evil.com") {
		t.Fatal("wildcard should allow all origins")
	}
}

// [P2-1] TestOriginSet_ResetAllowAll 验证 set() 正确重置 allowAll 标志。
func TestOriginSet_ResetAllowAll(t *testing.T) {
	o := newOriginSet()

	// First set with wildcard
	o.set([]string{"*"})
	if !o.isAllowed("http://evil.com") {
		t.Fatal("wildcard should allow all")
	}

	// Then set without wildcard — allowAll should be reset
	o.set([]string{"http://localhost:3000"})
	if o.isAllowed("http://evil.com") {
		t.Fatal("after removing wildcard, unknown origins should be rejected")
	}
	if !o.isAllowed("http://localhost:3000") {
		t.Fatal("allowed origin should still work")
	}
}
