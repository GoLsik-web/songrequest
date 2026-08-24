// Package errs — ошибки с коротким кодом и человеческим текстом.
//
// Смысл кода: стример на своём компьютере видит в панели «SP-04» и просто
// называет его голосом, а разработчик по коду сразу знает место в программе.
// Текст при этом пишем так, чтобы человек понял, что делать, без слова «токен».
package errs

import "fmt"

// Code — короткий код ошибки, который видно в панели.
type Code string

const (
	// Настройки
	NoClientID   Code = "CFG-01" // не заполнен client_id Spotify
	ConfigSave   Code = "CFG-02" // не смогли сохранить настройки
	ConfigBroken Code = "CFG-03" // файл настроек повреждён

	// Spotify
	SpotifyAuthStart   Code = "SP-01" // не удалось открыть страницу входа
	SpotifyAuthDenied  Code = "SP-02" // вход отклонён или подделан ответ
	SpotifyAuthToken   Code = "SP-03" // не удалось завершить вход
	SpotifyAuthExpired Code = "SP-04" // авторизация слетела, нужен повторный вход
	SpotifyNoPremium   Code = "SP-05" // на аккаунте нет Premium
	SpotifyNoDevice    Code = "SP-06" // нет устройства, где играть
	SpotifyUnreachable Code = "SP-07" // Spotify не отвечает
	SpotifyRateLimit   Code = "SP-08" // Spotify просит подождать
	SpotifySnapshot    Code = "SP-09" // не удалось запомнить, что играло
	SpotifyRestore     Code = "SP-10" // не удалось вернуть воспроизведение
	SpotifyStale       Code = "SP-11" // за время заказа музыку переключили руками
	SpotifyNothing     Code = "SP-12" // возвращать нечего
	SpotifyBadResponse Code = "SP-13" // Spotify ответил не тем, чего мы ждали
	SpotifyNoScope     Code = "SP-14" // не выданы права, нужен повторный вход
	SpotifyPlanUnknown Code = "SP-15" // Spotify не сказал, какая подписка
	SpotifyCountry     Code = "SP-16" // Spotify недоступен в стране аккаунта

	// Twitch
	TwitchNoClientID   Code = "TW-01" // не заполнен client_id Twitch
	TwitchAuthStart    Code = "TW-02" // не удалось начать вход
	TwitchAuthPending  Code = "TW-03" // код не подтвердили вовремя
	TwitchAuthExpired  Code = "TW-04" // авторизация слетела
	TwitchNoAffiliate  Code = "TW-05" // на канале нет баллов
	TwitchReward       Code = "TW-06" // не удалось создать награду
	TwitchRefund       Code = "TW-07" // не удалось вернуть баллы
	TwitchUnreachable  Code = "TW-08" // Twitch не отвечает
	TwitchRateLimit    Code = "TW-09" // Twitch просит подождать
	TwitchEventSub     Code = "TW-10" // оборвалась подписка на события
	TwitchBadResponse  Code = "TW-11" // Twitch ответил не тем, чего мы ждали
	TwitchForeignAward Code = "TW-12" // награду создали не мы, баллы не вернуть

	// Донаты
	DonationsNoClientID  Code = "DN-01" // не заполнен client_id сервиса
	DonationsAuth        Code = "DN-02" // вход не выполнен или слетел
	DonationsUnreachable Code = "DN-03" // сервис не отвечает
	DonationsBadResponse Code = "DN-04" // сервис ответил не тем
	DonationsTooSmall    Code = "DN-05" // донат меньше порога заказа

	// YouTube
	YouTubeNoTool      Code = "YT-01" // нет yt-dlp
	YouTubeNoMpv       Code = "YT-02" // нет mpv
	YouTubeNotFound    Code = "YT-03" // на YouTube ничего не нашлось
	YouTubePlay        Code = "YT-04" // не получилось включить
	YouTubeBadResponse Code = "YT-05" // yt-dlp ответил не тем
	YouTubeCookies     Code = "YT-06" // YouTube требует подтвердить, что мы не робот

	// Диагностика
	DiagExport Code = "DIAG-01" // не собрался архив с логом
)

// Error — ошибка с кодом. Message показываем стримеру, cause уходит в лог.
type Error struct {
	Code    Code
	Message string
	cause   error
}

// New создаёт ошибку с кодом и понятным текстом.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap оборачивает техническую ошибку в понятную.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func (e *Error) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.cause)
}

// Unwrap открывает исходную ошибку для errors.Is и errors.As.
func (e *Error) Unwrap() error { return e.cause }

// UserText — то, что видит стример: «SP-04 · Слетела авторизация Spotify…».
func (e *Error) UserText() string {
	return fmt.Sprintf("%s · %s", e.Code, e.Message)
}

// CodeOf достаёт код из любой ошибки; если кода нет — пустая строка.
func CodeOf(err error) Code {
	var e *Error
	if As(err, &e) {
		return e.Code
	}
	return ""
}

// Describe разбирает ошибку на код и текст по отдельности. Панель показывает
// их разными элементами, поэтому склеивать их здесь нельзя — иначе код
// напечатается дважды.
func Describe(err error) (Code, string) {
	var e *Error
	if As(err, &e) {
		return e.Code, e.Message
	}
	return "", "Непонятная ошибка. Загляни в лог приложения."
}
