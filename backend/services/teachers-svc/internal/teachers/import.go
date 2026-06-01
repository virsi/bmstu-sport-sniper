// Package teachers реализует сервис справочника преподавателей: импорт
// из embedded teachers.json, нормализация имён, генерация детерминированных
// UID, gRPC-сервер.
package teachers

import (
	"crypto/sha1" // #nosec G505 — sha1 здесь как hash для коротких uid'ов (не для безопасности).
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// embeddedJSON — JSON с агрегированными рейтингами BMSTU-преподавателей.
// Структура файла: { "<полное имя в lowercase>": {"rating": "5.00"} }.
//
//go:embed teachers.json
var embeddedJSON []byte

// EmbeddedJSON возвращает embedded teachers.json. Доступно тестам и Refresh.
func EmbeddedJSON() []byte { return embeddedJSON }

// rawEntry — формат значения в JSON.
type rawEntry struct {
	Rating string `json:"rating"`
}

// ImportedTeacher — нормализованная запись после парса JSON.
type ImportedTeacher struct {
	// UID — детерминированный 16-символьный hex (sha1 от NameNormalized).
	UID string
	// Name — оригинальное имя, как в JSON (lowercase, как в source).
	Name string
	// NameNormalized — lowercased + trimmed.
	NameNormalized string
	// Rating — рейтинг 0..5; nil если в JSON пусто или not a number.
	Rating *float64
}

// ParseJSON парсит teachers.json в []ImportedTeacher.
//
//   - Ключи приводятся к lower(trim) (хотя файл уже в lowercase, страхуемся).
//   - rating — строка вроде "4.86"; пустая или нечисловая → nil.
//   - uid = sha1(name_normalized)[:16] (hex), детерминирован.
//   - Порядок результата — стабильный (отсортирован по NameNormalized).
func ParseJSON(data []byte) ([]ImportedTeacher, error) {
	var raw map[string]rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("teachers: parse json: %w", err)
	}

	result := make([]ImportedTeacher, 0, len(raw))
	for k, v := range raw {
		norm := NormalizeName(k)
		if norm == "" {
			continue
		}
		entry := ImportedTeacher{
			UID:            GenerateUID(norm),
			Name:           k,
			NameNormalized: norm,
		}
		if r, ok := parseRating(v.Rating); ok {
			entry.Rating = &r
		}
		result = append(result, entry)
	}

	// Сортируем для детерминированного порядка (для тестов и логов).
	sortByNormalized(result)
	return result, nil
}

// NormalizeName приводит имя к каноничному виду: lower + collapsed spaces + trim.
// Чистая функция, изолирована для тестов.
func NormalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Сжимаем повторяющиеся пробелы в один.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n':
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// GenerateUID возвращает детерминированный 16-символьный hex UID.
// sha1(normalized)[:16] — для коротких ключей в БД.
// Чистая функция, изолирована для тестов.
func GenerateUID(normalized string) string {
	if normalized == "" {
		return ""
	}
	h := sha1.Sum([]byte(normalized)) // #nosec G401 — используем sha1 как ID-функцию, не для безопасности.
	return hex.EncodeToString(h[:])[:16]
}

// parseRating — "4.86" → 4.86; пустая или мусор → (0, false).
func parseRating(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	// Защита от NaN/Inf (если когда-то в JSON попадёт).
	if v != v || v < 0 || v > 5 {
		return 0, false
	}
	return v, true
}

// sortByNormalized — стабильная сортировка по NameNormalized.
// Используем insertion sort через slices.SortFunc нельзя без go1.21 dep; пишем простой quicksort.
// Реализация через sort.Slice сохранила бы простоту. Чтобы не тянуть лишний import — используем
// небольшой helper.
func sortByNormalized(s []ImportedTeacher) {
	if len(s) < 2 {
		return
	}
	// Простейший вариант — insertion sort: для теста на 200 записей быстрее, чем сортировать руками.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].NameNormalized > s[j].NameNormalized; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
