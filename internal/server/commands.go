package server

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"songrequest/internal/errs"
	"songrequest/internal/i18n"
	"songrequest/internal/match"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// Команды в чате — для стримера и модераторов. Обычным зрителям они
// недоступны: заказ идёт только через награду или донат, иначе баллы теряют
// смысл. Права берём из значков сообщения, а не отдельным запросом к API.
//
// ГЛАВНОЕ ПРАВИЛО ЭТОГО ФАЙЛА: на всякую команду, которая дошла до сюда от
// человека с правами, в чат уходит ответ. Раньше половина команд работала
// молча — и «сработало» выглядело ровно как «не сработало». Отсюда и жалоба
// «пишу !скип, ничего не происходит»: команда часто как раз происходила.
//
// Молчим только в одном случае: слово после знака команды нам незнакомо. Там
// вполне может быть чужой бот с тем же знаком, и спорить с ним в чате незачем.

// say пишет в чат, если Twitch подключён. Молчаливый отказ здесь уместен:
// не работает чат — заказы всё равно принимаются.
func (s *Server) say(ctx context.Context, text string) {
	if s.twitch == nil || !s.twitch.Connected() {
		return
	}
	if err := s.twitch.Say(ctx, text); err != nil {
		// С кодом и с самой ошибкой, а не молчаливой строкой в Debug.
		//
		// Жалоба «приложение не отвечает зрителям» иначе неразбираема: в
		// логе строка есть, а причины — нет прав на чат, 401, слоумод,
		// AutoMod — нет. Весь способ работы держится на разборе по логу.
		code, _ := errs.Describe(err)
		s.log.Warn("сообщение в чат не ушло", "текст", text, "код", code, "ошибка", err)
		s.state.NotifyError(err)
	}
}

// dropInvisible выбрасывает знаки, которых не видно.
//
// Зачем. Часть чат-клиентов и ботов дописывает к сообщению «пустой» знак
// (U+200B, U+FEFF, метки направления письма), чтобы Twitch не счёл сообщение
// повтором и не проглотил его. Управляющие знаки вычищает cleanInput в
// internal/twitch, а форматирующие — нет: они не управляющие. До сюда доезжала
// строка вида «!скип» с пустым знаком на конце, не совпадала ни с одной командой, и команда молча
// пропадала. Разобрать такое по жалобе «пишу !скип, ничего не происходит»
// невозможно: в чате обе строки выглядят одинаково.
func dropInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Cf, r): // управляющие форматом: U+200B и родня
			return -1
		case r == '\uFEFF': // метка порядка байтов
			return -1
		case r >= 0xE0000 && r <= 0xE007F:
			// Блок «Tags». Именно им чаще всего и дописывают: у Twitch-клиентов
			// это самый ходовой способ обмануть проверку на повтор сообщения. В
			// таблице знаков он числится незанятым, поэтому под unicode.Cf не
			// попадает и до этой правки проходил насквозь.
			return -1
		}
		return r
	}, s)
}

// parseCommand разбирает строку чата на команду и её доводы.
//
// ok=false означает «это не команда»: нет знака в начале или после него пусто.
//
// Что здесь важно:
//
//   - Регистр не имеет значения: !Скип, !СКИП и !SKIP — одно и то же.
//     strings.ToLower умеет и кириллицу, отдельной ветки для неё не нужно.
//   - Пробел после знака допустим: «! скип» — обычная опечатка на телефоне.
//   - Невидимые знаки выбрасываются, см. dropInvisible.
func parseCommand(text, prefix string) (cmd string, args []string, ok bool) {
	if prefix == "" {
		prefix = "!"
	}
	text = strings.TrimSpace(dropInvisible(text))
	if !strings.HasPrefix(text, prefix) {
		return "", nil, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, prefix))
	if len(fields) == 0 {
		return "", nil, false
	}
	return strings.ToLower(fields[0]), fields[1:], true
}

// chatCommands — слова команд из onChat. Нужны resolveOrder: зритель путает
// награду с чатом и пишет туда «!скип» (12.10 по такому заказу заиграл
// «Call Me Human (feat. Skip Marley)»).
var chatCommands = map[string]bool{
	"очередь": true, "queue": true, "q": true, "трек": true, "что": true,
	"сейчас": true, "now": true, "np": true, "скип": true, "skip": true, "s": true,
	"вернуть": true, "undo": true, "назад": true, "плейлисты": true, "плейлист": true,
	"playlists": true, "pl": true, "одобрить": true, "принять": true, "approve": true,
	"ok": true, "отклонить": true, "отказать": true, "reject": true, "удалить": true,
	"remove": true, "rm": true, "вверх": true, "поднять": true, "top": true,
	"громкость": true, "volume": true, "vol": true, "пауза": true, "pause": true,
	"продолжить": true, "resume": true, "play": true, "очистить": true, "clear": true,
	"бан": true, "ban": true, "разбан": true, "unban": true, "стоп": true,
	"stop": true, "старт": true, "start": true, "помощь": true, "help": true,
	"команды": true, "пауза-трека": true, "паузатрека": true, "стопзаказы": true,
	// Английские синонимы — работают при любом языке.
	"song": true, "current": true, "commands": true, "back": true,
	"playlist": true, "deny": true, "delete": true, "del": true,
	"bump": true, "unpause": true,
}

// viewerCommands — то, что можно писать любому зрителю, без прав модератора.
var viewerCommands = map[string]bool{
	"очередь": true, "queue": true, "q": true,
	"помощь": true, "help": true, "команды": true, "commands": true,
}

// isChatCommand — текст заказа на деле команда чата.
func isChatCommand(text, prefix string) bool {
	cmd, _, ok := parseCommand(text, prefix)
	return ok && chatCommands[cmd]
}

// onChat разбирает сообщение из чата.
func (s *Server) onChat(msg twitch.ChatMessage) {
	cfg := s.cfg.Get()
	prefix := cfg.CommandPrefix
	if prefix == "" {
		prefix = "!"
	}

	cmd, args, ok := parseCommand(msg.Text, prefix)
	if !ok {
		return
	}

	actor := msg.DisplayName
	if actor == "" {
		actor = msg.Login
	}

	// Чужие команды (других ботов с тем же знаком) отсекаем до проверки
	// прав: иначе на каждое «!дискорд» от зрителя шёл запрос к Twitch
	// «а не модератор ли он».
	if !chatCommands[cmd] {
		return
	}

	ctx, cancel := context.WithTimeout(s.baseContext(), 30*time.Second)
	defer cancel()

	// Справку и очередь можно всем: зритель, который спрашивает «что
	// играет дальше», ничего не ломает, а молчание в ответ выглядит поломкой.
	if viewerCommands[cmd] && !msg.CanCommand() {
		switch cmd {
		case "очередь", "queue", "q":
			s.say(ctx, s.queueLine())
		default:
			s.say(ctx, helpLine(prefix))
		}
		return
	}

	// В чате отказ молчаливый — спорить со зрителем при всех незачем. А вот в
	// логе он обязан быть: жалоба «пишу !скип, ничего не происходит» иначе
	// неразбираема. Без этой строки одинаково выглядят «сообщение не дошло
	// вовсе» и «дошло, но у человека нет прав», а чинить это надо по-разному.
	if !s.canCommand(ctx, msg) {
		s.log.Debug("команда от того, кому нельзя",
			"кто", msg.Login, "команда", cmd,
			"стример", msg.IsBroadcaster, "модератор", msg.IsModerator,
			"значки", msg.Badges)
		return
	}

	s.log.Debug("команда из чата", "кто", actor, "команда", cmd, "аргументы", args)

	switch cmd {
	case "очередь", "queue", "q":
		s.say(ctx, s.queueLine())

	case "трек", "что", "сейчас", "now", "np", "song", "current":
		s.say(ctx, s.nowLine())

	case "скип", "skip", "s":
		s.cmdSkip(ctx, actor, args)

	case "вернуть", "undo", "назад", "back":
		s.cmdUndoSkip(ctx, actor)

	case "плейлисты", "плейлист", "playlists", "pl", "playlist":
		s.say(ctx, s.playlistsLine())

	case "одобрить", "принять", "approve", "ok":
		s.cmdApprovePlaylist(ctx, actor, args)

	case "отклонить", "отказать", "reject", "deny":
		s.cmdRejectPlaylist(ctx, actor, args)

	case "удалить", "remove", "rm", "delete", "del":
		s.cmdRemove(ctx, actor, args)

	case "вверх", "поднять", "top", "bump":
		s.cmdTop(ctx, actor, args)

	case "громкость", "volume", "vol":
		s.cmdVolume(ctx, actor, args)

	case "пауза", "пауза-трека", "паузатрека", "pause":
		s.cmdHold(ctx, actor, true)

	case "продолжить", "resume", "play", "unpause":
		s.cmdHold(ctx, actor, false)

	case "очистить", "clear":
		if err := s.clearQueue(ctx, true, actor); err != nil {
			s.log.Warn("не очистил очередь", "ошибка", err)
			s.say(ctx, i18n.F("@%s, очередь очистить не вышло — посмотри в приложении", actor))
			return
		}
		s.say(ctx, i18n.T("Очередь очищена, баллы вернулись"))

	case "бан", "ban":
		s.cmdBan(ctx, actor, args)

	case "разбан", "unban":
		s.cmdUnban(ctx, actor, args)

	case "стоп", "стопзаказы", "stop":
		s.setOrdersPaused(ctx, true)
		s.modLog(actor, "остановил приём заказов", "")
		// Прямо говорим, чего эта команда НЕ делает. Именно на этом месте
		// люди спотыкались чаще всего: писали «!стоп», ожидая, что музыка
		// замолчит, и считали приложение сломанным. Пауза музыки — «!пауза».
		s.say(ctx, i18n.F("Новые заказы больше не принимаются. Музыка играет дальше — остановить её это %s",
			prefix+cmdName("пауза", "pause")))

	case "старт", "start":
		s.setOrdersPaused(ctx, false)
		s.modLog(actor, "возобновил приём заказов", "")
		s.say(ctx, i18n.T("Заказы снова принимаются"))

	default:
		// Знак команды угадан верно, а слово — нет. Молчим в чате (мало ли
		// что там пишут другие боты с тем же знаком), но пишем в лог: так
		// видно опечатки и чужие команды, которые к нам не относятся.
		s.log.Debug("незнакомая команда", "кто", actor, "команда", cmd)

	case "помощь", "help", "команды", "commands":
		s.say(ctx, helpLine(prefix))
	}
}

// helpLine — список команд на текущем языке.
func helpLine(prefix string) string {
	if i18n.IsEN() {
		p := prefix
		return "Commands: " + p + "queue, " + p + "song, " + p +
			"skip (also " + p + "skip 2 or " + p + "skip title), " +
			p + "undo, " + p + "remove N, " + p + "top N, " +
			p + "volume 40, " + p + "pause, " + p + "resume, " +
			p + "playlists, " + p + "approve, " + p + "reject, " +
			p + "clear, " + p + "ban nick, " + p + "unban nick, " +
			p + "stop, " + p + "start. Viewers can use " + p + "queue and " + p +
			"help; the rest is for moderators."
	}
	return "Команды: " + prefix + "очередь, " + prefix + "трек, " + prefix +
		"скип (можно " + prefix + "скип 2 или " + prefix + "скип название), " +
		prefix + "вернуть, " + prefix + "удалить N, " + prefix + "вверх N, " +
		prefix + "громкость 40, " + prefix + "пауза, " + prefix + "продолжить, " +
		prefix + "плейлисты, " + prefix + "одобрить, " + prefix + "отклонить, " +
		prefix + "очистить, " + prefix + "бан ник, " + prefix + "разбан ник, " +
		prefix + "стоп, " + prefix + "старт. Зрителям доступны " + prefix + "очередь и " +
		prefix + "помощь, остальное — только модераторам."
}

// loginRe — так выглядит логин Twitch: 3–25 латинских букв, цифр и «_».
var loginRe = regexp.MustCompile(`^[a-z0-9_]{3,25}$`)

func validLogin(login string) bool { return loginRe.MatchString(login) }

// cmdName — имя команды для подсказок: русское или английское по языку.
func cmdName(ru, en string) string {
	if i18n.IsEN() {
		return en
	}
	return ru
}

// queueLine — очередь одной строкой для чата.
func (s *Server) queueLine() string {
	items, err := s.queue.List()
	if err != nil {
		return i18n.T("Не смог прочитать очередь")
	}

	now := s.player.Now()
	if now == nil && len(items) == 0 {
		return i18n.T("Очередь пуста")
	}

	var b strings.Builder
	if now != nil {
		b.WriteString(i18n.F("Сейчас: %s", artistTitle(now.Item.Artist, now.Item.Title)))
	}
	if len(items) == 0 {
		b.WriteString(i18n.T(" · дальше ничего"))
		return b.String()
	}

	if now != nil {
		b.WriteString(i18n.T(" · далее: "))
	} else {
		b.WriteString(i18n.T("В очереди: "))
	}

	// В чат влезает немного, поэтому показываем первые несколько и число
	// остальных: длинное сообщение Twitch обрежет посреди слова.
	const show = 5
	for i, it := range items {
		if i >= show {
			b.WriteString(i18n.F(" … и ещё %d", len(items)-show))
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(i+1) + ". " + artistTitle(it.Artist, it.Title))
	}
	return b.String()
}

// nowLine — что играет прямо сейчас, одной строкой.
//
// Отдельно от «очереди»: чаще всего спрашивают именно это, а список из шести
// позиций в ответ на «что играет» приходится вычитывать глазами.
func (s *Server) nowLine() string {
	if now := s.player.Now(); now != nil {
		line := i18n.F("Сейчас: %s", artistTitle(now.Item.Artist, now.Item.Title))
		if now.Item.Requester != "" {
			line += i18n.F(" · заказал %s", now.Item.Requester)
		}
		if now.Paused() {
			line += i18n.T(" · на паузе")
		}
		return line
	}
	// Запуск заказа (скачивание звука с YouTube) тоже даёт Waiting(),
	// но ждать конца трека тут нечего — говорим, что заказ запускается.
	if it := s.player.LaunchingItem(); it != nil {
		return i18n.F("Запускается заказ: %s", artistTitle(it.Artist, it.Title))
	}
	if s.player.Waiting() {
		return i18n.T("Заказ ждёт конца трека канала. Чтобы не ждать — скип")
	}
	if own := s.own(); own != nil {
		return i18n.F("Сейчас музыка канала: %s", artistTitle(own.Artist, own.Title))
	}
	return i18n.T("Сейчас ничего не играет")
}

// ── скип ─────────────────────────────────────────────────────────────
//
// У скипа три способа сказать, что именно убрать, и все три придумал не я —
// так спрашивают в чате:
//
//	!скип            то, что играет
//	!скип 0          то же самое, для тех, кто считает с нуля
//	!скип 2          второй заказ в очереди (нумерация из «!очередь»)
//	!скип doja cat   любой заказ, в названии или артисте которого это есть
//
// Баллы возвращаются во всех трёх случаях. Это решение владельца, и оно
// снимает самую частую жалобу зрителей: заказ оборвали, а баллы списаны.

func (s *Server) cmdSkip(ctx context.Context, actor string, args []string) {
	arg := strings.TrimSpace(strings.Join(args, " "))

	// Без доводов и «0» — то, что в эфире.
	if arg == "" || arg == "0" {
		s.skipOnAir(ctx, actor)
		return
	}

	// Число — позиция в очереди из «!очередь».
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 0 {
			s.say(ctx, i18n.F("@%s, номер не бывает отрицательным. 0 — то, что играет, 1 — следующий", actor))
			return
		}
		s.skipAt(ctx, actor, n)
		return
	}

	// Всё остальное — название. Ищем и в эфире, и в очереди.
	s.skipNamed(ctx, actor, arg)
}

// skipOnAir убирает с эфира то, что играет.
func (s *Server) skipOnAir(ctx context.Context, actor string) {
	// Своя музыка канала — не заказ, и «скипать» её приложению нечего:
	// очередь тут ни при чём, а плейлистом стример правит сам. Молчать об
	// этом нельзя, иначе команда выглядит сломанной.
	if s.player.Now() == nil && !s.player.Waiting() {
		if s.own() != nil {
			s.say(ctx, i18n.F("@%s, сейчас играет музыка канала, а не заказ — скипать нечего", actor))
			return
		}
		s.say(ctx, i18n.F("@%s, сейчас ничего не играет", actor))
		return
	}

	res, err := s.skipPlaying(actor)
	if err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}
	if res.Hurried {
		s.say(ctx, i18n.T("Не жду конца трека канала, включаю заказ"))
		return
	}
	s.say(ctx, i18n.F("Скипнул: %s", trackLine(res.Item))+refundTail(res.Refunded))
}

// skipAt убирает заказ по номеру из «!очередь».
func (s *Server) skipAt(ctx context.Context, actor string, n int) {
	if n == 0 {
		s.skipOnAir(ctx, actor)
		return
	}

	items, err := s.queue.List()
	if err != nil {
		s.say(ctx, i18n.F("@%s, не смог прочитать очередь", actor))
		return
	}
	if n > len(items) {
		s.say(ctx, i18n.F("@%s, в очереди столько нет: %s", actor, queueSize(len(items))))
		return
	}

	s.takeOutOfQueue(ctx, actor, items[n-1])
}

// skipNamed ищет заказ по названию — и в эфире, и в очереди.
func (s *Server) skipNamed(ctx context.Context, actor, name string) {
	if now := s.player.Now(); now != nil && trackMatches(now.Item, name) {
		s.skipOnAir(ctx, actor)
		return
	}

	items, err := s.queue.List()
	if err != nil {
		s.say(ctx, i18n.F("@%s, не смог прочитать очередь", actor))
		return
	}

	found := -1
	for i, it := range items {
		if !trackMatches(it, name) {
			continue
		}
		if found >= 0 {
			// Двух одинаково подходящих трогать нельзя: угадаешь не тот —
			// и зритель лишится заказа ни за что. Пусть человек назовёт номер.
			s.say(ctx, i18n.F("@%s, под «%s» подходит больше одного заказа — скажи номер из %s",
				actor, name, s.prefix()+cmdName("очередь", "queue")))
			return
		}
		found = i
	}

	if found < 0 {
		s.say(ctx, i18n.F("@%s, не нашёл «%s» ни в эфире, ни в очереди", actor, name))
		return
	}
	s.takeOutOfQueue(ctx, actor, items[found])
}

// takeOutOfQueue убирает заказ из очереди с возвратом баллов и говорит об этом.
//
// Имя не dropFromQueue: так уже называется соседняя работа в twitch.go —
// «зритель сам отменил награду, убери его заказ». Две разные вещи с одним
// именем однажды перепутались бы.
func (s *Server) takeOutOfQueue(ctx context.Context, actor string, item queue.Item) {
	s.rememberSkip(item, actor)
	if err := s.removeFromQueue(ctx, item.ID, true, actor); err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}
	s.say(ctx, i18n.F("Убрал из очереди: %s", trackLine(item))+refundTail(item.RedemptionID != ""))
}

// cmdUndoSkip возвращает в очередь то, что скипнули последним.
//
// Баллы при этом обратно НЕ списываются, и это не недосмотр: Twitch умеет
// вернуть баллы за награду, но забрать их снова у него нечем — отменённый
// заказ отменён навсегда. Поэтому возвращённый трек играет бесплатно, и в
// чате об этом сказано прямо, чтобы зритель не решил, что с него взяли дважды.
func (s *Server) cmdUndoSkip(ctx context.Context, actor string) {
	item, who, err := s.takeSkipBack()
	if err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}

	// Новый заказ, а не оживший старый: у прежнего баллы уже возвращены, и
	// тянуть за собой его номер награды нельзя — иначе приложение однажды
	// попробует вернуть их второй раз.
	back := queue.Item{
		Source:         queue.SourceManual,
		Requester:      item.Requester,
		RequesterLogin: item.RequesterLogin,
		RawRequest:     item.RawRequest,
		Provider:       item.Provider,
		Via:            item.Via,
		TrackID:        item.TrackID,
		URI:            item.URI,
		Title:          item.Title,
		Artist:         item.Artist,
		DurationMs:     item.DurationMs,
		CoverURL:       item.CoverURL,
		Uncertain:      item.Uncertain,
	}

	added, err := s.queue.Add(back, false)
	if err != nil {
		s.log.Warn("не вернул скипнутый заказ", "ошибка", err)
		s.say(ctx, i18n.F("@%s, не получилось вернуть заказ в очередь", actor))
		return
	}
	if err := s.queue.MoveTop(added.ID); err != nil {
		// Не беда: заказ в очереди, просто не первым. Говорить об этом
		// отдельно незачем — позицию человек увидит в «!очередь».
		s.log.Warn("вернул заказ, но не поднял в начало", "ошибка", err)
	}

	s.modLog(actor, "вернул скипнутое", trackLine(item))
	s.state.Notify("info", i18n.F("%s вернул в очередь: %s", actor, trackLine(item)))
	s.syncPlayback()
	s.player.Nudge()

	tail := ""
	if item.RedemptionID != "" {
		tail = i18n.T(" Баллы уже вернулись, так что этот раз бесплатно.")
	}
	s.say(ctx, i18n.F("Вернул в начало очереди: %s (скипнул %s).", trackLine(item), who)+tail)
}

// cmdTop поднимает заказ в начало очереди.
func (s *Server) cmdTop(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, i18n.F("@%s, нужен номер из %s: %s 3", actor, s.prefix()+cmdName("очередь", "queue"), cmdName("вверх", "top")))
		return
	}

	items, err := s.queue.List()
	if err != nil {
		s.say(ctx, i18n.F("@%s, не смог прочитать очередь", actor))
		return
	}

	item, ok := s.pickFromQueue(ctx, actor, items, strings.Join(args, " "))
	if !ok {
		return
	}
	if err := s.queue.MoveTop(item.ID); err != nil {
		s.say(ctx, i18n.F("@%s, не получилось поднять заказ", actor))
		return
	}

	s.modLog(actor, "поднял заказ в начало", trackLine(item))
	s.state.Notify("info", i18n.F("%s поднял в начало: %s", actor, trackLine(item)))
	s.syncPlayback()
	s.say(ctx, i18n.F("Поднял в начало очереди: %s", trackLine(item)))
}

// pickFromQueue выбирает заказ по номеру или названию и сам объясняет отказ.
func (s *Server) pickFromQueue(ctx context.Context, actor string, items []queue.Item, arg string) (queue.Item, bool) {
	arg = strings.TrimSpace(arg)
	if len(items) == 0 {
		s.say(ctx, i18n.F("@%s, очередь пуста", actor))
		return queue.Item{}, false
	}

	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(items) {
			s.say(ctx, i18n.F("@%s, в очереди столько нет: %s", actor, queueSize(len(items))))
			return queue.Item{}, false
		}
		return items[n-1], true
	}

	found := -1
	for i, it := range items {
		if !trackMatches(it, arg) {
			continue
		}
		if found >= 0 {
			s.say(ctx, i18n.F("@%s, под «%s» подходит больше одного заказа — скажи номер", actor, arg))
			return queue.Item{}, false
		}
		found = i
	}
	if found < 0 {
		s.say(ctx, i18n.F("@%s, не нашёл «%s» в очереди", actor, arg))
		return queue.Item{}, false
	}
	return items[found], true
}

// cmdVolume меняет громкость играющего заказа прямо из чата.
//
// Только заказа: своей музыкой стример правит в самом Spotify, а каждая
// команда громкости — отдельный запрос к нему.
func (s *Server) cmdVolume(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, i18n.F("@%s, нужно число от 0 до 100: %s 40", actor, cmdName("громкость", "volume")))
		return
	}
	percent, err := strconv.Atoi(strings.TrimSuffix(args[0], "%"))
	if err != nil || percent < 0 || percent > 100 {
		s.say(ctx, i18n.F("@%s, громкость — это число от 0 до 100", actor))
		return
	}

	if s.player.Now() == nil {
		s.say(ctx, i18n.F("@%s, сейчас не играет ни один заказ", actor))
		return
	}
	if err := s.player.SetVolumeLevel(ctx, percent); err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}

	s.modLog(actor, "поставил громкость", strconv.Itoa(percent)+"%")
	s.syncPlayback()
	s.say(ctx, i18n.F("Громкость заказа: %d%%", percent))
}

// cmdHold ставит на паузу то, что играет, и снимает с неё.
//
// Работает и для заказа, и для музыки канала — развилка живёт в приложении, а
// не в голове у модератора: он видит одну плашку «сейчас играет» и хочет её
// остановить.
func (s *Server) cmdHold(ctx context.Context, actor string, on bool) {
	word := i18n.T("Продолжаю")
	deed := "снял с паузы"
	if on {
		word, deed = i18n.T("Пауза"), "поставил на паузу"
	}

	if now := s.player.Now(); now != nil {
		if err := s.player.Hold(ctx, on); err != nil {
			s.say(ctx, "@"+actor+", "+errText(err))
			return
		}
		s.modLog(actor, deed, trackLine(now.Item))
		s.syncPlayback()
		s.say(ctx, word+": "+trackLine(now.Item))
		return
	}

	own := s.own()
	if own == nil {
		s.say(ctx, i18n.F("@%s, сейчас ничего не играет", actor))
		return
	}
	if err := s.holdOwn(ctx, on); err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}
	s.modLog(actor, deed+" музыку канала", artistTitle(own.Artist, own.Title))
	s.say(ctx, word+": "+artistTitle(own.Artist, own.Title))
}

func (s *Server) cmdRemove(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, i18n.F("@%s, нужен номер: %s 2", actor, cmdName("удалить", "remove")))
		return
	}

	items, err := s.queue.List()
	if err != nil {
		s.say(ctx, i18n.F("@%s, не смог прочитать очередь", actor))
		return
	}
	item, ok := s.pickFromQueue(ctx, actor, items, strings.Join(args, " "))
	if !ok {
		return
	}

	s.rememberSkip(item, actor)
	if err := s.removeFromQueue(ctx, item.ID, true, actor); err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}
	s.say(ctx, i18n.F("Удалил: %s", trackLine(item))+refundTail(item.RedemptionID != ""))
}

func (s *Server) cmdBan(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, i18n.F("@%s, нужен ник: %s ник причина", actor, s.prefix()+cmdName("бан", "ban")))
		return
	}
	login := strings.TrimPrefix(strings.ToLower(args[0]), "@")
	reason := strings.Join(args[1:], " ")

	// Опечатка вроде «!бан @» или «!бан причина» записывала в бан-лист
	// мусор, а бан самого канала закрывал заказы стримеру.
	if !validLogin(login) {
		s.say(ctx, i18n.F("@%s, «%s» не похоже на ник Twitch", actor, login))
		return
	}
	if ch := s.state.Snapshot().Twitch.Channel; ch != "" && strings.EqualFold(ch, login) {
		s.say(ctx, i18n.F("@%s, стримера забанить нельзя", actor))
		return
	}

	if err := s.banMusic(login, reason); err != nil {
		s.log.Warn("не забанил", "ошибка", err)
		s.say(ctx, i18n.F("@%s, не получилось закрыть заказы для %s", actor, login))
		return
	}
	s.modLog(actor, "закрыл заказы", login)
	s.state.Notify("info", i18n.F("%s закрыл заказы для %s", actor, login))
	s.say(ctx, i18n.F("%s больше не может заказывать музыку", login))
}

func (s *Server) cmdUnban(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, i18n.F("@%s, нужен ник: %s ник", actor, s.prefix()+cmdName("разбан", "unban")))
		return
	}
	login := strings.TrimPrefix(strings.ToLower(args[0]), "@")

	if err := s.unbanMusic(login); err != nil {
		s.log.Warn("не разбанил", "ошибка", err)
		s.say(ctx, i18n.F("@%s, не получилось вернуть заказы для %s", actor, login))
		return
	}
	s.modLog(actor, "вернул заказы", login)
	s.state.Notify("info", i18n.F("%s вернул заказы для %s", actor, login))
	s.say(ctx, i18n.F("%s снова может заказывать музыку", login))
}

// announceQueued говорит зрителю, что его заказ принят и когда заиграет.
func (s *Server) announceQueued(ctx context.Context, item queue.Item, position int, wait time.Duration) {
	where := i18n.T("следующим")
	if position > 1 {
		where = i18n.F("%d-м в очереди", position)
	}

	text := i18n.F("@%s, принято: %s, %s", item.Requester, trackLine(item), where)
	if wait > time.Minute {
		text += i18n.F(" (примерно через %s)", afterDuration(wait))
	}
	// Зритель должен знать, что звук пойдёт не из Spotify: иначе «почему
	// играет какая-то запись с ютуба» — вопрос в чат через три минуты.
	if item.Provider == "youtube" {
		text += i18n.T(" — в Spotify нет, сыграет с YouTube")
	}
	s.say(ctx, text)
}

// ── мелочи, из которых собираются ответы ─────────────────────────────

// prefix — знак команд прямо сейчас. Подставляется в подсказки: в чате нельзя
// написать «!очередь», если у стримера стоит другой знак.
func (s *Server) prefix() string {
	if p := s.cfg.Get().CommandPrefix; p != "" {
		return p
	}
	return "!"
}

func trackLine(item queue.Item) string {
	return artistTitle(item.Artist, item.Title)
}

// refundTail — хвост про баллы.
//
// Говорим об этом в том же сообщении, а не отдельным: два сообщения подряд от
// бота в чате читаются хуже одного, а зритель должен увидеть «баллы вернулись»
// в ту же секунду, что и «твой трек убрали».
func refundTail(refunded bool) string {
	if refunded {
		return i18n.T(". Баллы вернул")
	}
	return ""
}

func queueSize(n int) string {
	if n == 0 {
		return i18n.T("очередь пуста")
	}
	return strconv.Itoa(n) + " " + i18n.Plural(n, [3]string{"заказ", "заказа", "заказов"}, [2]string{"request", "requests"})
}

// errText — текст отказа для чата, без кода.
//
// Код (PL-01, SP-08) нужен в панели и в логе: по нему стример находит, что
// делать. В чате он только пугает зрителей, а модератору всё равно не поможет.
func errText(err error) string {
	code, text := errs.Describe(err)
	if code == "" || text == "" {
		// Ошибка без нашего кода — техническая, зрителю её не пересказать.
		return i18n.T("не получилось, стример посмотрит, в чём дело")
	}
	// С маленькой буквы: текст встаёт после «@ник, ».
	r := []rune(text)
	r[0] = unicode.ToLower(r[0])
	return strings.TrimSuffix(string(r), ".")
}

// trackMatches — подходит ли заказ под то, что написали в чате.
//
// Сравниваем по-человечески, а не побуквенно: match.Normalize приводит регистр,
// ё/е, дефисы и лишние пробелы к одному виду — тот же разбор, которым
// приложение ищет треки по заказу зрителя. Иначе «!скип Ария» не находило бы
// «АРИЯ», а «!скип doja» — «Doja Cat».
//
// Достаточно вхождения куска: в чате пишут «!скип бэд гай», а не полное
// название с артистом. Ложное попадание не страшно — на два подходящих
// приложение отвечает «скажи номер» и ничего не трогает.
func trackMatches(item queue.Item, want string) bool {
	want = match.Normalize(want)
	if want == "" {
		return false
	}
	hay := match.Normalize(item.Artist + " " + item.Title + " " + item.RawRequest)
	return strings.Contains(hay, want)
}

// ── плейлисты, ждущие решения ────────────────────────────────────────
//
// Заказанный плейлист сам в эфир не идёт: сорок минут чужой музыки подряд
// решает хозяин эфира. Пока решения нет, плейлист виден в панели и здесь, в
// чате, — чтобы модератор мог одобрить его, не подходя к компьютеру стримера.

// playlistsLine — что ждёт решения, одной строкой.
func (s *Server) playlistsLine() string {
	list := s.pendingPlaylists()
	if len(list) == 0 {
		return i18n.T("Сейчас ни один плейлист не ждёт решения")
	}

	var b strings.Builder
	b.WriteString(i18n.T("Ждут решения: "))
	for i, p := range list {
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(i18n.F("%s. «%s» от %s — %d %s, %s",
			p.ID, p.Title, p.Requester,
			len(p.Tracks), tracksWord(len(p.Tracks)),
			humanDuration(time.Duration(p.TotalMs())*time.Millisecond)))
	}
	b.WriteString(i18n.F(". %sодобрить N или %sотклонить N", s.prefix(), s.prefix()))
	return b.String()
}

func tracksWord(n int) string {
	return i18n.Plural(n, [3]string{"трек", "трека", "треков"}, [2]string{"track", "tracks"})
}

func (s *Server) cmdApprovePlaylist(ctx context.Context, actor string, args []string) {
	p, added, err := s.approvePlaylist(ctx, strings.Join(args, " "), actor)
	if err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}

	// Сколько встало в очередь, а не сколько было в плейлисте: часть треков
	// могла не найтись нигде или оказаться длиннее разрешённого, и обещать
	// зрителю больше, чем сыграет, нельзя.
	tail := ""
	if added < len(p.Tracks) {
		tail = i18n.F(" (из %d — остальные не нашлись или слишком длинные)", len(p.Tracks))
	}
	s.say(ctx, i18n.F("Плейлист «%s» от %s одобрен: %d %s в очереди%s",
		p.Title, p.Requester, added, tracksWord(added), tail))
}

func (s *Server) cmdRejectPlaylist(ctx context.Context, actor string, args []string) {
	p, err := s.rejectPlaylist(ctx, strings.Join(args, " "), actor)
	if err != nil {
		s.say(ctx, "@"+actor+", "+errText(err))
		return
	}
	s.say(ctx, i18n.F("@%s, плейлист «%s» не одобрили. Баллы вернул.", p.Requester, p.Title))

}

// canCommand решает, слушать ли команду от этого человека.
//
// Два пути, и порядок важен.
//
// Первый — значки сообщения. Они приходят вместе с текстом, бесплатно, и в
// девяноста девяти случаях из ста вопрос закрывают.
//
// Второй — список модераторов у самого Twitch. Он нужен потому, что значков
// может не оказаться и у настоящего модератора: 09.09 владелец написал «!скип»
// с аккаунта, который на канале модератор, и получил отказ со строкой
// «стример=false модератор=false». Значки в чате — украшение, а не документ;
// кто модератор, знает Twitch, и спрашивать надо его. Запрос идёт только тогда,
// когда значков не хватило, и не чаще раза в минуту, см. Client.IsModerator.
func (s *Server) canCommand(ctx context.Context, msg twitch.ChatMessage) bool {
	if msg.CanCommand() {
		return true
	}
	if s.twitch == nil || !s.twitch.Connected() {
		return false
	}
	if !s.twitch.IsModerator(ctx, msg.Login) {
		return false
	}
	// Раз уж выяснили — скажем об этом в лог: иначе разбор следующей жалобы
	// снова упрётся в вопрос «а по чему приложение решило, что можно».
	s.log.Info("команда принята по списку модераторов канала, значков в сообщении не было",
		"кто", msg.Login, "значки", msg.Badges)
	return true
}

// artistTitle — «Артист — Название», а без артиста только название: иначе в
// чат уходило « — Название» с одиноким тире впереди.
func artistTitle(artist, title string) string {
	if artist == "" {
		return title
	}
	return artist + " — " + title
}
