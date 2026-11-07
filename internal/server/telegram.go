package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"songrequest/internal/config"
	tg "songrequest/internal/telegram"
	"songrequest/internal/twitch"
)

const telegramKey = "telegram_bot_token"

type telegramChatCount struct {
	Name           string
	Messages, Caps int
	Emotes, Night  int
	First          int64
}
type telegramState struct {
	mu             sync.Mutex
	revision       atomic.Int64
	Connected      bool
	Username, Note string
	Chats          map[string]telegramChatCount
	Period         int64
}

func (s *Server) telegramStatus() map[string]any {
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	var token string
	if s.secrets != nil {
		_ = s.secrets.GetJSON(telegramKey, &token)
	}
	_, linkErr := s.relayLink()
	return map[string]any{"settings": s.cfg.Get().Telegram, "has_token": token != "", "shared_link": linkErr == nil, "shared_relay_url": SharedRelayURL, "connected": s.telegram.Connected, "username": s.telegram.Username, "note": s.telegram.Note}
}
func (s *Server) handleTelegramGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.telegramStatus())
}
func (s *Server) handleTelegramSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		config.Telegram
		Token  string `json:"token"`
		Forget bool   `json:"forget"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "Не разобрал настройки Telegram", 400)
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Mode != "" && in.Mode != "personal" && in.Mode != "shared" {
		http.Error(w, "Неизвестный способ подключения Telegram", 400)
		return
	}
	if in.Mode == "shared" {
		current := s.cfg.Get().Telegram
		in.OwnerID = current.OwnerID
		in.RelayURL = current.RelayURL
		if in.Token != "" {
			http.Error(w, "Для общего бота токен не нужен", 400)
			return
		}
		if in.Enabled {
			if _, err := s.relayLink(); err != nil || in.OwnerID <= 0 {
				http.Error(w, "Сначала подключи общий бот кнопкой в приложении", 400)
				return
			}
		}
	}
	in.PublishChannel = strings.TrimSpace(in.PublishChannel)
	if in.OwnerID < 0 || len(in.PublishChannel) > 100 {
		http.Error(w, "Проверь ID владельца и канал", 400)
		return
	}
	if in.PublishChannel != "" && !validTelegramChannel(in.PublishChannel) {
		http.Error(w, "Канал: @имя или числовой ID вида -100…", 400)
		return
	}
	if in.Token != "" && !tg.ValidToken(in.Token) {
		http.Error(w, "Проверь токен от BotFather", 400)
		return
	}
	if in.Forget && in.Token != "" {
		http.Error(w, "Нельзя одновременно заменить и удалить токен", 400)
		return
	}
	if s.secrets == nil {
		http.Error(w, "Хранилище паролей недоступно", 503)
		return
	}
	if in.Forget {
		if err := s.secrets.Delete(telegramKey); err != nil {
			http.Error(w, "Не удалось удалить токен", 500)
			return
		}
		in.Enabled = false
	}
	if in.Token != "" {
		if err := s.secrets.PutJSON(telegramKey, in.Token); err != nil {
			http.Error(w, "Не удалось сохранить токен", 500)
			return
		}
	}
	var token string
	_ = s.secrets.GetJSON(telegramKey, &token)
	if in.Enabled && in.Mode != "shared" && token == "" {
		http.Error(w, "Укажи токен бота", 400)
		return
	}
	if err := s.cfg.Update(func(c *config.Config) { c.Telegram = in.Telegram }); err != nil {
		http.Error(w, "Не удалось сохранить настройки", 500)
		return
	}
	s.telegram.revision.Add(1)
	writeJSON(w, s.telegramStatus())
}
func validTelegramChannel(v string) bool {
	if strings.HasPrefix(v, "-100") {
		n, e := strconv.ParseInt(v, 10, 64)
		return e == nil && n < 0
	}
	if !strings.HasPrefix(v, "@") || len(v) < 6 {
		return false
	}
	for _, r := range v[1:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
func (s *Server) telegramNote(connected bool, user, note string) {
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	s.telegram.Connected = connected
	s.telegram.Username = user
	s.telegram.Note = note
}

func (s *Server) watchTelegram(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var cancel context.CancelFunc
	var done chan struct{}
	var signature string
	defer func() {
		if cancel != nil {
			cancel()
			<-done
		}
		s.saveTelegramChat()
	}()
	for {
		cfg := s.cfg.Get().Telegram
		sig := fmt.Sprintf("%+v:%d", cfg, s.telegram.revision.Load())
		if sig != signature {
			if cancel != nil {
				cancel()
				<-done
				cancel = nil
			}
			signature = sig
			s.telegramNote(false, "", "")
			if cfg.Enabled {
				client, err := s.telegramClient(cfg)
				if err == nil {
					cancel, done = s.startTelegram(ctx, client, cfg)
				} else {
					text := "Укажи токен и свой Telegram ID"
					if cfg.Mode == "shared" {
						text = "Подключи общий бот кнопкой в приложении"
					}
					s.telegramNote(false, "", text)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) startTelegram(ctx context.Context, client *tg.Client, cfg config.Telegram) (context.CancelFunc, chan struct{}) {
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if cfg.Mode == "shared" {
			s.loadTelegramChat()
			reportsDone := make(chan struct{})
			go func() { defer close(reportsDone); s.watchTelegramReports(run, client, cfg) }()
			defer func() { cancel(); <-reportsDone }()
		}
		s.runTelegram(run, client, cfg)
	}()
	return cancel, done
}

func (s *Server) runTelegram(ctx context.Context, c *tg.Client, cfg config.Telegram) {
	defer c.Close()
	var me struct {
		Username string `json:"username"`
	}
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.Call(ctx, "getMe", map[string]any{}, &me); err == nil {
			break
		} else {
			s.telegramNote(false, "", telegramErrorNote(err))
			if !telegramWait(ctx, err, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	// A previous webhook or another process must be fixed explicitly, never deleted silently.
	if strings.EqualFold(me.Username, "songrequest_wrapped_bot") {
		s.telegramNote(false, me.Username, errTelegramSupportBot.Error())
		return
	}
	if cfg.Mode != "shared" && strings.EqualFold(me.Username, "songrequest_streamer_bot") {
		s.telegramNote(false, me.Username, errTelegramSharedBot.Error())
		return
	}
	var hook struct {
		URL string `json:"url"`
	}
	for ctx.Err() == nil {
		if err := c.Call(ctx, "getWebhookInfo", map[string]any{}, &hook); err != nil {
			s.telegramNote(false, me.Username, telegramErrorNote(err))
			if !telegramWait(ctx, err, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
		} else {
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	if hook.URL != "" {
		s.telegramNote(false, me.Username, "У бота включён webhook. Используй отдельного бота для приложения.")
		return
	}
	if cfg.Mode != "shared" {
		s.loadTelegramChat()
	}
	pollCtx, stop := context.WithCancel(ctx)
	defer stop()
	reportsDone := make(chan struct{})
	go func() {
		defer close(reportsDone)
		if cfg.Mode != "shared" {
			s.watchTelegramReports(pollCtx, c, cfg)
		}
	}()
	defer func() { stop(); <-reportsDone }()
	offset := int64(0)
	if raw, ok, _ := s.db.GetKV("telegram_offset:" + me.Username); ok {
		offset, _ = strconv.ParseInt(raw, 10, 64)
	}
	// Discard old control commands on every start. They must never run hours later.
	started := time.Now().Unix()
	backoff = time.Second
	for ctx.Err() == nil {
		var updates []tg.Update
		err := c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if err != nil {
			s.telegramNote(false, me.Username, telegramErrorNote(err))
			if !telegramWait(ctx, err, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		s.telegramNote(true, me.Username, "")
		for _, u := range updates {
			if u.ID < offset {
				continue
			}
			offset = u.ID + 1
			// At-most-once control: persist before executing, including across crashes.
			if s.db.SetKV("telegram_offset:"+me.Username, strconv.FormatInt(offset, 10)) != nil {
				s.telegramNote(false, me.Username, "Не удалось сохранить состояние Telegram")
				return
			}
			actionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			s.telegramUpdate(actionCtx, c, cfg, u, started)
			cancel()
		}
	}
}
func telegramErrorNote(err error) string {
	var api *tg.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case 401:
			return "Токен Telegram недействителен"
		case 409:
			return "Этот бот уже запущен в другой программе. Останови её или создай отдельного бота."
		case 429:
			return "Telegram просит подождать; подключение повторится"
		case 403:
			return "Открой своего бота и нажми «Старт»"
		}
	}
	return "Telegram недоступен; подключение повторится автоматически"
}
func telegramWait(ctx context.Context, err error, delay time.Duration) bool {
	var api *tg.APIError
	if errors.As(err, &api) && api.RetryAfter > 0 {
		delay = time.Duration(min(api.RetryAfter, 3600)) * time.Second
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func tgButton(text, data string) map[string]string {
	return map[string]string{"text": text, "callback_data": data}
}
func tgKeyboard() map[string]any {
	return map[string]any{"inline_keyboard": [][]map[string]string{
		{tgButton("📊 Итоги", "stats"), tgButton("🏆 Номинации", "awards"), tgButton("🖼 Карточка", "card")},
		{tgButton("📜 Заказы", "history"), tgButton("💬 Чат", "chat"), tgButton("🎛 Пульт", "panel")},
		{tgButton("⏸ Пауза", "pause"), tgButton("▶ Играть", "play"), tgButton("⏭ Скип", "skip")},
		{tgButton("Очередь", "queue"), tgButton("Закрыть заказы", "close"), tgButton("Открыть заказы", "open")},
		{tgButton("📁 CSV отчёт", "export"), {"text": "🆘 Поддержка", "url": "https://t.me/songrequest_wrapped_bot?start=support"}},
		{tgButton("⚙️ Настройки", "settings"), tgButton("Проверить состояние", "status")},
	}}
}
func (s *Server) telegramSend(ctx context.Context, c *tg.Client, chat int64, text string) error {
	parts := tg.SplitText(text)
	for index, part := range parts {
		payload := map[string]any{"chat_id": chat, "text": part}
		if index == len(parts)-1 {
			payload["reply_markup"] = tgKeyboard()
		}
		if err := c.Call(ctx, "sendMessage", payload, nil); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) telegramUpdate(ctx context.Context, c *tg.Client, cfg config.Telegram, u tg.Update, started int64) {
	var msg tg.Message
	var action string
	if u.Callback != nil {
		cb := u.Callback
		// Private chat AND sender identity: group members cannot use a forwarded panel.
		if cb.From.ID != cfg.OwnerID || cb.Message.Chat.ID != cfg.OwnerID || cb.Message.Chat.Type != "private" {
			return
		}
		msg = cb.Message
		if msg.Date < started && telegramMutatingAction(cb.Data) {
			_ = c.Call(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": cb.ID, "text": "Пульт устарел. Отправь /start заново."}, nil)
			return
		}
		action = cb.Data
		_ = c.Call(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": cb.ID}, nil)
	} else if u.Message != nil {
		msg = *u.Message
		if msg.Chat.Type != "private" {
			return
		}
		publicFields := strings.Fields(msg.Text)
		publicCommand := ""
		if len(publicFields) > 0 {
			publicCommand = strings.Split(publicFields[0], "@")[0]
		}
		if publicCommand == "/id" {
			_ = c.Call(ctx, "sendMessage", map[string]any{"chat_id": msg.Chat.ID, "text": fmt.Sprintf("Твой Telegram ID: %d", msg.From.ID)}, nil)
			return
		}
		if msg.From.ID != cfg.OwnerID || msg.Chat.ID != cfg.OwnerID {
			if publicCommand == "/start" || publicCommand == "/help" {
				_ = c.Call(ctx, "sendMessage", map[string]any{"chat_id": msg.Chat.ID, "text": "Это личный компаньон songrequest. Отправь /id и укажи свой ID в настройках Telegram в приложении на компьютере. Управление доступно только настроенному владельцу. Поддержка: https://t.me/songrequest_wrapped_bot?start=support"}, nil)
			}
			return
		}
		if msg.Date < started-5 {
			return
		}
		fields := strings.Fields(msg.Text)
		if len(fields) == 0 {
			return
		}
		action = strings.TrimPrefix(strings.Split(fields[0], "@")[0], "/")
		if action == "history" && len(fields) > 1 {
			page, err := strconv.Atoi(fields[1])
			if err != nil || page < 1 || page > 100001 {
				_ = s.telegramSend(ctx, c, cfg.OwnerID, "Номер страницы должен быть положительным числом.")
				return
			}
			action = fmt.Sprintf("history:%d", page-1)
		}
		if action == "who" {
			q := ""
			if len(fields) > 1 {
				q = fields[1]
			}
			_ = s.telegramSend(ctx, c, cfg.OwnerID, s.telegramHistory(ctx, q))
			return
		}
	} else {
		return
	}
	text := ""
	if action == "history" || strings.HasPrefix(action, "history:") {
		page := 0
		if action != "history" {
			value, err := strconv.Atoi(strings.TrimPrefix(action, "history:"))
			if err != nil || value < 0 || value > 100000 {
				return
			}
			page = value
		}
		if err := s.sendTelegramHistory(ctx, c, cfg.OwnerID, page); err != nil {
			s.telegramNote(false, "", telegramErrorNote(err))
		}
		return
	}
	switch action {
	case "start", "help":
		text = "songrequest Recap\n/stats — итоги эфира\n/awards — номинации\n/chat — активность чата\n/card — карточка итогов\n/history — история с перелистыванием\n/who ник — заказы зрителя\n/export — полный CSV-отчёт\n/panel — пульт, /queue — очередь\n/settings — автоитоги и уведомления\n/status — состояние подключения\n/support — написать в поддержку"
	case "support":
		text = "Поддержка songrequest: https://t.me/songrequest_wrapped_bot?start=support\nНапиши вопрос боту. Ответ придёт в том же чате."
	case "status":
		text = fmt.Sprintf("Приложение songrequest работает.\nID владельца: %d\nАвтоитоги: %s\nУведомления о треках: %s\nКанал: %s", cfg.OwnerID, telegramOnOff(cfg.AutoRecap), telegramOnOff(cfg.NotifyOrders), cfg.PublishChannel)
	case "settings", "setting:auto", "setting:notify":
		if action != "settings" {
			if err := s.cfg.Update(func(v *config.Config) {
				if action == "setting:auto" {
					v.Telegram.AutoRecap = !v.Telegram.AutoRecap
				} else {
					v.Telegram.NotifyOrders = !v.Telegram.NotifyOrders
				}
			}); err != nil {
				_ = s.telegramSend(ctx, c, cfg.OwnerID, "Не удалось сохранить настройки. Попробуй ещё раз.")
				return
			}
		}
		updated := s.cfg.Get().Telegram
		_ = c.Call(ctx, "sendMessage", map[string]any{"chat_id": cfg.OwnerID, "text": "⚙️ Настройки Telegram\nТокен, владелец и канал настраиваются в приложении на компьютере.", "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{tgButton("Автоитоги: "+telegramOnOff(updated.AutoRecap), "setting:auto")}, {tgButton("Новые треки: "+telegramOnOff(updated.NotifyOrders), "setting:notify")}, {tgButton("← Меню", "start")}}}}, nil)
		return
	case "stats", "awards":
		text = s.telegramRecap(ctx, action == "awards")
	case "export":
		if err := s.sendTelegramExport(ctx, c, cfg.OwnerID); err != nil {
			s.telegramNote(false, "", telegramErrorNote(err))
		}
		return
	case "chat":
		text = s.telegramChatText()
	case "card":
		text = s.telegramRecap(ctx, false)
		picture, err := tg.Card(s.state.Snapshot().Twitch.Channel, strings.Split(text, "\n"))
		if err == nil {
			err = c.Photo(ctx, strconv.FormatInt(cfg.OwnerID, 10), "Итоги эфира · songrequest", picture)
		}
		if err == nil {
			return
		}
		text = "Не удалось отправить карточку. Попробуй ещё раз."
	case "panel", "queue":
		text = s.telegramPanel()
	case "skip":
		s.skipCurrent("Telegram")
		text = "Текущий заказ пропущен.\n" + s.telegramPanel()
	case "pause", "play":
		text = s.telegramControl(ctx, action)
	case "open", "close":
		s.setOrdersPaused(ctx, action == "close")
		text = "Приём заказов открыт"
		if action == "close" {
			text = "Приём заказов закрыт"
		}
	default:
		parts := strings.Split(action, ":")
		if len(parts) == 2 && (parts[0] == "top" || parts[0] == "remove") {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || id <= 0 {
				return
			}
			if parts[0] == "top" {
				err = s.queue.MoveTop(id)
			} else {
				err = s.removeFromQueue(ctx, id, false, "Telegram")
			}
			if err != nil {
				text = "Заказ уже изменился или удалён. Обнови очередь."
			} else {
				s.syncPlayback()
				text = s.telegramPanel()
			}
		} else {
			text = "Выбери действие кнопками ниже."
		}
	}
	if err := s.telegramSend(ctx, c, cfg.OwnerID, text); err != nil {
		s.telegramNote(false, "", telegramErrorNote(err))
	}
	if action == "queue" {
		snap := s.state.Snapshot()
		for i, item := range snap.Queue {
			if i >= 10 {
				break
			}
			_ = c.Call(ctx, "sendMessage", map[string]any{"chat_id": cfg.OwnerID, "text": fmt.Sprintf("%d. %s — %s · %s", i+1, item.Artist, item.Title, item.Requester), "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{tgButton("Наверх", fmt.Sprintf("top:%d", item.ID)), tgButton("Убрать", fmt.Sprintf("remove:%d", item.ID))}}}}, nil)
		}
	}
}
func (s *Server) telegramControl(ctx context.Context, action string) string {
	if err := s.setPlayerHold(ctx, action == "pause"); err != nil {
		return "Не удалось управлять плеером: " + errText(err)
	}
	return s.telegramPanel()
}
func (s *Server) telegramPanel() string {
	snap := s.state.Snapshot()
	var b strings.Builder
	b.WriteString("🎛 Пульт музыки\n")
	if n := snap.Now; n != nil {
		fmt.Fprintf(&b, "%s — %s\nЗаказал: %s\n", n.Artist, n.Title, n.Requester)
	} else {
		b.WriteString("Сейчас нет играющего трека\n")
	}
	fmt.Fprintf(&b, "В очереди: %d\n", len(snap.Queue))
	for i, it := range snap.Queue {
		if i >= 15 {
			b.WriteString("…\n")
			break
		}
		fmt.Fprintf(&b, "%d. %s — %s · %s\n", i+1, it.Artist, it.Title, it.Requester)
	}
	if snap.Paused {
		b.WriteString("Приём заказов закрыт")
	} else {
		b.WriteString("Приём заказов открыт")
	}
	return b.String()
}
func (s *Server) telegramSince() int64 {
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	if s.telegram.Period > 0 {
		return s.telegram.Period
	}
	return s.state.Snapshot().Session.StartedAt.Unix()
}
func (s *Server) telegramHistory(ctx context.Context, who string) string {
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT requester,title,artist,outcome FROM history WHERE played_at>=? AND (?='' OR lower(requester)=lower(?)) ORDER BY played_at DESC LIMIT 40`, s.telegramSince(), who, who)
	if err != nil {
		return "История пока недоступна"
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString("📜 Заказы эфира\n")
	count := 0
	for rows.Next() {
		var user, title, artist, outcome string
		if rows.Scan(&user, &title, &artist, &outcome) != nil {
			continue
		}
		fmt.Fprintf(&b, "%s: %s — %s (%s)\n", user, artist, title, map[string]string{"played": "сыграно", "skipped": "пропущено", "rejected": "отклонено"}[outcome])
		count++
	}
	if rows.Err() != nil {
		return "История пока недоступна"
	}
	if count == 0 {
		b.WriteString("Пока нет завершённых заказов")
	} else if count == 40 {
		b.WriteString("\nПоказаны последние 40 заказов. /export — полный список за эфир.")
	}
	return b.String()
}

type recapUser struct {
	Name                   string
	Total, Played, Skipped int
}

func (s *Server) telegramRecap(ctx context.Context, awards bool) string {
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT requester,outcome,COUNT(*) FROM history WHERE played_at>=? GROUP BY requester,outcome ORDER BY requester`, s.telegramSince())
	if err != nil {
		return "Не удалось прочитать итоги"
	}
	defer rows.Close()
	users := map[string]*recapUser{}
	total, played, skipped, rejected := 0, 0, 0, 0
	for rows.Next() {
		var name, outcome string
		var n int
		if rows.Scan(&name, &outcome, &n) != nil {
			continue
		}
		key := strings.ToLower(name)
		u := users[key]
		if u == nil {
			u = &recapUser{Name: name}
			users[key] = u
		}
		u.Total += n
		total += n
		switch outcome {
		case "played":
			played += n
			u.Played += n
		case "skipped":
			skipped += n
			u.Skipped += n
		case "rejected":
			rejected += n
		}
	}
	list := make([]recapUser, 0, len(users))
	for _, u := range users {
		list = append(list, *u)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Total == list[j].Total {
			return list[i].Name < list[j].Name
		}
		return list[i].Total > list[j].Total
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Итоги эфира · %s\nЗаказов: %d · зрителей: %d\nСыграно: %d\nПропущено: %d · отклонено: %d\n", s.state.Snapshot().Twitch.Channel, total, len(users), played, skipped, rejected)
	if len(list) == 0 {
		b.WriteString("Пока нет завершённых заказов.\n")
		return b.String()
	}
	b.WriteString("\n🏆 Диджеи эфира\n")
	for i, u := range list {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "%d. %s — %d\n", i+1, u.Name, u.Total)
	}
	b.WriteString(s.telegramOrderAwards(ctx))
	if awards {
		b.WriteString("\n" + s.telegramChatText())
	}
	b.WriteString(s.telegramTaste(ctx))
	return b.String()
}
func (s *Server) telegramChat(msg twitch.ChatMessage) {
	if !s.cfg.Get().Telegram.Enabled || msg.IsBroadcaster || strings.HasSuffix(strings.ToLower(msg.Login), "bot") {
		return
	}
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	if s.telegram.Chats == nil {
		s.telegram.Chats = map[string]telegramChatCount{}
	}
	key := strings.ToLower(msg.Login)
	if key == "" {
		return
	}
	if len(s.telegram.Chats) >= 5000 {
		if _, ok := s.telegram.Chats[key]; !ok {
			return
		}
	}
	c := s.telegram.Chats[key]
	c.Name = msg.DisplayName
	if c.Name == "" {
		c.Name = msg.Login
	}
	c.Messages++
	c.Emotes += msg.Emotes
	if c.First == 0 {
		c.First = time.Now().Unix()
	}
	if time.Now().Hour() < 5 {
		c.Night++
	}
	letters, upper := 0, 0
	for _, r := range msg.Text {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters >= 5 && upper*100/letters >= 80 {
		c.Caps++
	}
	s.telegram.Chats[key] = c
}
func (s *Server) telegramChatText() string {
	s.telegram.mu.Lock()
	list := make([]telegramChatCount, 0, len(s.telegram.Chats))
	counts := make(map[string]int, len(s.telegram.Chats))
	total := 0
	for login, v := range s.telegram.Chats {
		list = append(list, v)
		counts[login] = v.Messages
		total += v.Messages
	}
	s.telegram.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Messages == list[j].Messages {
			return list[i].Name < list[j].Name
		}
		return list[i].Messages > list[j].Messages
	})
	text := fmt.Sprintf("💬 Чат эфира\nСообщений: %d · участников: %d\n", total, len(list))
	for i, c := range list {
		if i >= 5 {
			break
		}
		text += fmt.Sprintf("%d. %s — %d\n", i+1, c.Name, c.Messages)
	}
	if len(list) > 0 {
		text += fmt.Sprintf("Самый активный чатерс: %s\n", list[0].Name)
		best := func(value func(telegramChatCount) int64) telegramChatCount {
			winner := list[0]
			for _, c := range list[1:] {
				if value(c) > value(winner) {
					winner = c
				}
			}
			return winner
		}
		first := best(func(c telegramChatCount) int64 {
			if c.First == 0 {
				return -1 << 62
			}
			return -c.First
		})
		text += fmt.Sprintf("Первым пришёл: %s\n", first.Name)
		emo := best(func(c telegramChatCount) int64 { return int64(c.Emotes) })
		if emo.Emotes >= 5 {
			text += fmt.Sprintf("Король эмоутов: %s (%d)\n", emo.Name, emo.Emotes)
		}
		night := best(func(c telegramChatCount) int64 { return int64(c.Night) })
		if night.Night >= 5 {
			text += fmt.Sprintf("Ночная сова чата: %s (%d)\n", night.Name, night.Night)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Caps == list[j].Caps {
			return list[i].Name < list[j].Name
		}
		return list[i].Caps > list[j].Caps
	})
	if len(list) > 0 && list[0].Caps > 0 {
		text += fmt.Sprintf("Капслок-оратор: %s (%d)\n", list[0].Name, list[0].Caps)
	}
	rows, err := s.db.SQL().Query(`SELECT requester,COUNT(*) FROM history WHERE played_at>=? GROUP BY lower(requester) HAVING COUNT(*)>=2 ORDER BY COUNT(*) DESC,requester LIMIT 50`, s.telegramSince())
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var n int
			if rows.Scan(&name, &n) == nil && counts[strings.ToLower(name)] <= 2 {
				text += fmt.Sprintf("Тихий меломан: %s (%d заказов)\n", name, n)
				break
			}
		}
	}
	return text
}
func (s *Server) saveTelegramChat() {
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	raw, err := json.Marshal(struct {
		Period int64
		Chats  map[string]telegramChatCount
	}{s.telegram.Period, s.telegram.Chats})
	if err == nil {
		_ = s.db.SetKV("telegram_chat", string(raw))
	}
}
func (s *Server) loadTelegramChat() {
	s.telegram.mu.Lock()
	defer s.telegram.mu.Unlock()
	if s.telegram.Period != 0 {
		return
	}
	var saved struct {
		Period int64
		Chats  map[string]telegramChatCount
	}
	if raw, ok, _ := s.db.GetKV("telegram_chat"); ok {
		_ = json.Unmarshal([]byte(raw), &saved)
	}
	s.telegram.Period = saved.Period
	s.telegram.Chats = saved.Chats
	if s.telegram.Period == 0 {
		s.telegram.Period = s.state.Snapshot().Session.StartedAt.Unix()
	}
}
func (s *Server) watchTelegramReports(ctx context.Context, c *tg.Client, cfg config.Telegram) {
	if cfg.OwnerID == 0 {
		return
	}
	warmCtx, stop := context.WithCancel(ctx)
	warmDone := make(chan struct{})
	go func() { defer close(warmDone); s.warmTelegramMusic(warmCtx) }()
	defer func() { stop(); <-warmDone }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer s.saveTelegramChat()
	lastOrder := ""
	if n := s.player.Now(); n != nil {
		lastOrder = fmt.Sprint(n.Item.ID)
	}
	checks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if cfg.NotifyOrders {
			n := s.player.Now()
			if n != nil && fmt.Sprint(n.Item.ID) != lastOrder {
				call, cancel := context.WithTimeout(ctx, 15*time.Second)
				err := s.telegramSend(call, c, cfg.OwnerID, "Новый трек\n"+n.Item.Artist+" — "+n.Item.Title+"\nЗаказал: "+n.Item.Requester)
				cancel()
				if err == nil {
					lastOrder = fmt.Sprint(n.Item.ID)
				}
			}
		}
		if cfg.AutoRecap && checks%12 == 0 {
			s.checkTelegramStream(ctx, c, cfg)
		}
		if checks%6 == 0 {
			s.saveTelegramChat()
		}
		checks++
	}
}
func (s *Server) checkTelegramStream(ctx context.Context, c *tg.Client, cfg config.Telegram) {
	u := s.twitch.Account()
	if u == nil {
		return
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	live, start, err := s.twitch.StreamStatus(call, u.ID)
	cancel()
	if err != nil {
		return
	}
	pending, _, _ := s.db.GetKV("telegram_live_start")
	if live {
		period := strconv.FormatInt(start.Unix(), 10)
		if pending != period {
			s.telegram.mu.Lock()
			if s.telegram.Period != start.Unix() {
				s.telegram.Period = start.Unix()
				s.telegram.Chats = map[string]telegramChatCount{}
			}
			s.telegram.mu.Unlock()
			_ = s.db.SetKV("telegram_live_start", period)
		}
		return
	}
	if pending == "" {
		return
	}
	s.deliverTelegramRecap(ctx, c, cfg, pending)
}

func (s *Server) deliverTelegramRecap(ctx context.Context, c *tg.Client, cfg config.Telegram, pending string) {
	// A failed send remains pending and is retried on the next successful status check.
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text := s.telegramRecap(call, false)
	ownerKey := fmt.Sprintf("telegram_recap_owner:%d:%s", cfg.OwnerID, pending)
	ownerSent, _, err := s.db.GetKV(ownerKey)
	if err != nil {
		return
	}
	if ownerSent != "sent" {
		if err := s.telegramSend(call, c, cfg.OwnerID, text); err != nil {
			return
		}
		if err := s.db.SetKV(ownerKey, "sent"); err != nil {
			return
		}
	}
	if cfg.PublishChannel != "" {
		channelKey := "telegram_recap_channel:" + pending + ":" + cfg.PublishChannel
		channelSent, _, err := s.db.GetKV(channelKey)
		if err != nil {
			return
		}
		if channelSent != "sent" {
			picture, err := tg.Card(s.state.Snapshot().Twitch.Channel, strings.Split(text, "\n"))
			if err == nil {
				err = c.Photo(call, cfg.PublishChannel, "Итоги эфира · songrequest", picture)
			}
			if err != nil {
				s.telegramNote(true, "", "Итоги отправлены владельцу, но карточка не опубликована в канале. Проверь права бота.")
				return
			}
			if err := s.db.SetKV(channelKey, "sent"); err != nil {
				return
			}
		}
	}
	_ = s.db.SetKV("telegram_live_start", "")
}
