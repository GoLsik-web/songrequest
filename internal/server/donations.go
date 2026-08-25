package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"songrequest/internal/donations"
	"songrequest/internal/errs"
	"songrequest/internal/match"
	"songrequest/internal/queue"
	"songrequest/internal/secrets"
)

// setupDonations собирает источники донатов.
//
// Добавить новый сервис — значит написать один файл с интерфейсом Source и
// одну строчку здесь. Ни очередь, ни панель об этом не узнают.
func (s *Server) setupDonations() {
	s.donations = donations.NewHub(s.log)
	s.donations.OnDonation = s.onDonation
	s.donations.OnStatus = func(name string, connected bool, detail string) {
		if connected {
			s.state.SetConnOK(name, detail)
			return
		}
		if detail == "Не настроено" {
			s.state.SetConnIdle(name, detail)
			return
		}
		s.state.SetConnFail(name, detail)
	}

	keys := secrets.New()
	s.donationAlerts = donations.NewDonationAlerts(s.log, keys, s.donations.Status("DonationAlerts"))
	s.donations.Add(s.donationAlerts)
	s.donations.Add(donations.NewDonatePay(s.log,
		func() string { return s.cfg.Get().DonatePayKey },
		s.donations.Status("DonatePay")))
	s.donations.Add(donations.NewDonateX(s.log,
		func() string { return s.cfg.Get().DonateXKey },
		s.donations.Status("DonateX")))
}

// StartDonations поднимает подключения к сервисам донатов.
func (s *Server) StartDonations(ctx context.Context) {
	if s.donations == nil {
		return
	}
	s.donations.Run(ctx)
}

// onDonation превращает донат в заказ.
//
// Сообщение к донату — это и есть заказ. Если оно пустое или сумма меньше
// порога, заказа не выйдет, но деньги остаются у стримера: возвращать донат
// приложение не должно и не умеет.
func (s *Server) onDonation(d donations.Donation) {
	cfg := s.cfg.Get()
	ctx, cancel := context.WithTimeout(s.baseContext(), resolveTimeout)
	defer cancel()

	money := fmt.Sprintf("%.0f %s", d.Amount, d.Currency)

	if cfg.DonationMin > 0 && d.Amount < cfg.DonationMin {
		s.log.Info("донат меньше порога заказа",
			"от", d.Username, "сумма", d.Amount, "порог", cfg.DonationMin)
		s.state.Notify("info", "Донат "+money+" от "+d.Username+" — меньше порога заказа")
		return
	}

	text := strings.TrimSpace(d.Message)
	if text == "" {
		s.state.Notify("info", "Донат "+money+" от "+d.Username+" — без сообщения, заказа нет")
		return
	}

	s.state.Notify("info", "Донат "+money+" от "+d.Username+": "+text)

	req := match.Parse(text)
	if req.Title == "" {
		return
	}

	opts := matchOptionsFromConfig(cfg)
	// Та же поправка на страну, что и у заказов за баллы: трек, не изданный
	// в стране аккаунта, всё равно не заиграет — незачем ставить его в
	// очередь и обещать зрителю то, чего не будет.
	if me := s.spotify.Account(); me != nil {
		opts.Market = me.Country
	}

	res, err := match.Find(ctx, s.spotify, req, opts)
	if err != nil {
		s.log.Error("не смог подобрать трек по донату", "ошибка", err)
		s.state.NotifyError(err)
		s.say(ctx, "@"+d.Username+", спасибо за донат! Трек поискать не вышло, скажи стримеру.")
		return
	}
	if !res.Found {
		s.log.Info("трек из доната не найден", "текст", text, "запросы", res.Attempts)
		s.state.NotifyWarn(errs.Code(""), "По донату от "+d.Username+" трек не нашёлся: "+text)
		s.say(ctx, "@"+d.Username+", спасибо за донат! Такого трека в Spotify не нашлось.")
		return
	}

	// Заказ за донат в очередь ставится без возврата: денег вернуть нельзя,
	// поэтому и поле редемпшена пустое.
	s.enqueue(ctx, queue.Item{
		Source:     queue.SourceDonation,
		Requester:  d.Username,
		RawRequest: text,
		Provider:   "spotify",
		TrackID:    res.Track.ID,
		URI:        res.Track.URI,
		Title:      res.Track.Title,
		Artist:     res.Track.Artists[0],
		DurationMs: res.Track.DurationMs,
		CoverURL:   res.Track.CoverURL,
		Uncertain:  res.Uncertain,
	})
}

// handleDonationAlertsLogin начинает вход в DonationAlerts.
func (s *Server) handleDonationAlertsLogin(w http.ResponseWriter, r *http.Request) {
	url, err := s.donationAlerts.AuthURL(s.cfg.Get().DonationAlertsClientID, s.addr+"/donations/callback")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, map[string]string{"url": url, "redirect_uri": s.addr + "/donations/callback"})
}

// handleDonationAlertsCallback принимает ответ после входа.
//
// Ключ приходит не в адресе, а в его якорной части — она до сервера не
// доходит вовсе. Поэтому страница сначала отдаёт крошечный скрипт, который
// читает якорь и присылает ключ обратно.
func (s *Server) handleDonationAlertsCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<title>DonationAlerts</title><style>
body{background:#08090a;color:#f2f4f5;font:16px/1.5 "Segoe UI",system-ui,sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center}
div{max-width:520px;padding:0 24px}h1{font-size:22px;margin:0 0 12px}
p{color:#6f777e;margin:0}</style></head><body>
<div><h1 id="t">Подключаю…</h1><p id="p">Секунду.</p></div>
<script>
(async () => {
  const hash = new URLSearchParams(location.hash.slice(1));
  const token = hash.get("access_token");
  if (!token) {
    document.getElementById("t").textContent = "Не получилось";
    document.getElementById("p").textContent = "DonationAlerts не выдал доступ. Вернись в панель и попробуй ещё раз.";
    return;
  }
  const r = await fetch("/api/donations/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  const ok = r.ok;
  document.getElementById("t").textContent = ok ? "Готово" : "Не получилось";
  document.getElementById("p").textContent = ok
    ? "DonationAlerts подключён. Эту вкладку можно закрыть."
    : "Приложение не приняло ключ. Загляни в панель.";
})();
</script></body></html>`)
}

// handleDonationsToken принимает ключ, вычитанный страницей из якоря.
func (s *Server) handleDonationsToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &body); err != nil {
		s.fail(w, errs.New(errs.DonationsAuth, "Не разобрал ответ DonationAlerts."))
		return
	}

	if err := s.donationAlerts.SaveToken(body.Token); err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	s.log.Info("DonationAlerts подключён")
	s.state.Notify("info", "DonationAlerts подключён")

	// Поднимаем подключение прямо сейчас, не дожидаясь перезапуска.
	s.restartDonationAlerts()

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleDonationAlertsLogout(w http.ResponseWriter, r *http.Request) {
	s.stopDonationAlerts()
	s.donationAlerts.Logout()
	s.state.SetConnIdle("DonationAlerts", "Не настроено")
	s.state.Notify("info", "DonationAlerts отключён")
	writeJSON(w, map[string]bool{"ok": true})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

// Одно подключение к DonationAlerts, а не сколько нажали.
//
// Раньше каждый успешный вход запускал ещё одну горутину с вебсокетом, и
// ничем её было не остановить: вошёл дважды — два подключения до конца
// работы приложения, а после «Отключить» цикл жил дальше и упорно
// перекрашивал только что погасшую лампочку обратно в красное.

// restartDonationAlerts поднимает подключение, погасив прежнее.
func (s *Server) restartDonationAlerts() {
	s.stopDonationAlerts()

	ctx, cancel := context.WithCancel(s.baseContext())
	s.mu.Lock()
	s.daCancel = cancel
	s.mu.Unlock()

	go s.donationAlerts.Run(ctx, s.donations.Handle)
}

// stopDonationAlerts гасит подключение, если оно было.
func (s *Server) stopDonationAlerts() {
	s.mu.Lock()
	cancel := s.daCancel
	s.daCancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}
