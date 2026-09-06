package server

import (
	"net/http"
	"testing"

	"songrequest/internal/config"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// Команды в чате.
//
// Жалоба владельца 06.09: «!скип», «!skip» и «!s» не работают. Настоящая
// причина оказалась не в разборе команды, а в том, что вход в Twitch у
// приложения не сохранён вовсе — чат не подписан, и до сюда не доезжает ни
// одно сообщение. Но проверить сам разбор всё равно надо: пока его никто не
// проверял, «работает» было словом, а не фактом.

// fromMod — сообщение от модератора канала.
func fromMod(text string) twitch.ChatMessage {
	return twitch.ChatMessage{
		Login: "moder", DisplayName: "Moder", Text: text, IsModerator: true,
	}
}

// fromViewer — сообщение от обычного зрителя.
func fromViewer(text string) twitch.ChatMessage {
	return twitch.ChatMessage{Login: "zritel", DisplayName: "Zritel", Text: text}
}

func TestParseCommandIsBlindToCaseAndLanguage(t *testing.T) {
	same := [][]string{
		{"!скип", "!Скип", "!СКИП", "!скИп"},
		{"!skip", "!Skip", "!SKIP", "!sKiP"},
		{"!s", "!S"},
	}
	for _, group := range same {
		want, _, ok := parseCommand(group[0], "!")
		if !ok {
			t.Fatalf("%q вообще не разобралась", group[0])
		}
		for _, text := range group[1:] {
			got, _, ok := parseCommand(text, "!")
			if !ok || got != want {
				t.Errorf("%q разобралась как %q (ok=%v), ждали %q", text, got, ok, want)
			}
		}
	}
}

// Часть чат-клиентов дописывает невидимый знак, чтобы Twitch не счёл сообщение
// повтором. В чате обе строки выглядят одинаково, и разобрать такую жалобу
// глазами невозможно. Знаки записаны кодами нарочно: в исходнике их иначе не
// видно, а метку порядка байтов Go вообще не пускает в файл.
func TestParseCommandSurvivesInvisibleCharacters(t *testing.T) {
	texts := []string{
		"!скип\u200b",     // пустой знак в конце
		"\ufeff!скип",     // метка порядка байтов в начале
		"!скип\U000e0000", // метка языка, которую любят боты
		" !скип ",         // просто пробелы
	}
	for _, text := range texts {
		cmd, _, ok := parseCommand(text, "!")
		if !ok || cmd != "скип" {
			t.Errorf("%q разобралась как %q (ok=%v)", text, cmd, ok)
		}
	}
}

func TestParseCommandTakesArgumentsAndPrefix(t *testing.T) {
	cmd, args, ok := parseCommand("!бан Вася болтает лишнее", "!")
	if !ok || cmd != "бан" {
		t.Fatalf("команда разобралась как %q (ok=%v)", cmd, ok)
	}
	if len(args) != 3 || args[0] != "Вася" {
		t.Fatalf("доводы разобрались не так: %#v", args)
	}

	// Свой знак команд: у стримера мог быть занят «!» другим ботом.
	if cmd, _, ok := parseCommand("?скип", "?"); !ok || cmd != "скип" {
		t.Fatalf("свой знак не сработал: %q ok=%v", cmd, ok)
	}
	// И чужой знак к нам не относится.
	if _, _, ok := parseCommand("?скип", "!"); ok {
		t.Fatal("команда с чужим знаком принята за свою")
	}
	// Пробел после знака — обычная опечатка на телефоне.
	if cmd, _, ok := parseCommand("! скип", "!"); !ok || cmd != "скип" {
		t.Fatalf("«! скип» не разобралась: %q ok=%v", cmd, ok)
	}
}

// Дальше — весь путь целиком: сообщение из чата доезжает до действия.
// Проверяем на тех командах, чей итог видно без Twitch и без плеера.

// banned — закрыты ли заказы этому человеку. Через ту же дорогу, которой
// пользуется сам приём заказов, а не мимо неё.
func banned(t *testing.T, srv *Server, login string) bool {
	t.Helper()
	no, _ := srv.isMusicBanned(login)
	return no
}

func commandServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {})
	return srv
}

func TestChatCommandsStopAndStart(t *testing.T) {
	srv := commandServer(t)

	for _, text := range []string{"!стоп", "!СТОП", "!пауза", "!pause", "!Pause"} {
		srv.player.SetPaused(false)
		srv.onChat(fromMod(text))
		if !srv.player.Paused() {
			t.Errorf("%q не остановила приём заказов", text)
		}
	}
	for _, text := range []string{"!старт", "!СТАРТ", "!resume", "!play"} {
		srv.player.SetPaused(true)
		srv.onChat(fromMod(text))
		if srv.player.Paused() {
			t.Errorf("%q не вернула приём заказов", text)
		}
	}
}

func TestChatCommandsBanAndUnban(t *testing.T) {
	srv := commandServer(t)

	srv.onChat(fromMod("!бан Вася надоел"))
	if !banned(t, srv, "вася") {
		t.Fatal("«!бан» не закрыл заказы")
	}
	srv.onChat(fromMod("!разбан ВАСЯ"))
	if banned(t, srv, "вася") {
		t.Fatal("«!разбан» не вернул заказы")
	}

	// То же самое по-английски и с собачкой перед ником: её пишут по привычке.
	srv.onChat(fromMod("!ban @petya"))
	if !banned(t, srv, "petya") {
		t.Fatal("«!ban @ник» не сработала")
	}
	srv.onChat(fromMod("!unban petya"))
	if banned(t, srv, "petya") {
		t.Fatal("«!unban» не сработала")
	}
}

func TestChatCommandsClearQueue(t *testing.T) {
	srv := commandServer(t)
	if _, err := srv.queue.Add(queue.Item{
		Source: queue.SourceManual, Requester: "кто-то", Provider: "spotify",
		URI: "spotify:track:x", Title: "трек", Artist: "артист",
	}, false); err != nil {
		t.Fatal(err)
	}

	srv.onChat(fromMod("!очистить"))
	items, err := srv.queue.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("очередь не очистилась: осталось %d", len(items))
	}
}

// Обычному зрителю команды недоступны — иначе бан-лист и очередь были бы в
// руках у любого, кто прочитал справочник.
func TestChatCommandsIgnoreViewers(t *testing.T) {
	srv := commandServer(t)
	srv.player.SetPaused(false)

	srv.onChat(fromViewer("!стоп"))
	if srv.player.Paused() {
		t.Fatal("зритель остановил приём заказов")
	}

	srv.onChat(fromViewer("!бан кто-то"))
	if banned(t, srv, "кто-то") {
		t.Fatal("зритель закрыл кому-то заказы")
	}
}

// Свой знак команд из настроек: у стримера «!» мог занять другой бот.
func TestChatCommandsUseConfiguredPrefix(t *testing.T) {
	srv := commandServer(t)
	if err := srv.cfg.Update(func(c *config.Config) { c.CommandPrefix = "?" }); err != nil {
		t.Fatal(err)
	}

	srv.player.SetPaused(false)
	srv.onChat(fromMod("?стоп"))
	if !srv.player.Paused() {
		t.Fatal("команда со своим знаком не сработала")
	}

	srv.player.SetPaused(false)
	srv.onChat(fromMod("!стоп"))
	if srv.player.Paused() {
		t.Fatal("команда с чужим знаком сработала")
	}
}
