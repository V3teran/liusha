package proxy

import (
	"sync"
	"testing"
	"time"
)

func mkSnap(method, host, uri string, body []byte) *TrafficSnapshot {
	return &TrafficSnapshot{
		Method:      method,
		Host:        host,
		URI:         uri,
		RequestBody: body,
		Timestamp:   time.Now(),
	}
}

func TestDedup_Hash(t *testing.T) {
	d := NewTrafficDeduplicator()
	a := mkSnap("POST", "vulnapp", "/login", []byte(`{"u":"x"}`))
	b := mkSnap("POST", "vulnapp", "/login", []byte(`{"u":"x"}`))
	if d.CalculateHash(a) != d.CalculateHash(b) {
		t.Fatalf("相同输入应得同 hash")
	}
}

func TestDedup_DifferentBody(t *testing.T) {
	d := NewTrafficDeduplicator()
	a := mkSnap("POST", "vulnapp", "/login", []byte(`{"u":"x"}`))
	b := mkSnap("POST", "vulnapp", "/login", []byte(`{"u":"y"}`))
	if d.CalculateHash(a) == d.CalculateHash(b) {
		t.Fatalf("body 不同 hash 必须不同")
	}
}

func TestDedup_DifferentMethodHostURI(t *testing.T) {
	d := NewTrafficDeduplicator()
	base := mkSnap("GET", "vulnapp", "/api", nil)
	for _, mut := range []*TrafficSnapshot{
		mkSnap("POST", "vulnapp", "/api", nil),
		mkSnap("GET", "evil", "/api", nil),
		mkSnap("GET", "vulnapp", "/admin", nil),
	} {
		if d.CalculateHash(base) == d.CalculateHash(mut) {
			t.Fatalf("method/host/uri 任一变化应改变 hash; mut=%+v", mut)
		}
	}
}

func TestDedup_IsDuplicate_Within60s(t *testing.T) {
	d := NewTrafficDeduplicator()
	hash := "h-1"
	const window int64 = 60
	if d.IsDuplicate(hash, 1000, window) {
		t.Fatalf("首次见到不应判重")
	}
	if !d.IsDuplicate(hash, 1030, window) {
		t.Fatalf("60s 窗口内重复应返 true")
	}
	if !d.IsDuplicate(hash, 1059, window) {
		t.Fatalf("窗口边界内仍应判重")
	}
}

func TestDedup_IsDuplicate_Beyond60s(t *testing.T) {
	d := NewTrafficDeduplicator()
	hash := "h-2"
	const window int64 = 60
	if d.IsDuplicate(hash, 1000, window) {
		t.Fatalf("首次不应判重")
	}
	if d.IsDuplicate(hash, 1060, window) {
		t.Fatalf("超 60s 应当作新记录，返 false")
	}
	// 新窗口起点已被刷新到 1060
	if !d.IsDuplicate(hash, 1080, window) {
		t.Fatalf("新窗口内重复应判重")
	}
}

func TestDedup_Cleanup(t *testing.T) {
	d := NewTrafficDeduplicator()
	const window int64 = 60
	d.IsDuplicate("a", 1000, window)
	d.IsDuplicate("b", 1010, window)
	d.IsDuplicate("c", 1100, window)
	if d.Size() != 3 {
		t.Fatalf("Size want 3, got %d", d.Size())
	}
	// now=1100：a(Δ=100) b(Δ=90) 应被清；c(Δ=0) 留下
	cleaned := d.Cleanup(1100, window)
	if cleaned != 2 {
		t.Fatalf("cleaned=%d want 2", cleaned)
	}
	if d.Size() != 1 {
		t.Fatalf("剩余=%d want 1", d.Size())
	}
}

func TestDedup_Concurrent_NoRace(t *testing.T) {
	d := NewTrafficDeduplicator()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d.IsDuplicate("k", int64(1000+i), 60)
			_ = d.Size()
			_ = d.Cleanup(int64(2000+i), 60)
		}(i)
	}
	wg.Wait()
}
