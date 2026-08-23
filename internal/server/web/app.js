// Панель рисуется целиком из одного снимка состояния. Приложение присылает
// снимок при подключении и потом только когда что-то изменилось, поэтому
// здесь нет ни одного setInterval для опроса.
(() => {
  const $ = (id) => document.getElementById(id);
  const status = $("status");

  const icon = (name, cls = "") =>
    `<svg class="${cls}"><use href="#i-${name}"></use></svg>`;

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));

  const mmss = (ms) => {
    const t = Math.max(0, Math.round(ms / 1000));
    return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
  };

  const human = (ms) => {
    const m = Math.round(ms / 60000);
    return m < 1 ? "меньше минуты" : `ещё ${m} мин`;
  };

  // ── связь с приложением ────────────────────────────────────────────

  let retryDelay = 1000;
  let lastQueueIds = new Set();

  function connect() {
    const socket = new WebSocket(`ws://${location.host}/ws`);

    socket.onopen = () => {
      retryDelay = 1000;
      say(`Подключено · ${location.host}`);
    };
    socket.onmessage = (e) => render(JSON.parse(e.data));
    socket.onclose = () => {
      say("Приложение не отвечает. Проверь, запущено ли оно.", true);
      // Пауза растёт: если приложение закрыто, браузер не должен
      // долбиться в него каждую секунду до конца стрима.
      setTimeout(connect, retryDelay);
      retryDelay = Math.min(retryDelay * 2, 15000);
    };
  }

  function say(text, bad) {
    status.textContent = text;
    status.className = bad ? "offline" : "online";
  }

  async function post(path, btn) {
    if (btn) btn.disabled = true;
    try {
      const r = await fetch(path, { method: "POST" });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        say(d.code ? `${d.code} · ${d.error}` : "Не получилось", true);
        return null;
      }
      return d;
    } catch {
      say("Приложение не отвечает", true);
      return null;
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  // ── шапка ──────────────────────────────────────────────────────────

  function renderBar(s) {
    const cells = s.connections.map((c) => {
      const cls = c.connected ? "on" : (c.detail === "Не настроено" ? "idle" : "off");
      const detail = c.detail ? `<b>${esc(c.detail)}</b>` : "";
      return `<div class="st ${cls}"><i class="led"></i>${esc(c.name)} ${detail}</div>`;
    }).join("");

    $("bar").innerHTML = cells +
      `<div class="bar-tail">
         <a href="#" id="toggle-settings">настройки</a>
         <a href="/widget" target="_blank">виджет</a>
         <span>${esc(s.version)}</span>
       </div>`;

    $("toggle-settings").onclick = (e) => {
      e.preventDefault();
      const panel = $("settings");
      panel.hidden = !panel.hidden;
      if (!panel.hidden) loadPlaylists();
    };
  }

  // ── эфир ───────────────────────────────────────────────────────────

  function renderStage(s) {
    // Самый первый запуск: Client ID ещё не вставлен. Отдельный экран, потому
    // что человеку тут нужно не «нажать кнопку», а сходить за строчкой в
    // инструкции — и найти настройки, которые спрятаны.
    if (!s.spotify.has_client_id) {
      $("stage").innerHTML = `
        <div class="eyebrow quiet"><i class="live"></i> Первый запуск</div>
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Нужен Client ID Spotify</div>
          <div class="say">Открой файл «Инструкция-Spotify» — там по шагам, как его получить.
            Это делается один раз и занимает минут десять.</div>
          <div class="acts"><button class="act key" id="stage-setup">Открыть настройки</button></div>
        </div>`;
      $("stage-setup").onclick = () => {
        $("settings").hidden = false;
        $("client-id").focus();
        $("client-id").scrollIntoView({ block: "center" });
      };
      return;
    }

    const spotifyDown = s.connections.some((c) => c.name === "Spotify" && !c.connected);

    if (spotifyDown) {
      const conn = s.connections.find((c) => c.name === "Spotify");
      $("stage").innerHTML = `
        <div class="eyebrow alarm"><i class="live"></i> Связи нет</div>
        <div class="void hot">
          <div class="rule"></div>
          <div class="mark">${esc(conn.detail)}</div>
          <div class="say">Заказы будут копиться в очереди и заиграют, как только подключишься.</div>
          <div class="acts"><button class="act key" id="stage-login">Подключить Spotify</button></div>
        </div>`;
      $("stage-login").onclick = doLogin;
      return;
    }

    const now = s.now;
    if (!now) {
      $("stage").innerHTML = `
        <div class="eyebrow quiet"><i class="live"></i> Тихо</div>
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Заказов нет</div>
          <div class="say">В Spotify играет то, что ты включил сам. Как только зритель закажет трек,
            он появится здесь, а после очереди музыка вернётся на ту же секунду.</div>
          ${snapshotLine(s.spotify)}
        </div>`;
      wireStageButtons();
      return;
    }

    const pct = now.duration_ms ? (now.position_ms / now.duration_ms) * 100 : 0;
    $("stage").innerHTML = `
      <div class="eyebrow">
        <i class="live"></i> В эфире
        <span class="sep">/</span>
        <span class="where">${now.provider === "youtube" ? "YouTube" : "Spotify"}${now.uncertain ? " · неточное совпадение" : ""}</span>
      </div>
      <div class="stage-row">
        <div class="art">${now.cover_url ? `<img src="${esc(now.cover_url)}" alt="">` : icon("music")}</div>
        <div style="min-width:0;flex:1">
          <h1 class="headline">${esc(now.title)}</h1>
          <div class="subhead">${esc(now.artist)}</div>
          ${now.requester ? `<div class="credit">${icon("user")}заказал <b>${esc(now.requester)}</b></div>` : ""}
          <div class="meter">
            <span class="t">${mmss(now.position_ms)}</span>
            <span class="bar"><i style="width:${pct}%"></i></span>
            <span class="t">${mmss(now.duration_ms)}</span>
          </div>
        </div>
      </div>
      <div class="acts">
        <button class="act key" id="skip">${icon("skip-forward")}Скипнуть</button>
        <button class="act" id="pause">${icon("pause")}Пауза</button>
        <button class="act" data-do="restore">${icon("rotate-cw")}Вернуть как было</button>
        <button class="act" data-do="snapshot">${icon("clock")}Запомнить состояние</button>
      </div>`;

    wireStageButtons();
  }

  // Строка снимка живёт в пустом состоянии: именно там она нужна — стример
  // видит, куда приложение вернётся, ещё до того как придёт первый заказ.
  function snapshotLine(sp) {
    if (!sp.snapshot_text) {
      return `<div class="acts">
        <button class="act key" data-do="snapshot">Запомнить состояние</button>
      </div>`;
    }
    const at = new Date(sp.snapshot_at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
    return `<div class="say" style="margin-top:16px;color:var(--dim)">
        Вернусь к: ${esc(sp.snapshot_text)}
        <span style="color:var(--dimmer)">· запомнено в ${at}</span>
      </div>
      <div class="acts">
        <button class="act key" data-do="restore">${icon("rotate-cw")}Вернуть как было</button>
        <button class="act" data-do="snapshot">Запомнить состояние</button>
      </div>`;
  }

  // Кнопки снимка живут в разных состояниях экрана, поэтому обработчик
  // один на всех и находит их по data-do.
  function wireStageButtons() {
    document.querySelectorAll("#stage [data-do]").forEach((b) => {
      b.onclick = (e) => post(`/api/spotify/${b.dataset.do}`, e.currentTarget);
    });
  }

  // ── очередь ────────────────────────────────────────────────────────

  function renderQueue(s) {
    $("q-count").textContent = s.queue.length;
    $("q-count").style.color = s.queue.length ? "var(--lime)" : "var(--dimmer)";

    const left = s.queue.reduce((sum, i) => sum + i.duration_ms, 0);
    $("q-rest").textContent = s.queue.length ? human(left) : "";

    if (!s.queue.length) {
      $("queue").innerHTML = `
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Пусто</div>
          <div class="say">Заказы появятся здесь, как только подключим Twitch и донаты.</div>
        </div>`;
      lastQueueIds = new Set();
      return;
    }

    const rows = s.queue.map((item, i) => {
      const fresh = lastQueueIds.size && !lastQueueIds.has(item.id) ? " fresh" : "";
      const marks = [];
      if (item.source === "donation") marks.push('<span class="chip money">донат</span>');
      if (item.uncertain) marks.push('<span class="chip doubt">неточно</span>');
      if (item.provider === "youtube") marks.push(icon("radio") + " YouTube");

      return `<li class="${fresh.trim()}" data-id="${item.id}">
        <span class="idx">${String(i + 1).padStart(2, "0")}</span>
        <span class="body">
          <span class="ttl">${esc(item.artist)} — ${esc(item.title)}</span>
          <span class="sub">${marks.join(" ")} ${esc(item.requester)}</span>
        </span>
        <span class="len">${mmss(item.duration_ms)}</span>
        <span class="tools">
          <button title="Наверх" data-act="top">${icon("arrow-up")}</button>
          <button class="kill" title="Удалить" data-act="remove">${icon("trash-2")}</button>
        </span>
      </li>`;
    }).join("");

    $("queue").innerHTML = `<ul class="rows">${rows}</ul>`;
    lastQueueIds = new Set(s.queue.map((i) => i.id));
  }

  // ── хроника ────────────────────────────────────────────────────────

  function renderFeed(s) {
    const notices = [...s.notices].reverse();
    if (!notices.length) {
      $("feed").innerHTML = `<li style="color:var(--dimmer);border:none">Пока ничего не происходило</li>`;
      return;
    }
    $("feed").innerHTML = notices.map((n) => {
      const when = new Date(n.at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
      const code = n.code
        ? `<span class="${n.level === "error" ? "fcode" : "wcode"}">${esc(n.code)}</span>`
        : "";
      return `<li><span class="when">${when}</span>
        <span class="${n.level === "error" ? "fault" : ""}">${code}${esc(n.text)}</span></li>`;
    }).join("");
  }

  // ── настройки ──────────────────────────────────────────────────────

  let config = null;

  async function loadConfig() {
    try {
      config = await (await fetch("/api/config")).json();
      $("client-id").value = config.spotify_client_id || "";
      $("twitch-id").value = config.twitch_client_id || "";
      applyMode(config.resume_fail_mode);
      $("playlist").value = config.fallback_playlist_id || "";
      refreshModeHints();
    } catch {
      say("Не смог прочитать настройки", true);
    }
  }

  function applyMode(mode) {
    document.querySelectorAll('input[name="resume"]').forEach((r) => {
      r.checked = r.value === mode;
      r.closest(".mode").classList.toggle("sel", r.checked);
    });
  }

  // Выбран запасной плейлист, но сам плейлист не выбран — приложение молча
  // деградирует до тишины. Молча для стрима, но не для настроек: тут это
  // должно быть видно.
  function refreshModeHints() {
    const broken = config
      && config.resume_fail_mode === "playlist"
      && !config.fallback_playlist_id;

    const playlistMode = document.querySelector('input[value="playlist"]').closest(".mode");
    playlistMode.classList.toggle("broken", broken);
    playlistMode.querySelector(".why").textContent = broken
      ? "Плейлист не выбран — пока работает как «ничего не включать»."
      : "Самый предсказуемый вариант: заранее знаешь, что заиграет.";

    const hint = $("playlist-hint");
    hint.className = broken ? "hint warn" : "hint";
    hint.textContent = broken ? "Выбери плейлист, иначе режим не сработает." : "";
  }

  async function saveConfig(patch) {
    try {
      const current = await (await fetch("/api/config")).json();
      Object.assign(current, patch);
      const r = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current),
      });
      if (!r.ok) throw new Error();
      config = await r.json();
      refreshModeHints();
      return true;
    } catch {
      say("Не смог сохранить настройки", true);
      return false;
    }
  }

  let playlistsLoaded = false;
  async function loadPlaylists() {
    if (playlistsLoaded) return;
    try {
      const r = await fetch("/api/spotify/playlists");
      const list = await r.json();
      if (!r.ok) {
        $("playlist-hint").className = "hint warn";
        $("playlist-hint").textContent = `${list.code} · ${list.error}`;
        return;
      }
      const chosen = config ? config.fallback_playlist_id : "";
      $("playlist").innerHTML = '<option value="">— не выбран —</option>' +
        list.map((p) => `<option value="${esc(p.id)}"${p.id === chosen ? " selected" : ""}>
          ${esc(p.name)} · ${p.tracks} треков</option>`).join("");
      playlistsLoaded = true;
      refreshModeHints();
    } catch {
      say("Не смог получить список плейлистов", true);
    }
  }

  document.querySelectorAll('input[name="resume"]').forEach((radio) => {
    radio.onchange = async () => {
      applyMode(radio.value);
      await saveConfig({ resume_fail_mode: radio.value });
      if (radio.value === "playlist") loadPlaylists();
    };
  });

  $("playlist").onchange = async (e) => {
    const name = e.target.selectedOptions[0].textContent.split(" · ")[0].trim();
    await saveConfig({
      fallback_playlist_id: e.target.value,
      fallback_playlist_name: e.target.value ? name : "",
    });
  };

  $("save-id").onclick = async (e) => {
    e.target.disabled = true;
    if (await saveConfig({ spotify_client_id: $("client-id").value.trim() })) {
      say("Client ID сохранён. Теперь нажми «Подключить Spotify».");
    }
    e.target.disabled = false;
  };

  async function doLogin(e) {
    const data = await post("/api/spotify/login", e && e.currentTarget);
    if (!data) return;
    // Показываем адрес возврата: если вход не пройдёт, первым делом сверяют
    // именно эту строку с тем, что вписано в настройках Spotify.
    $("redirect").textContent = `Адрес возврата: ${data.redirect_uri}`;
    window.open(data.url, "_blank", "noopener");
  }

  $("login").onclick = doLogin;
  $("logout").onclick = (e) => post("/api/spotify/logout", e.currentTarget);
  $("debug-log").onchange = (e) => post(`/api/log/debug?on=${e.target.checked ? 1 : 0}`);

  // ── сборка ─────────────────────────────────────────────────────────

  function renderAccount(sp) {
    const box = $("account");
    if (!sp.connected) {
      box.className = "account";
      box.innerHTML = `<div class="who">Вход не выполнен</div>`;
      return;
    }

    const bad = sp.plan === "free";
    const iffy = sp.plan === "unknown";
    box.className = "account" + (bad ? " bad" : iffy ? " iffy" : " ok");
    box.innerHTML = `
      <div class="who">${esc(sp.account || "аккаунт без имени")}</div>
      ${sp.email ? `<div class="mail">${esc(sp.email)}</div>` : ""}
      <div class="plan">${esc(sp.plan_label)}</div>
      ${sp.plan_note ? `<div class="note">
        ${sp.plan_note_code ? `<b>${esc(sp.plan_note_code)}</b> ` : ""}${esc(sp.plan_note)}
      </div>` : ""}`;
  }

  // ── Twitch ─────────────────────────────────────────────────────────

  function renderBanner(tw) {
    const banner = $("banner");
    if (!tw.pending_code) {
      banner.hidden = true;
      return;
    }
    const left = Math.max(0, Math.round((new Date(tw.pending_expires) - Date.now()) / 60000));
    banner.hidden = false;
    banner.innerHTML = `
      <div class="what">Открой <a href="${esc(tw.pending_url)}" target="_blank">${esc(tw.pending_url)}</a>
        на любом устройстве и введи код:</div>
      <div class="code">${esc(tw.pending_code)}</div>
      <div class="left">код действует ещё ${left} мин</div>`;
  }

  function renderTwitchAccount(tw) {
    const box = $("twitch-account");
    if (!tw.connected) {
      box.className = "account";
      box.innerHTML = `<div class="who">Вход не выполнен</div>`;
      return;
    }

    const bad = !tw.has_points;
    box.className = "account" + (bad ? " bad" : tw.reward_ready ? " ok" : " iffy");
    box.innerHTML = `
      <div class="who">${esc(tw.channel || "канал")}</div>
      <div class="mail">${esc(tw.channel_type)}</div>
      <div class="plan">${tw.reward_ready
        ? `награда «${esc(tw.reward_title)}» · ${tw.reward_cost} баллов`
        : "награда не создана"}</div>
      ${tw.note ? `<div class="note">
        ${tw.note_code ? `<b>${esc(tw.note_code)}</b> ` : ""}${esc(tw.note)}
      </div>` : ""}`;
  }

  // Заказы за баллы. Очереди ещё нет, поэтому они показываются списком —
  // так проверяется весь путь: нажатие зрителем, приём, возврат баллов.
  function renderRedemptions(list) {
    if (!list.length) return false;

    $("q-count").textContent = list.length;
    $("q-count").style.color = "var(--lime)";
    $("q-rest").textContent = "очередь появится на следующем этапе";

    $("queue").innerHTML = `<ul class="rows">${list.map((r, i) => {
      const at = new Date(r.at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
      const busy = r.status !== "новый";
      return `<li data-id="${esc(r.id)}">
        <span class="idx">${String(i + 1).padStart(2, "0")}</span>
        <span class="body">
          <span class="ttl">${esc(r.text) || "<без текста>"}</span>
          <span class="sub"><span class="chip money">${r.cost} баллов</span> ${esc(r.user)} · ${at}</span>
        </span>
        <span class="len"></span>
        <span class="deal">${busy
          ? `<span class="done ${r.status === "баллы возвращены" ? "paid" : ""}">${esc(r.status)}</span>`
          : `<button data-act="fulfill">Принять</button>
             <button class="kill" data-act="refund">Вернуть баллы</button>`}</span>
      </li>`;
    }).join("")}</ul>`;

    $("queue").querySelectorAll("button[data-act]").forEach((b) => {
      b.onclick = (e) => {
        const id = e.currentTarget.closest("li").dataset.id;
        post(`/api/redemptions/${encodeURIComponent(id)}?action=${b.dataset.act}`, e.currentTarget);
      };
    });
    return true;
  }

  $("save-twitch-id").onclick = async (e) => {
    e.target.disabled = true;
    if (await saveConfig({ twitch_client_id: $("twitch-id").value.trim() })) {
      say("Client ID Twitch сохранён. Теперь нажми «Подключить Twitch».");
    }
    e.target.disabled = false;
  };
  $("twitch-login").onclick = (e) => post("/api/twitch/login", e.currentTarget);
  $("twitch-logout").onclick = (e) => post("/api/twitch/logout", e.currentTarget);

  function render(s) {
    renderBar(s);
    renderAccount(s.spotify);
    renderBanner(s.twitch);
    renderTwitchAccount(s.twitch);
    renderStage(s);
    if (!renderRedemptions(s.redemptions)) renderQueue(s);
    renderFeed(s);
    $("debug-log").checked = s.debug_log;
  }

  fetch("/static/icons/sprite.svg")
    .then((r) => r.text())
    .then((svg) => { $("sprite").innerHTML = svg; });

  loadConfig();
  connect();
})();
