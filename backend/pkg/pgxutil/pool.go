// Package pgxutil предоставляет helper-функции для работы с PostgreSQL через
// pgx/v5: построение connection pool с retries, проверка соединения и
// миграции через goose/golang-migrate (через CLI или embed).
package pgxutil

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig — параметры пула.
type PoolConfig struct {
	// DSN — connection string в формате pgx (postgres://user:pass@host:port/db?sslmode=...).
	DSN string
	// MaxConns — максимум открытых соединений в пуле. <=0 → дефолт pgx (4 * NumCPU).
	MaxConns int32
	// MinConns — минимум tepl-соединений. <=0 → дефолт pgx (0).
	MinConns int32
	// ConnectTimeout — таймаут на одну попытку подключения.
	ConnectTimeout time.Duration
	// MaxAttempts — сколько раз ретраить connect (с backoff). <=0 → 5.
	MaxAttempts int
	// BackoffBase — стартовая задержка между попытками; каждая следующая ×2.
	BackoffBase time.Duration
}

// NewPool строит pgxpool.Pool с retry-логикой. Делает Ping после connect.
//
// Реализует exponential backoff: BackoffBase × 2^attempt, до MaxAttempts.
// Возвращает первый успешный пул либо последнюю ошибку.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("pgxutil: empty DSN")
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}

	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgxutil: parse DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}

	var lastErr error
	delay := cfg.BackoffBase
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
		pool, err := pgxpool.NewWithConfig(cctx, pcfg)
		if err == nil {
			pingErr := pool.Ping(cctx)
			cancel()
			if pingErr == nil {
				return pool, nil
			}
			pool.Close()
			lastErr = pingErr
		} else {
			cancel()
			lastErr = err
		}

		if attempt == cfg.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return nil, fmt.Errorf("pgxutil: connect after %d attempts: %w", cfg.MaxAttempts, lastErr)
}
