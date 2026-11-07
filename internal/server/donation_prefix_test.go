package server

import (
	"songrequest/internal/donations"
	"testing"
)

func TestUnmarkedDonationDoesNotReachMusicServices(t *testing.T) {
	s := commandServer(t)
	// Музыкальные клиенты здесь не подключены: обычный донат должен
	// завершиться до любого обращения к поиску или очереди.
	s.onDonation(donations.Donation{Username: "viewer", Amount: 100000, Message: "Спасибо за стрим!"})
}

func TestDonationRequestRequiresSeparatePrefixAtStart(t *testing.T) {
	for _, tc := range []struct {
		message, prefix, want string
		marked                bool
	}{
		{"Спасибо за стрим", "!музыка", "", false},
		{"Привет !музыка Кино", "!музыка", "", false},
		{"!музыка123 Кино", "!музыка", "", false},
		{"!музыка Кино — Группа крови", "!музыка", "Кино — Группа крови", true},
		{"  !музыка https://open.spotify.com/track/test  ", "", "https://open.spotify.com/track/test", true},
		{"!трек\nКино", " !трек ", "Кино", true},
		{"!музыка Кино", "!трек", "", false},
		{"!музыка", "!музыка", "", true},
	} {
		got, marked := donationRequest(tc.message, tc.prefix)
		if got != tc.want || marked != tc.marked {
			t.Errorf("request(%q, %q) = %q, %v; want %q, %v", tc.message, tc.prefix, got, marked, tc.want, tc.marked)
		}
	}
}
