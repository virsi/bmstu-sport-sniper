// Package orchestrator — ядро poller-svc: тикер, опрос юзеров,
// circuit-breaker для bmstu, экспоненциальный backoff per-user.
package orchestrator

import (
	"math"
	"sync"
	"time"
)

// BackoffConfig — параметры экспоненциального backoff'а.
type BackoffConfig struct {
	// Initial — стартовая пауза после первой ошибки.
	Initial time.Duration
	// Max — верхняя граница паузы.
	Max time.Duration
	// Factor — множитель экспоненты (обычно 2.0).
	Factor float64
}

// DefaultBackoff — разумные дефолты: 30s → 30m (max).
func DefaultBackoff() BackoffConfig {
	return BackoffConfig{
		Initial: 30 * time.Second,
		Max:     30 * time.Minute,
		Factor:  2.0,
	}
}

// Backoff — потокобезопасный регистр backoff-состояний по userID.
//
// Хранит счётчик подряд-идущих ошибок и timestamp последней ошибки. Reset
// сбрасывается на успешном проходе цикла.
type Backoff struct {
	cfg   BackoffConfig
	now   func() time.Time
	state sync.Map // userID -> *entry
}

type entry struct {
	mu        sync.Mutex
	errCount  int
	lastError time.Time
}

// NewBackoff создаёт регистр.
func NewBackoff(cfg BackoffConfig) *Backoff {
	if cfg.Initial <= 0 {
		cfg.Initial = 30 * time.Second
	}
	if cfg.Max <= 0 {
		cfg.Max = 30 * time.Minute
	}
	if cfg.Factor < 1.0 {
		cfg.Factor = 2.0
	}
	return &Backoff{cfg: cfg, now: time.Now}
}

// RegisterFailure инкрементит счётчик и обновляет lastError.
func (b *Backoff) RegisterFailure(userID string) {
	e := b.entryFor(userID)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errCount++
	e.lastError = b.now()
}

// Reset обнуляет backoff после успешного цикла.
func (b *Backoff) Reset(userID string) {
	e := b.entryFor(userID)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errCount = 0
	e.lastError = time.Time{}
}

// ShouldSkip возвращает true, если юзера сейчас нужно пропустить (ещё не
// прошёл backoff-интервал от последней ошибки).
func (b *Backoff) ShouldSkip(userID string) bool {
	e, ok := b.lookup(userID)
	if !ok {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.errCount == 0 {
		return false
	}
	wait := b.nextDelay(e.errCount)
	return b.now().Before(e.lastError.Add(wait))
}

// Snapshot возвращает копию текущего состояния по userID (диагностика).
func (b *Backoff) Snapshot(userID string) (errCount int, lastError time.Time, ok bool) {
	e, ok := b.lookup(userID)
	if !ok {
		return 0, time.Time{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.errCount, e.lastError, true
}

func (b *Backoff) lookup(userID string) (*entry, bool) {
	v, ok := b.state.Load(userID)
	if !ok {
		return nil, false
	}
	e, _ := v.(*entry)
	return e, true
}

func (b *Backoff) entryFor(userID string) *entry {
	v, _ := b.state.LoadOrStore(userID, &entry{})
	e, _ := v.(*entry)
	return e
}

// nextDelay = Initial * Factor^(errCount-1), capped Max.
func (b *Backoff) nextDelay(errCount int) time.Duration {
	if errCount <= 1 {
		return b.cfg.Initial
	}
	mult := math.Pow(b.cfg.Factor, float64(errCount-1))
	d := time.Duration(float64(b.cfg.Initial) * mult)
	if d > b.cfg.Max || d < 0 {
		return b.cfg.Max
	}
	return d
}
