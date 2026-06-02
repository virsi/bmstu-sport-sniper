// Package config — env-конфигурация bmstu-svc.
package config

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	basecfg "github.com/fizcultor/backend/pkg/config"
	"github.com/fizcultor/backend/pkg/crypto"
)

// Config — параметры bmstu-svc.
type Config struct {
	basecfg.Base

	// AESMasterKeyHex — 64-символьная hex-строка (32 байта) AES-256 для шифра
	// BMSTU-кредов и cookie-jar at-rest.
	AESMasterKeyHex string `env:"AES_MASTER_KEY,required"`

	// SemesterUUIDBasic — UUID семестра LKS для основной группы здоровья.
	// Подставляется в `/lks-back/api/v1/fv/{uuid}/groups` при FetchGroups.
	SemesterUUIDBasic string `env:"SEMESTER_UUID_BASIC,required"`
	// SemesterUUIDPreparatory — UUID семестра LKS для подготовительной группы.
	SemesterUUIDPreparatory string `env:"SEMESTER_UUID_PREPARATORY,required"`
	// SemesterUUIDSpecialMedical — UUID семестра LKS для специальной
	// медицинской группы (СМГ).
	SemesterUUIDSpecialMedical string `env:"SEMESTER_UUID_SPECIAL_MEDICAL,required"`
	// SemesterUUIDAFK — UUID семестра LKS для адаптивной физкультуры (АФК).
	SemesterUUIDAFK string `env:"SEMESTER_UUID_AFK,required"`

	// LKSBaseURL — базовый URL LKS BMSTU.
	LKSBaseURL string `env:"LKS_BASE_URL" envDefault:"https://lks.bmstu.ru"`

	// OIDCUseBrowser — fallback на chromedp, если pure HTTP сломается.
	OIDCUseBrowser bool `env:"OIDC_USE_BROWSER" envDefault:"false"`

	// HTTPClientTimeoutSeconds — таймаут запросов к LKS.
	HTTPClientTimeoutSeconds int `env:"HTTP_CLIENT_TIMEOUT_SECONDS" envDefault:"15"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "bmstu-svc"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate валидирует Config.
func (c *Config) Validate() error {
	if err := c.Base.Validate(); err != nil {
		return err
	}
	if c.PostgresDSN == "" {
		return errors.New("config: POSTGRES_DSN is required")
	}
	if _, err := crypto.KeyFromHex(c.AESMasterKeyHex); err != nil {
		return err
	}
	checks := []struct {
		name string
		val  string
	}{
		{"SEMESTER_UUID_BASIC", c.SemesterUUIDBasic},
		{"SEMESTER_UUID_PREPARATORY", c.SemesterUUIDPreparatory},
		{"SEMESTER_UUID_SPECIAL_MEDICAL", c.SemesterUUIDSpecialMedical},
		{"SEMESTER_UUID_AFK", c.SemesterUUIDAFK},
	}
	for _, ch := range checks {
		if _, err := uuid.Parse(ch.val); err != nil {
			return fmt.Errorf("config: %s is not a valid UUID: %w", ch.name, err)
		}
	}
	return nil
}

// SemesterUUIDFor возвращает UUID семестра LKS для указанной группы здоровья.
//
// UNSPECIFIED трактуется как BASIC (бэкворд-совместимость + дефолт схемы БД).
// Неизвестные значения — тоже BASIC, чтобы FetchGroups не падал при чтении
// исторических/повреждённых записей; на запись же бэкэнд всегда нормализует
// через health.ProtoToDB.
func (c *Config) SemesterUUIDFor(hg commonv1.HealthGroup) string {
	switch hg {
	case commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY:
		return c.SemesterUUIDPreparatory
	case commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL:
		return c.SemesterUUIDSpecialMedical
	case commonv1.HealthGroup_HEALTH_GROUP_AFK:
		return c.SemesterUUIDAFK
	case commonv1.HealthGroup_HEALTH_GROUP_BASIC,
		commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED:
		return c.SemesterUUIDBasic
	default:
		return c.SemesterUUIDBasic
	}
}
