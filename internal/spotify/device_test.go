package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Жалоба тестера 26.08: «с плейлиста на заказ переключает, а с заказного трека
// на заказной — не может». Причина: заказ играет одним треком без источника,
// после него Spotify останавливается, устройство перестаёт быть активным, и
// следующий заказ получает 404 NO_ACTIVE_DEVICE.
func TestPlayTrackWakesSleepingDevice(t *testing.T) {
	plays := 0
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/me/player/play":
			plays++
			if plays == 1 {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":{"status":404,"message":"Player command failed: No active device found","reason":"NO_ACTIVE_DEVICE"}}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/me/player/devices":
			w.Write([]byte(`{"devices":[
				{"id":"телефон","name":"Телефон","type":"Smartphone","is_active":false,"is_restricted":false},
				{"id":"комп","name":"Компьютер стримера","type":"Computer","is_active":false,"is_restricted":false}
			]}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	// Устройство из снимка — компьютер, с которого идёт звук в эфир.
	if err := c.PlayTrack(context.Background(), "spotify:track:второй-заказ", "комп"); err != nil {
		t.Fatalf("второй заказ подряд не включился: %v", err)
	}

	var transferred bool
	var lastPlayQuery string
	for _, cl := range *calls {
		if cl.Path == "/me/player" && cl.Method == http.MethodPut {
			transferred = true
		}
		if cl.Path == "/me/player/play" {
			lastPlayQuery = cl.Query
		}
	}
	if !transferred {
		t.Fatal("уснувшее устройство не разбудили переносом воспроизведения")
	}
	if !strings.Contains(lastPlayQuery, "device_id=%D0%BA%D0%BE%D0%BC%D0%BF") &&
		!strings.Contains(lastPlayQuery, "device_id=комп") {
		t.Fatalf("вторая попытка ушла не на компьютер стримера: %q", lastPlayQuery)
	}
	if plays != 2 {
		t.Fatalf("попыток включить трек %d, ждали две", plays)
	}
}

// Если Spotify закрыт совсем, будить нечего — стример должен увидеть
// человеческий текст про «открой Spotify», а не молчание.
func TestPlayTrackKeepsHumanTextWhenNothingToWake(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player/devices" {
			w.Write([]byte(`{"devices":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"status":404,"message":"Player command failed: No active device found","reason":"NO_ACTIVE_DEVICE"}}`))
	})

	err := c.PlayTrack(context.Background(), "spotify:track:любой", "")
	if err == nil {
		t.Fatal("Spotify закрыт, а заказ считается включённым")
	}
	if !strings.Contains(err.Error(), "Spotify") {
		t.Fatalf("текст не для человека: %v", err)
	}
}
