package logx

import "testing"

func TestRedactorHidesKnownSecrets(t *testing.T) {
	r := &Redactor{}
	r.Add("BQD_supersecret_access_token_value")

	got := r.Clean("запрос с токеном BQD_supersecret_access_token_value внутри")
	if got != "запрос с токеном "+mask+" внутри" {
		t.Fatalf("секрет не затёрт: %q", got)
	}
}

func TestRedactorIgnoresShortValues(t *testing.T) {
	r := &Redactor{}
	r.Add("abc") // слишком коротко, иначе изуродует весь лог

	if got := r.Clean("abc def"); got != "abc def" {
		t.Fatalf("короткое значение не должно затираться: %q", got)
	}
}

func TestRedactorHidesUnknownTokensByShape(t *testing.T) {
	r := &Redactor{}

	cases := map[string]string{
		"json":   `{"access_token":"AQBn0987654321abcdef","expires_in":3600}`,
		"header": "Authorization: Bearer AQBn0987654321abcdef",
		"form":   "grant_type=refresh_token&refresh_token=AQBn0987654321abcdef",
		"verify": `{"code_verifier":"abcdefghijklmnopqrstuvwxyz012345678901234567"}`,
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := r.Clean(in)
			if got == in {
				t.Fatalf("токен неизвестного вида не затёрт: %q", got)
			}
			if contains2(got, "AQBn0987654321abcdef") || contains2(got, "abcdefghijklmnopqrstuvwxyz012345678901234567") {
				t.Fatalf("секрет остался в строке: %q", got)
			}
		})
	}
}

func TestRedactorKeepsUsefulContext(t *testing.T) {
	r := &Redactor{}
	// Лог должен остаться читаемым: код ошибки и текст никуда не деваются.
	in := `{"error":{"status":403,"message":"Player command failed: Premium required"}}`
	if got := r.Clean(in); got != in {
		t.Fatalf("полезный текст испорчен: %q", got)
	}
}

func contains2(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
