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

  // Главный экран.
  //
  // Пока заказов нет, показывать «ничего не происходит» серым текстом — значит
  // отдать три четверти экрана под пустоту. Вместо этого экран показывает
  // готовность: что уже настроено, чего не хватает и куда вернётся музыка.
  // Это настоящие сведения, они же и есть инструкция по первому запуску.

  // Шаги готовности. Каждый знает, выполнен ли он, что показать и что нажать.
  function steps(s) {
    const sp = s.spotify;
    const tw = s.twitch;

    const spotifyStep = () => {
      if (!sp.has_client_id) {
        return { state: "todo", value: "Client ID не вставлен",
                 hint: "Открой «Инструкция-Spotify» — это делается один раз",
                 button: `<button class="act key small" data-open-settings>Открыть настройки</button>` };
      }
      if (!sp.connected) {
        return { state: "todo", value: "Вход не выполнен",
                 hint: "Разреши доступ на странице Spotify",
                 button: `<button class="act key small" data-do="login">Подключить Spotify</button>` };
      }
      if (!sp.account) {
        return { state: "fail", value: "Spotify не отвечает",
                 hint: "Проверь интернет; если пользуешься VPN — включи его",
                 button: `<button class="act small" data-do="check">Проверить связь</button>` };
      }
      if (sp.plan === "free") {
        return { state: "fail", value: `${sp.account} · без Premium`,
                 hint: "Управлять музыкой Spotify разрешает только с подпиской",
                 button: `<button class="act small" data-open-settings>Посмотреть аккаунт</button>` };
      }
      return {
        state: sp.plan === "unknown" ? "warn" : "done",
        value: sp.account,
        hint: sp.plan_label,
      };
    };

    const twitchStep = () => {
      if (!tw.has_client_id) {
        return { state: "todo", value: "Client ID не вставлен",
                 hint: "Открой «Инструкция-Twitch»",
                 button: `<button class="act key small" data-open-settings>Открыть настройки</button>` };
      }
      if (!tw.connected) {
        return { state: "todo", value: "Вход не выполнен",
                 hint: "Приложение покажет короткий код",
                 button: `<button class="act key small" data-do="twitch-login">Подключить Twitch</button>` };
      }
      if (!tw.channel) {
        return { state: "fail", value: "Twitch не отвечает", hint: "Канал пока неизвестен" };
      }
      return { state: "done", value: tw.channel, hint: tw.channel_type };
    };

    const rewardStep = () => {
      if (!tw.connected) return { state: "wait", value: "—", hint: "после подключения Twitch" };
      if (!tw.has_points) {
        return { state: "off", value: "Баллов на канале нет",
                 hint: "Награда бывает только у аффилиатов и партнёров" };
      }
      if (!tw.reward_ready) {
        return { state: "fail", value: "Не создана",
                 hint: tw.note || "Приложение не смогло создать награду" };
      }
      return {
        state: "done",
        value: `«${esc(tw.reward_title)}»`,
        hint: `${tw.reward_cost} ${plural(tw.reward_cost, "балл", "балла", "баллов")} за заказ`,
      };
    };

    const snapshotStep = () => {
      if (!sp.connected || !sp.account) return { state: "wait", value: "—", hint: "после подключения Spotify" };
      if (!sp.snapshot_text) {
        return { state: "todo", value: "Не запомнено",
                 hint: "Включи музыку и нажми — приложение вернётся сюда после заказов",
                 button: `<button class="act key small" data-do="snapshot">Запомнить состояние</button>` };
      }
      return {
        state: "done",
        value: esc(sp.snapshot_text),
        hint: `запомнено в ${hhmm(sp.snapshot_at)}`,
        button: `<button class="act small" data-do="restore">Вернуть сейчас</button>
                 <button class="act small" data-do="snapshot">Запомнить заново</button>`,
      };
    };

    return [
      { name: "Spotify", ...spotifyStep() },
      { name: "Twitch", ...twitchStep() },
      { name: "Награда", ...rewardStep() },
      { name: "Точка возврата", ...snapshotStep() },
    ];
  }

  function renderStage(s) {
    const list = steps(s);
    const now = s.now;

    // Играет заказ — всё остальное отходит на второй план.
    if (now) {
      const pct = now.duration_ms ? (now.position_ms / now.duration_ms) * 100 : 0;
      swap($("stage"), `
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
          <button class="act key" data-do="skip">${icon("skip-forward")}Скипнуть</button>
          <button class="act" data-do="restore">${icon("rotate-cw")}Вернуть Spotify</button>
        </div>`);
      return;
    }

    const done = list.filter((x) => x.state === "done").length;
    const broken = list.some((x) => x.state === "fail");
    const ready = done === list.length;

    let brow, mark, sub;
    if (broken) {
      brow = `<div class="eyebrow alarm"><i class="live"></i> Нужно вмешаться</div>`;
      mark = "Что-то отвалилось";
      sub = "Ниже видно, что именно. Заказы пока не принимаются.";
    } else if (ready) {
      brow = `<div class="eyebrow"><i class="live"></i> Готов принимать заказы</div>`;
      mark = "Всё настроено";
      sub = `Зрители заказывают трек за ${s.twitch.reward_cost} ${
        plural(s.twitch.reward_cost, "балл", "балла", "баллов")}. Как только заказ придёт, он появится здесь.`;
    } else {
      brow = `<div class="eyebrow quiet"><i class="live"></i> Настройка · ${done} из ${list.length}</div>`;
      mark = "Ещё не всё готово";
      sub = "Пройди шаги ниже — это делается один раз.";
    }

    swap($("stage"), `
      ${brow}
      <div class="head">
        <h1 class="big">${mark}</h1>
        <p class="lead">${sub}</p>
      </div>
      <div class="rail"><i style="width:${(done / list.length) * 100}%"></i></div>
      <ol class="steps">
        ${list.map((step, i) => `
          <li class="step ${step.state}">
            <div class="no">${String(i + 1).padStart(2, "0")}</div>
            <div class="step-body">
              <div class="step-name">${esc(step.name)}</div>
              <div class="step-value">${step.value}</div>
              <div class="step-hint">${esc(step.hint)}</div>
              ${step.button ? `<div class="step-acts">${step.button}</div>` : ""}
            </div>
          </li>`).join("")}
      </ol>`);
  }

  // Плавная подмена содержимого: без неё блок мигает новым текстом рывком.
  function swap(host, html) {
    const box = document.createElement("div");
    box.className = "swap";
    box.innerHTML = html;
    host.replaceChildren(box);
  }

  // ── очередь ────────────────────────────────────────────────────────
  //
  // Частые действия — скип и удаление — в один клик, без подтверждений.
  // Подтверждение только у того, что не отменить: очистка очереди и бан.

  let dragId = null;

  function renderOrders(s) {
    const list = s.queue;
    const box = $("orders");

    $("orders-count").textContent = list.length;
    $("orders-count").classList.toggle("zero", list.length === 0);

    const left = list.reduce((sum, i) => sum + i.duration_ms, 0);
    $("queue-rest").textContent = list.length ? `ещё ${minutesLeft(left)}` : "";

    $("queue-pause").textContent = s.paused ? "возобновить заказы" : "остановить заказы";
    $("queue-pause").classList.toggle("on", s.paused);
    $("queue-clear").hidden = list.length === 0;

    if (!list.length) {
      swap(box, s.twitch.reward_ready ? `
        <div class="preview">
          <div class="preview-label">так награду видят зрители</div>
          <div class="preview-card">
            <div class="preview-cost">${s.twitch.reward_cost}</div>
            <div class="preview-text">
              <div class="preview-title">${esc(s.twitch.reward_title)}</div>
              <div class="preview-prompt">Напиши артиста и название или ссылку.
                Если трек не найдётся — баллы вернутся.</div>
            </div>
          </div>
        </div>` : `
        <div class="void">
          <div class="rule"></div>
          <div class="mark">Очередь пуста</div>
          <div class="say">Заказы появятся здесь, когда на канале заработает награда за баллы.</div>
        </div>`);
      knownOrderIds = new Set();
      return;
    }

    // При первой отрисовке подсвечивать нечего: иначе после перезагрузки
    // страницы мигнёт весь список разом.
    const first = knownOrderIds === null;

    box.innerHTML = `<ul class="rows queue-rows">${list.map((r, i) => {
      const fresh = !first && !knownOrderIds.has(r.id) ? " fresh" : "";
      return `<li class="${fresh.trim()}" draggable="true" data-id="${r.id}">
        <span class="grip" title="перетащи, чтобы поменять порядок">${icon("grip-vertical")}</span>
        <span class="idx">${String(i + 1).padStart(2, "0")}</span>
        <span class="body">
          <span class="ttl">${esc(r.artist)} — ${esc(r.title)}</span>
          <span class="sub">
            ${r.source === "donation" ? `<span class="chip money">донат</span>` : ""}
            ${r.uncertain ? `<span class="tag doubt" title="совпадение неточное">неточно</span>` : ""}
            ${r.provider === "youtube" ? `<span class="tag miss">YouTube</span>` : ""}
            ${esc(r.requester)}
            ${r.raw_request ? `<span class="asked" title="${esc(r.raw_request)}">${esc(r.raw_request)}</span>` : ""}
          </span>
        </span>
        <span class="len">${mmss(r.duration_ms)}</span>
        <span class="deal">
          <button class="icon-btn" data-queue="${r.id}" data-act="top" title="Наверх">${icon("arrow-up")}</button>
          <button class="icon-btn" data-queue="${r.id}" data-act="remove" title="Удалить, баллы не возвращать">${icon("x")}</button>
          <button class="icon-btn kill" data-queue="${r.id}" data-act="refund" title="Удалить и вернуть баллы">${icon("trash-2")}</button>
        </span>
      </li>`;
    }).join("")}</ul>`;

    wireDrag(box);
    knownOrderIds = new Set(list.map((r) => r.id));
  }

  // Перетаскивание порядка. Родной drag&drop браузера: своя реализация на
  // мышиных событиях ломается на каждом обновлении списка.
  function wireDrag(box) {
    box.querySelectorAll("li[draggable]").forEach((li) => {
      li.ondragstart = (e) => {
        dragId = li.dataset.id;
        li.classList.add("dragging");
        e.dataTransfer.effectAllowed = "move";
      };
      li.ondragend = () => {
        li.classList.remove("dragging");
        box.querySelectorAll("li").forEach((x) => x.classList.remove("over"));
        dragId = null;
      };
      li.ondragover = (e) => {
        e.preventDefault();
        if (li.dataset.id !== dragId) li.classList.add("over");
      };
      li.ondragleave = () => li.classList.remove("over");
      li.ondrop = (e) => {
        e.preventDefault();
        li.classList.remove("over");
        if (!dragId || li.dataset.id === dragId) return;

        const ids = [...box.querySelectorAll("li")].map((x) => x.dataset.id);
        const from = ids.indexOf(dragId);
        ids.splice(from, 1);
        ids.splice(ids.indexOf(li.dataset.id), 0, dragId);

        fetch("/api/queue/reorder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(ids.map(Number)),
        }).catch(() => say("Не смог поменять порядок", true));
      };
    });
  }

  // Подтверждение прямо на кнопке: диалог браузера выглядит чужеродно и
  // выбивает из панели, а отменить очистку очереди уже нельзя.
  function confirmThen(btn, question, run) {
    if (btn.dataset.armed === "1") {
      btn.dataset.armed = "0";
      btn.textContent = btn.dataset.label;
      btn.classList.remove("armed");
      run();
      return;
    }
    btn.dataset.label = btn.textContent;
    btn.dataset.armed = "1";
    btn.textContent = question;
    btn.classList.add("armed");
    setTimeout(() => {
      if (btn.dataset.armed !== "1") return;
      btn.dataset.armed = "0";
      btn.textContent = btn.dataset.label;
      btn.classList.remove("armed");
    }, 4000);
  }

  $("queue-pause").onclick = (e) => {
    const on = !lastState || !lastState.paused;
    post(`/api/queue/pause?on=${on ? 1 : 0}`, e.currentTarget);
  };

  $("queue-clear").onclick = (e) => {
    confirmThen(e.currentTarget, "точно очистить?",
      () => post("/api/queue/clear?refund=1"));
  };

  $("ban-add").onclick = (e) => {
    const login = $("ban-login").value.trim().replace(/^@/, "");
    if (!login) return;
    confirmThen(e.currentTarget, `закрыть заказы для ${login}?`, async () => {
      if (await post(`/api/bans?login=${encodeURIComponent(login)}`)) {
        $("ban-login").value = "";
      }
    });
  };

  function renderBans(list) {
    const box = $("banlist");
    if (!list || !list.length) {
      box.innerHTML = `<div class="hint">Пока никому не закрыт.</div>`;
      return;
    }
    box.innerHTML = `<ul class="bans">${list.map((b) => `
      <li>
        <span class="who">${esc(b.login)}</span>
        ${b.reason ? `<span class="why">${esc(b.reason)}</span>` : ""}
        <button class="link-btn" data-unban="${esc(b.login)}">вернуть</button>
      </li>`).join("")}</ul>`;
  }

  // Итоги сессии. Числа настоящие, и в тихий вечер это единственное, что на
  // экране вообще меняется.
  function renderTally(s) {
    const ses = s.session;
    const minutes = Math.max(0, Math.round((Date.now() - new Date(ses.started_at)) / 60000));
    const uptime = minutes < 60
      ? `${minutes} ${plural(minutes, "минута", "минуты", "минут")}`
      : `${Math.floor(minutes / 60)} ч ${minutes % 60} мин`;

    const cell = (value, label) =>
      `<div class="cell"><div class="num">${value}</div><div class="lab">${label}</div></div>`;

    $("tally").innerHTML =
      cell(ses.orders, "заказов") +
      cell(ses.accepted, "принято") +
      cell(ses.refunded, "возвращено") +
      cell(uptime, "в работе");
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

  // Доступ к Twitch у публичных приложений живёт тридцать дней. Показываем
  // дату всегда, а не только перед концом: тогда это не сюрприз.
  function renewLine(tw) {
    if (!tw.renew_at) return "";
    const when = new Date(tw.renew_at).toLocaleDateString("ru-RU",
      { day: "numeric", month: "long" });
    return tw.renew_soon
      ? `<div class="note"><b>TW-04</b> Доступ кончается ${when} — нажми «Подключить Twitch» ещё раз.</div>`
      : `<div class="plan">доступ действует до ${when}</div>`;
  }

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
      ${renewLine(tw)}
      ${tw.note ? note(tw.note_code, tw.note) : ""}`;
  }

  // Запасной проигрыватель. Не готов — это не поломка: просто часть заказов
  // не сыграет, и об этом надо сказать заранее, а не в момент заказа.
  function renderYouTube(y) {
    const box = $("yt-status");
    box.className = "account " + (y.ready ? "ok" : "iffy");
    box.innerHTML = y.ready
      ? `<div class="who">Готов</div>
         <div class="plan">заказы, которых нет в Spotify, заиграют с YouTube</div>`
      : `<div class="who">Не готов</div>
         ${y.note ? note(y.note_code, y.note) : `<div class="plan">проверяю программы…</div>`}`;

    const select = $("yt-device");
    if (y.devices && y.devices.length && select.options.length <= 1) {
      select.innerHTML = `<option value="">системное устройство</option>` +
        y.devices.map((d) => `<option value="${esc(d)}">${esc(d)}</option>`).join("");
    }
    if (y.device && select.value !== y.device) select.value = y.device;
  }

  $("yt-browser").onchange = async (e) => {
    if (await saveConfig({ youtube_browser: e.target.value })) {
      say("Сохранено. Применится к следующему заказу с YouTube.");
    }
  };

  $("yt-device").onchange = async (e) => {
    if (await saveConfig({ audio_device: e.target.value })) {
      say("Устройство сохранено. Оно применится к следующему заказу с YouTube.");
    }
  };

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
      $("da-id").value = config.donationalerts_client_id || "";
      $("dp-key").value = config.donatepay_key || "";
      $("dx-key").value = config.donatex_key || "";
      $("donation-min").value = config.donation_min ?? "";
      $("yt-browser").value = config.youtube_browser || "";
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
    skip: (btn) => post("/api/queue/skip", btn),
    restore: (btn) => post("/api/spotify/restore", btn),
    "twitch-login": (btn) => saveThenConnect("twitch-id", "twitch_client_id",
      (b) => post("/api/twitch/login", b), btn),
    "da-login": (btn) => saveThenConnect("da-id", "donationalerts_client_id",
      (b) => post("/api/donations/login", b), btn).then((d) => {
        if (d && d.url) window.open(d.url, "_blank", "noopener");
      }),
    "da-logout": (btn) => post("/api/donations/logout", btn),
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

    const q = e.target.closest("[data-queue]");
    if (q) {
      const id = q.dataset.queue;
      const path = {
        top: `/api/queue/${id}/top`,
        remove: `/api/queue/${id}/remove`,
        refund: `/api/queue/${id}/remove?refund=1`,
      }[q.dataset.act];
      if (path) post(path, q);
      return;
    }

    const unban = e.target.closest("[data-unban]");
    if (unban) {
      post(`/api/bans?login=${encodeURIComponent(unban.dataset.unban)}&undo=1`, unban);
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
  $("save-da-id").onclick = async () => {
    if (await saveConfig({ donationalerts_client_id: $("da-id").value.trim() })) {
      say("Сохранено. Теперь нажми «Подключить DonationAlerts».");
    }
  };
  $("save-dp-key").onclick = async () => {
    if (await saveConfig({ donatepay_key: $("dp-key").value.trim() })) {
      say("Ключ DonatePay сохранён. Перезапусти приложение, чтобы он заработал.");
    }
  };
  $("save-dx-key").onclick = async () => {
    if (await saveConfig({ donatex_key: $("dx-key").value.trim() })) {
      say("Ключ DonateX сохранён. Перезапусти приложение, чтобы он заработал.");
    }
  };

  $("donation-min").onchange = async (e) => {
    const value = parseFloat(e.target.value.replace(",", "."));
    if (!isFinite(value) || value < 0) {
      say("Сумма должна быть числом", true);
      return;
    }
    await saveConfig({ donation_min: value });
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

  function render(s) {
    lastState = s;
    document.body.classList.remove("stale");

    const sig = JSON.stringify;
    draw("bar", sig(s.connections) + s.version, () => renderBar(s));
    draw("stage", sig([s.now, s.spotify, s.connections]), () => renderStage(s));
    draw("orders", sig([s.queue, s.paused, s.twitch.reward_title, s.twitch.reward_cost]), () => renderOrders(s));
    draw("bans", sig(s.bans), () => renderBans(s.bans));
    draw("yt", sig(s.youtube), () => renderYouTube(s.youtube));
    draw("feed", sig(s.notices), () => renderFeed(s));
    draw("tally", sig(s.session), () => renderTally(s));
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
      : s.queue.length > 0
        ? `${s.queue.length} в очереди · Заказ музыки`
        : "Заказ музыки";
  }

  fetch("/static/icons/sprite.svg")
    .then((r) => r.text())
    .then((svg) => { $("sprite").innerHTML = svg; });

  loadConfig();
  connect();
})();
