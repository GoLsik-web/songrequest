package app

import (
	"songrequest/internal/errs"
	"testing"
)

func TestOptionalFailureIsWarningAndBlockingFailureIsError(t *testing.T) {
	s := New("test")
	s.NotifyError(errs.New(errs.SpotifyPlaylistRestricted, "playlist"))
	s.NotifyWarning(errs.New(errs.SpotifyUnreachable, "optional continuation"))
	s.NotifyError(errs.New(errs.SpotifyRestore, "music stopped"))
	notices := s.Snapshot().Notices
	for index, want := range []string{"warn", "warn", "error"} {
		if notices[index].Level != want {
			t.Errorf("notice %d: %s, want %s", index, notices[index].Level, want)
		}
	}
}
