package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/tunnel"
)

// Обход блокировок со стороны панели.
//
// Раньше стример поднимал обход руками: ставил Psiphon, лез в его настройки,
// переписывал номер порта в наше поле «Прокси для Spotify». Теперь он вставляет
// сюда ключ от своего VPN (или ссылку на подписку), а приложение само скачивает
// программу обхода, перебирает серверы и оставляет тот, через который Spotify
// отвечает.
//
// Ручное поле прокси при этом никуда не делось: у кого-то уже стоит своя
// программа обхода, и отбирать у него рабочий путь незачем. Если включён обход
// внутри приложения, он главнее — иначе два посредника спорили бы друг с другом.

// keyringTunnel — под каким именем ключ лежит в хранилище паролей Windows.
//
// В настройках (config.json) ключа нет и не будет: это пароль от VPN стримера,
// а config.json попадает в архив диагностики и вообще читается глазами.
const keyringTunnel = "tunnel_key"

// maxReselects — сколько раз за запуск приложение само меняет сервер обхода
// из-за отказов Spotify. Дальше карусель бессмысленна: причина не в серверах.
const maxReselects = 5

// tunnelStartTimeout — сколько всего даём на включение обхода. Внутри может
// быть скачивание программы (двадцать мегабайт) и перебор серверов подписки.
const tunnelStartTimeout = 5 * time.Minute

// savedKey — то, что лежит в хранилище паролей.
type savedKey struct {
	Key string `json:"key"`
}

// handleTunnelOn включает обход: сохраняет ключ, если его прислали, и
// поднимает соединение. Эта же ручка работает кнопкой «Проверить обход» —
// проверка и есть попытка поднять всё заново.
func (s *Server) handleTunnelOn(w http.ResponseWriter, r *http.Request) {
	if s.tunnel == nil || s.secrets == nil {
		http.Error(w, "обход в этой сборке недоступен", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Key string `json:"key"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	}
	key := strings.TrimSpace(body.Key)

	if key != "" {
		if err := s.saveTunnelKey(key); err != nil {
			s.fail(w, err)
			return
		}
	} else {
		var saved savedKey
		if err := s.secrets.GetJSON(keyringTunnel, &saved); err != nil || saved.Key == "" {
			s.fail(w, errs.New(errs.TunnelNoKey,
				"Ключ обхода не вставлен. Вставь ключ от VPN или ссылку на подписку."))
			return
		}
		key = saved.Key
	}

	// Человек нажал кнопку сам — значит, разрешает попробовать заново, в том
	// числе перебор серверов после отказов Spotify.
	s.reselects.Store(0)

	if err := s.cfg.Update(func(c *config.Config) { c.TunnelOn = true }); err != nil {
		s.fail(w, err)
		return
	}

	// Контекст запроса здесь не годится: человек может закрыть панель или
	// свернуть окно, а включение обхода — дело на минуты (скачать программу,
	// перебрать серверы). Оборвать это на середине значит оставить наполовину включённое
	// состояние.
	ctx, cancel := context.WithTimeout(s.baseContext(), tunnelStartTimeout)
	defer cancel()

	// Пока идёт перебор, в панели должно быть написано, что происходит:
	// первое включение занимает минуты, и молчащая карточка выглядит так,
	// будто кнопка не сработала.
	s.syncTunnel("Проверяю, нужен ли обход…")

	// Сначала спрашиваем Spotify напрямую: если он отвечает и так, обход
	// поднимать нельзя — получится туннель поверх туннеля. Ключ при этом уже
	// сохранён, и приложение поднимет обход само, как только Spotify замолчит.
	//
	// Обход перед этим гасим: проверка идёт через нынешнюю настройку клиента, а
	// нам нужно узнать именно про прямую дорогу. Если окажется, что она не
	// работает, обход тут же поднимется заново — он и так поднимался бы, кнопка
	// именно об этом.
	s.tunnel.Stop()
	s.applyProxy()
	checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
	direct, ownVPN := s.spotifyWorksDirect(checkCtx)
	checkCancel()
	if ownVPN {
		s.warnAboutOwnVPN()
	}
	if direct {
		s.log.Info("обход не понадобился: Spotify отвечает напрямую")
		s.applyProxy()
		s.syncTunnel("Обход наготове: Spotify пока отвечает и без него.")
		s.state.Notify("info", "Spotify отвечает и без обхода — похоже, у тебя уже включён VPN. "+
			"Ключ сохранён, обход держу наготове: перестанет отвечать — подниму сам.")
		writeJSON(w, map[string]any{"on": false, "standby": true})
		return
	}

	s.syncTunnel("Включаю обход… это может занять пару минут.")

	status, err := s.tunnel.Start(ctx, key, s.cfg.Get().TunnelServer, s.cfg.Get().TunnelToolPath)
	if err != nil {
		s.log.Error("обход не включился", "ошибка", err)
		s.state.NotifyError(err)
		s.syncTunnel(tunnelNote(err))
		s.fail(w, err)
		return
	}

	if err := s.cfg.Update(func(c *config.Config) { c.TunnelServer = status.Server }); err != nil {
		s.log.Warn("не запомнил рабочий сервер обхода", "ошибка", err)
	}
	s.applyProxy()
	s.syncTunnel("Обход работает: " + status.Server)
	s.state.Notify("info", "Обход включён, Spotify отвечает через "+status.Server)
	writeJSON(w, map[string]any{"on": true, "server": status.Server})
}

// handleTunnelOff выключает обход и возвращает Spotify на прямой путь (или на
// ручной прокси, если он вписан).
func (s *Server) handleTunnelOff(w http.ResponseWriter, r *http.Request) {
	if s.tunnel == nil {
		http.Error(w, "обход в этой сборке недоступен", http.StatusServiceUnavailable)
		return
	}
	s.tunnel.Stop()
	if err := s.cfg.Update(func(c *config.Config) { c.TunnelOn = false }); err != nil {
		s.fail(w, err)
		return
	}
	s.applyProxy()
	s.syncTunnel("Обход выключен.")
	s.state.Notify("info", "Обход выключен.")
	writeJSON(w, map[string]any{"on": false})
}

// handleTunnelForget убирает ключ совсем: из хранилища паролей и из настроек.
func (s *Server) handleTunnelForget(w http.ResponseWriter, r *http.Request) {
	if s.tunnel == nil || s.secrets == nil {
		http.Error(w, "обход в этой сборке недоступен", http.StatusServiceUnavailable)
		return
	}
	s.tunnel.Stop()
	if err := s.secrets.Delete(keyringTunnel); err != nil {
		s.log.Warn("не смог забыть ключ обхода", "ошибка", err)
	}
	if err := s.cfg.Update(func(c *config.Config) {
		c.TunnelOn = false
		c.TunnelKeyHint = ""
		c.TunnelServer = ""
	}); err != nil {
		s.fail(w, err)
		return
	}
	s.applyProxy()
	s.syncTunnel("Ключ забыт.")
	s.state.Notify("info", "Ключ обхода забыт.")
	writeJSON(w, map[string]any{"on": false, "has_key": false})
}

// saveTunnelKey кладёт ключ в хранилище паролей, а в настройки — только
// безобидную подпись для показа в панели.
func (s *Server) saveTunnelKey(key string) error {
	// Проверяем до сохранения: пусть человек узнает про опечатку сразу, а не
	// через минуту перебора серверов.
	hint, err := tunnelHint(key)
	if err != nil {
		return err
	}
	if err := s.secrets.PutJSON(keyringTunnel, savedKey{Key: key}); err != nil {
		return errs.Wrap(errs.TunnelNoKey, "Не смог сохранить ключ в хранилище Windows.", err)
	}
	// Ключ не должен попасть в лог ни через нашу строчку, ни через чужую
	// ошибку, где он окажется внутри адреса.
	s.log.Redactor.Add(key)

	return s.cfg.Update(func(c *config.Config) {
		c.TunnelKeyHint = hint
		// Ключ сменился — прошлый рабочий сервер к нему отношения не имеет.
		c.TunnelServer = ""
	})
}

// tunnelHint описывает вставленный ключ так, чтобы это можно было показать в
// панели: без опознавательного номера и пароля.
func tunnelHint(key string) (string, error) {
	if tunnel.LooksLikeSubscription(key) {
		u, err := url.Parse(strings.TrimSpace(key))
		if err != nil || u.Host == "" {
			return "", errs.New(errs.TunnelBadKey, "Ссылка на подписку записана непонятно.")
		}
		return "подписка " + u.Host, nil
	}

	servers, err := tunnel.ParseKey(key)
	if err != nil {
		return "", err
	}
	if len(servers) == 1 {
		return "ключ " + servers[0].String(), nil
	}
	return "ключи, серверов: " + strconv.Itoa(len(servers)), nil
}

// StartTunnel поднимает обход при запуске приложения, если он был включён.
//
// Делается это в стороне: скачивание программы и перебор серверов занимают
// время, а панель должна открыться сразу. Пока обход поднимается, Spotify
// ходит как ходил — то есть, скорее всего, никак, и об этом честно написано
// в панели.
func (s *Server) StartTunnel(ctx context.Context) {
	if s.tunnel == nil || s.secrets == nil {
		return
	}
	// Обход может умереть посреди стрима: сервер разорвал связь, кончилась
	// подписка. Молчать об этом нельзя — заказы начнут падать с «Spotify не
	// отвечает», и причину будут искать не там.
	s.tunnel.SetOnDown(func() {
		s.applyProxy()
		s.reviveTunnel("обход остановился сам")
	})

	cfg := s.cfg.Get()

	var saved savedKey
	if err := s.secrets.GetJSON(keyringTunnel, &saved); err == nil && saved.Key != "" {
		// Маскируем ключ в логе сразу, ещё до первой попытки: в тексте ошибки
		// от чужой программы он может встретиться целиком.
		s.log.Redactor.Add(saved.Key)
	}

	// Сторож работает всегда, даже когда обход сейчас выключен: его могут
	// включить кнопкой в панели, и следить за ним надо с этой же минуты.
	go s.watchTunnel(ctx)

	if !cfg.TunnelOn || saved.Key == "" {
		s.syncTunnel("")
		return
	}

	s.syncTunnel("Проверяю, нужен ли обход…")
	go func() {
		startCtx, cancel := context.WithTimeout(ctx, tunnelStartTimeout)
		defer cancel()

		// Сначала спрашиваем Spotify напрямую.
		//
		// Обход нужен не всегда: у стримера может быть включён свой VPN на весь
		// компьютер, и тогда Spotify отвечает и так. Поднимать поверх этого свой
		// обход — значит гнать каждый запрос через два туннеля подряд: живьём
		// 31.08 ответ шёл 19,5 секунды вместо 0,17, а приложение ждёт 12 и
		// считает такое молчанием. Заодно это экономит холодный старт: перебор
		// серверов занимает до сорока секунд.
		checkCtx, checkCancel := context.WithTimeout(startCtx, 30*time.Second)
		direct, ownVPN := s.spotifyWorksDirect(checkCtx)
		checkCancel()
		if ownVPN {
			s.warnAboutOwnVPN()
		}
		if direct {
			s.log.Info("обход не понадобился: Spotify отвечает напрямую")
			s.syncTunnel("Обход наготове: Spotify пока отвечает и без него.")
			s.state.Notify("info", "Spotify отвечает без обхода — обход держу наготове. "+
				"Перестанет отвечать (например, выключишь свой VPN) — подниму сам.")
			return
		}

		status, err := s.tunnel.Start(startCtx, saved.Key, cfg.TunnelServer, cfg.TunnelToolPath)
		if err != nil {
			s.log.Error("обход не поднялся при запуске", "ошибка", err)
			s.state.NotifyError(err)
			s.syncTunnel(tunnelNote(err))
			// Дальше пробуем сами: интернета могло не быть ещё пару секунд
			// (компьютер только что проснулся, Wi-Fi не поднялся).
			s.reviveTunnel("обход не поднялся при запуске")
			return
		}
		if status.Server != cfg.TunnelServer {
			if err := s.cfg.Update(func(c *config.Config) { c.TunnelServer = status.Server }); err != nil {
				s.log.Warn("не запомнил рабочий сервер обхода", "ошибка", err)
			}
		}
		s.applyProxy()
		s.syncTunnel("Обход работает: " + status.Server)
	}()
}

// noteSpotifyError решает, не в сервере ли обхода дело.
//
// Spotify отвечает «Spotify is unavailable in this country» тем адресам,
// которые считает неподходящей страной, — а такими бывают целые дата-центры,
// где стоят серверы обхода. Проверка при выборе сервера ходит без ключа
// доступа и такой отказ видит не всегда.
//
// Живьём 30.08 это выглядело так: в панели «Обход работает», а приложение не
// может даже запомнить играющий трек. Человек чинил обход, хотя чинить надо
// было выбор сервера. Теперь приложение делает это само: помечает сервер
// негодным и переходит на следующий.
func (s *Server) noteSpotifyError(err error) {
	if s.tunnel == nil {
		return
	}
	code := errs.CodeOf(err)

	// Spotify молчит (сеть, срок ожидания). Это не про страну сервера, это про
	// то, что выбранная дорога до Spotify перестала работать: либо обход лёг,
	// либо наоборот — стример включил свой VPN, и наш обход поверх него стал
	// слишком медленным. Разбирается отдельно.
	if code == errs.SpotifyUnreachable {
		s.noteSpotifySilence()
		return
	}
	if code != errs.SpotifyCountry {
		return
	}

	st := s.tunnel.Status()
	if !st.On || st.Server == "" {
		// Spotify отказывает по стране, а обхода нет. Если ключ есть и обход
		// разрешён — самое время его поднять: ровно для этого он и заведён.
		if s.cfg.Get().TunnelOn && s.reviveTunnel("Spotify не работает из этой страны") {
			// И заодно проверим, не чужой ли VPN тому виной: если до Spotify
			// достаёт и напрямую, дело не в провайдере, а в том, через какой
			// адрес мы к нему приходим. Проверка сетевая, поэтому в стороне.
			go func() {
				ctx, cancel := context.WithTimeout(s.baseContext(), 15*time.Second)
				defer cancel()
				if tunnel.CheckSpotify(ctx, "") == nil {
					s.warnAboutOwnVPN()
				}
			}()
		}
		return
	}
	// Один перебор за раз: отказы сыплются пачкой (опрос, заказ, проверка), и
	// без этого приложение начало бы менять сервер на каждый из них.
	if !s.reselecting.CompareAndSwap(false, true) {
		return
	}

	// И не одновременно с подъёмом упавшего обхода. Живьём 31.08: обход
	// отвалился, приложение начало поднимать его заново — и в ту же секунду
	// Spotify отказал через старый сервер, отчего пошёл ещё и перебор. Два
	// перебора шли навстречу друг другу и гасили работу друг друга: в логе
	// две «попытки=2» подряд и два поднятых обхода за секунду.
	if s.reviving.Load() {
		s.reselecting.Store(false)
		return
	}

	// И не бесконечно. Если Spotify отказывает через каждый сервер подряд,
	// дело не в сервере: скорее всего, страна аккаунта Spotify не та, и
	// перебор превратится в вечную карусель со сменой сервера каждую минуту.
	if s.reselects.Add(1) > maxReselects {
		s.reselecting.Store(false)
		s.log.Warn("Spotify отказывает через все серверы обхода — перебор остановлен")
		s.state.NotifyCode("error", string(errs.SpotifyCountry),
			"Spotify отказывает через все серверы обхода, которые приложение пробовало. "+
				"Дело, скорее всего, не в них: проверь страну аккаунта Spotify "+
				"(в неработающей стране он отказывает при любом VPN).")
		return
	}

	s.log.Warn("через этот сервер обхода Spotify отказывает — беру другой", "сервер", st.Server)
	s.tunnel.MarkBad(st.Server)
	s.state.Notify("info", "Через сервер обхода «"+st.Server+"» Spotify не работает. Беру другой сервер.")
	s.syncTunnel("Сервер не подошёл — ищу другой…")

	go func() {
		defer s.reselecting.Store(false)

		var saved savedKey
		if s.secrets == nil {
			return
		}
		if err := s.secrets.GetJSON(keyringTunnel, &saved); err != nil || saved.Key == "" {
			return
		}

		ctx, cancel := context.WithTimeout(s.baseContext(), tunnelStartTimeout)
		defer cancel()

		// prefer пустой: прошлый «рабочий» сервер как раз и оказался негодным.
		status, err := s.tunnel.Start(ctx, saved.Key, "", s.cfg.Get().TunnelToolPath)
		if err != nil {
			s.log.Error("другой сервер обхода не нашёлся", "ошибка", err)
			s.state.NotifyError(err)
			s.applyProxy()
			s.syncTunnel(tunnelNote(err))
			return
		}
		if err := s.cfg.Update(func(c *config.Config) { c.TunnelServer = status.Server }); err != nil {
			s.log.Warn("не запомнил рабочий сервер обхода", "ошибка", err)
		}
		s.applyProxy()
		s.syncTunnel("Обход работает: " + status.Server)
		s.state.Notify("info", "Обход перешёл на "+status.Server)
	}()
}

// reviveDelays — через сколько пробовать поднять упавший обход.
//
// Первая попытка почти сразу: чаще всего сервер просто разорвал связь, и
// следующий (или тот же) поднимается с первого раза. Дальше паузы растут — если
// у стримера кончилась подписка или пропал интернет, долбиться каждые три
// секунды бессмысленно. Последняя пауза повторяется, пока обход не поднимется
// или человек не выключит его сам: стрим идёт часами, и сдаваться нельзя.
var reviveDelays = []time.Duration{
	3 * time.Second,
	10 * time.Second,
	30 * time.Second,
	time.Minute,
	5 * time.Minute,
}

// reviveTunnel поднимает обход, который отвалился сам.
//
// Зачем это вообще появилось. До 31.08 приложение об упавшем обходе только
// сообщало: возвращало Spotify на прямой путь и писало в панель «нажми
// «Проверить обход»». Живьём вышло так: обход проработал сутки, умер в 07:10,
// и приложение до вечера сидело на прямом соединении — то есть без Spotify
// вообще. Стример в это время смотрел не в панель, а в игру, и выглядело это
// как «обход не работает». Теперь приложение поднимает его само и молча, а
// человека беспокоит только тогда, когда не получается.
func (s *Server) reviveTunnel(reason string) bool {
	if s.tunnel == nil || s.secrets == nil {
		return false
	}
	// Один подъём за раз. Позвать сюда могут двое разом: смерть программы
	// обхода и сторож, который в ту же секунду не достучался до посредника.
	if !s.reviving.CompareAndSwap(false, true) {
		return false
	}
	// И не поверх перебора серверов (см. noteSpotifyError): он занимается ровно
	// тем же самым — поднимает обход, только с другой причиной.
	if s.reselecting.Load() {
		s.reviving.Store(false)
		return false
	}

	s.log.Warn("поднимаю обход заново", "причина", reason)
	s.syncTunnel("Обход отвалился — поднимаю заново…")
	s.state.Notify("info", "Обход блокировок отвалился. Поднимаю заново, ничего нажимать не надо.")

	go func() {
		defer s.reviving.Store(false)

		ctx := s.baseContext()
		for attempt := 0; ; attempt++ {
			// Человек выключил обход сам или забыл ключ — подъём отменяется.
			if !s.cfg.Get().TunnelOn {
				s.log.Info("подъём обхода отменён: обход выключен в настройках")
				return
			}
			var saved savedKey
			if err := s.secrets.GetJSON(keyringTunnel, &saved); err != nil || saved.Key == "" {
				s.log.Info("подъём обхода отменён: ключа больше нет")
				return
			}

			delay := reviveDelays[min(attempt, len(reviveDelays)-1)]
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			startCtx, cancel := context.WithTimeout(ctx, tunnelStartTimeout)
			// prefer оставляем: чаще всего сервер жив, а связь просто оборвало,
			// и начинать перебор двадцати одного сервера заново незачем.
			status, err := s.tunnel.Start(startCtx, saved.Key, s.cfg.Get().TunnelServer, s.cfg.Get().TunnelToolPath)
			cancel()
			if err != nil {
				s.log.Warn("обход не поднялся", "попытка", attempt+1, "ошибка", err)
				s.syncTunnel("Обход отвалился, пробую поднять… (" + tunnelNote(err) + ")")
				// О неудаче говорим человеку один раз, на третьей попытке:
				// раньше — суета на ровном месте, позже — он уже сам заметил,
				// что заказы встали, и ищет причину.
				if attempt == 2 {
					s.state.NotifyError(err)
				}
				continue
			}

			if status.Server != s.cfg.Get().TunnelServer {
				if err := s.cfg.Update(func(c *config.Config) { c.TunnelServer = status.Server }); err != nil {
					s.log.Warn("не запомнил рабочий сервер обхода", "ошибка", err)
				}
			}
			s.applyProxy()
			s.syncTunnel("Обход работает: " + status.Server)
			s.state.Notify("info", "Обход поднялся сам: "+status.Server)
			s.log.Info("обход поднялся заново", "сервер", status.Server, "попытка", attempt+1)
			return
		}
	}()
	return true
}

// tunnelWatchStep — как часто сторож проверяет, что посредник ещё живой.
const tunnelWatchStep = time.Minute

// watchTunnel сторожит обход изнутри приложения.
//
// Смерть программы обхода ловится сама (см. tunnel.watch), но бывает тише:
// процесс жив, а вход посредника уже никого не пускает — например, Windows
// усыпила сеть после спящего режима. Тогда в панели написано «Обход работает»,
// а Spotify молчит. Проверка местная: стучимся в свой же порт на 127.0.0.1,
// наружу не ходим и норму запросов Spotify не тратим.
func (s *Server) watchTunnel(ctx context.Context) {
	t := time.NewTicker(tunnelWatchStep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.cfg.Get().TunnelOn || s.reviving.Load() {
				continue
			}
			if !s.tunnel.Status().On {
				// Обход в запасе: Spotify отвечает и без него (скорее всего, у
				// стримера включён свой VPN). Поднимать его просто так нельзя —
				// два туннеля подряд работают хуже одного. Понадобится —
				// поднимет noteSpotifySilence, как только Spotify замолчит.
				continue
			}
			if !s.tunnel.Alive() {
				s.log.Warn("посредник обхода перестал отвечать")
				s.tunnel.Stop()
				s.applyProxy()
				s.reviveTunnel("посредник перестал отвечать")
			}
		}
	}
}

// spotifyWorksDirect отвечает на главный вопрос: нужен ли обход прямо сейчас.
//
// Проверок две, и вторая обязательна.
//
// Первая — обычный запрос без входа: доходит ли до Spotify хоть что-нибудь.
// Вторая — запрос со входом. Разница между ними и подвела 31.08: Spotify
// отвечает всем подряд, но адресам, которые считает неподходящей страной (а
// это и целые дата-центры, где стоят серверы VPN), отказывает только тогда,
// когда его спрашивают от чьего-то имени. Приложение по первой проверке решило,
// что обход не нужен, а список плейлистов тут же получил «SP-16 Spotify не
// работает из этой страны».
//
// Звать только тогда, когда Spotify ходит напрямую: проверка идёт через
// нынешнюю настройку клиента, своей дороги у неё нет.
// Второе возвращаемое значение — догадка «у стримера включён свой VPN на весь
// компьютер»: до Spotify достаёт кто угодно, а вот со входом он отказывает
// именно адресам дата-центров, где такие VPN и живут. Это стоит сказать вслух:
// свой обход поверх чужого VPN работает, но так медленно, что запросы не
// укладываются в срок ожидания.
func (s *Server) spotifyWorksDirect(ctx context.Context) (works bool, vpnLikely bool) {
	if err := tunnel.CheckSpotify(ctx, ""); err != nil {
		s.log.Debug("напрямую Spotify не отвечает", "ошибка", err)
		return false, false
	}
	if s.spotify == nil || !s.spotify.Connected() {
		// Входа ещё нет — проверить со входом нечем. Скажем «работает»: если
		// окажется, что нет, приложение поднимет обход на первом же отказе.
		return true, false
	}

	// Спрашиваем именно плеер, а не профиль.
	//
	// 31.08 выяснилось на живой машине: через включённый VPN владельца профиль
	// (`/v1/me`) отвечал спокойно, а плейлисты и плеер — «403, не работает из
	// этой страны». Проверка по профилю поэтому ничего не значит: приложение
	// решало, что всё хорошо, а список плейлистов в ту же секунду получал SP-16.
	// Плеер — то, без чего приложение бесполезно, и стоит запрос ровно один.
	if _, _, err := s.spotify.State(ctx); err != nil {
		s.log.Info("напрямую Spotify отвечает, но плеер не отдаёт — обход нужен",
			"ошибка", err)
		return false, errs.CodeOf(err) == errs.SpotifyCountry
	}
	return true, false
}

// warnAboutOwnVPN объясняет, почему всё медленно, когда у стримера включён свой
// VPN, а Spotify через него не работает.
//
// Живьём 31.08 на машине владельца: через его VPN Spotify отвечал на запрос без
// входа, но отказывал со входом («не работает из этой страны»). Приложению
// пришлось поднимать свой обход поверх, и каждый запрос пошёл через два
// туннеля подряд — 19,5 секунды вместо 0,17 при сроке ожидания в 12. Само
// приложение тут бессильно: выключить чужой VPN оно не может, а идти в обход
// мимо него — тем более. Значит, надо сказать человеку.
func (s *Server) warnAboutOwnVPN() {
	s.log.Warn("свой VPN стримера мешает: Spotify через него не пускает со входом")
	s.state.Notify("error",
		"Похоже, у тебя включён свой VPN, а Spotify через него не работает: он отказывает "+
			"адресам таких серверов. Приложение подняло свой обход поверх твоего VPN — так всё "+
			"работает, но медленно, и заказы могут отваливаться. Лучше выключить свой VPN: "+
			"приложению он не нужен, обход у него свой.")
}

// silenceLimit — сколько отказов подряд считаем поломкой дороги, а не
// случайной заминкой. Плеер спрашивают раз в пару секунд, и одиночные «не
// дошло» бывают от чего угодно: моргнула сеть, обновлялся ключ доступа.
const silenceLimit = 3

// silenceWindow — за какое время эти отказы должны случиться. Три отказа за
// вечер — это не поломка.
const silenceWindow = 2 * time.Minute

// routeCooldown — как часто позволено менять дорогу до Spotify.
//
// Без этого приложение могло бы прыгать «обход → напрямую → обход» каждую
// минуту, когда плохо и там и там, и в панели мигали бы сообщения.
const routeCooldown = 3 * time.Minute

// noteSpotifySilence разбирается, почему Spotify замолчал, и меняет дорогу.
//
// Две беды, которые выглядят одинаково («Spotify не отвечает»):
//
//   - обход есть, но он лёг или стал непроходимым — надо идти напрямую или
//     поднимать обход заново;
//   - обхода нет, а провайдер Spotify не пускает — надо поднять обход.
//
// Отличить их можно только опытом: спросить Spotify напрямую и посмотреть,
// отвечает ли. Одна проверка на три отказа подряд — это дешевле, чем разбор
// по логу через сутки.
func (s *Server) noteSpotifySilence() {
	if s.tunnel == nil || !s.cfg.Get().TunnelOn {
		return
	}
	if s.reviving.Load() || s.reselecting.Load() {
		return
	}

	now := time.Now()
	s.silenceMu.Lock()
	if now.Sub(s.silenceFirst) > silenceWindow {
		s.silenceFirst, s.silenceCount = now, 0
	}
	s.silenceCount++
	enough := s.silenceCount >= silenceLimit
	tooSoon := now.Sub(s.routeChanged) < routeCooldown
	if enough {
		s.silenceCount = 0
	}
	s.silenceMu.Unlock()

	if !enough || tooSoon {
		return
	}

	on := s.tunnel.Status().On
	go func() {
		ctx, cancel := context.WithTimeout(s.baseContext(), 30*time.Second)
		defer cancel()

		direct := tunnel.CheckSpotify(ctx, "") == nil

		switch {
		case on && direct:
			// Здесь хватает проверки без входа: если прямая дорога окажется
			// негодной по стране, приложение узнает об этом на первом же отказе
			// со входом (SP-16) и поднимет обход обратно.
			// Через обход Spotify молчит, а напрямую отвечает. Почти наверняка
			// у стримера включился свой VPN на весь компьютер, и наш обход
			// оказался вторым туннелем поверх первого.
			s.silenceMu.Lock()
			s.routeChanged = time.Now()
			s.silenceMu.Unlock()

			s.log.Info("Spotify отвечает напрямую, а через обход нет — выключаю обход")
			s.tunnel.Stop()
			s.applyProxy()
			s.syncTunnel("Обход выключен: Spotify отвечает и без него.")
			s.state.Notify("info", "Spotify отвечает и без обхода — похоже, у тебя включён свой VPN. "+
				"Обход выключен, чтобы не гонять запросы через два туннеля подряд. "+
				"Выключишь VPN — приложение поднимет обход само.")

		case on && !direct:
			// Не отвечает нигде: обход лёг по-настоящему.
			s.reviveTunnel("через обход Spotify замолчал")

		case !on && !direct:
			// Обход в запасе, а Spotify не отвечает — самое время его поднять.
			s.silenceMu.Lock()
			s.routeChanged = time.Now()
			s.silenceMu.Unlock()
			s.reviveTunnel("Spotify перестал отвечать напрямую")
		}
	}()
}

// StopTunnel гасит обход при выходе: программа обхода не должна пережить
// приложение, как и mpv.
func (s *Server) StopTunnel() {
	if s.tunnel != nil {
		s.tunnel.Stop()
	}
}

// TunnelAddr — адрес посредника, поднятого обходом. Пусто, если обход не
// работает. Нужен окну входа в Spotify: оно ходит через тот же обход.
func (s *Server) TunnelAddr() string {
	if s.tunnel == nil {
		return ""
	}
	return s.tunnel.Addr()
}

// syncTunnel обновляет карточку обхода в панели. note — что сказать человеку.
func (s *Server) syncTunnel(note string) {
	cfg := s.cfg.Get()
	info := app.TunnelInfo{
		HasKey: cfg.TunnelKeyHint != "",
		Hint:   cfg.TunnelKeyHint,
		Note:   note,
	}
	if s.tunnel != nil {
		st := s.tunnel.Status()
		info.On = st.On
		info.Server = st.Server
		// Ключ есть, обход разрешён, а поднимать его не понадобилось: Spotify
		// отвечает и так. Человеку это надо показать иначе, чем «выключен», —
		// иначе он будет чинить то, что работает.
		info.Standby = !st.On && info.HasKey && cfg.TunnelOn
	}
	s.state.SetTunnel(info)
}

// tunnelNote превращает ошибку в строчку для карточки обхода: с кодом, чтобы
// человек мог назвать его голосом, и без технических подробностей.
func tunnelNote(err error) string {
	code, text := errs.Describe(err)
	if code == "" {
		return text
	}
	return string(code) + " · " + text
}
