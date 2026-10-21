package server

import (
	"context"
	"fmt"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/i18n"
	"songrequest/internal/links"
	"songrequest/internal/match"
	"songrequest/internal/twitch"
)

// resolveTimeout — сколько ждём подбор трека. Пять запросов к Spotify с
// повторами укладываются с запасом, а зритель не должен ждать минуту.
const resolveTimeout = 25 * time.Second

// resolveOrder ищет заказанный трек в Spotify и ставит его в очередь.
//
// Не нашли — баллы возвращаются, и зритель получает объяснение в чат. Заказ,
// за который списали баллы и промолчали, — это жалоба в чат через минуту.
func (s *Server) resolveOrder(ctx context.Context, r twitch.Redemption) {
	// Срок ограничивает только поиск. Возврат баллов и сообщение зрителю
	// живут отдельно: раньше они шли с тем же контекстом, и если поиск съел
	// все двадцать пять секунд, возврат падал сразу — баллы не возвращались,
	// зритель ничего не узнавал, в логе оставалась одна строка.
	base := ctx
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	// Плейлист в награде за трек — отдельный разговор.
	//
	// Раньше такой ссылки просто не существовало для приложения: она не
	// опознавалась вовсе, текст после вырезания адреса оказывался пустым, и
	// зритель получал «ты не написал, что заказываешь». Теперь плейлисты
	// приложение читает, но за них платят другой наградой и по другой цене —
	// и сказать об этом надо прямо, иначе человек будет кидать плейлист в ту
	// же награду, пока не кончатся баллы.
	if l, ok := links.Find(r.UserInput); ok && l.IsCollection() {
		s.log.Info("плейлист заказан не той наградой",
			"зритель", r.UserLogin, "ссылка", l.URL)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchFailed,
			Note:  i18n.T("Это плейлист, а не трек — нужна награда за плейлист"),
		})
		s.rejectRedemption(s.afterSearch(base), r, s.playlistRewardHint())
		return
	}

	// Команда вместо трека — зритель перепутал награду с чатом. Искать её
	// нельзя: «!скип» находил трек с Skip Marley, и баллы уходили за чужую
	// песню. Отказ с возвратом и подсказкой, и ни одного запроса к Spotify.
	if isChatCommand(r.UserInput, s.prefix()) {
		s.log.Info("в награду написали команду", "зритель", r.UserLogin, "текст", r.UserInput)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchFailed,
			Note:  i18n.T("Это команда чата, а не трек"),
		})
		s.rejectRedemption(base, r, i18n.T("это команда — её пишут в чат, а в награду пиши артиста и название"))
		return
	}

	// Ссылка важнее текста: зритель уже указал конкретный трек, и гадать
	// по названию незачем.
	link, hasLink := s.fromLink(ctx, r.UserInput)
	if hasLink && link.Track != nil {
		s.acceptTrack(ctx, r, *link.Track, false, i18n.T("Взято по ссылке"))
		return
	}

	// Адрес в поисковом запросе бесполезен всегда — и когда мы ссылку
	// разобрали, и когда она от незнакомого сервиса.
	text := links.Strip(r.UserInput)
	if hasLink && link.Query != "" {
		text = link.Query
	}

	req := match.Parse(text)
	key := match.Key(req)

	if req.Title == "" {
		// Текста нет, но есть ссылка — по цепочке она сыграется.
		if hasLink && s.playElsewhere(ctx, r, req, link, viaLink) {
			return
		}
		// Ссылка на ролик, а YouTube стример выключил.
		if hasLink && s.youtubeOff() && isElsewhereLink(r.UserInput) {
			s.state.SetOrderMatch(r.ID, app.OrderMatch{State: app.MatchFailed, Note: i18n.T("YouTube выключен в настройках")})
			s.rejectRedemption(s.afterSearch(base), r, i18n.T("ссылки на YouTube и Яндекс.Музыку здесь не играют — закажи трек из Spotify"))
			return
		}
		// Ссылка есть, а играть её нечем: запасной проигрыватель ещё не
		// готов. Раньше зритель получал в ответ «ты не написал, что
		// заказываешь» — самый сбивающий с толку ответ из возможных, ведь
		// написал он ровно то, что просили.
		if s.youtubeLinkNotReady(r.UserInput) {
			s.log.Info("ссылка на видео, а проигрыватель не готов",
				"зритель", r.UserLogin, "заказ", r.UserInput, "код", errs.YouTubeNoMpv)
			s.state.SetOrderMatch(r.ID, app.OrderMatch{
				State: app.MatchFailed,
				Note:  i18n.T("Ссылка на видео, а запасной проигрыватель ещё не готов"),
			})
			s.rejectRedemption(s.afterSearch(base), r,
				i18n.T("это ссылка на видео, а проигрыватель для него ещё не готов — попробуй позже или закажи текстом"))
			return
		}

		// Ссылка в заказе была, но прочитать её не вышло: YouTube не отдал
		// ролик, страница Яндекс.Музыки не открылась, Spotify не ответил.
		//
		// 27.08 владелец кинул обычную ссылку на YouTube и получил в ответ
		// «ты не написал, что заказываешь. Баллы вернул» — при том что
		// написал он ровно то, что просили. Проверка ниже ловила только
		// один случай из многих: «проигрыватель ещё не качается». Здесь
		// причина уже известна, её и говорим.
		if link.Note != "" {
			s.log.Info("ссылку прочитать не вышло",
				"зритель", r.UserLogin, "заказ", r.UserInput, "причина", link.Note)
			s.state.SetOrderMatch(r.ID, app.OrderMatch{
				State: app.MatchFailed,
				Note:  i18n.F("Ссылку прочитать не вышло: %s", link.Note),
			})
			s.rejectRedemption(s.afterSearch(base), r,
				i18n.F("твою ссылку не удалось прочитать (%s) — попробуй другую или напиши название текстом", link.Note))
			return
		}

		s.log.Info("заказ без текста", "зритель", r.UserLogin)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchFailed,
			Note:  i18n.T("Зритель не написал, что заказывает"),
		})
		s.rejectRedemption(s.afterSearch(base), r, i18n.T("ты не написал, что заказываешь"))
		return
	}

	// Сначала кэш: один и тот же трек заказывают десятками за стрим.
	if hit, ok, err := s.matchCache.Get(ctx, key); err != nil {
		s.log.Warn("не прочитал кэш подбора", "ошибка", err)
	} else if ok {
		s.log.Info("трек взят из памяти",
			"заказ", r.UserInput, "трек", hit.Artist+" — "+hit.Title, "ручное", hit.Manual)
		s.acceptTrack(ctx, r, match.Candidate{
			ID: hit.TrackID, URI: "spotify:track:" + hit.TrackID,
			Title: hit.Title, Artists: []string{hit.Artist},
			DurationMs: hit.DurationMs, CoverURL: hit.CoverURL,
		}, false, memoryNote(hit.Manual))
		return
	}

	cfg := s.cfg.Get()
	opts := matchOptionsFromConfig(cfg)
	// Страна аккаунта: у Spotify свой каталог в каждой стране, и трек,
	// не лицензированный в ней, попросту не заиграет.
	if me := s.spotify.Account(); me != nil {
		opts.Market = me.Country
	}
	// Длительность из ссылки — самый сильный сигнал против каверов,
	// ускоренных версий и часовых лупов.
	opts.WantMs = link.WantMs

	res, err := match.Find(ctx, s.spotify, req, opts)

	// Spotify не пустил по стране или не ответил — это дело дороги, а не
	// поиска. Поднимаем обход, ждём его и спрашиваем ещё раз.
	//
	// 11.10 у тестера: обход не был поднят, поиск получил «не работает из
	// этой страны», и заказ «Я в деле — SLAVA MARLOW», который в Spotify
	// есть, ушёл на YouTube. Раньше отказ поиска обход не будил вовсе.
	//
	// Только если обхода сейчас нет: если он уже стоит и Spotify всё равно
	// отказал, сменой сервера занимается noteSpotifyError, и держать заказ
	// ради этого незачем.
	if err != nil && roadTrouble(err) && s.tunnel != nil && s.cfg.Get().TunnelOn && !s.tunnel.Status().On {
		s.noteSpotifyError(err)
		if s.waitTunnel(ctx, 40*time.Second) {
			s.log.Info("обход поднят — повторяю поиск", "заказ", r.UserInput)
			res, err = match.Find(ctx, s.spotify, req, opts)
		}
	}

	if err != nil {
		code, text := errs.Describe(err)
		s.log.Error("поиск трека не удался", "заказ", r.UserInput, "код", code, "ошибка", err)
		// Пробы поиска (полтора десятка запросов) нужны, когда поиск ломается
		// сам по себе. При отказе по стране или без связи они ничего не
		// скажут, а запросы потратят.
		//
		// И не при ограничении Spotify: 12.10 пробы ушли шестью запросами в
		// уже закрытый поиск — ответ известен заранее, а запросы на счету.
		if !roadTrouble(err) && errs.CodeOf(err) != errs.SpotifyRateLimit &&
			errs.CodeOf(err) != errs.SpotifyQuota {
			s.autoProbeSearch(req.Clean)
		}

		// Отказ поиска — не конец заказа. Запасные пути раньше стояли
		// только ниже, в ветке «поиск отработал, но ничего не подошло», и
		// любая ошибка Spotify выбрасывала вместе с собой уже разобранную
		// ссылку на ролик: название, длительность и адрес лежали готовые,
		// mpv стоял готовый, а зритель получал «попробуй ещё раз».
		//
		// Ровно это и увидел тестер 27.08: Spotify отвечал 400 на каждый
		// поиск трека, и вместе с текстовыми заказами перестали работать
		// ссылки на YouTube и Яндекс.Музыку — хотя играть их Spotify не
		// требуется вовсе.
		if s.playElsewhere(ctx, r, req, link, viaNoAnswer) {
			s.log.Info("Spotify не ответил — заказ сыграет мимо него",
				"заказ", r.UserInput, "код", code)
			return
		}

		s.state.SetOrderMatch(r.ID, app.OrderMatch{State: app.MatchFailed, Note: text})
		s.rejectRedemption(s.afterSearch(base), r, i18n.T("не получилось поискать трек, попробуй ещё раз"))
		return
	}

	if !res.Found {
		// «Кандидатов 0» и «кандидатов 20, но все мимо» — два разных диагноза,
		// и по телефону их не различить. Поэтому пишем и то, что Spotify
		// вернул, и почему это не подошло.
		why := make([]string, 0, len(res.Rejected))
		for _, bad := range res.Rejected {
			why = append(why, fmt.Sprintf("%s — %s (%.2f, %s)",
				bad.Artist, bad.Title, bad.Score, bad.Why))
		}
		s.log.Info("трек не найден",
			"заказ", r.UserInput, "артист", req.Artist, "название", req.Title,
			"запросы", res.Attempts, "кандидатов", res.Considered,
			"порог", cfg.MatchMaybe, "страна_аккаунта", opts.Market,
			"отсеяно_страной", res.AbroadOnly, "лучшие_из_отвергнутых", why)

		// «Нет в Spotify» и «есть, но не в твоей стране» — разные вещи.
		// Первое лечится другим запросом, второе — только сменой страны
		// аккаунта, и пока об этом не сказать, стример будет думать, что
		// сломан поиск.
		note := i18n.T("В Spotify не нашлось")
		if res.AbroadOnly > 0 {
			note = i18n.F("Есть в Spotify, но не в стране аккаунта (%s)", opts.Market)
			s.state.NotifyWarn(errs.SpotifyCountry,
				i18n.F("«%s» есть в Spotify, но не издан в стране твоего аккаунта (%s). Такие треки играть нельзя — их не найдёт и поиск.",
					req.Clean, opts.Market))
		}
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchMissing,
			Note:  note,
		})

		// Spotify не всесилен: в нём нет половины русского андеграунда и
		// почти ничего из мемов. Дальше — по цепочке запасных источников.
		if s.playElsewhere(ctx, r, req, link, viaMissing) {
			return
		}
		if res.AbroadOnly > 0 {
			s.rejectRedemption(s.afterSearch(base), r,
				i18n.T("этот трек не издан в стране аккаунта стримера, Spotify его не отдаёт"))
			return
		}
		if s.youtubeOff() {
			s.rejectRedemption(s.afterSearch(base), r, youtubeOffReason())
			return
		}
		s.rejectRedemption(s.afterSearch(base), r, i18n.T("не нашёл такого трека ни в Spotify, ни на YouTube"))
		return
	}

	// Зритель прислал ролик, а Spotify нашёл лишь похожее. Тогда играем
	// сам ролик: он точно то, что хотели.
	//
	// 11.10: ссылка на «Puvel — Малиновая елда» нашлась в Spotify как
	// «GAYAZOV$ BROTHER$ — МАЛИНОВАЯ ЛАДА» (артист мимо, оценка 0.57) — и
	// заиграла совсем другая песня. Точное совпадение по-прежнему берём из
	// Spotify: там звук лучше и не нужен второй проигрыватель.
	if link.YouTube != nil && res.Uncertain {
		s.log.Info("по ссылке на ролик Spotify нашёл лишь похожее — играю ролик",
			"заказ", r.UserInput, "в_spotify", firstArtist(res.Track.Artists)+" — "+res.Track.Title,
			"оценка", res.Score.Total)
		if s.playElsewhere(ctx, r, req, link, viaLink) {
			return
		}
	}

	s.log.Info("трек найден",
		"заказ", r.UserInput,
		"трек", firstArtist(res.Track.Artists)+" — "+res.Track.Title,
		"оценка", res.Score.Total, "почему", res.Score.Why,
		"неточно", res.Uncertain, "кандидатов", res.Considered)

	// В памяти держим только уверенные ответы: сомнительный подбор
	// закрепится и будет повторяться до конца стрима.
	if !res.Uncertain {
		if err := s.matchCache.Put(ctx, key, match.Hit{
			TrackID: res.Track.ID,
			Title:   res.Track.Title,
			Artist:  artistOf(res.Track),
			// Длительность и обложка обязательны: по ним считается лимит
			// длины, время до конца трека и картинка в панели. Без них заказ
			// из памяти обрывался на сорок пятой секунде.
			DurationMs: res.Track.DurationMs,
			CoverURL:   res.Track.CoverURL,
			Score:      res.Score.Total,
		}); err != nil {
			s.log.Warn("не запомнил подбор", "ошибка", err)
		}
	}

	note := ""
	if res.Uncertain {
		note = i18n.T("Совпадение неточное — проверь, тот ли трек")
	}

	s.acceptTrack(ctx, r, res.Track, res.Uncertain, note)
}

// rejectRedemption возвращает баллы и объясняет зрителю, почему.
func (s *Server) rejectRedemption(ctx context.Context, r twitch.Redemption, reason string) {
	refunded := true
	if r.ID != "" && r.RewardID != "" {
		if err := s.twitch.RefundRedemption(ctx, r.RewardID, r.ID); err != nil {
			s.log.Error("не смог вернуть баллы за неудачный заказ", "ошибка", err)
			s.state.NotifyError(err)
			refunded = false
		} else {
			s.state.SetRedemptionStatus(r.ID, app.OrderRefunded)
		}
	}
	// Обещать «вернул», когда Twitch возврат не принял, нельзя: зритель
	// ждёт баллы, которых не будет, пока стример не вернёт их сам.
	if !refunded {
		s.say(ctx, i18n.F("@%s, %s. Баллы вернёт стример вручную.", r.UserName, reason))
		return
	}
	s.say(ctx, i18n.F("@%s, %s. Баллы вернул.", r.UserName, reason))
}

func memoryNote(manual bool) string {
	if manual {
		return i18n.T("Из твоего исправления")

	}
	return ""
}

// matchOptionsFromConfig нужен тестам и панели, чтобы не собирать структуру
// вручную в двух местах.
func matchOptionsFromConfig(cfg config.Config) match.Options {
	return match.Options{
		Accept: cfg.MatchAccept,
		Maybe:  cfg.MatchMaybe,
		Weights: match.Weights{
			Title:      cfg.MatchWeight.Title,
			Artist:     cfg.MatchWeight.Artist,
			Duration:   cfg.MatchWeight.Duration,
			Popularity: cfg.MatchWeight.Popularity,
			Version:    cfg.MatchWeight.Version,
		},
	}
}

// artistOf — первый артист кандидата или пусто.
func artistOf(c match.Candidate) string {
	return firstArtist(c.Artists)
}

// firstArtist — первый артист трека или пустая строка.
//
// Отдельная функция, потому что мест, где брали Artists[0] голым, набралось
// три штуки, и каждое из них зритель может дёрнуть сам: Spotify изредка
// отдаёт трек без артистов (локальный файл, эпизод подкаста, снятый с
// продажи трек), и приложение падало целиком. Заказ за баллы, роняющий
// программу посреди стрима, — это слишком дешёвая цена за одну проверку.
func firstArtist(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// afterSearch даёт свежий срок на то, что делается после поиска: возврат
// баллов и ответ зрителю. Поиск мог израсходовать весь свой, а эти два дела
// обязаны случиться в любом случае.
func (s *Server) afterSearch(base context.Context) context.Context {
	ctx, cancel := context.WithTimeout(base, 20*time.Second)
	// Отменяем по сроку, а не по возврату из функции: вызывающий уходит
	// сразу, а запросу надо дожить.
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
