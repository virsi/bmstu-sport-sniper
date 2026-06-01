// Package crypto предоставляет AES-256-GCM шифрование для хранения секретов
// at-rest (BMSTU-кредсы, refresh-tokens). Ключ — 32 байта (KeySize), передаётся
// из env в hex-формате. Каждый Encrypt использует новый случайный nonce длиной
// NonceSize байт, который префиксится к ciphertext: [nonce || ciphertext+tag].
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// KeySize — размер ключа AES-256 в байтах.
const KeySize = 32

// NonceSize — длина nonce AES-GCM в байтах (стандартный 96-битный nonce).
// Совпадает с cipher.AEAD.NonceSize() для GCM-режима и фиксирует контракт:
// Encrypt всегда префиксует ciphertext nonce'ом ровно этой длины
// ([nonce(NonceSize) || ciphertext || tag(16)]). Вызывающий код может
// безопасно нарезать blob по этой константе вместо магического числа 12.
const NonceSize = 12

// ErrKeySize возвращается, когда длина ключа не равна KeySize.
var ErrKeySize = errors.New("crypto: key must be 32 bytes")

// ErrCiphertextShort возвращается, когда ciphertext короче nonce.
var ErrCiphertextShort = errors.New("crypto: ciphertext too short")

// NewKey генерирует случайный 32-байтовый AES-256 ключ. Используется для
// первичного bootstrap'а master-key (в prod ключ должен браться из env/vault).
func NewKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("crypto: read random: %w", err)
	}
	return key, nil
}

// KeyFromHex декодирует hex-строку (64 символа) в 32-байтовый ключ.
// Возвращает ErrKeySize если результат не равен KeySize байтам.
func KeyFromHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode hex: %w", err)
	}
	if len(b) != KeySize {
		return nil, ErrKeySize
	}
	return b, nil
}

// Encrypt шифрует plaintext алгоритмом AES-256-GCM с случайным nonce.
// Результат: nonce(NonceSize) || ciphertext || auth_tag(16).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	// Sanity-check: NonceSize-константа должна совпадать с реальным
	// значением AEAD. Если когда-нибудь stdlib изменит дефолт — мы упадём
	// здесь, а не молча сломаем БД-нарезку blob'ов в вызывающем коде.
	if gcm.NonceSize() != NonceSize {
		return nil, fmt.Errorf("crypto: unexpected gcm nonce size %d (want %d)", gcm.NonceSize(), NonceSize)
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}
	// gcm.Seal добавит auth-tag к концу.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt расшифровывает blob созданный Encrypt. Возвращает plaintext.
// Возвращает ошибку при тэмперинге (auth-tag не совпал).
func Decrypt(key, blob []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	if gcm.NonceSize() != NonceSize {
		return nil, fmt.Errorf("crypto: unexpected gcm nonce size %d (want %d)", gcm.NonceSize(), NonceSize)
	}
	if len(blob) < NonceSize {
		return nil, ErrCiphertextShort
	}
	nonce, ct := blob[:NonceSize], blob[NonceSize:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm open: %w", err)
	}
	return pt, nil
}
