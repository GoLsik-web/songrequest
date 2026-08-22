// Панель рисуется целиком из одного снимка состояния. Приложение присылает
// снимок при подключении и потом только когда что-то изменилось, поэтому
// здесь нет ни одного setInterval для опроса.
(() => {
  const $ = (id) => document.getElementById(id);
  const status = $("status");
  let socket = null;
  let retryDelay = 1000;

  function connect() {
    socket = new WebSocket(`ws://${location.host}/ws`);

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

  function mmss(ms) {
    const total = Math.max(0, Math.round(ms / 1000));
    return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
  }

  function render(state) {
    $("link").textContent = `виджет: ${location.origin}/widget`;

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
        li.innerHTML = `<span class="at">${time}</span>${escapeHtml(n.text)}`;
        return li;
      }));
    }
  }

  function escapeHtml(s) {
    return String(s ?? "").replace(/[&<>"']/g, (c) => (
      { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
    ));
  }

  connect();
})();
