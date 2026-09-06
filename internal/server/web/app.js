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

  // safeURL пропускает только http и https. Адрес приходит из ответа
  // Twitch, а клик по нему делает сам стример — в панели, где лежат все
  // его ключи и все кнопки управления. Экранирование закрывает выход из
  // атрибута, но не запрещает схему javascript:.
  const safeURL = (u) => (/^https?:\/\//i.test(String(u || "")) ? String(u) : "#");

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
    // Метка «это панель»: пока она открыта, приложение спрашивает Spotify
    // раз в секунду, чтобы перемотка в самом Spotify сразу двигала полосу.
    // Виджет в OBS такой метки не ставит — он висит весь стрим.
    const socket = new WebSocket(`ws://${location.host}/ws?panel=1`);

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

  // body — необязательный: почти все ручки панели ничего не принимают, а
  // обходу блокировок надо передать ключ, и в адрес его класть нельзя —
  // адреса попадают в лог целиком.
  async function post(path, btn, body) {
    if (btn) btn.disabled = true;
    try {
      const r = await fetch(path, body === undefined
        ? { method: "POST" }
        : { method: "POST", headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body) });
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
      const paused = pauseLeft(sp);
      if (paused) {
        const at = paused.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
        return { state: "fail", value: "Spotify просит паузу",
                 hint: `Ограничил приложение до ${at}: ${pausedParts[sp.paused_part] || "часть возможностей не работает"}`,
                 button: `<button class="act small" data-do="check">Проверить связь</button>` };
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
        value: `«${tw.reward_title}»`, // шаблон списка экранирует сам
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
        value: sp.snapshot_text, // экранируется в шаблоне списка
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

  // nowKey — что именно считать сменой трека. Всё, кроме положения внутри
  // него: полоса идёт сама и в перерисовке не нуждается.
  function nowKey(now) {
    if (!now) return null;
    return [now.title, now.artist, now.requester, now.source,
            now.provider, now.duration_ms, now.uncertain];
  }

  // Последний игравший трек.
  //
  // Показывается только в тишине: пока что-то играет, на экране есть вещь
  // поважнее, а два трека рядом — это вопрос «так который из них играет».
  // В тишине же это единственный ответ на «что это только что было»:
  // раньше за ним приходилось лезть в хронику, где он строчкой без обложки
  // и вперемешку с отказами.
  function lastPlayed(s) {
    const l = s.last_played;
    if (!l || !l.title) return "";

    const marks = [];
    if (l.requester) marks.push("заказал " + l.requester);
    else if (l.source === "own") marks.push("твоя музыка");
    if (l.provider === "youtube") marks.push("YouTube");
    if (l.at) marks.push(when(l.at));

    return `
      <div class="last">
        <div class="last-art">${l.cover_url ? `<img src="${esc(l.cover_url)}" alt="">` : icon("music")}</div>
        <div class="last-text">
          <div class="last-kicker">Последним играло</div>
          <div class="last-title">${esc(l.title)}</div>
          <div class="last-sub">${esc(l.artist)}</div>
        </div>
        ${marks.length ? `<div class="last-marks">${marks.map(esc).join(" · ")}</div>` : ""}
      </div>`;
  }

  function renderStage(s) {
    const list = steps(s);
    const now = s.now;

    // Играет заказ — всё остальное отходит на второй план.
    //
    // Раньше карточка пряталась целиком, если хоть один шаг настройки
    // сломан: вместо неё показывался список шагов. Вместе с карточкой
    // исчезала кнопка «Скипнуть» — ровно тогда, когда она нужнее всего.
    // Так 27.08 стример и не смог оборвать заказ, оглушивший эфир: скип в
    // плеере работал, нажать его было нечем. Теперь карточка остаётся
    // всегда, а поломка живёт полосой под кнопками — вместе со своей
    // кнопкой починки, чтобы попасть к настройке было откуда.
    if (now) {
      const own = now.source === "own";
      const broken = list.filter((x) => x.state === "fail");
      const yt = now.provider === "youtube";
      swap($("stage"), `
        <div class="eyebrow">
          <i class="live"></i> В эфире
          <span class="sep">/</span>
          <span class="where">${now.provider === "youtube" ? "YouTube" : "Spotify"}${
            own ? " · твоя музыка" : ""}${
            now.uncertain ? " · неточное совпадение" : ""}</span>
        </div>
        <div class="stage-row">
          <div class="art">${now.cover_url ? `<img src="${esc(now.cover_url)}" alt="">` : icon("music")}</div>
          <div class="stage-text">
            <h1 class="headline">${esc(now.title)}</h1>
            <div class="subhead">${esc(now.artist)}</div>
            ${now.requester ? `<div class="credit">${icon("user")}заказал <b>${esc(now.requester)}</b></div>` : ""}
            ${now.duration_ms > 0 ? `
            <div class="meter">
              <span class="t" id="at">${mmss(now.position_ms)}</span>
              <span class="bar"><i id="at-bar"></i></span>
              <span class="t">${mmss(now.duration_ms)}</span>
            </div>` : ""}
          </div>
        </div>
        <div class="acts">${own ? `
          <span class="hint" style="margin:0">Это твоя музыка, не заказ — скипать
            приложению тут нечего.</span>` : `
          <button class="act key" data-do="skip">${icon("skip-forward")}Скипнуть</button>
          <button class="act" data-do="restore">${icon("rotate-cw")}Вернуть Spotify</button>`}
        </div>
        ${yt ? `
        <label class="stage-vol">
          ${icon("volume-2")}
          <span class="t">Громкость <b id="vol-val">${config.youtube_volume ?? 100}%</b></span>
          <input type="range" id="vol" min="1" max="100" step="1"
                 value="${config.youtube_volume ?? 100}">
          <span class="hint">От громкости Spotify. Действует сразу, на этот же трек.</span>
        </label>` : ""}
        ${broken.length ? `
        <div class="stage-alarm">
          ${icon("triangle-alert")}
          <div>
            <b>${broken.map((x) => esc(x.name)).join(", ")}: ${esc(broken[0].value)}</b>
            <div class="hint">${esc(broken[0].hint)}</div>
          </div>
          ${broken[0].button || ""}
        </div>` : ""}`);
      if (yt) bindVolume();
      runMeter(now);
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
      sub = "Пройди шаги ниже — это делается один раз. " +
        "Или нажми «Провести по шагам»: мастер сделает то же самое, но с картинками и по одному шагу за раз.";
    }

    swap($("stage"), `
      ${brow}
      <div class="head">
        <h1 class="big">${mark}</h1>
        <p class="lead">${sub}</p>
      </div>
      ${lastPlayed(s)}
      ${ready || broken ? "" : `<div class="step-acts" style="margin:0 0 20px">
        <button class="act small key" id="stage-setup">Провести по шагам</button>
      </div>`}
      <div class="rail"><i style="width:${(done / list.length) * 100}%"></i></div>
      <ol class="steps">
        ${list.map((step, i) => `
          <li class="step ${step.state}">
            <div class="no">${String(i + 1).padStart(2, "0")}</div>
            <div class="step-body">
              <div class="step-name">${esc(step.name)}</div>
              <div class="step-value">${esc(step.value)}</div>
              <div class="step-hint">${esc(step.hint)}</div>
              ${step.button ? `<div class="step-acts">${step.button}</div>` : ""}
            </div>
          </li>`).join("")}
      </ol>`);

    // Кнопка живёт ровно столько, сколько карточка, поэтому вешается заново
    // после каждой отрисовки — как ползунок громкости рядом.
    const go = $("stage-setup");
    if (go) go.onclick = () => window.srSetupOpen && window.srSetupOpen();
  }

  // Ползунок громкости на карточке. Живёт ровно столько, сколько карточка,
  // поэтому вешается заново после каждой отрисовки.
  //
  // Пишем в те же настройки, что и ползунок в разделе «Если трека нет в
  // Spotify»: одно значение, два места, где до него можно дотянуться. Правка
  // уезжает в уже играющий mpv, а не только к следующему заказу.
  function bindVolume() {
    const el = $("vol");
    if (!el) return;
    el.oninput = () => { $("vol-val").textContent = el.value + "%"; };
    el.onchange = async () => {
      const v = parseInt(el.value, 10);
      if (await saveConfig({ youtube_volume: v })) {
        const slider = $("yt-volume");
        if (slider) { slider.value = v; $("yt-volume-val").textContent = v + "%"; }
      }
    };
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
      // Заказ, который ждёт конца трека стримера, уже вынут из очереди и
      // живёт у плеера: двигать и удалять его нечего, перетаскивать тоже.
      // Но показать обязаны — иначе его не видно нигде: из очереди пропал,
      // а «В эфире» ещё пусто. Ровно так 27.08 заказ и «потерялся».
      if (r.waiting) {
        return `<li class="waiting-row">
          <span class="grip">${icon("clock")}</span>
          <span class="idx">${String(i + 1).padStart(2, "0")}</span>
          <span class="body">
            <span class="ttl">${esc(r.artist)} — ${esc(r.title)}</span>
            <span class="sub">
              ${r.provider === "youtube" ? `<span class="tag miss">YouTube</span>` : ""}
              ${esc(r.requester)}
              <span class="tag">заиграет сразу после трека стримера</span>
            </span>
          </span>
          <span class="len">${mmss(r.duration_ms)}</span>
          <span class="deal"></span>
        </li>`;
      }
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
          <button class="icon-btn" data-queue="${r.id}" data-act="fix" title="Не тот трек — выбрать правильный">${icon("search")}</button>
          <button class="icon-btn" data-queue="${r.id}" data-act="top" title="Наверх">${icon("arrow-up")}</button>
          <button class="icon-btn" data-queue="${r.id}" data-act="remove" title="Удалить, баллы не возвращать">${icon("x")}</button>
          <button class="icon-btn kill" data-queue="${r.id}" data-act="refund" title="Удалить и вернуть баллы">${icon("trash-2")}</button>
        </span>
      </li>`;
    }).join("")}</ul>`;

    wireDrag(box);
    knownOrderIds = new Set(list.map((r) => r.id));
  }

  // Полоса времени идёт сама. Состояние прилетает только при смене трека,
  // поэтому если считать положение один раз при отрисовке, полоса застывает
  // на первой же секунде — так и было.
  let meterTimer = null;
  let meterFrom = 0;        // Date.now() в момент начала трека
  let meterLength = 0;
  let meterTick = null;     // перерисовка полосы, зовётся и вне таймера

  // Полоса идёт по своему таймеру: приложение шлёт состояние редко, а время
  // должно бежать каждую секунду.
  function runMeter(now) {
    clearInterval(meterTimer);
    meterLength = now.duration_ms || 0;
    meterFrom = Date.now() - (now.position_ms || 0);
    if (!meterLength) return;

    meterTick = () => {
      const bar = $("at-bar");
      if (!bar) {           // карточку перерисовали — этот таймер уже лишний
        clearInterval(meterTimer);
        return;
      }
      const at = Math.min(meterLength, Math.max(0, Date.now() - meterFrom));
      bar.style.width = (at / meterLength) * 100 + "%";
      $("at").textContent = mmss(at);
    };
    meterTick();
    meterTimer = setInterval(meterTick, 1000);
  }

  // Сверка со Spotify. Свой таймер знает только, когда трек начался, — а его
  // можно перемотать, и тогда весь дальнейший отсчёт врёт. Поэтому на каждом
  // состоянии переставляем точку отсчёта на настоящую.
  //
  // Карточку при этом не перерисовываем: положение внутри трека намеренно
  // выброшено из подписи, иначе она мигала бы каждые несколько секунд.
  function syncMeter(now) {
    if (!now || !now.duration_ms) return;
    if (!meterTimer || meterLength !== now.duration_ms) return;
    meterFrom = Date.now() - (now.position_ms || 0);
    // И сразу перерисовываем. Без этого перемотка доезжала до экрана только
    // к следующему тику своего таймера — то есть на секунду позже, чем мы уже
    // знали правду. На перемотке это заметно: полоса дёргалась с задержкой.
    if (meterTick) meterTick();
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

        // Отказ сервера ловим наравне с обрывом связи: иначе панель
        // показывает новый порядок, следующее состояние молча возвращает
        // старый, и выглядит это как «перетаскивание отскакивает само».
        fetch("/api/queue/reorder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(ids.map(Number)),
        })
          .then((r) => { if (!r.ok) say("Не смог поменять порядок", true); })
          .catch(() => say("Не смог поменять порядок", true));
      };
    });
  }

  // Подтверждение прямо на кнопке: диалог браузера выглядит чужеродно и
  // выбивает из панели, а отменить очистку очереди уже нельзя.
  // Второе нажатие подтверждает ровно тот вопрос, который на кнопке
  // написан. Раньше сверялся только факт «кнопка взведена»: стример
  // набирал ник, взводил кнопку, передумывал, стирал, набирал другой ник —
  // и второе нажатие мгновенно банило уже второго, без подтверждения,
  // хотя человек был уверен, что подтверждает первого.
  function confirmThen(btn, question, run) {
    if (btn.dataset.armed === "1" && btn.dataset.armedFor === question) {
      btn.dataset.armed = "0";
      btn.dataset.armedFor = "";
      btn.textContent = btn.dataset.label;
      btn.classList.remove("armed");
      run();
      return;
    }
    if (btn.dataset.armed !== "1") btn.dataset.label = btn.textContent;
    btn.dataset.armed = "1";
    btn.dataset.armedFor = question;
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
    const btn = e.currentTarget;
    // Подтверждение должно называть последствие: баллы вернутся всем, и это
    // не то же самое, что «удалить из очереди» построчно.
    confirmThen(btn, "очистить и вернуть баллы?",
      () => post("/api/queue/clear?refund=1", btn));
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
      <div class="what">Открой <a href="${esc(safeURL(tw.pending_url))}" target="_blank"
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

  // Пауза, которую объявил сам Spotify. Пока она идёт, не работает ничего:
  // ни заказы, ни «Запомнить». Живьём это выглядело как поломка всего
  // приложения сразу после включения обхода блокировок, и чинили не то.
  //
  // Время считаем в панели: сервер отдаёт конец паузы, а не остаток —
  // остаток протухает между обновлениями состояния.
  function pauseLeft(sp) {
    if (!sp.paused_until) return null;
    const until = new Date(sp.paused_until);
    if (isNaN(until.getTime()) || until <= new Date()) return null;
    return until;
  }

  // Что именно закрыто — важно: Spotify считает части по отдельности, и
  // закрытый плеер при работающем поиске совершенно обычное дело.
  const pausedParts = {
    "плеер": "заказы пока не сыграют, а «Запомнить» не сработает",
    "поиск": "новые заказы пока не найдутся",
    "аккаунт": "аккаунт пока не проверить",
    "плейлисты": "список плейлистов пока не обновить",
  };

  function pauseNote(sp) {
    const until = pauseLeft(sp);
    if (!until) return "";
    const at = until.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
    const what = pausedParts[sp.paused_part] || "часть возможностей пока не работает";
    return note("SP-08", `Spotify ограничил приложение до ${at}: ${what}. ` +
      `Это не поломка и не обход блокировок — Spotify снимет ограничение сам. ` +
      `Кнопка «Проверить связь» пробует ещё раз.`);
  }

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
        <div class="plan">но связи со Spotify не было — аккаунт неизвестен</div>
        ${pauseNote(sp)}`;
      return;
    }
    // Страна аккаунта важнее подписки: в неработающей стране Premium есть,
    // а поиск пустой — и без этой строки причину не найти.
    if (sp.country_note) box.className = "account bad";
    else box.className = "account " + (sp.plan === "free" ? "bad" : sp.plan === "unknown" ? "iffy" : "ok");
    box.innerHTML = `
      <div class="who">${esc(sp.account || "аккаунт без имени")}</div>
      ${sp.email ? `<div class="mail">${esc(sp.email)}</div>` : ""}
      ${sp.proxy ? `<div class="mail">через ${esc(sp.proxy)}</div>` : ""}
      <div class="plan">${esc(sp.plan_label)}${
        sp.country ? ` · страна аккаунта ${esc(sp.country)}` : ""}</div>
      ${pauseNote(sp)}
      ${sp.country_note ? note("SP-16", sp.country_note) : ""}
      ${sp.plan_note ? note(sp.plan_note_code, sp.plan_note) : ""}`;
  }

  // Карточка обхода блокировок. Ключа здесь нет и не будет: сервер его не
  // отдаёт, а показывать «vless://11111111-…» на стриме нельзя тем более.
  function renderTunnel(t) {
    const box = $("tunnel-state");

    // Поле после включения пустое: ключ уже лежит в хранилище паролей Windows,
    // и держать его на экране незачем. Но пустое поле читается как «ключ
    // слетел» — владелец так и решил. Поэтому подсказка внутри поля говорит,
    // что именно сохранено.
    const field = $("tunnel-key");
    if (field && !field.value) {
      field.placeholder = t.has_key
        ? `ключ сохранён (${t.hint}) — вставлять заново не надо`
        : "vless://… , trojan://… , ss://… или https://…";
    }

    if (!t.has_key) {
      box.className = "account";
      box.innerHTML = `<div class="who muted">Ключ не вставлен</div>
        <div class="plan">Spotify идёт напрямую или через прокси ниже</div>
        ${t.note ? `<div class="plan">${esc(t.note)}</div>` : ""}`;
      return;
    }

    // Состояние приходит одним словом (phase) и больше не вычисляется здесь по
    // трём признакам сразу. Раньше «поднять не вышло» и «поднимать не
    // понадобилось» выглядели одинаково — ключ есть, обход не работает, — и в
    // карточке писалось «Обход наготове» ровно тогда, когда всё лежало.
    const look = {
      connected:    ["Обход работает", "ok"],
      connecting:   ["Подключаюсь…", "iffy"],
      reconnecting: ["Переподключаюсь…", "iffy"],
      standby:      ["Обход наготове", "ok"],
      down:         ["Обход недоступен", "bad"],
      off:          ["Обход выключен", "iffy"],
    };
    const [word, mood] = look[t.phase] || look.off;

    box.className = "account " + mood;
    box.innerHTML = `
      <div class="who">${word}</div>
      <div class="mail">ключ сохранён · ${esc(t.hint)}</div>
      ${t.on && t.server ? `<div class="plan">через ${esc(t.server)}</div>` : ""}
      ${t.from_cache ? `<div class="plan">список серверов взят из памяти — сервис подписки не ответил</div>` : ""}
      ${t.note ? `<div class="plan">${esc(t.note)}</div>` : ""}`;
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
    // Что выбрано — знает конфиг. Поле y.device сервер заполняет один раз при
    // старте и только если yt-dlp с mpv на месте: без этого стример выбирал
    // виртуальный кабель, обновлял вкладку и снова видел «системное».
    const want = (config && config.audio_device) || y.device || "";
    if (want && select.value !== want) select.value = want;
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

  $("yt-volume").oninput = () => {
    $("yt-volume-val").textContent = $("yt-volume").value + "%";
  };
  $("yt-volume").onchange = async (e) => {
    const v = parseInt(e.target.value, 10);
    if (await saveConfig({ youtube_volume: v })) {
      // Тот же ползунок есть на карточке играющего трека: если он сейчас на
      // экране, он обязан показать то же число.
      const live = $("vol");
      if (live) { live.value = v; $("vol-val").textContent = v + "%"; }
      markSaved(e.target);
    }
  };

  // ── настройки ──────────────────────────────────────────────────────

  // Стоп-слова хранятся списком, а редактируются текстом по строке на слово.
  const NL = "\n";

  let config = null;
  let playlistsLoaded = false;
  // playlistsFailed — список не собрался из-за отказа Spotify. Отличать это
  // от «уже собран» нужно, чтобы «Проверить связь» дала второй шанс.
  let playlistsFailed = false;
  let redirectShown = false;

  // ── Меню разделов ─────────────────────────────────────────
  //
  // Как было. Каждая из трёх кнопок в верхней полосе снимала hidden со своего
  // куска страницы и прокручивала экран вниз, к нему. Человек терял место, на
  // которое смотрел, кнопки «назад» не было вовсе — только прокрутка обратно,
  // а настройки лежали одной сеткой из девяти блоков подряд.
  //
  // Как стало. Разделы живут в меню поверх страницы: слева список разделов,
  // справа один раздел за раз. Признак «раздел открыт» остался прежним —
  // hidden на самой секции, — поэтому весь остальной код (проверки вида
  // «панель виджета на экране, значит перерисуй кадр») работает как работал и
  // про меню ничего не знает.

  const MENU_SECTIONS = ["settings", "widget", "log"];

  // Меню гаснет плавно, и до конца исчезновения прятать его нельзя — иначе
  // вместо ухода будет рывок. Отсюда таймер и обязательная его отмена, если
  // меню успели открыть заново.
  let menuHideTimer = null;

  function menuShown() {
    return !$("menu").hidden;
  }

  // markSection зажигает один раздел и гасит остальные — и в самом меню, и в
  // верхней полосе. null означает «все погашены»: так меню закрывается.
  function markSection(name) {
    MENU_SECTIONS.forEach((id) => {
      const on = id === name;
      $(id).hidden = !on;
      const btn = $(id + "-toggle");
      btn.classList.toggle("active", on);
      btn.setAttribute("aria-expanded", String(on));
      const item = document.querySelector('#menu-nav [data-menu="' + id + '"]');
      if (item) item.classList.toggle("on", on);
    });
  }

  // Содержимое раздела собирается в тот момент, когда его открыли. Галерея из
  // сорока пяти наборов, история из базы и список плейлистов Spotify нужны
  // далеко не каждому заходу, а строятся и запрашиваются небесплатно.
  function fillSection(name) {
    if (name === "settings") fillTab(settingsTab);
    if (name === "log") { loadHistory(); loadModLog(); }
    if (name === "widget") { buildGallery(); drawWidget(); }
  }

  function openSection(name) {
    clearTimeout(menuHideTimer);
    menuHideTimer = null;

    const menu = $("menu");
    if (menu.hidden) {
      menu.hidden = false;
      // Страница позади перестаёт прокручиваться: две полосы прокрутки
      // рядом — своя у меню и чужая у страницы — сбивают с толку, а колесом
      // мыши над краем уезжало то, что под меню.
      document.body.classList.add("menu-open");
      // Класс вешаем следующим кадром: смену прямо в момент появления
      // элемента браузер переходом не считает, и меню выскочило бы рывком.
      requestAnimationFrame(() => menu.classList.add("on"));
    }
    markSection(name);
    $("menu-body").scrollTop = 0;
    fillSection(name);
  }

  function closeMenu() {
    const menu = $("menu");
    if (menu.hidden) return;
    menu.classList.remove("on");
    // Разделы прячем не сразу, а вместе с самим меню: иначе на время ухода в
    // нём осталась бы пустая рамка.
    menuHideTimer = setTimeout(() => {
      menu.hidden = true;
      document.body.classList.remove("menu-open");
      markSection(null);
      menuHideTimer = null;
    }, 340);
  }

  // ── Вкладки внутри настроек ─────────────────────────────────

  const SETTINGS_TABS = ["conn", "orders", "fallback", "misc"];
  let settingsTab = "conn";

  function fillTab(tab) {
    // Единственное здесь, что стоит запроса к Spotify, — список плейлистов.
    // Раньше он запрашивался при любом заходе в настройки, даже если человек
    // пришёл за громкостью YouTube. Норма запросов у Spotify тесная, и такие
    // «на всякий случай» из неё и складываются.
    if (tab === "fallback") loadPlaylists();
  }

  function openTab(tab) {
    if (!SETTINGS_TABS.includes(tab)) return;
    settingsTab = tab;
    SETTINGS_TABS.forEach((t) => { $("tab-" + t).hidden = t !== tab; });
    document.querySelectorAll("#settings-tabs .tab").forEach((b) => {
      b.classList.toggle("on", b.dataset.tab === tab);
    });
    $("menu-body").scrollTop = 0;
    fillTab(tab);
  }

  // Старые имена оставлены нарочно: их зовут из подсказок, с главного экрана и
  // кнопками верхней полосы. Смысл прежний — «показать этот раздел», меняется
  // только способ показа. Второй довод у настроек — вкладка, на которую надо
  // попасть: подсказка «впиши Client ID» обязана открыть именно ту.
  function toggleSettings(open, tab) {
    if (open === false) { closeMenu(); return; }
    if (open === undefined && menuShown() && !$("settings").hidden) { closeMenu(); return; }
    if (tab) openTab(tab);
    openSection("settings");
  }

  function toggleLog(open) {
    if (open === false) { closeMenu(); return; }
    if (open === undefined && menuShown() && !$("log").hidden) { closeMenu(); return; }
    openSection("log");
  }

  function toggleWidget(open) {
    if (open === false) { closeMenu(); return; }
    if (open === undefined && menuShown() && !$("widget").hidden) { closeMenu(); return; }
    openSection("widget");
  }

  async function loadConfig() {
    try {
      config = await (await fetch("/api/config")).json();
      $("client-id").value = config.spotify_client_id || "";
      $("proxy").value = config.spotify_proxy || "";
      $("twitch-id").value = config.twitch_client_id || "";
      $("da-id").value = config.donationalerts_client_id || "";
      $("dp-key").value = config.donatepay_key || "";
      $("dx-key").value = config.donatex_key || "";
      $("donation-min").value = config.donation_min ?? "";
      $("yt-browser").value = config.youtube_browser || "";
      $("yt-device").value = config.audio_device || "";
    $("yt-volume").value = config.youtube_volume ?? 100;
    $("yt-volume-val").textContent = ($("yt-volume").value) + "%";

      $("reward-title").value = config.reward_title || "";
      $("reward-cost").value = config.reward_cost ?? "";
      $("auto-reward").checked = !!config.auto_create_reward;
      // Внутри секунды, но человеку понятнее в минутах.
      $("max-minutes").value = Math.round((config.max_track_seconds || 0) / 60) || "";
      $("max-per-user").value = config.max_per_user ?? "";
      $("donation-priority").checked = !!config.donation_priority;
      $("wait-current").checked = !!config.wait_for_current;
      $("reject-words").value = (config.reject_keywords || []).join(NL);

      applyMode(config.resume_fail_mode);
      refreshModeHints();
      if (!$("widget").hidden) drawWidget();
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

  // Поле, которое сохраняется само, обязано об этом сказать. Иначе человек
  // правит цену награды, ничего не видит и решает, что настройка не работает.
  function markSaved(el) {
    if (!el) return;
    const field = el.closest(".field") || el.closest(".check") || el;
    field.classList.remove("saved");
    void field.offsetWidth;   // без сброса метка не мигнёт второй раз
    field.classList.add("saved");
    clearTimeout(field._savedTimer);
    field._savedTimer = setTimeout(() => field.classList.remove("saved"), 1800);
  }

  // query нужен одному-единственному полю — адресу прокси. Пустое поле в
  // обычном сохранении означает «я про него ничего не знаю» (вкладка могла
  // быть открыта до того, как приложение нашло прокси само), и сервер такое
  // пустое значение не применяет. Чтобы прокси действительно убрать, панель
  // обязана сказать об этом прямо.
  async function saveConfig(patch, query = "") {
    try {
      const current = await (await fetch("/api/config")).json();
      Object.assign(current, patch);
      const r = await fetch("/api/config" + query, {
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

  // showProbe рисует итог проверки поиска.
  //
  // Список нужен, чтобы человек своими глазами увидел: «этот способ прошёл, а
  // этот — нет». Разбираться всё равно буду я по логу, поэтому подробности
  // ответа тут обрезаны до кода и числа находок.
  function showProbe(d) {
    const box = $("probe-out");
    if (!box) return;
    if (!d || !d.probes) {
      box.innerHTML = "";
      return;
    }
    const rows = d.probes.map((p) => {
      const ok = p.http >= 200 && p.http < 300;
      const what = p.error ? esc(p.error) : `${p.http || "нет ответа"}`;
      return `<div>${ok ? "✅" : "❌"} ${esc(p.way)} — <b>${what}</b>` +
        `${ok ? `, нашлось ${p.found}` : ""}</div>`;
    }).join("");
    box.innerHTML = `<b>Проверка поиска: прошло ${d.ok} из ${d.total}.</b>` +
      `${rows}<br>Теперь нажми «Сохранить лог и историю» и пришли архив.`;
  }

  async function loadPlaylists() {
    if (playlistsLoaded) return;
    playlistsFailed = false;
    if (!lastState || !lastState.spotify.connected) return;
    try {
      const r = await fetch("/api/spotify/playlists");
      const list = await r.json();
      if (!r.ok) {
        $("playlist-hint").className = "hint warn";
        $("playlist-hint").textContent = `${list.code} · ${list.error}`;
        // Не повторяем без спроса: раньше при отвалившемся Spotify панель
        // молотила этот запрос на каждом состоянии, то есть каждые несколько
        // секунд весь стрим. Но и «навсегда» не годится: стример чинит связь,
        // жмёт «Проверить связь» — и список должен собраться заново, иначе
        // запасной плейлист выбрать нечем до перезагрузки страницы.
        playlistsFailed = true;
        playlistsLoaded = true;
        return;
      }
      const chosen = config ? config.fallback_playlist_id : "";
      $("playlist").innerHTML = '<option value="">— не выбран —</option>' +
        list.map((p) => `<option value="${esc(p.id)}" data-name="${esc(p.name)}"${
          p.id === chosen ? " selected" : ""}>${esc(p.name)}${
          // Ноль здесь чаще значит «Spotify не сказал», чем «пусто»: писать
          // «0 треков» у плейлиста, который стример сам же и слушает, — врать.
          p.tracks > 0
            ? ` · ${p.tracks} ${plural(p.tracks, "трек", "трека", "треков")}`
            : ""}</option>`).join("");
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

  // tunnelStart отдаёт ключ серверу и ждёт. Ждать приходится долго: в первый
  // раз внутри скачивается программа обхода, а потом перебираются серверы
  // подписки — до нескольких минут. Поле после успеха очищаем: ключ уже
  // сохранён в хранилище Windows, и держать его на экране незачем.
  async function tunnelStart(btn) {
    const key = $("tunnel-key").value.trim();
    const d = await post("/api/tunnel/on", btn, key ? { key } : {});
    if (d) {
      $("tunnel-key").value = "";
      // standby — приложение проверило и решило, что обход сейчас не нужен:
      // Spotify отвечает и без него. Ключ при этом сохранён.
      say(d.standby
        ? "Spotify отвечает и без обхода — держу обход наготове"
        : `Обход работает: ${d.server}`);
    }
    return d;
  }

  const actions = {
    login: (btn) => saveThenConnect("client-id", "spotify_client_id",
      (b) => post("/api/spotify/login", b), btn).then((d) => {
      if (!d) return;
      // Показываем адрес возврата: если вход не пройдёт, первым делом сверяют
      // именно эту строку с тем, что вписано в настройках Spotify.
      $("redirect").textContent = `Адрес возврата: ${d.redirect_uri}`;
      // in_app означает, что приложение уже открыло вход в своём окне — так
      // бывает, когда работает обход. Открыть ту же страницу ещё и в браузере
      // значит показать человеку два входа сразу, причём браузерный из России
      // не загрузится вовсе, и виноватым окажется приложение.
      if (!d.in_app) window.open(d.url, "_blank", "noopener");
      redirectShown = true;
      return d;
    }),
    logout: (btn) => post("/api/spotify/logout", btn),
    // «Включить обход» и «Проверить обход» — одна и та же ручка: проверка и
    // есть попытка поднять всё заново. Отдельная «проверка», которая ничего
    // не чинит, человеку бесполезна.
    "tunnel-on": (btn) => tunnelStart(btn),
    "tunnel-check": (btn) => tunnelStart(btn),
    "tunnel-off": (btn) => post("/api/tunnel/off", btn),
    "tunnel-forget": (btn) => new Promise((done) => {
      confirmThen(btn, "забыть ключ?", async () => {
        $("tunnel-key").value = "";
        done(await post("/api/tunnel/forget", btn));
      });
    }),
    "proxy-detect": (btn) => post("/api/spotify/proxy/detect", btn).then((d) => {
      // Сервер уже сохранил найденный адрес и включил его; поле просто
      // догоняет. Раньше поле заполнялось, а «Проверить связь» рядом
      // проверяла всё ещё старый прокси.
      if (d) {
        $("proxy").value = d.address || "";
        markSaved($("proxy"));
      }
      return d;
    }),
    check: (btn) => post("/api/spotify/check", btn).then((d) => {
      // Связь починили — список плейлистов имеет смысл собрать заново.
      if (playlistsFailed) {
        playlistsLoaded = false;
        loadPlaylists();
      }
      return d;
    }),
    // Пробник поиска: жмут его по моей просьбе, когда поиск отказывает без
    // видимой причины. Главное здесь — не картинка в панели, а строки «проба
    // поиска» в логе; на экране просто видно, что проверка дошла до конца.
    probe: (btn) => post("/api/spotify/probe", btn).then((d) => {
      showProbe(d);
      return d;
    }),
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
      toggleSettings(true, "conn");
      $("client-id").focus();
      $("client-id").scrollIntoView({ block: "center", behavior: "smooth" });
      return;
    }

    const q = e.target.closest("[data-queue]");
    if (q) {
      const id = q.dataset.queue;
      if (q.dataset.act === "fix") {
        openFix(id);
        return;
      }
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

  // ── «не тот трек» ──────────────────────────────────────────────────
  //
  // Подбор по названию иногда промахивается: кавер вместо оригинала, ремикс,
  // однофамилец. Выбор стримера запоминается навсегда, поэтому тот же заказ
  // впредь найдётся сразу и правильно — у любого зрителя.

  let fixOrder = null;
  let fixSeq = 0;

  function openFix(id) {
    const item = (lastState ? lastState.queue : []).find((r) => String(r.id) === String(id));
    if (!item) return;

    fixOrder = item;
    $("fix-asked").textContent = item.raw_request || "—";
    $("fix-was").textContent = `${item.artist} — ${item.title}`;
    $("fix-results").innerHTML = "";
    $("fix-note").textContent = "";
    $("fix").hidden = false;

    const input = $("fix-query");
    // Начинаем с того, что написал зритель: чаще всего достаточно уточнить
    // пару слов, а не набирать заново.
    input.value = item.raw_request || `${item.artist} ${item.title}`;
    input.focus();
    input.select();
    runFix();
  }

  function closeFix() {
    $("fix").hidden = true;
    fixOrder = null;
  }

  async function runFix() {
    const query = $("fix-query").value.trim();
    if (!query) return;

    const mine = ++fixSeq;
    $("fix-note").textContent = "Ищу…";

    try {
      const r = await fetch(`/api/search?q=${encodeURIComponent(query)}`);
      const data = await r.json();
      // Пока искали, стример успел набрать другое — старый ответ выкидываем.
      if (mine !== fixSeq) return;

      if (!r.ok) {
        $("fix-note").textContent = `${data.code} · ${data.error}`;
        return;
      }
      if (!data.length) {
        $("fix-note").textContent = "Ничего не нашлось. Попробуй иначе или вставь ссылку Spotify.";
        $("fix-results").innerHTML = "";
        return;
      }

      $("fix-note").textContent = "";
      $("fix-results").innerHTML = data.map((t) => `
        <li>
          <button class="pick" data-pick="${esc(t.id)}">
            <span class="pick-art">${t.cover_url
              ? `<img src="${esc(t.cover_url)}" alt="" loading="lazy">` : ""}</span>
            <span class="pick-text">
              <span class="pick-title">${esc(t.title)}</span>
              <span class="pick-sub">${esc(t.artist)}${t.album ? " · " + esc(t.album) : ""}</span>
            </span>
            <span class="pick-len">${mmss(t.duration_ms)}</span>
          </button>
        </li>`).join("");
    } catch {
      if (mine === fixSeq) $("fix-note").textContent = "Поиск не ответил";
    }
  }

  async function applyFix(trackID, btn) {
    if (!fixOrder) return;
    btn.disabled = true;
    try {
      const r = await fetch(`/api/queue/${fixOrder.id}/fix`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ track_id: trackID }),
      });
      if (!r.ok) {
        const data = await r.json().catch(() => ({}));
        $("fix-note").textContent = data.error || "Не получилось заменить трек";
        btn.disabled = false;
        return;
      }
      closeFix();
      say("Трек заменён. Такой же заказ впредь найдётся сразу.");
    } catch {
      $("fix-note").textContent = "Приложение не ответило";
      btn.disabled = false;
    }
  }

  $("fix-close").onclick = closeFix;
  $("fix").onclick = (e) => { if (e.target.id === "fix") closeFix(); };
  $("fix-find").onclick = runFix;
  $("fix-query").onkeydown = (e) => { if (e.key === "Enter") runFix(); };
  $("fix-results").onclick = (e) => {
    const pick = e.target.closest("[data-pick]");
    if (pick) applyFix(pick.dataset.pick, pick);
  };
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("fix").hidden) { closeFix(); return; }
    // Esc закрывает меню. Окно «не тот трек» лежит поверх него, поэтому
    // первым закрывается оно.
    if (e.key === "Escape" && menuShown()) closeMenu();
  });

  // ── хроника ────────────────────────────────────────────────────────
  //
  // Отдельная панель, а не колонка на главном экране: главный экран смотрят
  // две секунды между раундами, а сюда заходят разбираться — «кто заказал ту
  // хрень» и «кто её скипнул».

  let histTimer = null;

  const outcomeWord = {
    played: ["сыграл", "ok"],
    skipped: ["скипнут", "warn"],
    rejected: ["отказ", "bad"],
    removed: ["удалён", "warn"],
  };

  let histSeq = 0;

  async function loadHistory() {
    const q = $("hist-q").value.trim();
    const mine = ++histSeq;
    try {
      const rows = await (await fetch(`/api/history?q=${encodeURIComponent(q)}`)).json();
      // Пока ждали, успели набрать другое — старый ответ выкидываем.
      if (mine !== histSeq) return;
      if (!rows.length) {
        $("hist").innerHTML = "";
        $("hist-note").textContent = q
          ? "По этому запросу ничего нет."
          : "Пока пусто. Здесь окажется всё, что заказывали.";
        return;
      }
      $("hist-note").textContent = "";
      $("hist").innerHTML = rows.map((h) => {
        const [word, tone] = outcomeWord[h.outcome] || [h.outcome, ""];
        const name = h.artist || h.title ? `${esc(h.artist)} — ${esc(h.title)}` : esc(h.raw);
        return `<li>
          <span class="body">
            <span class="ttl">${name}</span>
            <span class="sub">
              <span class="tag ${tone}">${esc(word)}</span>
              ${esc(h.requester)}
              ${h.reason ? `<span class="asked">${esc(h.reason)}</span>` : ""}
            </span>
          </span>
          <span class="len">${when(h.at)}</span>
        </li>`;
      }).join("");
    } catch {
      $("hist-note").textContent = "Не смог прочитать хронику";
    }
  }

  async function loadModLog() {
    try {
      const rows = await (await fetch("/api/modlog")).json();
      $("modlog").innerHTML = rows.length
        ? rows.map((m) => `<li>
            <span class="body">
              <span class="ttl">${esc(m.actor)} · ${esc(m.action)}</span>
              ${m.target ? `<span class="sub">${esc(m.target)}</span>` : ""}
            </span>
            <span class="len">${when(m.at)}</span>
          </li>`).join("")
        : `<li class="none">Модераторы ничего не делали.</li>`;
    } catch {
      $("modlog").innerHTML = `<li class="none">Не смог прочитать журнал</li>`;
    }
  }

  // when — «сегодня в 21:14», «вчера в 03:02», иначе дата. Точная дата у
  // свежих записей мешает: важнее «только что это было или на той неделе».
  function when(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return "";
    const time = d.toLocaleTimeString("ru", { hour: "2-digit", minute: "2-digit" });
    const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const diff = (day(new Date()) - day(d)) / 86400000;
    if (diff === 0) return time;
    if (diff === 1) return "вчера " + time;
    return d.toLocaleDateString("ru", { day: "numeric", month: "short" }) + " " + time;
  }

  $("log-toggle").onclick = () => toggleLog();
  // Хронику открывают, чтобы разобраться по ходу стрима: застывший на
  // моменте открытия список для этого бесполезен.
  setInterval(() => {
    if (!$("log").hidden) {
      loadHistory();
      loadModLog();
    }
  }, 20000);

  $("hist-q").oninput = () => {
    // Печатают по букве, а запрос идёт в базу: ждём паузу.
    clearTimeout(histTimer);
    histTimer = setTimeout(loadHistory, 250);
  };

  $("settings-toggle").onclick = () => toggleSettings();

  // Ползунок прокрутки виден только тогда, когда прокручивают. Сама полоса
  // прозрачная всегда (см. .thin-scroll в app.css); здесь только признак «сейчас
  // едет», который сам гаснет через секунду после последнего движения.
  function fadingScrollbar(watch, mark) {
    let off = null;
    watch.addEventListener("scroll", () => {
      mark.classList.add("scrolling");
      clearTimeout(off);
      off = setTimeout(() => mark.classList.remove("scrolling"), 900);
    }, { passive: true });
  }
  // У самой страницы событие приходит окну, а полоса принадлежит <html>.
  fadingScrollbar(window, document.documentElement);
  fadingScrollbar($("menu-body"), $("menu-body"));

  $("menu-back").onclick = closeMenu;
  // Клик по затемнению — тот же «назад». Затемнение для того и нужно: видно,
  // что главный экран никуда не делся и до него один клик.
  document.querySelector(".menu-scrim").onclick = closeMenu;
  $("menu-nav").onclick = (e) => {
    const b = e.target.closest("[data-menu]");
    if (b) openSection(b.dataset.menu);
  };
  $("settings-tabs").onclick = (e) => {
    const b = e.target.closest(".tab");
    if (b) openTab(b.dataset.tab);
  };

  $("save-id").onclick = async () => {
    if (await saveConfig({ spotify_client_id: $("client-id").value.trim() })) {
      say("Client ID сохранён. Теперь нажми «Подключить Spotify».");
    }
  };
  $("save-proxy").onclick = async () => {
    if (await saveConfig({ spotify_proxy: $("proxy").value.trim() }, "?proxy=1")) {
      say($("proxy").value.trim()
        ? "Прокси включён. Самой программе Spotify он не нужен — нажми «Проверить связь»."
        : "Прокси убран, Spotify идёт напрямую.");
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
      // Возвращаем то, что сохранено: иначе на экране остаётся мусор, а
      // человек уходит уверенным, что порог изменён.
      e.target.value = config.donation_min ?? "";
      return;
    }
    if (await saveConfig({ donation_min: value })) markSaved(e.target);
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

  // ── правила заказа ─────────────────────────────────────────────────

  // Числовые поля сохраняем только осмысленными: пустое или испорченное
  // значение молча превратилось бы в ноль, а ноль здесь означает «запрещено
  // всё» — заметить это можно было бы только по сорванному стриму.
  function numberField(id, key, { min, max, scale = 1, blank = null }) {
    $(id).onchange = async (e) => {
      const raw = e.target.value.trim();
      if (raw === "" && blank !== null) {
        await saveConfig({ [key]: blank });
        return;
      }
      const value = parseInt(raw, 10);
      if (!isFinite(value) || value < min || value > max) {
        say(`Нужно число от ${min} до ${max}`, true);
        e.target.value = Math.round((config[key] || 0) / scale) || "";
        return;
      }
      if (await saveConfig({ [key]: value * scale })) {
        e.target.value = value;
        markSaved(e.target);
      }
    };
  }

  numberField("reward-cost", "reward_cost", { min: 1, max: 1000000 });
  numberField("max-minutes", "max_track_seconds", { min: 1, max: 60, scale: 60 });
  numberField("max-per-user", "max_per_user", { min: 1, max: 20 });

  $("reward-title").onchange = async (e) => {
    const title = e.target.value.trim();
    if (!title) {
      e.target.value = config.reward_title || "";
      return;
    }
    if (await saveConfig({ reward_title: title })) markSaved(e.target);
  };

  $("auto-reward").onchange = (e) =>
    saveConfig({ auto_create_reward: e.target.checked }).then(() => markSaved(e.target));
  $("donation-priority").onchange = (e) =>
    saveConfig({ donation_priority: e.target.checked }).then(() => markSaved(e.target));
  // Настройка была в конфиге с самого начала, но в панели её не было вовсе —
  // а именно она вызывает главную жалобу «заказ еле включается». Стример
  // должен уметь её выключить, не открывая config.json.
  $("wait-current").onchange = (e) =>
    saveConfig({ wait_for_current: e.target.checked }).then(() => markSaved(e.target));

  $("reject-words").onchange = async (e) => {
    const words = e.target.value.split(NL).map((w) => w.trim()).filter(Boolean);
    if (await saveConfig({ reject_keywords: words })) {
      e.target.value = words.join(NL);
      markSaved(e.target);
    }
  };

  // ── виджет ─────────────────────────────────────────────────────────
  //
  // Оформление хранится в настройках, а не в адресе: иначе каждая правка
  // означала бы «скопируй новый адрес и вставь в OBS заново». Адрес один
  // и навсегда, а вид меняется на лету — и в кадре, и здесь в образце.
  //
  // Образец рисуется тем же widget.css, что и настоящий виджет. Отдельная
  // «похожая» вёрстка означала бы, что панель показывает одно, а стрим
  // другое, — и разошлись бы они в первый же день.

  const WX_TRACKS = [
    ["Bohemian Rhapsody", "Queen", "zritel"],
    ["Группа крови", "Кино", "mikhail"],
    ["Everything In Its Right Place", "Radiohead", "nastya_k"],
    ["Плот", "Юрий Лоза", "dobryak2000"],
  ];
  let wxTrack = 0;
  // Каким показать образец: заказом или своей музыкой. Выбирать набор надо,
  // видя оба случая — строка под артистом у них разная.
  let wxSource = "order";
  let wxTimer = null;
  let wxSwapTimer = null;
  let galleryBuilt = false;

  function widget() {
    return (config && config.widget) || {};
  }

  // Одевает карточку по настройкам. Используется и образцом в кадре, и
  // каждой плиткой галереи — поэтому набор принимается отдельным доводом.
  function dressCard(card, w, preset) {
    const { family, variant } = widgetPreset(preset || w.preset);
    card.dataset.family = family.id;
    card.dataset.variant = variant.id;
    card.dataset.art = w.show_art === false ? "off" : "on";
    card.dataset.source = wxSource;
    card.dataset.bar = w.show_bar === false ? "off" : "on";
    card.dataset.motion = w.motion === false ? "off" : "on";
    return family;
  }

  function drawWidget() {
    const w = widget();
    const card = $("wx-card");
    const family = dressCard(card, w);

    card.style.setProperty("--scale", w.scale || 1);

    const gap = (Number.isFinite(w.gap) ? w.gap : 40) + "px";
    const pos = w.position || "left-bottom";
    card.style.left = card.style.right = card.style.top = card.style.bottom = "";
    if (family.id !== "ticker") card.style[pos.includes("left") ? "left" : "right"] = gap;
    card.style[pos.includes("top") ? "top" : "bottom"] = family.id === "ticker" ? "0px" : gap;
    card.dataset.pos = pos;
    // Образец стоит на середине трека: так видно и полосу, и положение
    // тонарма у винила.
    card.style.setProperty("--progress", ".46");

    // Тот же белый список, что и в самом виджете: предпросмотр не должен
    // показывать то, чего в OBS не будет. Раньше панель применяла всё, что
    // лежит в настройках, и ключи, которых виджет не знает, рисовались
    // только здесь — то есть панель обещала оформление, которого на стриме
    // не появится.
    const allowed = new Set(WIDGET_TWEAKS.map((t) => t.key));
    for (const t of WIDGET_TWEAKS) card.style.removeProperty(t.key);
    for (const [key, value] of Object.entries(w.tweaks || {})) {
      if (allowed.has(key)) card.style.setProperty(key, value);
    }
    $("wx-bar").style.width = "46%";

    // Тикер занимает всю ширину — выбор угла для него ничего не значит,
    // и делать вид, что значит, нечестно.
    const ticker = family.id === "ticker";
    $("wx-pos").disabled = ticker;
    $("wx-gap").disabled = ticker;
    $("wx-pos-hint").textContent = ticker
      ? "Тикер занимает всю ширину: остаётся только выбрать верх или низ."
      : "";

    const percent = Math.round((w.scale || 1) * 100);
    $("wx-scale").value = percent;
    $("wx-scale-val").textContent = percent + "%";
    const gapPx = Number.isFinite(w.gap) ? w.gap : 40;
    $("wx-gap").value = gapPx;
    $("wx-gap-val").textContent = gapPx + " px";
    $("wx-pos").value = pos;
    $("wx-art-on").checked = w.show_art !== false;
    $("wx-by-on").checked = w.show_requester !== false;
    $("wx-bar-on").checked = w.show_bar !== false;
    $("wx-motion-on").checked = w.motion !== false;
    $("wx-own-on").checked = w.show_own !== false;
    $("wx-by").textContent = wxByLine(w);
    $("wx-hold").value = w.hold_seconds || 0;
    $("wx-hold-val").textContent = w.hold_seconds ? w.hold_seconds + " с" : "не убирать";

    const url = location.origin + "/widget";
    $("wx-url").value = url;
    $("wx-open").href = url + "?demo=1";

    const chosen = widgetPreset(w.preset).id;
    document.querySelectorAll("#wx-gallery .tile").forEach((tile) => {
      tile.classList.toggle("on", tile.dataset.preset === chosen);
    });
    drawTweaks();
  }

  // Настройки виджета лежат одним блоком: сливаем поверх текущего, а не
  // собираем заново, иначе каждая правка стирала бы остальные.
  async function saveWidget(patch) {
    const next = Object.assign({}, widget(), patch);
    if (await saveConfig({ widget: next })) drawWidget();
  }

  function buildGallery() {
    if (galleryBuilt) return;

    $("wx-gallery").innerHTML = WIDGET_FAMILIES.map((f) => {
      const tiles = f.variants.map((v) => `
        <button class="tile" data-preset="${f.id}/${v.id}" title="${esc(f.name)} · ${esc(v.name)}">
          <span class="tile-stage">
            <span class="card show" data-family="${f.id}" data-variant="${v.id}"
                  data-pos="left-bottom" style="--scale:.6;--progress:.46">
              <span class="art"></span>
              <span class="text">
                <span class="title">Группа крови</span>
                <span class="artist">Кино</span>
                <span class="by">заказал mikhail</span>
                <span class="bar"><i style="width:52%"></i></span>
              </span>
            </span>
          </span>
          <span class="tile-name">${esc(v.name)}</span>
        </button>`).join("");

      return `<div class="fam">
        <div class="fam-head">
          <h3>${esc(f.name)}</h3>
          <p>${esc(f.note)}</p>
        </div>
        <div class="tiles">${tiles}</div>
      </div>`;
    }).join("");

    $("wx-gallery").onclick = (e) => {
      const tile = e.target.closest(".tile");
      // Смена набора сбрасывает ручную правку: иначе розовый акцент от
      // прошлого набора переехал бы в новый и испортил его.
      if (!tile) return;
      saveWidget({ preset: tile.dataset.preset, tweaks: null }).then(() => wxPlay("enter"));
    };

    galleryBuilt = true;
  }

  // Тонкая настройка — всегда поверх набора: нетронутое остаётся набором,
  // а не застывшим слепком его текущих значений.
  function drawTweaks() {
    const tweaks = widget().tweaks || {};
    const computed = getComputedStyle($("wx-card"));

    $("wx-tweaks").innerHTML = WIDGET_TWEAKS.map((t) => {
      const set = tweaks[t.key] !== undefined;
      const now = (tweaks[t.key] || computed.getPropertyValue(t.key) || "").trim();
      const off = set
        ? `<button class="tweak-off" data-clear="${t.key}" title="вернуть как в наборе">×</button>`
        : "";

      if (t.kind === "color") {
        return `<label class="tweak${set ? " set" : ""}" title="${esc(t.hint || t.name)}">
          <input type="color" data-tweak="${t.key}" value="${esc(asHex(now))}">
          <span class="tweak-name">${esc(t.name)}</span>${off}
        </label>`;
      }

      const px = parseInt(now, 10) || 0;
      return `<label class="tweak wide${set ? " set" : ""}">
        <span class="tweak-name">${esc(t.name)} <b>${px} px</b>${off}</span>
        <input type="range" data-tweak="${t.key}" min="${t.min}" max="${t.max}" value="${px}">
      </label>`;
    }).join("");
  }

  // input[type=color] понимает только #rrggbb, а из набора значение может
  // приехать как rgba(...). Берём то, что реально нарисовал браузер.
  function asHex(value) {
    const v = String(value).trim();
    if (/^#[0-9a-f]{6}$/i.test(v)) return v;
    const probe = document.createElement("span");
    probe.style.color = v;
    document.body.appendChild(probe);
    const rgb = getComputedStyle(probe).color.match(/\d+/g);
    probe.remove();
    if (!rgb) return "#ffffff";
    return "#" + rgb.slice(0, 3).map((n) => Number(n).toString(16).padStart(2, "0")).join("");
  }

  function setTweak(key, value) {
    const tweaks = Object.assign({}, widget().tweaks || {});
    if (value === null) delete tweaks[key];
    else tweaks[key] = value;
    saveWidget({ tweaks: Object.keys(tweaks).length ? tweaks : null });
  }

  // Ползунки двигают образец сразу, а на диск пишут по отпусканию: иначе
  // каждое движение мыши было бы записью файла и рассылкой в OBS.
  function live(input, onMove, onDone) {
    input.oninput = () => onMove(parseInt(input.value, 10));
    input.onchange = () => onDone(parseInt(input.value, 10));
  }

  function drawGap(px) {
    if (widgetPreset(widget().preset).family.id === "ticker") return;
    const card = $("wx-card");
    const pos = widget().position || "left-bottom";
    card.style[pos.includes("left") ? "left" : "right"] = px + "px";
    card.style[pos.includes("top") ? "top" : "bottom"] = px + "px";
  }

  $("widget-toggle").onclick = () => toggleWidget();
  $("wx-pos").onchange = (e) => saveWidget({ position: e.target.value });
  $("wx-art-on").onchange = (e) => saveWidget({ show_art: e.target.checked });
  $("wx-by-on").onchange = (e) => saveWidget({ show_requester: e.target.checked });
  $("wx-bar-on").onchange = (e) => saveWidget({ show_bar: e.target.checked });
  $("wx-motion-on").onchange = (e) => saveWidget({ motion: e.target.checked });
  $("wx-own-on").onchange = (e) => saveWidget({ show_own: e.target.checked });

  // Ровно та же строка, что рисует настоящий виджет.
  function wxByLine(w) {
    if (wxSource === "own") return "музыка канала";
    if (w.show_requester !== false) return "заказал " + WX_TRACKS[wxTrack][2];
    return "заказ зрителя";
  }

  $("wx-source-pick").onclick = (e) => {
    const btn = e.target.closest("[data-source]");
    if (!btn) return;
    wxSource = btn.dataset.source;
    $("wx-source-pick").querySelectorAll("button").forEach((b) => {
      b.classList.toggle("on", b === btn);
    });
    drawWidget();
  };
  $("wx-reset").onclick = () => saveWidget({ tweaks: null });

  live($("wx-scale"), (v) => {
    $("wx-scale-val").textContent = v + "%";
    $("wx-card").style.setProperty("--scale", v / 100);
  }, (v) => saveWidget({ scale: v / 100 }));

  live($("wx-gap"), (v) => {
    $("wx-gap-val").textContent = v + " px";
    drawGap(v);
  }, (v) => saveWidget({ gap: v }));

  live($("wx-hold"), (v) => {
    $("wx-hold-val").textContent = v ? v + " с" : "не убирать";
  }, (v) => saveWidget({ hold_seconds: v }));

  $("wx-tweaks").oninput = (e) => {
    const key = e.target.dataset.tweak;
    if (!key) return;
    const value = e.target.type === "color" ? e.target.value : e.target.value + "px";
    $("wx-card").style.setProperty(key, value);
  };
  $("wx-tweaks").onchange = (e) => {
    const key = e.target.dataset.tweak;
    if (!key) return;
    setTweak(key, e.target.type === "color" ? e.target.value : e.target.value + "px");
  };
  $("wx-tweaks").onclick = (e) => {
    const clear = e.target.closest("[data-clear]");
    if (clear) setTweak(clear.dataset.clear, null);
  };

  $("wx-scene-pick").onclick = (e) => {
    const btn = e.target.closest("[data-scene]");
    if (!btn) return;
    $("wx-stage").dataset.scene = btn.dataset.scene;
    $("wx-scene-pick").querySelectorAll("button").forEach((b) => {
      b.classList.toggle("on", b === btn);
    });
  };

  // Появление, смена трека и уход — три разных состояния. Класс снимается
  // после проигрыша, иначе он глушит постоянные анимации.
  function wxPlay(kind) {
    const card = $("wx-card");
    clearTimeout(wxTimer);
    card.classList.remove("enter", "swap-out", "swap-in", "leave", "show");
    void card.offsetWidth;
    card.classList.add("show", kind);
    wxTimer = setTimeout(() => card.classList.remove(kind), 1600);
  }

  $("wx-play").onclick = () => wxPlay("enter");
  $("wx-leave").onclick = () => wxPlay("leave");

  // Смена трека — две фазы, как в настоящем виджете: старое уходит, текст
  // подменяется, новое приходит. Длительность ухода объявлена оформлением.
  $("wx-next").onclick = () => {
    const card = $("wx-card");
    const out = parseFloat(getComputedStyle(card).getPropertyValue("--swap-out")) || 150;
    wxPlay("swap-out");
    clearTimeout(wxSwapTimer);
    wxSwapTimer = setTimeout(() => {
      wxTrack = (wxTrack + 1) % WX_TRACKS.length;
      const [title, artist, by] = WX_TRACKS[wxTrack];
      $("wx-title").textContent = title;
      $("wx-artist").textContent = artist;
      $("wx-by").textContent = wxByLine(widget());
      wxPlay("swap-in");
    }, out);
  };

  $("wx-copy").onclick = async () => {
    try {
      await navigator.clipboard.writeText($("wx-url").value);
      say("Адрес скопирован — вставь его в источник «Браузер» в OBS.");
    } catch {
      // Буфер обмена доступен не везде; выделенный текст скопируется вручную.
      $("wx-url").select();
      say("Скопируй выделенный адрес: Ctrl+C", true);
    }
  };

  $("ban-login").onkeydown = (e) => {
    if (e.key === "Enter") $("ban-add").click();
  };

  // Мастер первой настройки: показать ещё раз по просьбе. Сам мастер живёт в
  // setup.js — здесь только кнопка.
  const again = $("setup-again");
  if (again) {
    again.onclick = () => {
      if (window.srSetupOpen) window.srSetupOpen();
      else say("Мастер настройки не загрузился — перезапусти приложение", true);
    };
  }

  // ── сборка ─────────────────────────────────────────────────────────

  let lastState = null;

  function render(s) {
    lastState = s;
    document.body.classList.remove("stale");

    const sig = JSON.stringify;
    draw("bar", sig(s.connections) + s.version, () => renderBar(s));
    // Позиция трека намеренно выброшена из подписи: она меняется каждые
    // несколько секунд, а перерисовывать из-за неё всю карточку — значит
    // мигать ею и сбивать собственный отсчёт полосы.
    draw("stage", sig([nowKey(s.now), s.spotify, s.twitch, s.connections,
                       s.last_played && s.last_played.at]), () => renderStage(s));
    // Перемотка в Spotify не меняет ни трек, ни подпись — значит карточку не
    // перерисуют. Но время после неё другое, и полосу надо переставить.
    syncMeter(s.now);
    draw("orders", sig([s.queue, s.paused, s.twitch.reward_title,
                        s.twitch.reward_cost, s.twitch.reward_ready]), () => renderOrders(s));
    draw("bans", sig(s.bans), () => renderBans(s.bans));
    draw("yt", sig(s.youtube), () => renderYouTube(s.youtube));
    draw("feed", sig(s.notices), () => renderFeed(s));
    // Время работы считается от Date.now(), а не приходит с сервера: без
    // огрублённой минуты в подписи блок не перерисовывался вовсе, и в тихий
    // вечер там часами висело «3 минуты».
    draw("tally", sig(s.session) + Math.floor(Date.now() / 60000), () => renderTally(s));
    draw("banner", sig([s.twitch.pending_code, s.twitch.pending_expires]), () => renderBanner(s.twitch));
    draw("sp-card", sig(s.spotify), () => renderSpotifyCard(s.spotify));
    draw("tw-card", sig(s.twitch), () => renderTwitchCard(s.twitch));
    draw("tunnel", sig(s.tunnel), () => renderTunnel(s.tunnel));

    if (s.spotify.connected && !playlistsLoaded &&
        !$("settings").hidden && settingsTab === "fallback") loadPlaylists();
    if (redirectShown && s.spotify.account) {
      $("redirect").textContent = "";
      redirectShown = false;
    }

    // Мастер настройки живёт в отдельном файле, но состояние у нас одно:
    // отдаём снимок ему, чтобы его проверки («Spotify подключён», «обход
    // работает») загорались сами, без единого нажатия.
    if (window.srSetupState) window.srSetupState(s);

    // В своей полосе заголовка окна пишем, что играет: панель бывает
    // прокручена, а полоса на виду всегда.
    const tb = $("titlebar-now");
    if (tb) tb.textContent = s.now ? `${s.now.artist} — ${s.now.title}` : "";

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
