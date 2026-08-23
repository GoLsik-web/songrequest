// Панель рисуется из одного снимка состояния. Приложение присылает снимок при
// подключении и потом только когда что-то изменилось, поэтому здесь нет ни
// одного setInterval для опроса.
//
// Каждый блок перерисовывается только если изменился именно он: панель открыта
// часами, и полная перерисовка на каждое сообщение сбивала бы наведение мыши,
// заново проигрывала анимации и мигала текстом.
(() => {
  const $ = (id) => document.getElementById(id);
  const status = $("status");

  const icon = (name) => `<svg aria-hidden="true"><use href="#i-${name}"></use></svg>`;

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));

  const mmss = (ms) => {
    const t = Math.max(0, Math.round(ms / 1000));
    return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
  };

  const hhmm = (iso) =>
    new Date(iso).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });

  // Склонение — мелочь, но «5 треков» и «2 трека» стоят рядом с числами,
  // и «5 трек» читается как недоделка.
  const plural = (n, one, few, many) => {
    const a = Math.abs(n) % 100;
    const b = a % 10;
    if (a > 10 && a < 20) return many;
    if (b > 1 && b < 5) return few;
    if (b === 1) return one;
    return many;
  };

  const minutesLeft = (ms) => {
    const m = Math.round(ms / 60000);
    return m < 1 ? "меньше минуты" : `${m} ${plural(m, "минута", "минуты", "минут")}`;
  };

  // draw перерисовывает блок только при изменении: сравниваем подпись данных.
  const drawn = new Map();
  function draw(key, signature, paint) {
    if (drawn.get(key) === signature) return;
    drawn.set(key, signature);
    paint();
  }

  // ── связь с приложением ────────────────────────────────────────────

  let retryDelay = 1000;
  let knownOrderIds = null;

  function connect() {
    const socket = new WebSocket(`ws://${location.host}/ws`);

    socket.onopen = () => {
      retryDelay = 1000;
      say(`Подключено · ${location.host}`);
    };
    socket.onmessage = (e) => render(JSON.parse(e.data));
    socket.onclose = () => {
      say("Приложение не отвечает. Проверь, запущено ли оно.", true);
      // Приглушаем всю страницу: показанное на ней уже неактуально, и это
      // должно быть видно, а не выглядеть как живые данные.
      document.body.classList.add("stale");
      // Пауза растёт: если приложение закрыто, браузер не должен долбиться
      // в него каждую секунду до конца стрима.
      setTimeout(connect, retryDelay);
      retryDelay = Math.min(retryDelay * 2, 15000);
    };
  }

  let sayTimer = null;
  function say(text, bad) {
    status.textContent = text;
    status.className = bad ? "offline" : "online";
    // Ошибка висит, пока её не сменят. Сообщение об удаче гаснет само: строка
    // внизу не должна навсегда застревать на «Client ID сохранён».
    clearTimeout(sayTimer);
    if (!bad) {
      sayTimer = setTimeout(() => {
        if (document.body.classList.contains("stale")) return;
        status.textContent = `Подключено · ${location.host}`;
      }, 6000);
    }
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

  // ── лента подключений ──────────────────────────────────────────────

  function renderBar(s) {
    $("conns").innerHTML = s.connections.map((c) => {
      // Цвет берём из уровня, а не угадываем по тексту: иначе новая
      // формулировка молча перекрасила бы ячейку.
      const cls = { ok: "on", idle: "idle", fail: "off" }[c.level] || "idle";
      return `<div class="st ${cls}" title="${esc(c.name)}: ${esc(c.detail)}">
        <i class="led"></i>${esc(c.name)}${c.detail ? ` <b>${esc(c.detail)}</b>` : ""}
      </div>`;
    }).join("");
    $("version").textContent = s.version;
  }

  // ── эфир ───────────────────────────────────────────────────────────

  // Главный экран. Ради него панель и открывают на две секунды, поэтому он
  // обязан показывать настоящее положение дел, а не «что-то не так»:
  // «не настроено» — это не авария, отсутствие Premium не чинится повторным
  // входом, а обещать заказы, когда Twitch не подключён, — враньё.
  function renderStage(s) {
    const stage = $("stage");
    const sp = s.spotify;
    const tw = s.twitch;

    const screen = (mood, eyebrow, mark, say, buttons) => {
      stage.innerHTML = `
        <div class="eyebrow ${mood}"><i class="live"></i> ${eyebrow}</div>
        <div class="void ${mood === "alarm" ? "hot" : ""}">
          <div class="rule"></div>
          <div class="mark">${mark}</div>
          <div class="say">${say}</div>
          ${buttons ? `<div class="acts">${buttons}</div>` : ""}
        </div>`;
    };

    // 1. Ещё ничего не настроено.
    if (!sp.has_client_id) {
      screen("quiet", "Первый запуск", "Нужен Client ID Spotify",
        "Открой файл «Инструкция-Spotify» — там по шагам, как его получить. " +
        "Делается один раз, минут за десять.",
        `<button class="act key" data-open-settings>Открыть настройки</button>`);
      return;
    }

    // 2. Client ID вставлен, но входа ещё не было. Это ожидаемый шаг, а не
    // поломка, поэтому спокойный экран, а не красный.
    if (!sp.connected) {
      screen("quiet", "Почти готово", "Осталось подключить Spotify",
        "Приложение откроет страницу Spotify, там надо разрешить доступ. " +
        "Аккаунт нужен тот, в котором ты слушаешь музыку на стриме.",
        `<button class="act key" data-do="login">Подключить Spotify</button>`);
      return;
    }

    // 3. Вход есть, а связи не было: аккаунт ещё не прочитан.
    if (!sp.account) {
      screen("alarm", "Связи нет", "Spotify не отвечает",
        "Вход сохранён, но узнать аккаунт не вышло. Проверь интернет — " +
        "и если пользуешься VPN, включи его.",
        `<button class="act key" data-do="check">Проверить связь</button>`);
      return;
    }

    // 4. Подписки нет. Повторный вход это не лечит, и предлагать его —
    // значит гонять человека по кругу.
    if (sp.plan === "free") {
      screen("alarm", "Играть не сможет", "На аккаунте нет Spotify Premium",
        `Вход выполнен как <b>${esc(sp.account)}</b>, но управлять музыкой Spotify ` +
        "разрешает только с подпиской. Проверь в настройках, тем ли аккаунтом вошёл.",
        `<button class="act" data-open-settings>Посмотреть аккаунт</button>`);
      return;
    }

    // 5. Spotify готов, а заказывать пока неоткуда.
    if (!tw.connected) {
      screen("quiet", "Заказы не принимаются", "Twitch не подключён",
        "Spotify готов, но зрителям пока нечего нажимать: награда на канале " +
        "появится после подключения Twitch.",
        `<button class="act key" data-do="twitch-login">Подключить Twitch</button>
         <button class="act" data-open-settings>Настройки</button>`);
      return;
    }
    if (!tw.reward_ready) {
      screen("quiet", "Заказы не принимаются", "Награды на канале нет",
        tw.note ? esc(tw.note)
                : "Приложение не смогло создать награду за баллы. Загляни в настройки.",
        `<button class="act" data-open-settings>Настройки</button>`);
      return;
    }

    // 6. Всё готово, но тихо — самый частый экран.
    const now = s.now;
    if (!now) {
      stage.innerHTML = `
        <div class="eyebrow quiet"><i class="live"></i> Тихо</div>
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Заказов нет</div>
          <div class="say">В Spotify играет то, что ты включил сам. Когда зритель закажет
            трек за ${tw.reward_cost} ${plural(tw.reward_cost, "балл", "балла", "баллов")},
            он появится здесь.</div>
          ${snapshotBlock(sp)}
        </div>`;
      return;
    }

    // 7. Играет заказ.
    const pct = now.duration_ms ? (now.position_ms / now.duration_ms) * 100 : 0;
    stage.innerHTML = `
      <div class="eyebrow">
        <i class="live"></i> В эфире
        <span class="sep">/</span>
        <span class="where">${now.provider === "youtube" ? "YouTube" : "Spotify"}${
          now.uncertain ? " · неточное совпадение" : ""}</span>
      </div>
      <div class="stage-row">
        <div class="art">${now.cover_url ? `<img src="${esc(now.cover_url)}" alt="">` : icon("music")}</div>
        <div class="stage-text">
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
        <button class="act key" data-do="restore">${icon("rotate-cw")}Вернуть как было</button>
        <button class="act" data-do="snapshot">${icon("clock")}Запомнить заново</button>
      </div>`;
  }

  // Блок снимка живёт в пустом состоянии: именно там он и нужен — видно, куда
  // приложение вернётся, ещё до того как придёт первый заказ.
  function snapshotBlock(sp) {
    if (!sp.snapshot_text) {
      return `<div class="acts">
        <button class="act key" data-do="snapshot">${icon("clock")}Запомнить состояние</button>
      </div>`;
    }
    return `<div class="recall">
        <span class="label">вернусь к</span>
        <span class="what">${esc(sp.snapshot_text)}</span>
        <span class="when">запомнено в ${hhmm(sp.snapshot_at)}</span>
      </div>
      <div class="acts">
        <button class="act key" data-do="restore">${icon("rotate-cw")}Вернуть как было</button>
        <button class="act" data-do="snapshot">${icon("clock")}Запомнить заново</button>
      </div>`;
  }

  // ── заказы ─────────────────────────────────────────────────────────

  function renderOrders(s) {
    const list = s.redemptions;
    const box = $("orders");

    // Считаем только неразобранные: иначе бейдж горит вечно, а обнулить его
    // нечем — удаления записей нет.
    const pending = list.filter((r) => r.status === "new").length;
    $("orders-count").textContent = pending;
    $("orders-count").classList.toggle("zero", pending === 0);

    if (!list.length) {
      box.innerHTML = `
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Пока пусто</div>
          <div class="say">Заказы появятся здесь, как только зритель потратит баллы на
            награду${s.twitch.reward_title ? ` «${esc(s.twitch.reward_title)}»` : ""}.</div>
        </div>`;
      knownOrderIds = new Set();
      return;
    }

    // При первой отрисовке подсвечивать нечего: иначе после перезагрузки
    // страницы мигнёт весь список разом.
    const first = knownOrderIds === null;

    box.innerHTML = `<ul class="rows">${list.map((r, i) => {
      const fresh = !first && !knownOrderIds.has(r.id) ? " fresh" : "";
      const done = r.status !== "new";
      return `<li class="${fresh.trim()}">
        <span class="idx">${String(i + 1).padStart(2, "0")}</span>
        <span class="body">
          <span class="ttl">${r.text ? esc(r.text) : `<span class="muted">без текста</span>`}</span>
          <span class="sub">
            <span class="chip money">${r.cost} ${plural(r.cost, "балл", "балла", "баллов")}</span>
            ${esc(r.user)} · ${hhmm(r.at)}
          </span>
        </span>
        <span class="deal">${done
          ? `<span class="done">${r.status === "refunded" ? "баллы возвращены" : "принят"}</span>`
          : `<button class="act small" data-order="${esc(r.id)}" data-act="fulfill">Принять</button>
             <button class="act small risky" data-order="${esc(r.id)}" data-act="refund">Вернуть баллы</button>`}
        </span>
      </li>`;
    }).join("")}</ul>`;

    knownOrderIds = new Set(list.map((r) => r.id));
  }

  // ── хроника ────────────────────────────────────────────────────────
  //
  // Показываем несколько последних записей. Полная лента нужна при разборе
  // проблемы, но в обычное время это самая длинная колонка на экране при
  // наименьшей ценности — она не должна перетягивать внимание.

  const FEED_SHORT = 5;
  let feedOpen = false;

  function renderFeed(s) {
    const all = [...s.notices].reverse();

    if (!all.length) {
      $("feed").innerHTML = `<li class="muted">Пока ничего не происходило</li>`;
      $("feed-more").hidden = true;
      return;
    }

    const shown = feedOpen ? all : all.slice(0, FEED_SHORT);

    $("feed").innerHTML = shown.map((n) => {
      const code = n.code
        ? `<span class="${n.level === "error" ? "fcode" : "wcode"}">${esc(n.code)}</span>`
        : "";
      // Повторы схлопнуты в счётчик ещё в приложении, здесь только показываем.
      const times = n.count > 1 ? `<span class="times">×${n.count}</span>` : "";
      return `<li class="lvl-${esc(n.level)}">
        <span class="when">${hhmm(n.at)}</span>
        <span class="what">${code}${esc(n.text)}${times}</span>
      </li>`;
    }).join("");

    const more = $("feed-more");
    const hidden = all.length - FEED_SHORT;
    more.hidden = hidden <= 0;
    more.textContent = feedOpen
      ? "свернуть"
      : `ещё ${hidden} ${plural(hidden, "запись", "записи", "записей")}`;
  }

  $("feed-more").onclick = () => {
    feedOpen = !feedOpen;
    if (lastState) renderFeed(lastState);
  };

  // ── Twitch ─────────────────────────────────────────────────────────

  let bannerTimer = null;

  function renderBanner(tw) {
    const banner = $("banner");
    clearInterval(bannerTimer);

    if (!tw.pending_code) {
      banner.hidden = true;
      return;
    }

    banner.hidden = false;
    banner.innerHTML = `
      <div class="what">Открой <a href="${esc(tw.pending_url)}" target="_blank"
        rel="noopener">${esc(tw.pending_url)}</a> на любом устройстве и введи код:</div>
      <div class="code">${esc(tw.pending_code)}</div>
      <div class="left" id="code-left"></div>`;

    // Единственный таймер в панели, и живёт он только пока висит код: без него
    // человек не понимает, сколько у него ещё есть времени.
    const tick = () => {
      const el = $("code-left");
      if (!el) return;
      const left = new Date(tw.pending_expires) - Date.now();
      if (left <= 0) {
        el.textContent = "код истёк — нажми «Подключить Twitch» ещё раз";
        clearInterval(bannerTimer);
        return;
      }
      el.textContent = `код действует ещё ${minutesLeft(left)}`;
    };
    tick();
    bannerTimer = setInterval(tick, 20000);
  }

  // ── карточки аккаунтов ─────────────────────────────────────────────

  const note = (code, text) =>
    `<div class="note">${code ? `<b>${esc(code)}</b> ` : ""}${esc(text)}</div>`;

  function renderSpotifyCard(sp) {
    const box = $("account");
    if (!sp.connected) {
      box.className = "account";
      box.innerHTML = `<div class="who muted">Вход не выполнен</div>`;
      return;
    }
    if (!sp.account) {
      box.className = "account bad";
      box.innerHTML = `<div class="who">Вход сохранён</div>
        <div class="plan">но связи со Spotify не было — аккаунт неизвестен</div>`;
      return;
    }
    box.className = "account " + (sp.plan === "free" ? "bad" : sp.plan === "unknown" ? "iffy" : "ok");
    box.innerHTML = `
      <div class="who">${esc(sp.account || "аккаунт без имени")}</div>
      ${sp.email ? `<div class="mail">${esc(sp.email)}</div>` : ""}
      <div class="plan">${esc(sp.plan_label)}</div>
      ${sp.plan_note ? note(sp.plan_note_code, sp.plan_note) : ""}`;
  }

  function renderTwitchCard(tw) {
    const box = $("twitch-account");
    if (!tw.connected) {
      box.className = "account";
      box.innerHTML = `<div class="who muted">Вход не выполнен</div>`;
      return;
    }
    if (!tw.channel) {
      box.className = "account bad";
      box.innerHTML = `<div class="who">Вход сохранён</div>
        <div class="plan">но связи с Twitch не было — канал неизвестен</div>`;
      return;
    }
    // Канал без баллов — не поломка, поэтому нейтральный вид, а не красный.
    box.className = "account " + (tw.reward_ready ? "ok" : "iffy");
    box.innerHTML = `
      <div class="who">${esc(tw.channel || "канал")}</div>
      <div class="mail">${esc(tw.channel_type)}</div>
      <div class="plan">${tw.reward_ready
        ? `награда «${esc(tw.reward_title)}» · ${tw.reward_cost} ${plural(tw.reward_cost, "балл", "балла", "баллов")}`
        : tw.has_points ? "награда не создана" : "заказы за баллы недоступны"}</div>
      ${tw.note ? note(tw.note_code, tw.note) : ""}`;
  }

  // ── настройки ──────────────────────────────────────────────────────

  let config = null;
  let playlistsLoaded = false;
  let redirectShown = false;

  function toggleSettings(open) {
    const panel = $("settings");
    const want = open === undefined ? panel.hidden : open;
    panel.hidden = !want;
    $("settings-toggle").classList.toggle("active", want);
    $("settings-toggle").setAttribute("aria-expanded", String(want));
    if (want) {
      panel.scrollIntoView({ behavior: "smooth", block: "start" });
      loadPlaylists();
    }
  }

  async function loadConfig() {
    try {
      config = await (await fetch("/api/config")).json();
      $("client-id").value = config.spotify_client_id || "";
      $("twitch-id").value = config.twitch_client_id || "";
      applyMode(config.resume_fail_mode);
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
  // деградирует до тишины. Молча для стрима, но не для настроек.
  function refreshModeHints() {
    const broken = config && config.resume_fail_mode === "playlist" && !config.fallback_playlist_id;
    const row = document.querySelector('input[value="playlist"]').closest(".mode");
    row.classList.toggle("broken", broken);
    row.querySelector(".why").textContent = broken
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

  async function loadPlaylists() {
    if (playlistsLoaded) return;
    if (!lastState || !lastState.spotify.connected) return;
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
        list.map((p) => `<option value="${esc(p.id)}" data-name="${esc(p.name)}"${
          p.id === chosen ? " selected" : ""}>${esc(p.name)} · ${p.tracks} ${
          plural(p.tracks, "трек", "трека", "треков")}</option>`).join("");
      playlistsLoaded = true;
      refreshModeHints();
    } catch {
      say("Не смог получить список плейлистов", true);
    }
  }

  // ── действия ───────────────────────────────────────────────────────

  // Сохраняет поле, если его успели изменить, и только потом подключается.
  async function saveThenConnect(field, key, run, btn) {
    const value = $(field).value.trim();
    if (!config || config[key] !== value) {
      if (!(await saveConfig({ [key]: value }))) return null;
    }
    return run(btn);
  }

  const actions = {
    login: (btn) => saveThenConnect("client-id", "spotify_client_id",
      (b) => post("/api/spotify/login", b), btn).then((d) => {
      if (!d) return;
      // Показываем адрес возврата: если вход не пройдёт, первым делом сверяют
      // именно эту строку с тем, что вписано в настройках Spotify.
      $("redirect").textContent = `Адрес возврата: ${d.redirect_uri}`;
      window.open(d.url, "_blank", "noopener");
      redirectShown = true;
      return d;
    }),
    logout: (btn) => post("/api/spotify/logout", btn),
    check: (btn) => post("/api/spotify/check", btn),
    snapshot: (btn) => post("/api/spotify/snapshot", btn),
    restore: (btn) => post("/api/spotify/restore", btn),
    "twitch-login": (btn) => saveThenConnect("twitch-id", "twitch_client_id",
      (b) => post("/api/twitch/login", b), btn),
    "twitch-logout": (btn) => post("/api/twitch/logout", btn),
  };

  // Один обработчик на всю страницу: кнопки живут внутри блоков, которые
  // перерисовываются, и навешивать им обработчики заново каждый раз — верный
  // способ однажды об этом забыть.
  document.addEventListener("click", (e) => {
    if (e.target.closest("[data-open-settings]")) {
      toggleSettings(true);
      $("client-id").focus();
      $("client-id").scrollIntoView({ block: "center", behavior: "smooth" });
      return;
    }

    const order = e.target.closest("[data-order]");
    if (order) {
      post(`/api/redemptions/${encodeURIComponent(order.dataset.order)}?action=${order.dataset.act}`, order);
      return;
    }

    const act = e.target.closest("[data-do]");
    if (act && actions[act.dataset.do]) actions[act.dataset.do](act);
  });

  $("settings-toggle").onclick = () => toggleSettings();

  $("save-id").onclick = async () => {
    if (await saveConfig({ spotify_client_id: $("client-id").value.trim() })) {
      say("Client ID сохранён. Теперь нажми «Подключить Spotify».");
    }
  };
  $("save-twitch-id").onclick = async () => {
    if (await saveConfig({ twitch_client_id: $("twitch-id").value.trim() })) {
      say("Client ID Twitch сохранён. Теперь нажми «Подключить Twitch».");
    }
  };

  document.querySelectorAll('input[name="resume"]').forEach((radio) => {
    radio.onchange = async () => {
      applyMode(radio.value);
      await saveConfig({ resume_fail_mode: radio.value });
      if (radio.value === "playlist") loadPlaylists();
    };
  });

  $("playlist").onchange = async (e) => {
    const name = e.target.selectedOptions[0].dataset.name || "";
    await saveConfig({
      fallback_playlist_id: e.target.value,
      fallback_playlist_name: e.target.value ? name : "",
    });
  };

  $("debug-log").onchange = (e) => post(`/api/log/debug?on=${e.target.checked ? 1 : 0}`);

  // ── сборка ─────────────────────────────────────────────────────────

  let lastState = null;

  const pendingOrders = (s) => s.redemptions.filter((r) => r.status === "new").length;

  function render(s) {
    lastState = s;
    document.body.classList.remove("stale");

    const sig = JSON.stringify;
    draw("bar", sig(s.connections) + s.version, () => renderBar(s));
    draw("stage", sig([s.now, s.spotify, s.connections]), () => renderStage(s));
    draw("orders", sig([s.redemptions, s.twitch.reward_title]), () => renderOrders(s));
    draw("feed", sig(s.notices), () => renderFeed(s));
    draw("banner", sig([s.twitch.pending_code, s.twitch.pending_expires]), () => renderBanner(s.twitch));
    draw("sp-card", sig(s.spotify), () => renderSpotifyCard(s.spotify));
    draw("tw-card", sig(s.twitch), () => renderTwitchCard(s.twitch));

    $("debug-log").checked = s.debug_log;

    if (s.spotify.connected && !playlistsLoaded && !$("settings").hidden) loadPlaylists();
    if (redirectShown && s.spotify.account) {
      $("redirect").textContent = "";
      redirectShown = false;
    }

    // Заголовок вкладки — тоже часть панели: стример держит её в фоне.
    document.title = s.now
      ? `${s.now.artist} — ${s.now.title}`
      : pendingOrders(s) > 0
        ? `${pendingOrders(s)} · Заказ музыки`
        : "Заказ музыки";
  }

  fetch("/static/icons/sprite.svg")
    .then((r) => r.text())
    .then((svg) => { $("sprite").innerHTML = svg; });

  loadConfig();
  connect();
})();
