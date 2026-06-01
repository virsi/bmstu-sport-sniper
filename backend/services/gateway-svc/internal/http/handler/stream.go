package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
	"github.com/fizcultor/backend/services/gateway-svc/internal/sse"
)

// keepaliveInterval — период служебного `: ping` от сервера, чтобы прокси
// (Caddy/Nginx) не закрывали idle SSE-стрим. 25 сек — стандарт для большинства
// прокси с idle_timeout=60s.
const keepaliveInterval = 25 * time.Second

// streamDeps — расширение Deps только для StreamHandler. Хранится отдельно
// от общих Deps, чтобы StreamHandler можно было сконфигурировать независимо
// (e.g. поменять Hub в тестах).
type streamDeps struct {
	hub *sse.Hub
}

// StreamHandler — отдельная структура, потому что для /api/stream нужна
// дополнительная зависимость (Hub), которой нет в общем handler.Deps.
type StreamHandler struct {
	streamDeps
}

// NewStreamHandler создаёт SSE-хэндлер.
func NewStreamHandler(hub *sse.Hub) *StreamHandler {
	return &StreamHandler{streamDeps: streamDeps{hub: hub}}
}

// Stream — GET /api/stream. Требует Auth middleware (поддерживает ?access= fallback).
//
// Контракт по api.md §6:
//   - text/event-stream, no-cache, X-Accel-Buffering: no.
//   - Каждые 25 сек шлём `: ping\n\n` keepalive.
//   - На NATS-сообщение шлём `event: new-slot\ndata: <json>\n\n`.
//   - На клиентский disconnect (r.Context().Done()) → unsubscribe NATS.
//
// Реализация single-loop через select по 3 источникам:
//   - msg ← NATS канал → пишем в response.
//   - tick ← time.Ticker → пишем keepalive.
//   - ctx.Done → выходим.
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	if h.streamHandler == nil {
		WriteError(w, r, NewBadRequest("SSE not configured"))
		return
	}
	h.streamHandler.serve(w, r)
}

// serve — собственно SSE-цикл, вынесен из метода Handler чтобы тестироваться
// отдельно от глобального Handler.
func (s *StreamHandler) serve(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, r, NewBadRequest("response writer does not support flushing"))
		return
	}

	ch, cleanup, err := s.hub.Subscribe(r.Context(), userID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer cleanup()

	// Headers до первого write, иначе net/http зафиксирует Content-Type автомат.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	// Nginx-специфичный — отключает буферизацию proxied response.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Initial flush — чтобы клиент сразу увидел headers и onopen сработал.
	flusher.Flush()

	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	slog.Debug("sse: stream opened", slog.String("user_id", userID))
	defer slog.Debug("sse: stream closed", slog.String("user_id", userID))

	for {
		select {
		case <-r.Context().Done():
			return

		case <-ticker.C:
			if _, werr := fmt.Fprint(w, ": ping\n\n"); werr != nil {
				return
			}
			flusher.Flush()

		case payload, ok := <-ch:
			if !ok {
				// Канал закрыт cleanup-функцией → выходим.
				return
			}
			// SSE-фрейм: event-name + data + пустая строка-разделитель.
			// payload уже валидный JSON (notifier гарантирует).
			if _, werr := fmt.Fprintf(w, "event: new-slot\ndata: %s\n\n", payload); werr != nil {
				return
			}
			flusher.Flush()
		}
	}
}
