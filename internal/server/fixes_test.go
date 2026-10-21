package server

import (
	"testing"

	"songrequest/internal/app"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// Метка скипа относится к одному заказу. Скип, который не успел сработать
// (трек доиграл сам), не должен достаться следующему заказу.
func TestSkipMarkBelongsToItsOrder(t *testing.T) {
	srv := playlistServer(t)
	srv.setSkip(1, "скипнул мод", true)
	who, refund := srv.takeSkip(2)
	if refund || who == "скипнул мод" {
		t.Fatalf("чужая метка скипа досталась другому заказу: %q %v", who, refund)
	}

	srv.setSkip(3, "скипнул мод", true)
	who, refund = srv.takeSkip(3)
	if !refund || who != "скипнул мод" {
		t.Fatalf("своя метка скипа потерялась: %q %v", who, refund)
	}
}

// Пока плейлист одобряют, он остаётся в ожидании, но второй раз его не взять.
func TestPlaylistStaysPendingWhileApproving(t *testing.T) {
	srv := playlistServer(t)
	srv.addPending(samplePlaylist("один", pendingTrack{Artist: "а", Title: "б"}))

	p, err := srv.claimPending("")
	if err != nil {
		t.Fatal(err)
	}
	if len(srv.pendingPlaylists()) != 1 {
		t.Fatal("плейлист вынут из ожидания до того, как треки встали в очередь")
	}
	if _, err := srv.claimPending(p.ID); err == nil {
		t.Fatal("одобряемый плейлист взят второй раз")
	}
	if _, err := srv.takePending(p.ID); err == nil {
		t.Fatal("одобряемый плейлист отклонён посреди одобрения")
	}
	srv.releasePending(p.ID, true)
	if len(srv.pendingPlaylists()) != 0 {
		t.Fatal("одобренный плейлист остался в ожидании")
	}
}

// Ждущие решения заказы с YouTube переживают перезапуск.
func TestYouTubeAsksSurviveRestart(t *testing.T) {
	srv := playlistServer(t)
	if srv.queue == nil {
		t.Skip("в тестовом сервере нет базы")
	}
	srv.mu.Lock()
	srv.ytAskNo = 7
	srv.ytAsks = []ytAsk{{
		view: app.YouTubeAsk{ID: "7", Title: "Мем"},
		r:    twitch.Redemption{ID: "red-1", UserName: "Viewer"},
		item: queue.Item{URI: "https://youtu.be/xxxxxxxxxxx", Title: "Мем"},
	}}
	srv.mu.Unlock()
	srv.syncYouTubeAsks()

	// «Перезапуск»: память пуста, в базе — то, что было.
	srv.mu.Lock()
	srv.ytAsks = nil
	srv.ytAskNo = 0
	srv.mu.Unlock()
	srv.loadYouTubeAsks()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.ytAsks) != 1 || srv.ytAsks[0].r.ID != "red-1" || srv.ytAskNo != 7 {
		t.Fatalf("ждущий заказ не поднялся после перезапуска: %+v", srv.ytAsks)
	}
}
