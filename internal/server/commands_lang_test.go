package server

import (
	"strings"
	"testing"

	"songrequest/internal/i18n"
)

// Английский режим: ответы в чате по-английски, а русские команды работают
// как раньше.
func TestChatCommandsEnglish(t *testing.T) {
	i18n.SetLang("en")
	defer i18n.SetLang("ru")

	help := helpLine("!")
	if !strings.HasPrefix(help, "Commands: !queue") || strings.ContainsAny(help, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
		t.Errorf("справка не английская: %q", help)
	}

	srv := commandServer(t)
	if got := srv.nowLine(); got != "Nothing is playing right now" {
		t.Errorf("nowLine в английском режиме: %q", got)
	}

	for _, c := range []string{"!song", "!np", "!queue", "!q", "!skip", "!remove 1", "!help", "!commands", "!скип", "!очередь"} {
		if !isChatCommand(c, "!") {
			t.Errorf("%q не узнана как команда", c)
		}
	}

	// Английский синоним и русская команда делают одно и то же.
	srv.onChat(fromMod("!ban troll"))
	if !banned(t, srv, "troll") {
		t.Error("!ban не сработала")
	}
	srv.onChat(fromMod("!разбан troll"))
	if banned(t, srv, "troll") {
		t.Error("!разбан не сработала в английском режиме")
	}
}

// Русский режим остаётся прежним байт-в-байт.
func TestChatCommandsRussianUnchanged(t *testing.T) {
	i18n.SetLang("ru")
	if got := helpLine("!"); !strings.HasPrefix(got, "Команды: !очередь, !трек, !скип (можно !скип 2") {
		t.Errorf("русская справка изменилась: %q", got)
	}
	if got := queueSize(3); got != "3 заказа" {
		t.Errorf("queueSize: %q", got)
	}
}
