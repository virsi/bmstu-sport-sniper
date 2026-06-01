//go:build integration

package testhelpers

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	// pgx stdlib driver — needed by goose to apply migrations through database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresContainer is a Postgres testcontainer wired with goose-applied
// migrations from backend/migrations/<dbName>.
//
// Lifecycle:
//
//	pg := testhelpers.StartPostgres(t, "auth_db")
//	// pg.Pool is ready for use; container terminates via t.Cleanup.
type PostgresContainer struct {
	// DSN is the full Postgres URL with the per-test DB name.
	DSN string
	// Pool is a pgxpool.Pool already connected, ready to use.
	Pool *pgxpool.Pool
}

// sharedPgState keeps a single Postgres container reused across all tests in
// a process. Each test gets its own database name (via CREATE DATABASE) so
// state remains isolated; we save ~5-8 seconds per test by not booting a new
// container per call.
var (
	sharedOnce sync.Once
	sharedDSN  string
	sharedErr  error
)

// migrationsRoot returns the absolute path to backend/migrations.
// We resolve it from the location of this source file so tests can be
// invoked from any working directory.
//
// Layout assumption: backend/tests/testhelpers/pg.go → backend/migrations/
func migrationsRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("testhelpers: cannot resolve source path")
	}
	// .../backend/tests/testhelpers/pg.go → .../backend/migrations
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations"), nil
}

// startSharedPostgres boots a single shared Postgres container the first
// time it is called, then reuses it for the rest of the test process.
func startSharedPostgres(ctx context.Context) (string, error) {
	sharedOnce.Do(func() {
		c, err := tcpostgres.Run(ctx,
			"postgres:16-alpine",
			tcpostgres.WithDatabase("postgres"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			sharedErr = fmt.Errorf("testhelpers: start postgres: %w", err)
			return
		}
		dsn, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedErr = fmt.Errorf("testhelpers: get dsn: %w", err)
			return
		}
		sharedDSN = dsn
	})
	return sharedDSN, sharedErr
}

// StartPostgres returns a ready-to-use *pgxpool.Pool bound to a fresh
// per-test database named "<dbName>_<nanos>" with migrations applied from
// backend/migrations/<dbName>/.
//
// The underlying Postgres container is shared across calls within a single
// `go test` invocation for speed; each test still gets its own logical DB to
// prevent cross-test interference.
func StartPostgres(t *testing.T, dbName string) *PostgresContainer {
	t.Helper()
	ctx := context.Background()

	baseDSN, err := startSharedPostgres(ctx)
	require.NoError(t, err, "start shared postgres")

	// Connect to the default DB to CREATE the per-test schema.
	rootPool, err := pgxpool.New(ctx, baseDSN)
	require.NoError(t, err, "connect to postgres for create database")
	defer rootPool.Close()

	// CREATE DATABASE <dbName>_<nanos> — short unique suffix per call.
	testDBName := fmt.Sprintf("%s_%d", dbName, time.Now().UnixNano())
	_, err = rootPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", testDBName))
	require.NoError(t, err, "create test database %s", testDBName)

	// Build DSN for the freshly-created DB.
	testDSN := replaceDatabase(baseDSN, testDBName)

	// Apply goose migrations from backend/migrations/<dbName>.
	migDir, err := migrationsRoot()
	require.NoError(t, err, "resolve migrations root")
	require.NoError(t,
		applyGoose(testDSN, filepath.Join(migDir, dbName)),
		"apply migrations for %s", dbName,
	)

	// pgxpool for the test.
	pool, err := pgxpool.New(ctx, testDSN)
	require.NoError(t, err, "connect to test db pool")

	t.Cleanup(func() {
		pool.Close()
		// Drop the test database to free space; tolerate errors during teardown.
		dropCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rp, rerr := pgxpool.New(dropCtx, baseDSN)
		if rerr != nil {
			return
		}
		defer rp.Close()
		_, _ = rp.Exec(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", testDBName))
	})

	return &PostgresContainer{
		DSN:  testDSN,
		Pool: pool,
	}
}

// applyGoose applies the goose migrations from migDir against dsn.
func applyGoose(dsn, migDir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql: %w", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, migDir); err != nil {
		return fmt.Errorf("goose up %s: %w", migDir, err)
	}
	return nil
}

// replaceDatabase rewrites the database name in a Postgres DSN like
// "postgres://user:pass@host:5432/old?sslmode=disable" → "...new?sslmode=disable".
//
// Implementation note: testcontainers may give either form (path-based or
// query "database="), so we use a tolerant approach by scanning for the last
// "/" before the "?".
func replaceDatabase(dsn, newDB string) string {
	q := -1
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '?' {
			q = i
			break
		}
	}
	end := q
	if end < 0 {
		end = len(dsn)
	}
	lastSlash := -1
	for i := end - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash < 0 {
		return dsn
	}
	return dsn[:lastSlash+1] + newDB + dsn[end:]
}
