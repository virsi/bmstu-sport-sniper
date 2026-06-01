// Package auth реализует доменную логику auth-svc: хеширование паролей
// (argon2id), генерация/верификация refresh-токенов, gRPC-сервер
// AuthServiceServer.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2 параметры по умолчанию (OWASP Password Storage CS 2024-08).
// Сервис может переопределить через AuthConfig.
const (
	defaultArgonMemoryKiB  uint32 = 64 * 1024 // 64 MiB
	defaultArgonIterations uint32 = 3
	defaultArgonParallel   uint8  = 2
	argonSaltLen           uint32 = 16
	argonKeyLen            uint32 = 32
)

// ErrInvalidHashFormat — хеш не соответствует формату PHC $argon2id$.
var ErrInvalidHashFormat = errors.New("auth: invalid hash format")

// ErrUnsupportedAlgorithm — формат корректен, но алгоритм не argon2id.
var ErrUnsupportedAlgorithm = errors.New("auth: unsupported hash algorithm")

// ErrUnsupportedVersion — формат корректен, но версия argon2 не v19.
var ErrUnsupportedVersion = errors.New("auth: unsupported argon2 version")

// Argon2Params — параметры argon2id для хеширования.
//
// Если поле = 0, используется defaultArgon*-константа.
type Argon2Params struct {
	// MemoryKiB — память argon2id в KiB.
	MemoryKiB uint32
	// Iterations — число итераций.
	Iterations uint32
	// Parallelism — степень параллелизма.
	Parallelism uint8
}

// effective возвращает копию p с подставленными дефолтами для нулевых полей.
func (p Argon2Params) effective() Argon2Params {
	if p.MemoryKiB == 0 {
		p.MemoryKiB = defaultArgonMemoryKiB
	}
	if p.Iterations == 0 {
		p.Iterations = defaultArgonIterations
	}
	if p.Parallelism == 0 {
		p.Parallelism = defaultArgonParallel
	}
	return p
}

// HashPassword хеширует пароль argon2id в PHC-формате
// `$argon2id$v=19$m=...,t=...,p=...$<salt-b64>$<hash-b64>`.
//
// salt — 16 байт из crypto/rand. Каждый вызов выдаёт новый salt, идемпотентности нет.
func HashPassword(plain string, params Argon2Params) (string, error) {
	p := params.effective()

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, p.Iterations, p.MemoryKiB, p.Parallelism, argonKeyLen)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.MemoryKiB,
		p.Iterations,
		p.Parallelism,
		b64.EncodeToString(salt),
		b64.EncodeToString(key),
	), nil
}

// VerifyPassword проверяет соответствие plain пароля и PHC-хеша.
// Возвращает (true, nil) при совпадении, (false, nil) при несовпадении.
// Ошибка != nil — структурный issue хеша (не интерпретируется как «не совпало»).
func VerifyPassword(plain, hash string) (bool, error) {
	parts := strings.Split(hash, "$")
	// "$argon2id$v=19$m=...,t=...,p=...$salt$key" → ["", "argon2id", "v=19", "m=...,t=...,p=...", "salt", "key"].
	if len(parts) != 6 {
		return false, ErrInvalidHashFormat
	}
	if parts[1] != "argon2id" {
		return false, ErrUnsupportedAlgorithm
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("%w: parse version: %v", ErrInvalidHashFormat, err)
	}
	if version != argon2.Version {
		return false, ErrUnsupportedVersion
	}

	p, err := parsePHCParams(parts[3])
	if err != nil {
		return false, err
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("%w: decode salt: %v", ErrInvalidHashFormat, err)
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("%w: decode hash: %v", ErrInvalidHashFormat, err)
	}

	// keyLen берём из длины записанного ключа — для будущей совместимости,
	// если поменяем константу.
	keyLen, err := safeUint32(len(want))
	if err != nil {
		return false, fmt.Errorf("%w: hash length: %w", ErrInvalidHashFormat, err)
	}
	got := argon2.IDKey([]byte(plain), salt, p.Iterations, p.MemoryKiB, p.Parallelism, keyLen)

	// Постоянное время сравнения.
	return subtle.ConstantTimeCompare(want, got) == 1, nil
}

// parsePHCParams парсит "m=<int>,t=<int>,p=<int>".
func parsePHCParams(s string) (Argon2Params, error) {
	var p Argon2Params
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return p, fmt.Errorf("%w: bad params block", ErrInvalidHashFormat)
	}
	for _, kv := range parts {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return p, fmt.Errorf("%w: bad kv %q", ErrInvalidHashFormat, kv)
		}
		key, val := kv[:eq], kv[eq+1:]
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return p, fmt.Errorf("%w: parse %s: %v", ErrInvalidHashFormat, key, err)
		}
		switch key {
		case "m":
			p.MemoryKiB = uint32(n)
		case "t":
			p.Iterations = uint32(n)
		case "p":
			if n > 255 {
				return p, fmt.Errorf("%w: parallelism out of range", ErrInvalidHashFormat)
			}
			p.Parallelism = uint8(n)
		default:
			return p, fmt.Errorf("%w: unknown key %q", ErrInvalidHashFormat, key)
		}
	}
	return p, nil
}

// safeUint32 безопасно конвертирует int в uint32 для длины буфера.
func safeUint32(n int) (uint32, error) {
	if n < 0 {
		return 0, fmt.Errorf("auth: negative length %d", n)
	}
	if uint64(n) > uint64(^uint32(0)) {
		return 0, fmt.Errorf("auth: length %d overflows uint32", n)
	}
	return uint32(n), nil
}
