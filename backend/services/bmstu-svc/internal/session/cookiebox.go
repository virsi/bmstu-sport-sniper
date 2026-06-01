// Package session управляет cookies BMSTU/Keycloak: сериализует
// []*http.Cookie через gob, шифрует AES-256-GCM, persist в bmstu_sessions.
//
// Дизайн-выбор: один мастер-ключ на креды И cookies — на DRY. Если в
// будущем понадобится отдельная ротация — добавим параметр Subkey.
package session

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/fizcultor/backend/pkg/crypto"
)

// EncodeCookies сериализует cookies через gob и шифрует AES-GCM.
// Возвращает (blob, nonce, error), где blob = nonce||ciphertext||tag
// (контракт pkg/crypto.Encrypt), а nonce продублирован отдельно для
// удобства логирования / отладочной симметрии с bmstu_credentials.
func EncodeCookies(key []byte, cookies []*http.Cookie) (blob, nonce []byte, err error) {
	var buf bytes.Buffer
	if encErr := gob.NewEncoder(&buf).Encode(cookies); encErr != nil {
		return nil, nil, fmt.Errorf("session: gob encode: %w", encErr)
	}
	ct, err := crypto.Encrypt(key, buf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	// pkg/crypto.Encrypt префиксит blob nonce'ом длиной crypto.NonceSize;
	// отделяем его в отдельное поле (для аудита/логирования, decrypt
	// делает это самостоятельно — см. crypto.Decrypt).
	if len(ct) < crypto.NonceSize {
		return nil, nil, crypto.ErrCiphertextShort
	}
	n := make([]byte, crypto.NonceSize)
	copy(n, ct[:crypto.NonceSize])
	return ct, n, nil
}

// DecodeCookies — обратная операция: расшифровывает blob и gob-декодит.
func DecodeCookies(key, blob []byte) ([]*http.Cookie, error) {
	plain, err := crypto.Decrypt(key, blob)
	if err != nil {
		return nil, fmt.Errorf("session: decrypt: %w", err)
	}
	var cookies []*http.Cookie
	if err := gob.NewDecoder(bytes.NewReader(plain)).Decode(&cookies); err != nil {
		return nil, fmt.Errorf("session: gob decode: %w", err)
	}
	return cookies, nil
}

// LoadJar строит cookiejar.Jar и заливает в него cookies для всех URL'ов,
// от которых они были получены. URL вычисляется по полю Cookie.Domain.
//
// Если у cookie домен пустой, по дефолту привязываем к baseURL.
func LoadJar(cookies []*http.Cookie, baseURL string) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("session: new jar: %w", err)
	}
	bu, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("session: parse base url: %w", err)
	}

	// Группируем cookies по origin (scheme://host).
	groups := make(map[string][]*http.Cookie, 2)
	for _, ck := range cookies {
		origin := originFromCookie(ck, bu)
		groups[origin] = append(groups[origin], ck)
	}
	for origin, cs := range groups {
		u, err := url.Parse(origin)
		if err != nil {
			continue
		}
		jar.SetCookies(u, cs)
	}
	return jar, nil
}

// ExpiresUTC возвращает максимальный Expires среди cookies в UTC,
// или nil если все cookies — session-only.
func ExpiresUTC(cookies []*http.Cookie) *time.Time {
	var latest time.Time
	for _, ck := range cookies {
		if ck == nil || ck.Expires.IsZero() {
			continue
		}
		if ck.Expires.After(latest) {
			latest = ck.Expires
		}
	}
	if latest.IsZero() {
		return nil
	}
	u := latest.UTC()
	return &u
}

// originFromCookie выбирает origin (https://host) для конкретной cookie:
// предпочитает Domain если задан, иначе использует baseURL.
func originFromCookie(ck *http.Cookie, base *url.URL) string {
	host := ck.Domain
	if host == "" {
		return base.Scheme + "://" + base.Host
	}
	// Обрезаем leading dot для совместимости с url.Parse.
	if host != "" && host[0] == '.' {
		host = host[1:]
	}
	scheme := "https"
	if base.Scheme != "" {
		scheme = base.Scheme
	}
	return scheme + "://" + host
}
