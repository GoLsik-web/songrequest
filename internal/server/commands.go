package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"songrequest/internal/errs"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// Команды в чате — для стримера и модераторов. Обычным зрителям они
// недоступны: заказ идёт только через награду или донат, иначе баллы теряют
// смысл. Права берём из значков сообщения, а не отдельным запросом к API.

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
// строка вида «!скип\u200b», не совпадала ни с одной командой, и команда молча
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

	// В чате отказ молчаливый — спорить со зрителем при всех незачем. А вот в
	// логе он обязан быть: жалоба «пишу !скип, ничего не происходит» иначе
	// неразбираема. Без этой строки одинаково выглядят «сообщение не дошло
	// вовсе» и «дошло, но у человека нет прав», а чинить это надо по-разному.
	if !msg.CanCommand() {
		s.log.Debug("команда от того, кому нельзя",
			"кто", msg.Login, "команда", cmd,
			"стример", msg.IsBroadcaster, "модератор", msg.IsModerator)
		return
	}

	ctx, cancel := context.WithTimeout(s.baseContext(), 30*time.Second)
	defer cancel()

	s.log.Debug("команда из чата", "кто", actor, "команда", cmd, "аргументы", args)

	switch cmd {
	case "очередь", "queue", "q":
		s.say(ctx, s.queueLine())

	case "скип", "skip", "s":
		now := s.player.Now()
		switch {
		case now != nil:
			s.skipCurrent(actor)
			s.say(ctx, "Скипнул: "+now.Item.Artist+" — "+now.Item.Title)
		case s.player.Waiting():
			// Заказ уже взят, но ждёт конца трека стримера. Скип здесь
			// означает «не жди, включай» — и обязан работать.
			s.skipCurrent(actor)
			s.say(ctx, "Не жду конца трека, включаю заказ")
		default:
			s.say(ctx, "@"+actor+", сейчас ничего не играет")
		}

	case "удалить", "remove", "rm":
		s.cmdRemove(ctx, actor, args)

	case "очистить", "clear":
		if err := s.clearQueue(ctx, true, actor); err != nil {
			s.log.Warn("не очистил очередь", "ошибка", err)
			return
		}
		s.say(ctx, "Очередь очищена, баллы вернулись")

	case "бан", "ban":
		s.cmdBan(ctx, actor, args)

	case "разбан", "unban":
		s.cmdUnban(ctx, actor, args)

	case "стоп", "пауза", "pause":
		s.player.SetPaused(true)
		s.modLog(actor, "остановил приём заказов", "")
		s.say(ctx, "Заказы приостановлены")

	case "старт", "resume", "play":
		s.player.SetPaused(false)
		s.modLog(actor, "возобновил приём заказов", "")
		s.say(ctx, "Заказы снова принимаются")

	default:
		// Знак команды угадан верно, а слово — нет. Молчим в чате (мало ли
		// что там пишут другие боты с тем же знаком), но пишем в лог: так
		// видно опечатки и чужие команды, которые к нам не относятся.
		s.log.Debug("незнакомая команда", "кто", actor, "команда", cmd)

	case "помощь", "help", "команды":
		s.say(ctx, "Команды: "+prefix+"очередь, "+prefix+"скип, "+prefix+
			"удалить N, "+prefix+"очистить, "+prefix+"бан ник, "+prefix+
			"разбан ник, "+prefix+"стоп, "+prefix+"старт")
	}
}

// queueLine — очередь одной строкой для чата.
func (s *Server) queueLine() string {
	items, err := s.queue.List()
	if err != nil {
		return "Не смог прочитать очередь"
	}

	now := s.player.Now()
	if now == nil && len(items) == 0 {
		return "Очередь пуста"
	}

	var b strings.Builder
	if now != nil {
		b.WriteString("Сейчас: " + now.Item.Artist + " — " + now.Item.Title)
	}
	if len(items) == 0 {
		b.WriteString(" · дальше ничего")
		return b.String()
	}

	if now != nil {
		b.WriteString(" · далее: ")
	} else {
		b.WriteString("В очереди: ")
	}

	// В чат влезает немного, поэтому показываем первые несколько и число
	// остальных: длинное сообщение Twitch обрежет посреди слова.
	const show = 5
	for i, it := range items {
		if i >= show {
			b.WriteString(fmt.Sprintf(" … и ещё %d", len(items)-show))
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(i+1) + ". " + it.Artist + " — " + it.Title)
	}
	return b.String()
}

func (s *Server) cmdRemove(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, "@"+actor+", нужен номер: удалить 2")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		s.say(ctx, "@"+actor+", номер должен быть числом")
		return
	}

	items, err := s.queue.List()
	if err != nil || n > len(items) {
		s.say(ctx, "@"+actor+", такого номера в очереди нет")
		return
	}

	item := items[n-1]
	if err := s.removeFromQueue(ctx, item.ID, true, actor); err != nil {
		s.say(ctx, "@"+actor+", не получилось удалить")
		return
	}
	s.say(ctx, "Удалил: "+item.Artist+" — "+item.Title+", баллы вернулись")
}

func (s *Server) cmdBan(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, "@"+actor+", нужен ник: бан ник причина")
		return
	}
	login := strings.TrimPrefix(strings.ToLower(args[0]), "@")
	reason := strings.Join(args[1:], " ")

	if err := s.banMusic(login, reason); err != nil {
		s.log.Warn("не забанил", "ошибка", err)
		return
	}
	s.modLog(actor, "закрыл заказы", login)
	s.state.Notify("info", actor+" закрыл заказы для "+login)
	s.say(ctx, login+" больше не может заказывать музыку")
}

func (s *Server) cmdUnban(ctx context.Context, actor string, args []string) {
	if len(args) == 0 {
		s.say(ctx, "@"+actor+", нужен ник: разбан ник")
		return
	}
	login := strings.TrimPrefix(strings.ToLower(args[0]), "@")

	if err := s.unbanMusic(login); err != nil {
		s.log.Warn("не разбанил", "ошибка", err)
		return
	}
	s.modLog(actor, "вернул заказы", login)
	s.state.Notify("info", actor+" вернул заказы для "+login)
	s.say(ctx, login+" снова может заказывать музыку")
}

// announceQueued говорит зрителю, что его заказ принят и когда заиграет.
func (s *Server) announceQueued(ctx context.Context, item queue.Item, position int, wait time.Duration) {
	where := "следующим"
	if position > 1 {
		where = fmt.Sprintf("%d-м в очереди", position)
	}

	text := fmt.Sprintf("@%s, принято: %s — %s, %s", item.Requester, item.Artist, item.Title, where)
	if wait > time.Minute {
		text += fmt.Sprintf(" (примерно через %s)", humanDuration(wait))
	}
	s.say(ctx, text)
}
