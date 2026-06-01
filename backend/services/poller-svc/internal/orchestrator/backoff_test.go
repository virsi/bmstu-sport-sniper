package orchestrator

import (
	"sync"
	"testing"
	"time"
)

func TestBackoff_NoFailureNoSkip(t *testing.T) {
	b := NewBackoff(BackoffConfig{Initial: time.Second, Max: time.Minute, Factor: 2.0})
	if b.ShouldSkip("u1") {
		t.Fatal("свежий юзер не должен скипаться")
	}
}

func TestBackoff_RegisterAndSkip(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBackoff(BackoffConfig{Initial: 10 * time.Second, Max: time.Minute, Factor: 2.0})
	b.now = func() time.Time { return now }

	b.RegisterFailure("u1")
	if !b.ShouldSkip("u1") {
		t.Fatal("сразу после ошибки должен скипаться")
	}
	// Через 9s — всё ещё нет.
	b.now = func() time.Time { return now.Add(9 * time.Second) }
	if !b.ShouldSkip("u1") {
		t.Fatal("через 9s должен скипаться (initial=10s)")
	}
	// Через 11s — прошёл.
	b.now = func() time.Time { return now.Add(11 * time.Second) }
	if b.ShouldSkip("u1") {
		t.Fatal("через 11s не должен скипаться")
	}
}

func TestBackoff_ExponentialGrowth(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBackoff(BackoffConfig{Initial: time.Second, Max: time.Hour, Factor: 2.0})
	b.now = func() time.Time { return now }

	// 1 ошибка → 1s wait
	b.RegisterFailure("u")
	// 2 ошибки → 2s
	now = now.Add(time.Second + time.Millisecond)
	b.now = func() time.Time { return now }
	b.RegisterFailure("u")
	if !b.ShouldSkip("u") {
		t.Fatal("после 2-й ошибки ожидаем скип в течение 2s")
	}
	now = now.Add(time.Second)
	b.now = func() time.Time { return now }
	if !b.ShouldSkip("u") {
		t.Fatal("через 1s от 2-й ошибки ещё скипаемся (нужно 2s)")
	}
	now = now.Add(2 * time.Second)
	b.now = func() time.Time { return now }
	if b.ShouldSkip("u") {
		t.Fatal("через 3s от 2-й ошибки уже не скипаемся")
	}
}

func TestBackoff_MaxCap(t *testing.T) {
	b := NewBackoff(BackoffConfig{Initial: time.Second, Max: 4 * time.Second, Factor: 10.0})
	// errCount=3 → 1 * 100 = 100s, но Max=4s.
	if got := b.nextDelay(3); got != 4*time.Second {
		t.Errorf("ожидаем Max=4s, got=%v", got)
	}
}

func TestBackoff_Reset(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBackoff(BackoffConfig{Initial: time.Minute, Max: time.Hour, Factor: 2.0})
	b.now = func() time.Time { return now }
	b.RegisterFailure("u")
	if !b.ShouldSkip("u") {
		t.Fatal("должен скипаться сразу после ошибки")
	}
	b.Reset("u")
	if b.ShouldSkip("u") {
		t.Fatal("после Reset не должен скипаться")
	}
	count, _, ok := b.Snapshot("u")
	if !ok || count != 0 {
		t.Fatalf("ожидаем errCount=0 после Reset, got=(%d, %v)", count, ok)
	}
}

func TestBackoff_ParallelSafety(t *testing.T) {
	b := NewBackoff(DefaultBackoff())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.RegisterFailure("u")
			_ = b.ShouldSkip("u")
		}()
	}
	wg.Wait()
	count, _, ok := b.Snapshot("u")
	if !ok || count != 50 {
		t.Errorf("ожидаем errCount=50, got=(%d, %v)", count, ok)
	}
}
