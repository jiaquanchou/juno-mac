package audit

import "testing"

func TestQuoteValue(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"NULL", nil, "NULL"},
		{"整数", []byte("42"), "42"},
		{"负数", []byte("-7"), "-7"},
		{"小数", []byte("3.14"), "3.14"},
		{"普通文本", []byte("alice"), "'alice'"},
		{"含单引号", []byte("o'clock"), `'o''clock'`},
		{"含反斜杠", []byte(`a\b`), `'a\\b'`},
		{"日期时间", []byte("2026-09-26 20:06:52"), "'2026-09-26 20:06:52'"},
		{"二进制", []byte{0x00, 0xff}, "0x00ff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteValue(tc.in); got != tc.want {
				t.Errorf("quoteValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainsNUL(t *testing.T) {
	if !containsNUL([]byte{'a', 0, 'b'}) {
		t.Error("应检出 NUL 字节")
	}
	if containsNUL([]byte("abc")) {
		t.Error("普通文本不应检出 NUL")
	}
}

func TestContainsStr(t *testing.T) {
	if !containsStr([]string{"id", "name"}, "id") {
		t.Error("应包含 id")
	}
	if containsStr([]string{"id", "name"}, "email") {
		t.Error("不应包含 email")
	}
}
