package stats

import (
	"sync"
	"testing"
	"time"
)

func TestSnapshot_NoSideEffect(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("cmd_a", 1*time.Millisecond)
	c.Record("cmd_a", 3*time.Millisecond)

	now = base.Add(2 * time.Second)

	snap1 := c.Snapshot()
	snap2 := c.Snapshot()

	if snap1["cmd_a"].Last1s.Count != snap2["cmd_a"].Last1s.Count {
		t.Errorf("Snapshot idempotency broken: snap1 Count=%d, snap2 Count=%d",
			snap1["cmd_a"].Last1s.Count, snap2["cmd_a"].Last1s.Count)
	}
	if snap1["cmd_a"].Last1s.AvgNs != snap2["cmd_a"].Last1s.AvgNs {
		t.Errorf("Snapshot idempotency broken: snap1 AvgNs=%d, snap2 AvgNs=%d",
			snap1["cmd_a"].Last1s.AvgNs, snap2["cmd_a"].Last1s.AvgNs)
	}

	now = base.Add(5 * time.Second)
	snap3 := c.Snapshot()
	if snap3["cmd_a"].Last1s.Count != 0 {
		t.Errorf("Expected Last1s.Count=0 after time advance without Record, got %d",
			snap3["cmd_a"].Last1s.Count)
	}
	if snap3["cmd_a"].Last10s.Count != 2 {
		t.Errorf("Expected Last10s.Count=2 (still within 10s window), got %d",
			snap3["cmd_a"].Last10s.Count)
	}
}

func TestCommandEntry_Persists(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("cmd_a", 1*time.Millisecond)
	c.Record("cmd_b", 2*time.Millisecond)

	now = base.Add(2 * time.Second)
	c.Record("cmd_a", 1*time.Millisecond)

	now = base.Add(3 * time.Second)
	snap := c.Snapshot()

	if _, ok := snap["cmd_a"]; !ok {
		t.Fatal("cmd_a not found in snapshot")
	}
	if _, ok := snap["cmd_b"]; !ok {
		t.Fatal("cmd_b disappeared from snapshot")
	}
	if snap["cmd_b"].Last1s.Count != 0 {
		t.Errorf("cmd_b Last1s.Count = %d, want 0", snap["cmd_b"].Last1s.Count)
	}
	if snap["cmd_a"].Last1s.Count != 1 {
		t.Errorf("cmd_a Last1s.Count = %d, want 1", snap["cmd_a"].Last1s.Count)
	}
}

func TestWindowBoundary_Fixed(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 500*int(time.Millisecond), time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("cmd_a", 1*time.Millisecond)

	now = base.Add(1 * time.Second)
	c.Record("cmd_b", 2*time.Millisecond)

	now = base.Add(2 * time.Second)
	snap := c.Snapshot()

	if snap["cmd_a"].Last1s.Count != 0 {
		t.Errorf("cmd_a Last1s.Count = %d, want 0 (should be in second-1 slot)", snap["cmd_a"].Last1s.Count)
	}
	if snap["cmd_b"].Last1s.Count != 1 {
		t.Errorf("cmd_b Last1s.Count = %d, want 1 (should be in last completed slot)", snap["cmd_b"].Last1s.Count)
	}
	if snap["cmd_a"].Last10s.Count != 1 {
		t.Errorf("cmd_a Last10s.Count = %d, want 1", snap["cmd_a"].Last10s.Count)
	}
}

func TestMultiWindow_1s_10s_30s(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("op", 10*time.Millisecond)

	now = base.Add(5 * time.Second)
	c.Record("op", 20*time.Millisecond)

	now = base.Add(15 * time.Second)
	c.Record("op", 30*time.Millisecond)

	now = base.Add(16 * time.Second)
	snap := c.Snapshot()

	if snap["op"].Last1s.Count != 1 {
		t.Errorf("Last1s.Count = %d, want 1", snap["op"].Last1s.Count)
	}
	if snap["op"].Last1s.AvgNs != 30_000_000 {
		t.Errorf("Last1s.AvgNs = %d, want 30000000", snap["op"].Last1s.AvgNs)
	}

	if snap["op"].Last10s.Count != 1 {
		t.Errorf("Last10s.Count = %d, want 1 (only sec=15 is within 10s)", snap["op"].Last10s.Count)
	}

	if snap["op"].Last30s.Count != 3 {
		t.Errorf("Last30s.Count = %d, want 3", snap["op"].Last30s.Count)
	}
}

func TestMultiWindow_Rollover(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("op", 1*time.Millisecond)

	for i := 1; i <= 31; i++ {
		now = base.Add(time.Duration(i) * time.Second)
		c.Record("op", 2*time.Millisecond)
	}

	now = base.Add(32 * time.Second)
	snap := c.Snapshot()

	if snap["op"].Last30s.AvgNs == 1_000_000 {
		t.Error("First record (1ms) should have been evicted from 30s window")
	}
	if snap["op"].Last30s.Count != 30 {
		t.Errorf("Last30s.Count = %d, want 30", snap["op"].Last30s.Count)
	}
}

func TestCollectorConcurrency(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				c.Record("concurrent_cmd", 1*time.Microsecond)
			}
		}()
	}
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = c.Snapshot()
			}
		}()
	}
	wg.Wait()
}

func TestSnapshotEmpty(t *testing.T) {
	c := NewCollector()
	snap := c.Snapshot()
	if len(snap) != 0 {
		t.Errorf("expected empty snapshot, got %d entries", len(snap))
	}
}

func TestAdvanceLargeGap(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("op", 5*time.Millisecond)

	now = base.Add(45 * time.Second)
	c.Record("op", 10*time.Millisecond)

	now = base.Add(46 * time.Second)
	snap := c.Snapshot()

	if snap["op"].Last1s.Count != 1 {
		t.Errorf("Last1s.Count = %d, want 1", snap["op"].Last1s.Count)
	}
	if snap["op"].Last1s.AvgNs != 10_000_000 {
		t.Errorf("Last1s.AvgNs = %d, want 10000000", snap["op"].Last1s.AvgNs)
	}
	if snap["op"].Last30s.Count != 1 {
		t.Errorf("Last30s.Count = %d, want 1 (old data evicted by 45s gap)", snap["op"].Last30s.Count)
	}
}

func TestEMA_FirstRecord(t *testing.T) {
	c := NewCollector()
	c.Record("cmd_a", 1*time.Millisecond)
	snap := c.Snapshot()
	if snap["cmd_a"].EmaNs != 1_000_000 {
		t.Errorf("EmaNs = %d, want 1000000", snap["cmd_a"].EmaNs)
	}
}

func TestEMA_DecayUpdate(t *testing.T) {
	c := NewCollector()
	c.Record("cmd_a", 1*time.Millisecond)
	c.Record("cmd_a", 3*time.Millisecond)
	snap := c.Snapshot()
	expected := int64(0.95*1_000_000 + 0.05*3_000_000)
	if snap["cmd_a"].EmaNs != expected {
		t.Errorf("EmaNs = %d, want %d", snap["cmd_a"].EmaNs, expected)
	}
}

func TestEMA_Persists_NoRecord(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("cmd_a", 1*time.Millisecond)
	snap1 := c.Snapshot()
	emaBefore := snap1["cmd_a"].EmaNs

	now = base.Add(35 * time.Second)
	snap2 := c.Snapshot()
	if snap2["cmd_a"].Last30s.Count != 0 {
		t.Errorf("Last30s.Count should be 0 after 35s, got %d", snap2["cmd_a"].Last30s.Count)
	}
	if snap2["cmd_a"].EmaNs != emaBefore {
		t.Errorf("EmaNs changed from %d to %d without Record", emaBefore, snap2["cmd_a"].EmaNs)
	}
}

func TestEMA_InSnapshot(t *testing.T) {
	c := NewCollector()
	c.Record("cmd_a", 500*time.Microsecond)
	snap := c.Snapshot()
	if snap["cmd_a"].EmaNs <= 0 {
		t.Errorf("cmd_a EmaNs should be > 0, got %d", snap["cmd_a"].EmaNs)
	}
}

func TestAggregateAvgAndMax(t *testing.T) {
	c := NewCollector()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c.SetNowFunc(func() time.Time { return now })

	c.Record("op", 100*time.Microsecond)
	c.Record("op", 500*time.Microsecond)

	now = base.Add(1 * time.Second)
	c.Record("op", 200*time.Microsecond)
	c.Record("op", 300*time.Microsecond)
	c.Record("op", 800*time.Microsecond)

	now = base.Add(2 * time.Second)
	snap := c.Snapshot()

	if snap["op"].Last1s.Count != 3 {
		t.Errorf("Last1s.Count = %d, want 3", snap["op"].Last1s.Count)
	}
	expectedAvg1s := int64((200_000 + 300_000 + 800_000) / 3)
	if snap["op"].Last1s.AvgNs != expectedAvg1s {
		t.Errorf("Last1s.AvgNs = %d, want %d", snap["op"].Last1s.AvgNs, expectedAvg1s)
	}
	if snap["op"].Last1s.MaxNs != 800_000 {
		t.Errorf("Last1s.MaxNs = %d, want 800000", snap["op"].Last1s.MaxNs)
	}

	if snap["op"].Last10s.Count != 5 {
		t.Errorf("Last10s.Count = %d, want 5", snap["op"].Last10s.Count)
	}
	totalNs := int64(100_000 + 500_000 + 200_000 + 300_000 + 800_000)
	expectedAvg10s := totalNs / 5
	if snap["op"].Last10s.AvgNs != expectedAvg10s {
		t.Errorf("Last10s.AvgNs = %d, want %d", snap["op"].Last10s.AvgNs, expectedAvg10s)
	}
	if snap["op"].Last10s.MaxNs != 800_000 {
		t.Errorf("Last10s.MaxNs = %d, want 800000", snap["op"].Last10s.MaxNs)
	}
}
