// Package testhelpers provides shared fixtures for backend integration tests.
//
// All public functions are safe to call concurrently — each returns its own
// container/pool. They use t.Cleanup to tear down resources at test end.
//
// Build tag `integration` is required to import this package from tests,
// which keeps testcontainers (heavy Docker dependency) out of normal unit
// test runs.
package testhelpers
