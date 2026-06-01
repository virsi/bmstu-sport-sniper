package teachers_test

import (
	"strings"
	"testing"

	"github.com/fizcultor/backend/services/teachers-svc/internal/teachers"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"Иванов Иван Иванович", "иванов иван иванович"},
		{"иванов  иван   иванович", "иванов иван иванович"},
		{"\tАвдеева  Людмила Васильевна", "авдеева людмила васильевна"},
	}
	for _, tc := range tests {
		got := teachers.NormalizeName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateUID_Deterministic(t *testing.T) {
	a := teachers.GenerateUID("иванов иван иванович")
	b := teachers.GenerateUID("иванов иван иванович")
	if a != b {
		t.Errorf("uid not deterministic: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("uid wrong length: %d, want 16", len(a))
	}
}

func TestGenerateUID_DifferentForDifferentNames(t *testing.T) {
	a := teachers.GenerateUID("ivan ivanov")
	b := teachers.GenerateUID("petr petrov")
	if a == b {
		t.Errorf("expected different uids for different names")
	}
}

func TestGenerateUID_EmptyEmpty(t *testing.T) {
	if teachers.GenerateUID("") != "" {
		t.Error("empty name → empty uid")
	}
}

func TestParseJSON_Golden(t *testing.T) {
	raw := []byte(`{
        "авдеева людмила васильевна": {"rating": "5.00"},
        "иванов иван иванович":       {"rating": "4.86"},
        "бад рейтинг":                {"rating": "not-a-number"},
        "пусто":                      {"rating": ""},
        "выше пяти":                  {"rating": "5.5"}
    }`)
	got, err := teachers.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(got))
	}

	byNorm := map[string]teachers.ImportedTeacher{}
	for _, t := range got {
		byNorm[t.NameNormalized] = t
	}

	// rating "5.00" → 5.0
	if v := byNorm["авдеева людмила васильевна"].Rating; v == nil || *v != 5.0 {
		t.Errorf("avdeyeva: rating mismatch: %v", v)
	}
	// "not-a-number" → nil
	if v := byNorm["бад рейтинг"].Rating; v != nil {
		t.Errorf("bad: rating should be nil, got %v", *v)
	}
	// "" → nil
	if v := byNorm["пусто"].Rating; v != nil {
		t.Errorf("empty: rating should be nil")
	}
	// "5.5" out of [0,5] → nil
	if v := byNorm["выше пяти"].Rating; v != nil {
		t.Errorf("out of range: rating should be nil")
	}
	// uid детерминизм: одна и та же норм → одна и та же uid
	expected := teachers.GenerateUID("авдеева людмила васильевна")
	if byNorm["авдеева людмила васильевна"].UID != expected {
		t.Errorf("uid mismatch")
	}
}

func TestParseJSON_RealEmbedded(t *testing.T) {
	data := teachers.EmbeddedJSON()
	if len(data) == 0 {
		t.Fatal("embeddedJSON is empty")
	}
	got, err := teachers.ParseJSON(data)
	if err != nil {
		t.Fatalf("parse embedded: %v", err)
	}
	if len(got) < 100 {
		t.Errorf("expected at least 100 teachers, got %d", len(got))
	}
	// Все uid детерминированы и не пустые.
	for _, e := range got {
		if e.UID == "" {
			t.Errorf("empty uid for %q", e.NameNormalized)
		}
		if len(e.UID) != 16 {
			t.Errorf("uid length %d for %q", len(e.UID), e.NameNormalized)
		}
		// NameNormalized — точно lower-cased.
		if e.NameNormalized != strings.ToLower(e.NameNormalized) {
			t.Errorf("normalized name not lowercase: %q", e.NameNormalized)
		}
	}
}

func TestParseJSON_Empty(t *testing.T) {
	got, err := teachers.ParseJSON([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestParseJSON_Malformed(t *testing.T) {
	_, err := teachers.ParseJSON([]byte(`{bad}`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
