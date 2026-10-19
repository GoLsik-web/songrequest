// Package errs — ошибки с коротким кодом и человеческим текстом.
//
// Смысл кода: стример на своём компьютере видит в панели «SP-04» и просто
// называет его голосом, а разработчик по коду сразу знает место в программе.
// Текст при этом пишем так, чтобы человек понял, что делать, без слова «токен».
package errs

import (
	"fmt"

	"songrequest/internal/i18n"
)

// Code — короткий код ошибки, который видно в панели.
type Code string

const (
	// Настройки
	NoClientID   Code = "CFG-01" // не заполнен client_id Spotify
	ConfigSave   Code = "CFG-02" // не смогли сохранить настройки
	ConfigBroken Code = "CFG-03" // файл настроек повреждён

	// Spotify
	SpotifyAuthStart    Code = "SP-01" // не удалось открыть страницу входа
	SpotifyAuthDenied   Code = "SP-02" // вход отклонён или подделан ответ
	SpotifyAuthToken    Code = "SP-03" // не удалось завершить вход
	SpotifyAuthExpired  Code = "SP-04" // авторизация слетела, нужен повторный вход
	SpotifyNoPremium    Code = "SP-05" // на аккаунте нет Premium
	SpotifyNoDevice     Code = "SP-06" // нет устройства, где играть
	SpotifyUnreachable  Code = "SP-07" // Spotify не отвечает
	SpotifyRateLimit    Code = "SP-08" // Spotify просит подождать
	SpotifySnapshot     Code = "SP-09" // не удалось запомнить, что играло
	SpotifyRestore      Code = "SP-10" // не удалось вернуть воспроизведение
	SpotifyStale        Code = "SP-11" // за время заказа музыку переключили руками
	SpotifyNothing      Code = "SP-12" // возвращать нечего
	SpotifyBadResponse  Code = "SP-13" // Spotify ответил не тем, чего мы ждали
	SpotifyNoScope      Code = "SP-14" // не выданы права, нужен повторный вход
	SpotifyPlanUnknown  Code = "SP-15" // Spotify не сказал, какая подписка
	SpotifyCountry      Code = "SP-16" // Spotify недоступен в стране аккаунта
	SpotifyProxy        Code = "SP-17" // прокси для Spotify настроен неверно
	SpotifyForeignTrack Code = "SP-18" // трек не издан в стране аккаунта стримера
	SpotifySearchLimit  Code = "SP-19" // Spotify отдаёт меньше результатов, чем просим
	// SpotifyQuota — кончился дневной объём запросов на весь аккаунт
	// разработчика. Отдельно от SP-08 нарочно: у той пауза меряется
	// секундами и лечится темпом, у этой — часами, и лечится только
	// ожиданием. Человеку это разные новости.
	SpotifyQuota Code = "SP-20"

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
	TwitchChat         Code = "TW-13" // Twitch не пропустил сообщение в чат
	// TwitchScopes — вход выдан без части прав. Чаще всего не хватает чтения
	// чата: право появилось позже приложения, старый вход про него не знает —
	// заказы идут, команды в чате молчат.
	TwitchScopes Code = "TW-14"

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
	// YouTubeAskNone — решают судьбу заказа с YouTube, который уже не ждёт:
	// стример нажал кнопку ровно тогда, когда вышел срок.
	YouTubeAskNone Code = "YT-07"

	// Обход блокировок
	TunnelNoKey        Code = "OB-01" // ключ не вставлен или в нём ничего нет
	TunnelBadKey       Code = "OB-02" // ключ записан непонятно
	TunnelSubscription Code = "OB-03" // не удалось скачать список серверов по ссылке
	TunnelNoTool       Code = "OB-04" // не удалось получить программу обхода
	TunnelStart        Code = "OB-05" // обход не запустился
	TunnelCheck        Code = "OB-06" // через обход Spotify всё равно не отвечает
	// TunnelCountry — обход работает, но Spotify не пускает адрес этого
	// сервера: «unavailable in this country». Отдельный код от OB-06, потому
	// что лечится он по-другому — не подъёмом обхода заново, а сменой сервера.
	// Ловить его по тексту ошибки нельзя: текст переписывают, код — нет.
	TunnelCountry Code = "OB-07"

	// Управление воспроизведением.
	//
	// PlayerIdle — нажали кнопку плеера, когда заказ не играет. Не поломка, но
	// сказать надо: иначе кнопка выглядит сломанной.
	PlayerIdle Code = "PL-01"
	// PlayerNoTool — заказ играет запасным проигрывателем, а его уже нет.
	PlayerNoTool Code = "PL-02"
	// PlayerNothingBack — «вернуть» нажали, а возвращать нечего: за этот
	// запуск ничего не скипали, или скипнутый заказ уже вернули.
	PlayerNothingBack Code = "PL-03"

	// Заказ плейлиста.
	//
	// PlaylistNone — решают судьбу плейлиста, которого в ожидании нет:
	// его уже одобрили, отклонили или номер назвали не тот.
	PlaylistNone Code = "PLS-01"
	// PlaylistEmpty — плейлист одобрили, а сыграть из него нечего: ни один
	// трек не нашёлся ни в Spotify, ни на YouTube.
	PlaylistEmpty Code = "PLS-02"

	// Обновление приложения
	//
	// UpdateNoRepo — не сказано, откуда брать обновления, или репозитория нет.
	UpdateNoRepo Code = "UPD-01"
	// UpdateCheck — не удалось спросить GitHub про выпуски.
	UpdateCheck Code = "UPD-02"
	// UpdateNoFile — выпуск есть, а файла программы в нём нет.
	UpdateNoFile Code = "UPD-03"
	// UpdateDownload — не удалось скачать или распаковать сборку.
	UpdateDownload Code = "UPD-04"
	// UpdateApply — скачали, а подменить программу не вышло: чаще всего нет
	// прав на папку, где она лежит.
	UpdateApply Code = "UPD-05"

	// Связь с OBS (obs-websocket). Не «OB-»: эти коды уже заняты обходом.
	OBSUnreachable Code = "OBS-01" // OBS не запущен или в нём выключен сервер WebSocket
	OBSAuth        Code = "OBS-02" // неверный или не введённый пароль
	OBSNoSource    Code = "OBS-03" // в сценах нет источника с виджетом
	OBSRequest     Code = "OBS-04" // OBS отказался выполнить команду

	// Яндекс.Музыка
	YandexRead Code = "YM-01" // не удалось прочитать страницу трека

	// Диагностика
	DiagExport Code = "DIAG-01" // не собрался архив с логом
)

// Error — ошибка с кодом. Message показываем стримеру, cause уходит в лог.
//
// Message всегда русский: он же пишется в журнал. Стримеру текст уходит
// через Text — там он переводится на язык панели. Для текстов со вставками
// (Newf, Wrapf) храним строку формата и значения, чтобы перевести формат
// целиком, а не склеенный результат.
type Error struct {
	Code    Code
	Message string
	cause   error
	format  string
	args    []any
}

// New создаёт ошибку с кодом и понятным текстом.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap оборачивает техническую ошибку в понятную.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// Newf — New с текстом по строке формата: Newf(code, "Ответ %d.", status).
// Строка формата — ключ перевода, поэтому собирать её на ходу нельзя.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), format: format, args: args}
}

// Wrapf — Wrap с текстом по строке формата. cause может быть nil.
func Wrapf(code Code, cause error, format string, args ...any) *Error {
	e := Newf(code, format, args...)
	e.cause = cause
	return e
}

// Phrase — вставка в Newf/Wrapf, которую тоже надо перевести
// («без объяснения»). Переводится в момент показа.
type Phrase string

func (p Phrase) String() string { return i18n.T(string(p)) }

// Text — текст для человека на языке панели.
func (e *Error) Text() string {
	if e.format != "" {
		return i18n.F(e.format, e.args...)
	}
	return i18n.T(e.Message)
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
	return fmt.Sprintf("%s · %s", e.Code, e.Text())
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
//
// К тексту добавляется настоящая причина, если она у нас же и с кодом. Иначе
// получается разговор ни о чём: владелец нажал «Запомнить», увидел «SP-09 Не
// смог запомнить, что играло в Spotify» — и всё. А внутри лежало объяснение,
// ради которого код и писался: «Spotify временно ограничил приложение и просит
// долгую паузу (осталось 3ч56м)». Человек в это время думал на свежий обход
// блокировок и чинил не то.
func Describe(err error) (Code, string) {
	var e *Error
	if !As(err, &e) {
		return "", i18n.T("Непонятная ошибка. Загляни в лог приложения.")
	}
	text := e.Text()
	if root := rootError(e); root != nil && root.Message != e.Message {
		text += " " + root.Text()
	}
	return e.Code, text
}

// rootError достаёт самое внутреннее наше объяснение. Чужие ошибки (обрыв
// сети, отказ библиотеки) сюда не попадают: они написаны не для людей.
func rootError(e *Error) *Error {
	var root *Error
	for cause := e.cause; cause != nil; {
		var inner *Error
		if !As(cause, &inner) {
			break
		}
		root = inner
		cause = inner.cause
	}
	return root
}

// HTTPStatusText — понятная фраза вместо голого номера ответа сервиса.
// Сам номер человеку ничего не говорит, его место в логе.
// Возвращает русский текст (для журнала и склейки); стримеру — через HTTPStatus.
func HTTPStatusText(service string, status int) string {
	return fmt.Sprintf(httpStatusFormat(status), service)
}

// HTTPStatus — ошибка с кодом и фразой HTTPStatusText, переводимая при показе.
func HTTPStatus(code Code, service string, status int) *Error {
	return Newf(code, httpStatusFormat(status), service)
}

// HTTPStatusPhrase — та же фраза как вставка в Newf/Wrapf: переводится при показе.
func HTTPStatusPhrase(service string, status int) fmt.Stringer {
	return httpPhrase{service, status}
}

type httpPhrase struct {
	service string
	status  int
}

func (p httpPhrase) String() string { return i18n.F(httpStatusFormat(p.status), p.service) }

func httpStatusFormat(status int) string {
	switch {
	case status >= 500:
		return "%s временно сбоит. Попробуй чуть позже."
	case status == 403 || status == 429:
		return "%s временно не пускает. Попробуй чуть позже."
	}
	return "%s отказал. Подробности в логе."
}
