// Панель рисуется целиком из одного снимка состояния. Приложение присылает
// снимок при подключении и потом только когда что-то изменилось, поэтому
// здесь нет ни одного setInterval для опроса.
(() => {
  const $ = (id) => document.getElementById(id);
  const status = $("status");
  let retryDelay = 1000;

  function connect() {
    const socket = new WebSocket(`ws://${location.host}/ws`);

    socket.onopen = () => {
      retryDelay = 1000;
      status.textContent = `Подключено · ${location.host}`;
      status.className = "online";
    };

    socket.onmessage = (event) => render(JSON.parse(event.data));

    socket.onclose = () => {
      status.textContent = "Приложение не отвечает. Проверь, запущено ли оно.";
      status.className = "offline";
      // Переподключение с ростом паузы: если приложение закрыто, браузер не
      // должен долбиться в него каждую секунду до конца стрима.
      setTimeout(connect, retryDelay);
      retryDelay = Math.min(retryDelay * 2, 15000);
    };
  }

  // post отправляет команду и показывает ошибку так же, как её видит панель:
  // с кодом, который можно продиктовать голосом.
  async function post(path, button) {
    if (button) button.disabled = true;
    try {
      const response = await fetch(path, { method: "POST" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        flash(data.code ? `${data.code} · ${data.error}` : "Не получилось", true);
        return null;
      }
      return data;
    } catch (e) {
      flash("Приложение не отвечает", true);
      return null;
    } finally {
      if (button) button.disabled = false;
    }
  }

  function flash(text, isError) {
    status.textContent = text;
    status.className = isError ? "offline" : "online";
  }

  // Настройки читаем один раз при загрузке: они меняются редко и только
  // руками, гонять их через сокет вместе с состоянием ни к чему.
  async function loadConfig() {
    try {
      const cfg = await (await fetch("/api/config")).json();
      $("sp-clientid").value = cfg.spotify_client_id || "";
    } catch (e) {
      flash("Не смог прочитать настройки", true);
    }
  }

  $("sp-save-id").onclick = async (e) => {
    e.target.disabled = true;
    try {
      // Читаем настройки целиком и меняем одно поле: так сохранение из панели
      // не затирает то, что стример правил в файле руками.
      const cfg = await (await fetch("/api/config")).json();
      cfg.spotify_client_id = $("sp-clientid").value.trim();
      const response = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(cfg),
      });
      if (!response.ok) throw new Error();
      flash("Client ID сохранён. Теперь нажми «Подключить Spotify».");
    } catch (err) {
      flash("Не смог сохранить Client ID", true);
    } finally {
      e.target.disabled = false;
    }
  };

  $("sp-login").onclick = async (e) => {
    const data = await post("/api/spotify/login", e.target);
    if (!data) return;
    // Показываем адрес возврата рядом с кнопкой: если вход не пройдёт, первым
    // делом сверяют именно эту строку с тем, что вписано в настройках Spotify.
    $("sp-redirect").textContent = data.redirect_uri;
    // Страницу входа открываем в новой вкладке, чтобы панель осталась на месте.
    window.open(data.url, "_blank", "noopener");
  };
  $("sp-check").onclick = (e) => post("/api/spotify/check", e.target);
  $("sp-logout").onclick = (e) => post("/api/spotify/logout", e.target);
  $("sp-snap").onclick = (e) => post("/api/spotify/snapshot", e.target);
  $("sp-restore").onclick = (e) => post("/api/spotify/restore", e.target);

  $("debug-log").onchange = (e) => post(`/api/log/debug?on=${e.target.checked ? 1 : 0}`);

  function mmss(ms) {
    const total = Math.max(0, Math.round(ms / 1000));
    return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
  }

  function render(state) {
    $("version").textContent = `версия ${state.version}`;

    $("conns").replaceChildren(...state.connections.map((c) => {
      const el = document.createElement("span");
      el.className = c.connected ? "conn on" : "conn";
      el.innerHTML = `<i class="dot"></i>${c.name}`;
      if (c.detail) {
        const d = document.createElement("span");
        d.className = "detail";
        d.textContent = c.detail;
        el.append(d);
      }
      return el;
    }));

    renderSpotify(state.spotify);
    $("debug-log").checked = state.debug_log;

    const now = state.now;
    if (!now) {
      $("now").innerHTML = '<p class="empty">Тишина</p>';
    } else {
      const pct = now.duration_ms ? (now.position_ms / now.duration_ms) * 100 : 0;
      $("now").innerHTML = `
        ${now.cover_url ? `<img src="${now.cover_url}" alt="">` : ""}
        <div class="now-text">
          <div class="now-title">${escapeHtml(now.title)}</div>
          <div class="now-artist">${escapeHtml(now.artist)}</div>
          <div class="now-meta">
            ${now.requester ? `заказал ${escapeHtml(now.requester)} · ` : ""}
            ${now.provider === "youtube" ? "YouTube" : "Spotify"}
            ${now.uncertain ? ' · <span class="tag-uncertain">неточное совпадение</span>' : ""}
          </div>
          <div class="bar"><i style="width:${pct}%"></i></div>
          <div class="now-meta">${mmss(now.position_ms)} / ${mmss(now.duration_ms)}</div>
        </div>`;
    }

    $("queue-count").textContent = state.queue.length;
    if (!state.queue.length) {
      $("queue").innerHTML = '<li class="empty">Очередь пуста</li>';
    } else {
      $("queue").replaceChildren(...state.queue.map((item) => {
        const li = document.createElement("li");
        li.innerHTML = `
          <span class="q-title">${escapeHtml(item.artist)} — ${escapeHtml(item.title)}
            ${item.uncertain ? '<span class="tag-uncertain">неточно</span>' : ""}
          </span>
          <span class="q-by">${escapeHtml(item.requester)}</span>
          <span class="q-dur">${mmss(item.duration_ms)}</span>`;
        return li;
      }));
    }

    const notices = [...state.notices].reverse();
    if (!notices.length) {
      $("notices").innerHTML = '<li class="empty">Пока ничего не происходило</li>';
    } else {
      $("notices").replaceChildren(...notices.map((n) => {
        const li = document.createElement("li");
        li.className = `lvl-${n.level}`;
        const time = new Date(n.at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
        li.innerHTML = `<span class="at">${time}</span>` +
          (n.code ? `<b class="code">${escapeHtml(n.code)}</b> ` : "") +
          escapeHtml(n.text);
        return li;
      }));
    }
  }

  function renderSpotify(sp) {
    const box = $("sp-status");
    if (!sp.connected) {
      box.className = "sp-status bad";
      box.textContent = "Не подключён. Нажми «Подключить Spotify».";
    } else if (!sp.premium) {
      box.className = "sp-status bad";
      box.textContent = `${sp.account || "Аккаунт"} — без Premium. Управлять музыкой не получится.`;
    } else {
      box.className = "sp-status ok";
      box.textContent = `${sp.account} · Premium`;
    }

    $("sp-login").textContent = sp.connected ? "Подключить заново" : "Подключить Spotify";

    const snap = $("sp-snapshot");
    if (!sp.snapshot_text) {
      snap.className = "snapshot empty";
      snap.textContent = "Состояние ещё не запоминали";
    } else {
      const at = new Date(sp.snapshot_at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
      snap.className = "snapshot";
      snap.textContent = `${at} — ${sp.snapshot_text}`;
    }
  }

  function escapeHtml(s) {
    return String(s ?? "").replace(/[&<>"']/g, (c) => (
      { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
    ));
  }

  loadConfig();
  connect();
})();
