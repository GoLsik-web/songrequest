package errs

// Severity задаёт цвет сообщения по последствиям для пользователя.
// Контекст может понизить уровень: например, необязательное продолжение
// не собралось, но восстановленный трек уже играет.
func Severity(err error) string {
	return SeverityCode(CodeOf(err))
}

// SeverityCode используется там, где уже сохранён код, без создания ошибки.
func SeverityCode(code Code) string {
	switch code {
	case SpotifyStale, SpotifyNothing, DonationsTooSmall, YouTubeAskNone, TwitchForeignAward:
		return "info"
	case SpotifyRateLimit, SpotifyPlanUnknown, SpotifyForeignTrack,
		SpotifySearchLimit, SpotifyPlaylistRestricted,
		TwitchRateLimit, TwitchEventSub, TwitchChat,
		DonationsUnreachable, YouTubeNoTool, YouTubeNoMpv,
		OBSUnreachable, OBSAuth, OBSNoSource, OBSRequest,
		UpdateCheck, UpdateDownload, UpdateNoFile, UpdateNoRepo:
		return "warn"
	default:
		return "error"
	}
}
