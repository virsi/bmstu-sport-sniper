package auth

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRefreshToken_Format(t *testing.T) {
	t.Parallel()

	raw, hash, err := GenerateRefreshToken()
	require.NoError(t, err)

	// raw — base64url без padding, 32 байта → 43 символа.
	assert.Len(t, raw, 43, "raw length")
	// hash — hex sha256 → 64 символа, валидный hex.
	assert.Len(t, hash, 64, "hash length")
	_, err = hex.DecodeString(hash)
	assert.NoError(t, err, "hash is hex")
}

func TestGenerateRefreshToken_HashIsDeterministic(t *testing.T) {
	t.Parallel()

	raw, hash1, err := GenerateRefreshToken()
	require.NoError(t, err)

	// Повторное хеширование того же raw даёт тот же hash.
	hash2 := HashRefreshToken(raw)
	assert.Equal(t, hash1, hash2)
}

func TestGenerateRefreshToken_Uniqueness(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		raw, _, err := GenerateRefreshToken()
		require.NoError(t, err)
		if _, dup := seen[raw]; dup {
			t.Fatalf("duplicate token: %s", raw)
		}
		seen[raw] = struct{}{}
	}
}

func TestGenerateOpaqueToken(t *testing.T) {
	t.Parallel()

	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		tok, err := GenerateOpaqueToken(16)
		require.NoError(t, err)
		assert.NotEmpty(t, tok)
		// 16 байт → 22 base64url-символа без padding.
		assert.Len(t, tok, 22)
	})
	t.Run("zero size", func(t *testing.T) {
		t.Parallel()
		_, err := GenerateOpaqueToken(0)
		assert.Error(t, err)
	})
	t.Run("negative size", func(t *testing.T) {
		t.Parallel()
		_, err := GenerateOpaqueToken(-1)
		assert.Error(t, err)
	})
}
