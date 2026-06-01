// Package ticket — in-memory store одноразовых ticket-токенов для SSE-подключения.
//
// Зачем: EventSource не умеет ставить кастомные headers, поэтому исторически
// auth-токен передавался в query (`?access_token=`). JWT в query попадает в
// access-логи прокси/CDN/балансировщиков — это утечка долгоживущего токена
// (см. docs/review-findings.md #3). Решение — короткоживущий ticket с TTL 5 минут
// и одноразовым redeem: даже если ticket залогируется, он уже не работает.
//
// Тонкости:
//   - Хранилище — `sync.Map`, KISS-выбор: не тащим Redis ради одной ручки.
//     Trade-off: при горизонтальном масштабировании gateway-svc ticket, выпущенный
//     одной репликой, не валидируется другой. На прод сейчас одна реплика gateway,
//     этого хватает. См. docs/runbook.md для миграции на Redis.
//   - Cleanup: фоновая goroutine раз в TTL сканирует Map и выкидывает expired-записи.
//     При context.Done goroutine завершается.
//   - Random source — crypto/rand. 32 байта → 256 бит энтропии, столкновения
//     невозможны в обозримом будущем.
package ticket

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ticketSize — длина случайного payload-а ticket-а в байтах (256 бит).
const ticketSize = 32

// ErrInvalidTicket — sentinel: ticket не найден / уже использован / истёк.
//
// Caller (SSE-middleware) не должен различать причины — это не помогает атакующему,
// логика проще.
var ErrInvalidTicket = errors.New("ticket: invalid or expired")

// entry — хранится во внутренней map: за каким user-ом закреплён ticket
// и когда он истечёт.
type entry struct {
	userID    string
	expiresAt time.Time
}

// Store — in-memory ticket store.
//
// Безопасен для concurrent доступа. Создавать через New, не литералом
// (нужно проинициализировать ttl).
type Store struct {
	tickets sync.Map // string ticket → entry
	ttl     time.Duration
	now     func() time.Time
}

// New создаёт пустой Store с заданным TTL для каждого ticket.
//
// ttl <= 0 ⇒ значение по умолчанию 5 минут. Для тестов передавайте свой,
// иначе фоновая очистка будет редкой.
func New(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Store{
		ttl: ttl,
		now: time.Now,
	}
}

// Issue выпускает одноразовый ticket для userID.
//
// Возвращает base64url-encoded ticket (без padding, чтобы лучше уживался в URL)
// и момент истечения. Ticket уникален (256 бит энтропии).
//
// Если crypto/rand упадёт (исключительно редкая ситуация в Linux/macOS) —
// возвращает пустой ticket; caller проверяет на пустоту и отдаёт 500.
func (s *Store) Issue(userID string) (ticket string, expiresAt time.Time) {
	buf := make([]byte, ticketSize)
	if _, err := rand.Read(buf); err != nil {
		slog.Error("ticket: rand failed", slog.Any("error", err))
		return "", time.Time{}
	}
	ticket = base64.RawURLEncoding.EncodeToString(buf)
	expiresAt = s.now().Add(s.ttl)
	s.tickets.Store(ticket, entry{userID: userID, expiresAt: expiresAt})
	return ticket, expiresAt
}

// Redeem атомарно проверяет и удаляет ticket.
//
// Возвращает userID, к которому привязан ticket. Если ticket не найден,
// уже redeem-нут (один раз можно) или expired — возвращает ErrInvalidTicket.
//
// Гарантии:
//   - one-shot: повторный Redeem того же ticket вернёт ErrInvalidTicket.
//   - thread-safe: одновременные Redeem из двух goroutine — победит ровно один.
func (s *Store) Redeem(ticket string) (userID string, err error) {
	if ticket == "" {
		return "", ErrInvalidTicket
	}
	v, ok := s.tickets.LoadAndDelete(ticket)
	if !ok {
		return "", ErrInvalidTicket
	}
	e, ok := v.(entry)
	if !ok {
		// Не должно случаться, но если случилось — лучше провалиться,
		// чем отдать пустой userID.
		return "", ErrInvalidTicket
	}
	if s.now().After(e.expiresAt) {
		return "", ErrInvalidTicket
	}
	return e.userID, nil
}

// Cleanup запускает фоновую очистку expired-ticket-ов с периодом ttl.
//
// Блокирующий метод: возвращается только когда ctx завершён. Запускать в
// отдельной goroutine на старте сервиса:
//
//	go store.Cleanup(ctx)
//
// Тики раз в TTL — компромисс между точностью (сколько expired-entries в map
// в моменте) и накладными расходами. Можно пройти и за пол-TTL, если нужно.
func (s *Store) Cleanup(ctx context.Context) {
	if s.ttl <= 0 {
		return
	}
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.purgeExpired()
		}
	}
}

// purgeExpired — итерирует sync.Map и удаляет entries с expiresAt в прошлом.
//
// Performance: O(N) на каждый тик. N — количество live ticket-ов; на нашей
// нагрузке (≤1000 одновременных юзеров × 1 ticket в 5 мин) это пренебрежимо мало.
func (s *Store) purgeExpired() {
	now := s.now()
	s.tickets.Range(func(key, value any) bool {
		if e, ok := value.(entry); ok && now.After(e.expiresAt) {
			s.tickets.Delete(key)
		}
		return true
	})
}
