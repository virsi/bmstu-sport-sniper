-- +goose Up
-- +goose StatementBegin

-- health_group хранит группу здоровья студента БМГТУ, выбранную им при
-- сохранении BMSTU-кредов. Используется bmstu-svc.FetchGroups для подстановки
-- нужного UUID семестра в LKS API (см. cfg.SemesterUUIDFor).
--
-- Допустимые значения соответствуют common.v1.HealthGroup (без префикса
-- HEALTH_GROUP_*). UNSPECIFIED в БД не хранится — bmstu-svc заменяет его на
-- BASIC при записи. CHECK защищает от рассинхрона proto↔БД (миграции
-- независимы, см. database-per-service в CLAUDE.md).
--
-- DEFAULT 'BASIC' даёт бэкворд-совместимость: существующие строки получают
-- BASIC автоматически, что соответствует поведению до введения групп здоровья
-- (один общий SEMESTER_UUID). См. proto common/v1/common.proto::HealthGroup.
ALTER TABLE bmstu_credentials
    ADD COLUMN IF NOT EXISTS health_group TEXT NOT NULL DEFAULT 'BASIC'
        CHECK (health_group IN ('BASIC', 'PREPARATORY', 'SPECIAL_MEDICAL', 'AFK'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE bmstu_credentials DROP COLUMN IF EXISTS health_group;
-- +goose StatementEnd
