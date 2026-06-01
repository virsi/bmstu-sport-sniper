// Package format форматирует доменные данные в каналы доставки. На текущем
// этапе — только Telegram (HTML parse_mode). Логика портирована из
// legacy_main.py:209-242, эмодзи и текст сохранены символ-в-символ.
package format

import (
	"fmt"
	"html"
	"strings"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
)

// Константы внешних ссылок (как в legacy_main.py:317).
const (
	// RecordURL — линк «✍️ ЗАПИСАТЬСЯ» в самом конце сообщения.
	RecordURL = "https://lks.bmstu.ru/fv/new-record"
	// MessageTitle — заголовок алёрта (legacy_main.py:209).
	MessageTitle = "🔥 ДОСТУПНЫ НОВЫЕ СЛОТЫ!"
	// TelegramMessageLimit — максимальная длина одного TG-сообщения (4096).
	TelegramMessageLimit = 4096
	// telegramBatchSoftLimit — мягкий лимит, оставляющий запас на CTA-блок и
	// возможные неучтённые символы.
	telegramBatchSoftLimit = 4000
)

// Slot — данные одного слота, нужные для рендера карточки.
//
// Wrapper-структура (а не сразу *commonv1.MatchedSlot), чтобы юнит-тесты
// формат-слоя не зависели от proto-pkg.
type Slot struct {
	// Section — название секции ("Аэробика", "Силовая"). Пустая строка → "Тренировка".
	Section string
	// Week — день недели или номер недели, в зависимости от того, что прислал LKS.
	Week string
	// Time — строка вида "HH:MM-HH:MM".
	Time string
	// Place — название корпуса/зала.
	Place string
	// TeacherName — ФИО преподавателя строкой.
	TeacherName string
	// TeacherUID — идентификатор для look-up рейтинга. Может быть пустым.
	TeacherUID string
	// Vacancy — количество свободных мест.
	Vacancy int32
}

// SlotFromProto собирает Slot из commonv1.MatchedSlot (как его шлёт poller-svc).
// Поля с optional/proto3-presence заполняются дефолтами как в legacy.
func SlotFromProto(m *commonv1.MatchedSlot) Slot {
	if m == nil || m.GetSlot() == nil {
		return Slot{}
	}
	s := m.GetSlot()

	section := s.GetSection()
	if section == "" {
		section = "Тренировка"
	}
	teacher := s.GetTeacherName()
	if teacher == "" {
		teacher = "Преподаватель не указан"
	}
	place := s.GetPlace()
	if place == "" {
		place = "СК МГТУ"
	}
	week := ""
	if w := s.GetWeek(); w > 0 {
		week = fmt.Sprintf("%d", w)
	}
	if week == "" {
		week = "День недели"
	}
	timeStr := s.GetTime()
	if timeStr == "" {
		timeStr = "??"
	}

	return Slot{
		Section:     section,
		Week:        week,
		Time:        timeStr,
		Place:       place,
		TeacherName: teacher,
		TeacherUID:  s.GetTeacherUid(),
		Vacancy:     s.GetVacancy(),
	}
}

// TeacherRating — данные о преподавателе для рендера строки рейтинга.
type TeacherRating struct {
	// Rating — агрегированный рейтинг.
	Rating float64
	// URL — ссылка на профиль (например studizba.com/…). Может быть пустой.
	URL string
}

// FormatTelegram возвращает HTML-форматированное TG-сообщение для одного слота.
//
// Полный вид (см. legacy_main.py:232-239):
//
//	🏟 <b>{section}</b>
//	🗓 {week} | ⏰ {time}
//	📍 {place}
//	👨‍🏫 {teacher}
//	⭐️ Рейтинг: <b>{rating}</b> (<a href='{url}'>Studizba</a>)
//	🟢 Свободно мест: <b>{vacancy}</b>
//
// Если rating == nil → строка «ℹ️ Рейтинг: <i>не найден</i>».
func FormatTelegram(slot Slot, rating *TeacherRating) string {
	var b strings.Builder
	b.Grow(256)

	fmt.Fprintf(&b, "🏟 <b>%s</b>\n", html.EscapeString(slot.Section))
	fmt.Fprintf(&b, "🗓 %s | ⏰ %s\n", html.EscapeString(slot.Week), html.EscapeString(slot.Time))
	fmt.Fprintf(&b, "📍 %s\n", html.EscapeString(slot.Place))
	fmt.Fprintf(&b, "👨‍🏫 %s\n", html.EscapeString(slot.TeacherName))
	b.WriteString(formatRatingLine(rating))
	b.WriteString("\n")
	fmt.Fprintf(&b, "🟢 Свободно мест: <b>%d</b>", slot.Vacancy)

	return b.String()
}

// formatRatingLine возвращает строку «⭐️ …» или «ℹ️ …». В отличие от слота не
// заканчивается переводом строки — её добавит вызывающий.
func formatRatingLine(rating *TeacherRating) string {
	if rating == nil {
		return "ℹ️ Рейтинг: <i>не найден</i>"
	}
	// Формат рейтинга: одна цифра после точки, как в legacy ("4.5", "5.0").
	ratingStr := fmt.Sprintf("%.1f", rating.Rating)
	if rating.URL == "" {
		return fmt.Sprintf("⭐️ Рейтинг: <b>%s</b>", ratingStr)
	}
	return fmt.Sprintf(
		"⭐️ Рейтинг: <b>%s</b> (<a href='%s'>Studizba</a>)",
		ratingStr, html.EscapeString(rating.URL),
	)
}

// FormatBatch собирает одно или несколько TG-сообщений из набора слотов и
// рейтингов. Каждое сообщение начинается с заголовка и заканчивается CTA-блоком
// «✍️ ЗАПИСАТЬСЯ». Сообщения не превышают telegramBatchSoftLimit символов.
//
// ratings ключуется по teacher_uid; если uid пустой или uid отсутствует в карте
// — рендерится «не найден».
func FormatBatch(slots []Slot, ratings map[string]TeacherRating) []string {
	if len(slots) == 0 {
		return nil
	}

	header := "<b>" + MessageTitle + "</b>\n"
	footer := "\n\n<a href='" + RecordURL + "'><b>✍️ ЗАПИСАТЬСЯ</b></a>"

	var (
		out          []string
		current      strings.Builder
		currentCount int
	)
	current.Grow(telegramBatchSoftLimit)
	current.WriteString(header)

	flush := func() {
		if currentCount == 0 {
			return
		}
		current.WriteString(footer)
		out = append(out, current.String())
		current.Reset()
		current.WriteString(header)
		currentCount = 0
	}

	for _, s := range slots {
		var rPtr *TeacherRating
		if r, ok := ratings[s.TeacherUID]; ok && s.TeacherUID != "" {
			rCopy := r
			rPtr = &rCopy
		}
		card := FormatTelegram(s, rPtr)
		// Разделитель между карточками — пустая строка (legacy: "\n\n").
		piece := "\n" + card
		if currentCount > 0 {
			piece = "\n\n" + card
		}

		projected := current.Len() + len(piece) + len(footer)
		if projected > telegramBatchSoftLimit && currentCount > 0 {
			flush()
			piece = "\n" + card
		}
		current.WriteString(piece)
		currentCount++
	}
	flush()
	return out
}
