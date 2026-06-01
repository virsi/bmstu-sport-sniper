package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fizcultor/backend/pkg/crypto"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/store"
)

// ErrCredentialsNotLinked возвращается, когда у пользователя нет кредов.
var ErrCredentialsNotLinked = errors.New("session: credentials not linked")

// Store — узкий контракт persist-слоя, нужный менеджеру сессий.
// Совместим со *store.Queries; интерфейс выделен для тестов.
type Store interface {
	GetCredentials(ctx context.Context, userID string) (store.BmstuCredential, error)
	UpsertSession(ctx context.Context, arg store.UpsertSessionParams) error
	GetSession(ctx context.Context, userID string) (store.BmstuSession, error)
	DeleteSession(ctx context.Context, userID string) error
	TouchCredentialsLastLogin(ctx context.Context, userID string) error
}

// OIDCClient — узкий контракт OIDC-клиента, нужный менеджеру.
// В рантайме реализуется *oidc.Client; в тестах — fake.
type OIDCClient interface {
	Login(ctx context.Context, login, password string) (*oidc.LoginResult, error)
	IsAlive(ctx context.Context, c *http.Client) bool
}

// Config — параметры конструктора Manager.
type Config struct {
	// MasterKey — 32 байта AES-256.
	MasterKey []byte
	// LKSBaseURL — нужен для восстановления Jar из cookies без Domain.
	LKSBaseURL string
}

// Manager — стейт-машина сессии: получить рабочий http.Client с cookies
// или сделать новый логин.
type Manager struct {
	store     Store
	oidc      OIDCClient
	cfg       Config
	newClient func() (*http.Client, error)
}

// New строит Manager. newClient — фабрика «голого» http.Client с пустым
// cookiejar; нужен, чтобы тесты могли подсунуть свой Transport.
func New(s Store, o OIDCClient, cfg Config, newClient func() (*http.Client, error)) (*Manager, error) {
	if len(cfg.MasterKey) != crypto.KeySize {
		return nil, crypto.ErrKeySize
	}
	if newClient == nil {
		newClient = defaultNewClient
	}
	return &Manager{store: s, oidc: o, cfg: cfg, newClient: newClient}, nil
}

// Acquire возвращает *http.Client готовый к запросам в LKS.
// Логика:
//  1. читает creds (если нет — ErrCredentialsNotLinked);
//  2. читает session (если есть валидный blob — расшифровывает и грузит в jar);
//  3. иначе — выполняет Login и persist'ит cookies.
func (m *Manager) Acquire(ctx context.Context, userID string) (*http.Client, error) {
	creds, err := m.store.GetCredentials(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCredentialsNotLinked
		}
		return nil, fmt.Errorf("session: get creds: %w", err)
	}

	// Пытаемся реюзнуть существующую сессию.
	if hc, ok := m.tryLoadSession(ctx, userID); ok {
		return hc, nil
	}

	// Делаем свежий логин.
	return m.login(ctx, userID, creds)
}

// Refresh принудительно делает re-login (даже если сессия валидна).
func (m *Manager) Refresh(ctx context.Context, userID string) (*http.Client, error) {
	creds, err := m.store.GetCredentials(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCredentialsNotLinked
		}
		return nil, fmt.Errorf("session: get creds: %w", err)
	}
	return m.login(ctx, userID, creds)
}

// Invalidate удаляет сессию из БД (creds сохраняются).
func (m *Manager) Invalidate(ctx context.Context, userID string) error {
	return m.store.DeleteSession(ctx, userID)
}

// DecryptLogin расшифровывает login пользователя (нужно вызывающему RPC,
// чтобы передать в OIDC.Login).
func (m *Manager) DecryptLogin(c store.BmstuCredential) (string, error) {
	pt, err := crypto.Decrypt(m.cfg.MasterKey, c.EncLogin)
	if err != nil {
		return "", fmt.Errorf("session: decrypt login: %w", err)
	}
	return string(pt), nil
}

// DecryptPassword расшифровывает пароль.
func (m *Manager) DecryptPassword(c store.BmstuCredential) (string, error) {
	pt, err := crypto.Decrypt(m.cfg.MasterKey, c.EncPassword)
	if err != nil {
		return "", fmt.Errorf("session: decrypt password: %w", err)
	}
	return string(pt), nil
}

// tryLoadSession возвращает (client, true) если в БД есть пригодная сессия.
// Любая ошибка → (nil, false) и тихий fallback на login.
func (m *Manager) tryLoadSession(ctx context.Context, userID string) (*http.Client, bool) {
	sess, err := m.store.GetSession(ctx, userID)
	if err != nil {
		return nil, false
	}
	cookies, err := DecodeCookies(m.cfg.MasterKey, sess.CookiesBlob)
	if err != nil {
		return nil, false
	}
	jar, err := LoadJar(cookies, m.cfg.LKSBaseURL)
	if err != nil {
		return nil, false
	}
	hc, err := m.newClient()
	if err != nil {
		return nil, false
	}
	hc.Jar = jar
	// watchdog — если есть.
	if !m.oidc.IsAlive(ctx, hc) {
		_ = m.store.DeleteSession(ctx, userID)
		return nil, false
	}
	return hc, true
}

// login выполняет OIDC handshake, шифрует cookies и persist'ит.
func (m *Manager) login(ctx context.Context, userID string, creds store.BmstuCredential) (*http.Client, error) {
	login, err := m.DecryptLogin(creds)
	if err != nil {
		return nil, err
	}
	password, err := m.DecryptPassword(creds)
	if err != nil {
		return nil, err
	}

	res, err := m.oidc.Login(ctx, login, password)
	if err != nil {
		return nil, err
	}

	blob, nonce, err := EncodeCookies(m.cfg.MasterKey, res.SessionCookies)
	if err != nil {
		return nil, err
	}
	expires := ExpiresUTC(res.SessionCookies)
	if upsertErr := m.store.UpsertSession(ctx, store.UpsertSessionParams{
		UserID:      userID,
		CookiesBlob: blob,
		Nonce:       nonce,
		ExpiresAt:   expires,
	}); upsertErr != nil {
		return nil, fmt.Errorf("session: upsert: %w", upsertErr)
	}
	if touchErr := m.store.TouchCredentialsLastLogin(ctx, userID); touchErr != nil {
		// Не блокирующая ошибка: статус-телеметрия.
		// Логирование делается на уровне server.
		_ = touchErr
	}

	jar, err := LoadJar(res.SessionCookies, m.cfg.LKSBaseURL)
	if err != nil {
		return nil, err
	}
	hc, err := m.newClient()
	if err != nil {
		return nil, err
	}
	hc.Jar = jar
	return hc, nil
}

// defaultNewClient — фабрика http.Client с таймаутом 15s.
func defaultNewClient() (*http.Client, error) {
	return &http.Client{Timeout: 15 * time.Second}, nil
}
