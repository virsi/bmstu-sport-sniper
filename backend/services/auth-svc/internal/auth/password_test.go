package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastParams — щадящие параметры argon2 для тестов (тяжёлые дефолты делают
// прогон секундами; PHC-формат и round-trip от этого не страдают).
var fastParams = Argon2Params{
	MemoryKiB:   8 * 1024, // 8 MiB
	Iterations:  1,
	Parallelism: 1,
}

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()

	const plain = "correct horse battery staple"

	hash, err := HashPassword(plain, fastParams)
	require.NoError(t, err)
	require.NotEmpty(t, hash)

	// Формат PHC: $argon2id$v=19$m=...,t=...,p=...$salt$key.
	parts := strings.Split(hash, "$")
	require.Len(t, parts, 6, "phc format: %s", hash)
	assert.Equal(t, "argon2id", parts[1])
	assert.Equal(t, "v=19", parts[2])

	ok, err := VerifyPassword(plain, hash)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHashPassword_WrongPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("right", fastParams)
	require.NoError(t, err)

	ok, err := VerifyPassword("wrong", hash)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHashPassword_DifferentSaltsProduceDifferentHashes(t *testing.T) {
	t.Parallel()

	h1, err := HashPassword("same", fastParams)
	require.NoError(t, err)
	h2, err := HashPassword("same", fastParams)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "salt should be random")

	// Но оба должны верифицироваться.
	ok1, _ := VerifyPassword("same", h1)
	ok2, _ := VerifyPassword("same", h2)
	assert.True(t, ok1)
	assert.True(t, ok2)
}

func TestVerifyPassword_InvalidHashFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"too few parts", "$argon2id$v=19$m=8$salt"},
		{"wrong algorithm", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA"},
		{"bad version", "$argon2id$v=99$m=8,t=1,p=1$c2FsdA$aGFzaA"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := VerifyPassword("x", tc.hash)
			assert.Error(t, err)
		})
	}
}

func TestArgon2Params_Defaults(t *testing.T) {
	t.Parallel()

	p := Argon2Params{}.effective()
	assert.Equal(t, defaultArgonMemoryKiB, p.MemoryKiB)
	assert.Equal(t, defaultArgonIterations, p.Iterations)
	assert.Equal(t, defaultArgonParallel, p.Parallelism)
}
