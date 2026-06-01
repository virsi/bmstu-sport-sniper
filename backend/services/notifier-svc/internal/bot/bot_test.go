package bot

import "testing"

func TestMaskChatID(t *testing.T) {
	cases := []struct {
		in   int64
		want int64
	}{
		{1234567890, 7890},
		{-1234567890, -7890},
		{0, 0},
		{42, 42},
		{-9999, -9999},
	}
	for _, c := range cases {
		if got := maskChatID(c.in); got != c.want {
			t.Errorf("maskChatID(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNew_EmptyToken(t *testing.T) {
	_, err := New(Config{}, nil, nil)
	if err == nil {
		t.Fatal("ожидаем ошибку для пустого токена")
	}
}
