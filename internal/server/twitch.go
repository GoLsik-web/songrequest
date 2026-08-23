package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/errs"
	"songrequest/internal/twitch"
)

// kvRewardID — идентификатор созданной награды. Хранится между запусками,
// чтобы приложение не плодило по новой награде на каждый старт.
const kvRewardID = "twitch_reward_id"

func (s *Server) handleTwitchLogin(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	login, err := s.twitch.StartLogin(ctx)
	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	expires := login.ExpiresAt
	s.state.UpdateTwitch(func(t *app.TwitchInfo) {
		t.PendingCode = login.UserCode
		t.PendingURL = login.VerifyURL
		t.PendingExpires = &expires
		t.Note = ""
		t.NoteCode = ""
	})
	s.state.Notify("info", "Открой "+login.VerifyURL+" и введи код "+login.UserCode)

	// Ждём подтверждения в фоне: стример вводит код на телефоне, а панель
	// в это время должна оставаться живой.
	go s.awaitTwitchLogin()

	writeJSON(w, login)
}

// awaitTwitchLogin дожидается подтверждения кода и поднимает всё остальное.
func (s *Server) awaitTwitchLogin() {
	ctx, cancel := context.WithTimeout(s.baseContext(), 31*time.Minute)
	defer cancel()

	if err := s.twitch.WaitLogin(ctx); err != nil {
		if ctx.Err() != nil {
			return // приложение закрывают, это не ошибка
		}
		s.state.NotifyError(err)
		s.clearPendingCode()
		s.syncTwitchInfo()
		return
	}

	s.clearPendingCode()
	s.state.Notify("info", "Twitch подключён")
	s.startTwitch(s.baseContext())
}

func (s *Server) clearPendingCode() {
	s.state.UpdateTwitch(func(t *app.TwitchInfo) {
		t.PendingCode = ""
		t.PendingURL = ""
		t.PendingExpires = nil
	})
}

// startTwitch доводит подключение до рабочего состояния: узнаёт канал,
// заводит награду и подписывается на заказы.
func (s *Server) startTwitch(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	user, err := s.twitch.CheckAccount(checkCtx)
	if err != nil {
		s.state.NotifyError(err)
		s.syncTwitchInfo()
		return
	}
	s.syncTwitchInfo()

	// Баллов на обычном канале нет. Это не поломка приложения, а условие
	// самого Twitch, поэтому говорим прямо и работу не прекращаем: донаты
	// на таком канале будут работать.
	if !user.HasChannelPoints() {
		problem := errs.New(errs.TwitchNoAffiliate,
			"Канал подключён, но баллов на нём нет — они бывают только у аффилиатов и партнёров Twitch. Приложение исправно, просто заказывать за баллы не получится. Донаты будут работать.")
		s.state.NotifyWarn(problem.Code, problem.Message)
		s.log.Info("на канале нет баллов, награду не создаём",
			"канал", user.Login, "тип_канала", user.Type)
		s.state.UpdateTwitch(func(t *app.TwitchInfo) {
			t.Note = problem.Message
			t.NoteCode = string(problem.Code)
		})
		return
	}

	knownID, _, err := s.db.GetKV(kvRewardID)
	if err != nil {
		s.log.Warn("не прочитал прежнюю награду", "ошибка", err)
	}

	reward, err := s.twitch.EnsureReward(checkCtx, knownID)
	if err != nil {
		s.state.NotifyError(err)
		code, text := errs.Describe(err)
		s.state.UpdateTwitch(func(t *app.TwitchInfo) {
			t.Note = text
			t.NoteCode = string(code)
		})
		return
	}

	if err := s.db.SetKV(kvRewardID, reward.ID); err != nil {
		s.log.Warn("не запомнил награду", "ошибка", err)
	}

	s.mu.Lock()
	s.rewardID = reward.ID
	s.mu.Unlock()

	s.state.UpdateTwitch(func(t *app.TwitchInfo) {
		t.RewardTitle = reward.Title
		t.RewardCost = reward.Cost
		t.RewardReady = true
		t.Note = ""
		t.NoteCode = ""
	})
	s.state.Notify("info", "Награда «"+reward.Title+"» готова, стоит "+strconv.Itoa(reward.Cost)+" баллов")

	s.startEventSub(ctx, reward.ID)
}

// startEventSub поднимает подписку на заказы, если она ещё не поднята.
func (s *Server) startEventSub(ctx context.Context, rewardID string) {
	s.mu.Lock()
	if s.eventsRunning {
		s.mu.Unlock()
		return
	}
	s.eventsRunning = true
	s.mu.Unlock()

	events := twitch.NewEventSub(s.twitch)
	events.OnRedemption = s.onRedemption
	events.OnStatus = func(connected bool, detail string) {
		if connected {
			s.state.SetConnOK("Twitch", detail)
			return
		}
		s.state.SetConnFail("Twitch", detail)
	}

	go func() {
		events.Run(ctx, rewardID)
		s.mu.Lock()
		s.eventsRunning = false
		s.mu.Unlock()
	}()
}

// onRedemption принимает заказ за баллы.
//
// Очереди и поиска трека ещё нет, поэтому заказ просто попадает в список
// панели: так проверяется весь путь от нажатия зрителем до возврата баллов.
func (s *Server) onRedemption(r twitch.Redemption) {
	s.state.AddRedemption(app.RedemptionView{
		ID:       r.ID,
		RewardID: r.RewardID,
		User:     r.UserName,
		Text:     r.UserInput,
		Cost:     r.RewardCost,
		At:       r.RedeemedAt,
		Status:   app.OrderNew,
	})
	s.state.Notify("info", "Заказ от "+r.UserName+": "+r.UserInput)
}

// handleRedemptionAction возвращает баллы или отмечает заказ выполненным.
func (s *Server) handleRedemptionAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.URL.Query().Get("action")

	var rewardID string
	for _, item := range s.state.Snapshot().Redemptions {
		if item.ID == id {
			rewardID = item.RewardID
			break
		}
	}
	if rewardID == "" {
		s.mu.Lock()
		rewardID = s.rewardID
		s.mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var err error
	var status string
	switch action {
	case "refund":
		err, status = s.twitch.RefundRedemption(ctx, rewardID, id), app.OrderRefunded
	case "fulfill":
		err, status = s.twitch.FulfillRedemption(ctx, rewardID, id), app.OrderFulfilled
	default:
		s.fail(w, errs.New(errs.TwitchRefund, "Непонятное действие с заказом."))
		return
	}

	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	s.state.SetRedemptionStatus(id, status)
	if status == app.OrderRefunded {
		s.state.Notify("info", "Баллы за заказ возвращены")
	} else {
		s.state.Notify("info", "Заказ принят, баллы не возвращаются")
	}
	writeJSON(w, map[string]string{"status": status})
}

func (s *Server) handleTwitchLogout(w http.ResponseWriter, r *http.Request) {
	s.twitch.Logout()
	s.clearPendingCode()
	s.syncTwitchInfo()
	s.state.Notify("info", "Twitch отключён")
	writeJSON(w, map[string]bool{"ok": true})
}

// syncTwitchInfo обновляет карточку Twitch в панели.
func (s *Server) syncTwitchInfo() {
	user := s.twitch.Account()
	cfg := s.cfg.Get()

	s.mu.Lock()
	rewardID := s.rewardID
	s.mu.Unlock()

	s.state.UpdateTwitch(func(t *app.TwitchInfo) {
		t.HasClientID = strings.TrimSpace(cfg.TwitchClientID) != ""
		t.Connected = s.twitch.Connected()
		t.RewardReady = rewardID != ""
		if t.RewardTitle == "" {
			t.RewardTitle = cfg.RewardTitle
			t.RewardCost = cfg.RewardCost
		}
		if user != nil {
			t.Channel = user.Login
			t.ChannelType = user.StatusLabel()
			t.HasPoints = user.HasChannelPoints()
		} else {
			t.Channel = ""
			t.ChannelType = ""
			t.HasPoints = false
		}
	})

	info := s.state.Snapshot().Twitch
	switch {
	case !info.HasClientID:
		s.state.SetConnIdle("Twitch", "Не настроено")
	case !info.Connected:
		s.state.SetConnIdle("Twitch", "Не подключён")
	case user == nil:
		s.state.SetConnFail("Twitch", "Вход есть, но связи не было")
	case !info.HasPoints:
		// Не ошибка, а свойство канала: чинить нечего, красный цвет тут
		// сказал бы «приложение сломалось», и человек бросил бы установку.
		s.state.SetConnIdle("Twitch", user.Login+" · баллов на канале нет")
	case !info.RewardReady:
		s.state.SetConnFail("Twitch", user.Login+" · награда не создана")
	default:
		s.state.SetConnOK("Twitch", user.Login)
	}
}

// SyncTwitch обновляет карточку Twitch снаружи (при старте).
func (s *Server) SyncTwitch() { s.syncTwitchInfo() }

// StartTwitchIfConnected поднимает Twitch при запуске приложения, если вход
// уже был выполнен раньше.
func (s *Server) StartTwitchIfConnected(ctx context.Context) {
	if !s.twitch.Connected() {
		s.syncTwitchInfo()
		return
	}
	go s.startTwitch(ctx)
}
