package ticket_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fizcultor/backend/services/gateway-svc/internal/ticket"
)

func TestStore_IssueAndRedeem(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)

	tk, exp := s.Issue("user-42")
	require.NotEmpty(t, tk, "ticket should not be empty")
	assert.True(t, exp.After(time.Now()), "expires_at should be in the future")

	userID, err := s.Redeem(tk)
	require.NoError(t, err)
	assert.Equal(t, "user-42", userID)
}

func TestStore_OneShot_SecondRedeemFails(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)

	tk, _ := s.Issue("u")
	_, err := s.Redeem(tk)
	require.NoError(t, err)

	_, err = s.Redeem(tk)
	assert.ErrorIs(t, err, ticket.ErrInvalidTicket, "second redeem must fail")
}

func TestStore_EmptyTicket(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)
	_, err := s.Redeem("")
	assert.ErrorIs(t, err, ticket.ErrInvalidTicket)
}

func TestStore_UnknownTicket(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)
	_, err := s.Redeem("totally-not-a-ticket")
	assert.ErrorIs(t, err, ticket.ErrInvalidTicket)
}

func TestStore_Expired(t *testing.T) {
	t.Parallel()
	// TTL=1ns ⇒ любой ticket мгновенно протух.
	s := ticket.New(time.Nanosecond)
	tk, _ := s.Issue("u")
	// Дать тики тикнуть.
	time.Sleep(2 * time.Millisecond)
	_, err := s.Redeem(tk)
	assert.ErrorIs(t, err, ticket.ErrInvalidTicket)
}

func TestStore_Concurrent_SingleWinner(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)
	tk, _ := s.Issue("u")

	var winners atomic.Int32
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := s.Redeem(tk); err == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), winners.Load(),
		"exactly one goroutine must succeed for a one-shot ticket")
}

func TestStore_ManyTickets_NoCollisions(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)
	const n = 5_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		tk, _ := s.Issue("u")
		if _, dup := seen[tk]; dup {
			t.Fatalf("duplicate ticket issued at i=%d", i)
		}
		seen[tk] = struct{}{}
	}
}

func TestStore_Cleanup_RemovesExpired(t *testing.T) {
	t.Parallel()
	// TTL очень короткий, чтобы cleanup тикнул быстро.
	s := ticket.New(10 * time.Millisecond)
	tk, _ := s.Issue("u")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Cleanup(ctx)

	// Дать cleanup-у проснуться минимум один раз (Ticker первый тик ровно через ttl).
	time.Sleep(50 * time.Millisecond)
	_, err := s.Redeem(tk)
	assert.ErrorIs(t, err, ticket.ErrInvalidTicket,
		"cleanup should have purged the expired ticket")
}

func TestStore_Cleanup_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Cleanup(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("Cleanup did not stop on context cancel within 1s")
	}
}

func TestStore_ZeroTTLDefault(t *testing.T) {
	t.Parallel()
	// New(0) ⇒ TTL по умолчанию 5 минут — Issue работает, ticket жив.
	s := ticket.New(0)
	tk, exp := s.Issue("u")
	require.NotEmpty(t, tk)
	assert.True(t, exp.After(time.Now().Add(4*time.Minute)),
		"default TTL should be ~5 minutes")
	_, err := s.Redeem(tk)
	require.NoError(t, err)
}

// guard: проверим, что не возвращаем не-sentinel из обычных fail-кейсов.
func TestStore_ErrIsSentinel(t *testing.T) {
	t.Parallel()
	s := ticket.New(time.Minute)
	_, err := s.Redeem("nope")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ticket.ErrInvalidTicket))
}
