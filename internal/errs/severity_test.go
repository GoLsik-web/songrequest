package errs

import "testing"

func TestSeverityMatchesUserImpact(t *testing.T) {
	for _, tc := range []struct {
		code Code
		want string
	}{
		{SpotifyPlaylistRestricted, "warn"}, {UpdateCheck, "warn"},
		{OBSNoSource, "warn"}, {YouTubeNoMpv, "warn"},
		{SpotifyNoPremium, "error"}, {SpotifyCountry, "error"},
		{ConfigSave, "error"}, {SpotifyRestore, "error"},
		{SpotifyStale, "info"}, {DonationsTooSmall, "info"},
	} {
		if got := Severity(New(tc.code, "test")); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.code, got, tc.want)
		}
	}
}
