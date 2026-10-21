package server

import (
	"testing"

	"songrequest/internal/app"
)

// Бан: мусор вместо ника и сам канал в бан-лист не попадают.
func TestBanRejectsBadLoginAndChannel(t *testing.T) {
	srv := commandServer(t)
	srv.state.UpdateTwitch(func(ti *app.TwitchInfo) { ti.Channel = "streamer" })

	for _, text := range []string{"!бан @", "!бан ab", "!бан привет", "!бан @Streamer"} {
		srv.onChat(fromMod(text))
	}
	for _, login := range []string{"", "ab", "привет", "streamer"} {
		if banned(t, srv, login) {
			t.Errorf("%q попал в бан-лист", login)
		}
	}

	srv.onChat(fromMod("!бан @Troll_1"))
	if !banned(t, srv, "troll_1") {
		t.Error("обычный ник не забанился")
	}
}

// Зрителю управлять нельзя: «!стоп» от него приём не останавливает.
func TestViewerCannotStopOrders(t *testing.T) {
	srv := commandServer(t)
	srv.player.SetPaused(false)
	srv.onChat(fromViewer("!стоп"))
	if srv.player.Paused() {
		t.Fatal("зритель остановил приём заказов")
	}
}

func TestViewerCommandsAreKnown(t *testing.T) {
	for cmd := range viewerCommands {
		if !chatCommands[cmd] {
			t.Errorf("%q разрешена зрителям, но не числится командой", cmd)
		}
	}
	for _, c := range []string{"пауза-трека", "паузатрека", "стопзаказы"} {
		if !chatCommands[c] {
			t.Errorf("%q нет в списке команд", c)
		}
	}
}

func TestValidLogin(t *testing.T) {
	for login, want := range map[string]bool{
		"abc": true, "a_b_9": true, "ab": false, "": false,
		"ник": false, "abcdefghijklmnopqrstuvwxyz": false,
	} {
		if validLogin(login) != want {
			t.Errorf("validLogin(%q) != %v", login, want)
		}
	}
}
