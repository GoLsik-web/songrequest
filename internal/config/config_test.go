package config

import "testing"

// Дефолт режима возврата — «запасной плейлист»: он предсказуемее всего.
// Пока плейлист не выбран, он молча работает как «ничего не включать».
func TestDefaultResumeMode(t *testing.T) {
	d := Defaults()
	if d.ResumeFail != ResumeFallbackPlaylist {
		t.Fatalf("ждали %q, получили %q", ResumeFallbackPlaylist, d.ResumeFail)
	}
	if !d.ResumeModeIncomplete() {
		t.Fatal("без выбранного плейлиста режим должен считаться недонастроенным")
	}
	if d.EffectiveResumeMode() != ResumeNothing {
		t.Fatalf("ждали деградацию до тишины, получили %q", d.EffectiveResumeMode())
	}
}

func TestLanguageSwitch(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Get().Language != "ru" {
		t.Fatalf("по умолчанию язык %q, ждали ru", f.Get().Language)
	}
	if err := f.Update(func(c *Config) { c.Language = "en" }); err != nil {
		t.Fatal(err)
	}
	f2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f2.Get().Language != "en" {
		t.Fatalf("после перезапуска язык %q, ждали en", f2.Get().Language)
	}
	c := f2.Get()
	c.Language = "de"
	c.NormalizeLanguage()
	if c.Language != "ru" {
		t.Fatalf("неизвестный язык должен стать ru, а стал %q", c.Language)
	}
}
