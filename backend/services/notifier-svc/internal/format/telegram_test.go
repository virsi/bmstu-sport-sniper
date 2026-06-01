package format

import (
	"strings"
	"testing"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
)

func TestFormatTelegram_WithRating(t *testing.T) {
	got := FormatTelegram(
		Slot{
			Section: "Аэробика", Week: "Пн", Time: "10:00-11:30",
			Place: "ОФП-1", TeacherName: "Иванов И.И.", Vacancy: 3,
		},
		&TeacherRating{Rating: 4.5, URL: "https://studizba.com/x"},
	)
	mustContain(t, got, "🏟 <b>Аэробика</b>")
	mustContain(t, got, "🗓 Пн | ⏰ 10:00-11:30")
	mustContain(t, got, "📍 ОФП-1")
	mustContain(t, got, "👨‍🏫 Иванов И.И.")
	mustContain(t, got, "⭐️ Рейтинг: <b>4.5</b>")
	mustContain(t, got, "https://studizba.com/x")
	mustContain(t, got, "🟢 Свободно мест: <b>3</b>")
}

func TestFormatTelegram_NoRating(t *testing.T) {
	got := FormatTelegram(
		Slot{
			Section: "Силовая", Week: "Ср", Time: "12:00-13:30",
			Place: "СК", TeacherName: "Петров П.П.", Vacancy: 1,
		},
		nil,
	)
	mustContain(t, got, "ℹ️ Рейтинг: <i>не найден</i>")
	if strings.Contains(got, "Studizba") {
		t.Fatal("при отсутствии рейтинга не должно быть ссылки Studizba")
	}
}

func TestFormatTelegram_RatingWithoutURL(t *testing.T) {
	got := FormatTelegram(
		Slot{Section: "Йога", Week: "Пт", Time: "18:00-19:30", Place: "Зал", TeacherName: "Смирнов", Vacancy: 5},
		&TeacherRating{Rating: 5.0, URL: ""},
	)
	mustContain(t, got, "⭐️ Рейтинг: <b>5.0</b>")
	if strings.Contains(got, "Studizba") {
		t.Fatal("без URL не должно быть ссылки")
	}
}

func TestFormatTelegram_EscapesHTML(t *testing.T) {
	got := FormatTelegram(
		Slot{
			Section: "Бокс<script>alert(1)</script>",
			Week:    "Пн", Time: "10:00-11:30", Place: "Зал",
			TeacherName: "<b>Сидоров</b>", Vacancy: 2,
		},
		nil,
	)
	if strings.Contains(got, "<script>") {
		t.Fatal("пользовательский <script> должен быть экранирован")
	}
	mustContain(t, got, "&lt;script&gt;alert(1)&lt;/script&gt;")
	// Преподаватель — экранируем тэги внутри ФИО.
	mustContain(t, got, "&lt;b&gt;Сидоров&lt;/b&gt;")
}

func TestSlotFromProto_Defaults(t *testing.T) {
	m := &commonv1.MatchedSlot{Slot: &commonv1.Slot{}}
	s := SlotFromProto(m)
	if s.Section != "Тренировка" {
		t.Errorf("section дефолт: %q", s.Section)
	}
	if s.TeacherName != "Преподаватель не указан" {
		t.Errorf("teacher дефолт: %q", s.TeacherName)
	}
	if s.Place != "СК МГТУ" {
		t.Errorf("place дефолт: %q", s.Place)
	}
	if s.Week != "День недели" {
		t.Errorf("week дефолт: %q", s.Week)
	}
	if s.Time != "??" {
		t.Errorf("time дефолт: %q", s.Time)
	}
}

func TestSlotFromProto_AllFields(t *testing.T) {
	section := "Плавание"
	uid := "T-1"
	m := &commonv1.MatchedSlot{Slot: &commonv1.Slot{
		Id:          "deadbeef",
		Week:        3,
		Time:        "08:00-09:30",
		Section:     &section,
		Place:       "Бассейн",
		TeacherName: "Тренер",
		TeacherUid:  &uid,
		Vacancy:     7,
	}}
	s := SlotFromProto(m)
	if s.Section != "Плавание" || s.Week != "3" || s.Time != "08:00-09:30" ||
		s.Place != "Бассейн" || s.TeacherName != "Тренер" ||
		s.TeacherUID != "T-1" || s.Vacancy != 7 {
		t.Fatalf("unexpected slot: %+v", s)
	}
}

func TestFormatBatch_Empty(t *testing.T) {
	if got := FormatBatch(nil, nil); got != nil {
		t.Fatalf("ожидаем nil, получили %v", got)
	}
}

func TestFormatBatch_SingleMessage(t *testing.T) {
	slots := []Slot{
		{Section: "S1", Week: "Пн", Time: "10:00-11:30", Place: "P", TeacherName: "T1", Vacancy: 1, TeacherUID: "u1"},
		{Section: "S2", Week: "Вт", Time: "12:00-13:30", Place: "P", TeacherName: "T2", Vacancy: 2, TeacherUID: "u2"},
	}
	ratings := map[string]TeacherRating{
		"u1": {Rating: 4.0, URL: ""},
	}
	out := FormatBatch(slots, ratings)
	if len(out) != 1 {
		t.Fatalf("ожидаем 1 сообщение, получили %d", len(out))
	}
	msg := out[0]
	mustContain(t, msg, "<b>"+MessageTitle+"</b>")
	mustContain(t, msg, "🏟 <b>S1</b>")
	mustContain(t, msg, "🏟 <b>S2</b>")
	mustContain(t, msg, "⭐️ Рейтинг: <b>4.0</b>")
	// У второго teacher_uid рейтинг не найден.
	mustContain(t, msg, "ℹ️ Рейтинг: <i>не найден</i>")
	// CTA-блок в конце.
	mustContain(t, msg, RecordURL)
	mustContain(t, msg, "✍️ ЗАПИСАТЬСЯ")
}

func TestFormatBatch_SplitOnSizeLimit(t *testing.T) {
	// 40 слотов с длинным place — заставит batch разбиться.
	longPlace := strings.Repeat("место-описание-длинная-строка-", 5) // ~150 символов
	slots := make([]Slot, 40)
	for i := range slots {
		slots[i] = Slot{
			Section: "S", Week: "Пн", Time: "10:00-11:30",
			Place: longPlace, TeacherName: "Преподаватель", Vacancy: 1,
		}
	}
	out := FormatBatch(slots, nil)
	if len(out) < 2 {
		t.Fatalf("ожидаем разбиение на ≥2 сообщения, получили %d", len(out))
	}
	for i, msg := range out {
		if len(msg) > telegramBatchSoftLimit+len("\n\n<a href='"+RecordURL+"'><b>✍️ ЗАПИСАТЬСЯ</b></a>")+10 {
			t.Errorf("msg #%d превышает мягкий лимит: %d", i, len(msg))
		}
		if !strings.Contains(msg, MessageTitle) {
			t.Errorf("msg #%d без заголовка", i)
		}
		if !strings.Contains(msg, "✍️ ЗАПИСАТЬСЯ") {
			t.Errorf("msg #%d без CTA", i)
		}
	}
}

func TestFormatBatch_HardLimitTelegram(t *testing.T) {
	// Все сообщения должны быть короче TelegramMessageLimit (4096).
	slots := make([]Slot, 100)
	for i := range slots {
		slots[i] = Slot{
			Section: "Section", Week: "Пн", Time: "10:00-11:30",
			Place: "Place", TeacherName: "Teacher", Vacancy: 1,
		}
	}
	for i, msg := range FormatBatch(slots, nil) {
		if len(msg) > TelegramMessageLimit {
			t.Errorf("msg #%d превышает Telegram-лимит: %d", i, len(msg))
		}
	}
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("ожидаем подстроку %q в:\n%s", sub, s)
	}
}
