package app

import "songrequest/internal/errs"

// NotifyError показывает ошибку в панели вместе с её кодом.
// Стример видит «SP-06 · Spotify нигде не открыт…» и может назвать код голосом.
func (s *State) NotifyError(err error) {
	if err == nil {
		return
	}
	code, text := errs.Describe(err)
	s.NotifyCode(errs.Severity(err), string(code), text)
}

// NotifyWarning — операция необязательна, основная работа продолжается.
func (s *State) NotifyWarning(err error) {
	if err == nil {
		return
	}
	code, text := errs.Describe(err)
	s.NotifyCode("warn", string(code), text)
}

// NotifyWarn показывает предупреждение с кодом.
func (s *State) NotifyWarn(code errs.Code, text string) {
	s.NotifyCode("warn", string(code), text)
}
