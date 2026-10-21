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

  // Обложка — через само приложение, а не прямой ссылкой на склад Spotify.
  //
  // ПОЧЕМУ. Панель живёт в окне приложения, а окно — это WebView2, то есть
  // отдельный браузер. В интернет он ходит сам по себе и про обход блокировок,
  // поднятый приложением, ничего не знает. Отсюда и жалоба «иногда не
  // показывает картинку трека»: заголовок и артист на месте, а вместо обложки
  // пустой квадрат — картинку было некому скачать. У виджета в OBS ровно та же
  // беда решена ровно так же (см. internal/server/cover.go), только панель
  // почему-то осталась на прямых ссылках.
  //
  // Приложение качает картинку своим путём — через обход, если он поднят, —
  // складывает у себя и отдаёт с 127.0.0.1. Не смогло — переводит браузер на
  // прежний прямой адрес: хуже, чем было, от этого не станет.
  const coverSrc = (u) => "/cover?u=" + encodeURIComponent(u);

  // errLine — отказ приложения одной строкой: «код · текст», а без кода —
  // просто текст. Без текста — общая фраза, а не «undefined».
  function errLine(d) {
    const text = (d && d.error) || I18N.t("Не получилось.");
    return d && d.code ? `${d.code} · ${text}` : text;
  }

  // trackLine — «Артист — Название»; без артиста только название, без
  // одинокого тире впереди. Повторяет trackLine из commands.go.
  function trackLine(artist, title) {
    return artist ? `${artist} — ${title || ""}` : (title || "");
  }

  // art рисует обложку с запасным вариантом.
  //
  // onerror обязателен: адрес обложки может протухнуть, склад ответить
  // отказом, а картинка — оказаться битой. Без него на месте обложки остаётся
  // сломанный значок браузера, и это выглядит как поломка приложения. С ним
  // остаётся честная нотка — то же самое, что при треке вовсе без обложки.
  function art(url, cls) {
    if (!url) return `<div class="${cls}">${icon("music")}</div>`;
    return `<div class="${cls}"><img src="${esc(coverSrc(url))}" alt=""
      onerror="this.parentNode.innerHTML='${
        icon("music").replace(/"/g, "&quot;")}'"></div>`;
  }

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
    new Date(iso).toLocaleTimeString(I18N.locale(), { hour: "2-digit", minute: "2-digit" });

  // Склонение — мелочь, но «5 треков» и «2 трека» стоят рядом с числами,
  // и «5 трек» читается как недоделка.
  // В английском — две последние формы: plural(n, "трек", "трека", "треков", "track", "tracks").
  const plural = (n, one, few, many, en1, enN) => {
    if (I18N.lang() === "en" && en1 !== undefined) return Math.abs(n) === 1 ? en1 : enN;
    const a = Math.abs(n) % 100;
    const b = a % 10;
    if (a > 10 && a < 20) return many;
    if (b > 1 && b < 5) return few;
    if (b === 1) return one;
    return many;
  };

  const minutesLeft = (ms) => {
    const m = Math.round(ms / 60000);
    return m < 1 ? I18N.t("меньше минуты") : `${m} ${plural(m, "минута", "минуты", "минут", "minute", "minutes")}`;
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
      // Молча. Раньше здесь висело «Подключено · 127.0.0.1:8977» — адрес,
      // который стример и так знает, занимал полосу внизу весь стрим и
      // мозолил глаза под окном заказов. Строка нужна для того, что случилось,
      // а «всё в порядке» — это не событие.
      hush();
    };
    socket.onmessage = (e) => render(JSON.parse(e.data));
    socket.onclose = () => {
      say(I18N.t("Приложение не отвечает. Проверь, запущено ли оно."), true);
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
    status.hidden = false;
    // Ошибка висит, пока её не сменят. Сообщение об удаче гаснет само: строка
    // внизу не должна навсегда застревать на «Client ID сохранён».
    clearTimeout(sayTimer);
    if (!bad) {
      sayTimer = setTimeout(hush, 6000);
    }
  }

  // hush убирает строку внизу целиком, вместе с полосой.
  //
  // Пустой текст мало: у полосы своя подложка и рамка, и пустая она читается
  // как «что-то тут было, да пропало». Когда сказать нечего, её быть не
  // должно вовсе.
  function hush() {
    clearTimeout(sayTimer);
    if (document.body.classList.contains("stale")) return;
    status.textContent = "";
    status.hidden = true;
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
        say(errLine(d), true);
        return null;
      }
      return d;
    } catch {
      say(I18N.t("Приложение не отвечает"), true);
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
      return `<div class="st ${cls}">
        <i class="led"></i><span class="st-name">${esc(c.name)}</span>${
          c.detail ? `<b>${esc(c.detail)}</b>` : ""}
      </div>`;
    }).join("");

    // Состояние всех подключений сразу — одной точкой на иконке. Свернуть
    // ленту в иконку можно было только так: иначе поломка перестала бы быть
    // видна, а она и есть единственное, ради чего эта лента нужна.
    const broken = s.connections.filter((c) => c.level === "fail");
    const idle = s.connections.filter((c) => c.level === "idle");
    const btn = $("conn-toggle");
    btn.classList.toggle("bad", broken.length > 0);
    btn.classList.toggle("idle", broken.length === 0 && idle.length > 0);
    btn.title = broken.length
      ? I18N.t("Не работает: {list}", { list: broken.map((c) => c.name).join(", ") })
      : idle.length
        ? I18N.t("Не настроено: {list}", { list: idle.map((c) => c.name).join(", ") })
        : I18N.t("Всё подключено");

    $("version").textContent = s.version;
  }

  // Окошко подключений.
  //
  // Именно окошко, а не раздел меню: сюда заглядывают на секунду, посреди
  // эфира, чтобы понять, что именно отвалилось. Открывать ради этого целое
  // меню — слишком много движений.
  function toggleConns(open) {
    const pop = $("conn-pop");
    const want = open === undefined ? pop.hidden : open;
    pop.hidden = !want;
    $("conn-toggle").classList.toggle("on", want);
    $("conn-toggle").setAttribute("aria-expanded", String(want));
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
        return { state: "todo", value: I18N.t("Client ID не вставлен"),
                 hint: I18N.t("Нажми «Провести по шагам» — мастер покажет, где его взять"),
                 button: `<button class="act key small" data-open-settings>${I18N.t("Открыть настройки")}</button>` };
      }
      if (!sp.connected) {
        return { state: "todo", value: I18N.t("Вход не выполнен"),
                 hint: I18N.t("Разреши доступ на странице Spotify"),
                 button: `<button class="act key small" data-do="login">${I18N.t("Подключить Spotify")}</button>` };
      }
      const paused = pauseLeft(sp);
      if (paused) {
        const at = paused.toLocaleTimeString(I18N.locale(), { hour: "2-digit", minute: "2-digit" });
        return { state: "fail", value: I18N.t("Spotify просит паузу"),
                 hint: I18N.t("Ограничил приложение до {at}: {what}", { at, what: I18N.t(pausedParts[sp.paused_part] || "часть возможностей не работает") }),
                 button: `<button class="act small" data-do="check">${I18N.t("Проверить связь")}</button>` };
      }
      if (!sp.account) {
        return { state: "fail", value: I18N.t("Spotify не отвечает"),
                 hint: I18N.t("Проверь интернет; если пользуешься VPN — включи его"),
                 button: `<button class="act small" data-do="check">${I18N.t("Проверить связь")}</button>` };
      }
      if (sp.plan === "free") {
        return { state: "fail", value: I18N.t("{account} · без Premium", { account: sp.account }),
                 hint: I18N.t("Управлять музыкой Spotify разрешает только с подпиской"),
                 button: `<button class="act small" data-open-settings>${I18N.t("Посмотреть аккаунт")}</button>` };
      }
      return {
        state: sp.plan === "unknown" ? "warn" : "done",
        value: sp.account,
        hint: sp.plan_label,
      };
    };

    const twitchStep = () => {
      if (!tw.has_client_id) {
        return { state: "todo", value: I18N.t("Client ID не вставлен"),
                 hint: I18N.t("Нажми «Провести по шагам» — мастер покажет, где его взять"),
                 button: `<button class="act key small" data-open-settings>${I18N.t("Открыть настройки")}</button>` };
      }
      if (!tw.connected) {
        return { state: "todo", value: I18N.t("Вход не выполнен"),
                 hint: I18N.t("Приложение покажет короткий код"),
                 button: `<button class="act key small" data-do="twitch-login">${I18N.t("Подключить Twitch")}</button>` };
      }
      if (!tw.channel) {
        return { state: "fail", value: I18N.t("Twitch не отвечает"), hint: I18N.t("Канал пока неизвестен") };
      }
      return { state: "done", value: tw.channel, hint: tw.channel_type };
    };

    const rewardStep = () => {
      if (!tw.connected) return { state: "wait", value: "—", hint: I18N.t("после подключения Twitch") };
      if (!tw.has_points) {
        return { state: "off", value: I18N.t("Баллов на канале нет"),
                 hint: I18N.t("Награда бывает только у аффилиатов и партнёров") };
      }
      if (!tw.reward_ready) {
        return { state: "fail", value: I18N.t("Не создана"),
                 hint: tw.note || I18N.t("Приложение не смогло создать награду") };
      }
      return {
        state: "done",
        value: `«${tw.reward_title}»`, // шаблон списка экранирует сам
        hint: I18N.t("{n} {points} за заказ", { n: tw.reward_cost, points: plural(tw.reward_cost, "балл", "балла", "баллов", "point", "points") }),
      };
    };

    const snapshotStep = () => {
      if (!sp.connected || !sp.account) return { state: "wait", value: "—", hint: I18N.t("после подключения Spotify") };
      if (!sp.snapshot_text) {
        return { state: "todo", value: I18N.t("Не запомнено"),
                 hint: I18N.t("Включи музыку и нажми — приложение вернётся сюда после заказов"),
                 button: `<button class="act key small" data-do="snapshot">${I18N.t("Запомнить состояние")}</button>` };
      }
      return {
        state: "done",
        value: sp.snapshot_text, // экранируется в шаблоне списка
        hint: I18N.t("запомнено в {time}", { time: hhmm(sp.snapshot_at) }),
        button: `<button class="act small" data-do="restore">${I18N.t("Вернуть сейчас")}</button>
                 <button class="act small" data-do="snapshot">${I18N.t("Запомнить заново")}</button>`,
      };
    };

    return [
      { name: "Spotify", ...spotifyStep() },
      { name: "Twitch", ...twitchStep() },
      { name: I18N.t("Награда"), ...rewardStep() },
      { name: I18N.t("Точка возврата"), ...snapshotStep() },
    ];
  }

  // nowKey — что именно считать сменой трека. Всё, кроме положения внутри
  // него: полоса идёт сама и в перерисовке не нуждается.
  function nowKey(now) {
    if (!now) return null;
    // Громкости здесь нарочно нет: ползунок ведёт себя сам, а перерисовка
    // карточки на каждое его движение отбирала бы его прямо из-под руки.
    return [now.title, now.artist, now.requester, now.source,
            now.provider, now.via, now.duration_ms, now.uncertain, now.paused];
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

    // Ещё ничего не играло. Выдумывать сюда трек нельзя — человек поверит и
    // пойдёт искать его в хронике; пустое место тоже плохо — непонятно, то ли
    // не работает, то ли нечего показывать. Поэтому честная пустая карточка.
    if (!l || !l.title) {
      return `
        <div class="last empty">
          <div class="last-art">${icon("music")}</div>
          <div class="last-text">
            <div class="last-kicker">Последним играло</div>
            <div class="last-title">Пока ничего не воспроизводилось</div>
            <div class="last-sub">Первый сыгравший трек появится здесь</div>
          </div>
        </div>`;
    }

    const marks = [];
    if (l.requester) marks.push(I18N.t("заказал {who}", { who: l.requester }));
    else if (l.source === "own") marks.push(I18N.t("твоя музыка"));
    if (l.provider === "youtube") marks.push(l.via ? "YouTube · " + I18N.t(l.via) : "YouTube");
    if (l.at) marks.push(when(l.at));

    return `
      <div class="last">
        ${art(l.cover_url, "last-art")}
        <div class="last-text">
          <div class="last-kicker">Последним играло</div>
          <div class="last-title">${esc(l.title)}</div>
          <div class="last-sub">${esc(l.artist)}</div>
        </div>
        ${marks.length ? `<div class="last-marks">${marks.map(esc).join(" · ")}</div>` : ""}
        ${l.source === "order" && l.uri ? `
        <button class="act small" data-play="repeat" title="Поставить этот трек ещё раз">
          ${icon("repeat")}Повторить
        </button>` : ""}
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
          <span class="where">${now.provider === "youtube" ? "YouTube"
              : now.provider === "spotify" ? "Spotify" : I18N.t("Музыка на компьютере")}${
            now.via ? " · " + esc(I18N.t(now.via)) : ""}${
            own ? " · " + I18N.t("твоя музыка") : ""}${
            now.uncertain ? " · " + I18N.t("неточное совпадение") : ""}</span>
        </div>
        <div class="stage-row">
          ${art(now.cover_url, "art")}
          <div class="stage-text">
            <h1 class="headline">${esc(now.title)}</h1>
            <div class="subhead">${esc(now.artist)}</div>
            ${now.requester ? `<div class="credit">${icon("user")}${I18N.t("заказал")} <b>${esc(now.requester)}</b></div>` : ""}
            ${now.duration_ms > 0 ? `
            <div class="meter${own ? "" : " seekable"}">
              <span class="t" id="at">${mmss(now.position_ms)}</span>
              <span class="bar" id="at-seek"><i id="at-bar"></i></span>
              <span class="t">${now.duration_ms > 0 ? mmss(now.duration_ms) : "—"}</span>
            </div>` : ""}
          </div>
        </div>
        <div class="player">
          <button class="pbtn" data-play="prev"
                  title="${own ? I18N.t("Предыдущий трек") : I18N.t("В начало трека")}">${icon("skip-back")}</button>
          <button class="pbtn key" data-play="${now.paused ? "play" : "pause"}"
                  title="${now.paused ? I18N.t("Продолжить") : I18N.t("Пауза")}">${icon(now.paused ? "play" : "pause")}</button>
          <button class="pbtn" data-play="next"
                  title="${own ? I18N.t("Следующий трек") : I18N.t("Следующий заказ")}">${icon("skip-forward")}</button>
          ${own ? "" : `
          <span class="pvol">
            <button class="pbtn" data-play="mute" title="Заглушить">${icon("volume-2")}</button>
            <input type="range" id="vol" min="0" max="100" step="1" value="${now.volume ?? 100}">
            <b id="vol-val">${now.volume ?? 100}%</b>
          </span>`}
        </div>
        <div class="acts">${own ? `
          <span class="hint" style="margin:0">${I18N.t("Это твоя музыка, а не заказ. Пауза и переключение работают, а громкость и перемотка живут в самом Spotify — приложение туда не лезет.")}</span>` : `
          <button class="act key" data-do="skip">${icon("skip-forward")}Скипнуть</button>
          <button class="act" data-do="restore">${icon("rotate-cw")}Вернуть Spotify</button>`}
        </div>
        ${broken.length ? `
        <div class="stage-alarm">
          ${icon("triangle-alert")}
          <div>
            <b>${broken.map((x) => esc(x.name)).join(", ")}: ${esc(broken[0].value)}</b>
            <div class="hint">${esc(broken[0].hint)}</div>
          </div>
          ${broken[0].button || ""}
        </div>` : ""}`);
      if (!own) {
        bindVolume();
        bindSeek(now);
      }
      runMeter(now);
      return;
    }

    const done = list.filter((x) => x.state === "done").length;
    const broken = list.some((x) => x.state === "fail");
    const ready = done === list.length;

    let brow, mark, sub;
    if (broken) {
      brow = `<div class="eyebrow alarm"><i class="live"></i> Нужно вмешаться</div>`;
      mark = I18N.t("Что-то отвалилось");
      sub = I18N.t("Ниже видно, что именно. Заказы пока не принимаются.");
    } else if (ready) {
      brow = `<div class="eyebrow"><i class="live"></i> Готов принимать заказы</div>`;
      mark = I18N.t("Всё настроено");
      sub = I18N.t("Зрители заказывают трек за {n} {points}. Как только заказ придёт, он появится здесь.", {
        n: s.twitch.reward_cost,
        points: plural(s.twitch.reward_cost, "балл", "балла", "баллов", "point", "points") });
    } else {
      brow = `<div class="eyebrow quiet"><i class="live"></i> ${I18N.t("Настройка · {done} из {total}", { done, total: list.length })}</div>`;
      mark = I18N.t("Ещё не всё готово");
      sub = I18N.t("Пройди шаги ниже — это делается один раз. Или нажми «Провести по шагам»: мастер сделает то же самое, но с картинками и по одному шагу за раз.");
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
  // Перемотка.
  //
  // Один запрос на одно движение руки — на отпускание, а не на каждый пиксель.
  // Для Spotify это принципиально: перемотка там стоит запроса, а норма
  // запросов — узкое место всего приложения. Для заказов с YouTube канал
  // местный и бесплатный, но правило одно на всех: незачем два поведения.
  let seeking = false;

  function bindSeek(now) {
    const bar = $("at-seek");
    if (!bar || !now.duration_ms) return;

    const at = (e) => {
      const box = bar.getBoundingClientRect();
      const part = Math.min(1, Math.max(0, (e.clientX - box.left) / box.width));
      return Math.round(part * now.duration_ms);
    };
    const paint = (ms) => {
      const fill = $("at-bar");
      if (fill) fill.style.width = (ms / now.duration_ms) * 100 + "%";
      $("at").textContent = mmss(ms);
    };

    bar.onpointerdown = (e) => {
      seeking = true;
      bar.setPointerCapture(e.pointerId);
      paint(at(e));
    };
    bar.onpointermove = (e) => { if (seeking) paint(at(e)); };
    bar.onpointerup = async (e) => {
      if (!seeking) return;
      seeking = false;
      const ms = at(e);
      paint(ms);
      // Свои часы переставляем сразу, не дожидаясь ответа: приложение сделает
      // то же самое у себя, а полоса не должна дёргаться в ожидании сети.
      meterFrom = Date.now() - ms;
      await send("/api/player/seek", { position_ms: ms });
    };
    bar.onpointercancel = () => { seeking = false; };
  }

  // Громкость. Ползунок ведём у себя, а посылаем один раз — на отпускание.
  let volumeWas = 0;

  function bindVolume() {
    const el = $("vol");
    if (!el) return;
    el.oninput = () => { $("vol-val").textContent = el.value + "%"; };
    el.onchange = () => send("/api/player/volume", { percent: parseInt(el.value, 10) });
  }

  // send — одна команда плеера. Отказ показываем строкой внизу: кнопки плеера
  // нажимают, не глядя на экран, и молчащая кнопка читается как поломка.
  async function send(url, body) {
    try {
      const r = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body || {}),
      });
      if (r.ok) return true;
      const data = await r.json().catch(() => ({}));
      say(data.error || I18N.t("Не получилось"), true);
    } catch {
      say(I18N.t("Приложение не ответило"), true);
    }
    return false;
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

  // ── обновление приложения ──────────────────────────────────────────
  //
  // Приложение само в интернет не ходит: проверка только по кнопке — так
  // решил владелец. Поэтому кнопка «Обновить» в верхней полосе появляется не
  // сама по себе, а после того, как человек нажал «Проверить обновления» и
  // новая версия правда нашлась.
  //
  // Кнопка стоит в верхней полосе, а не только в настройках, по простой
  // причине: настройки открывают раз в месяц, а увидеть «вышла новая версия»
  // надо тогда, когда её выпустили.

  function renderUpdate(u) {
    const top = $("update-now");
    const busy = !!u.installing;

    // Верхняя полоса.
    top.hidden = !u.available;
    if (u.available) {
      top.innerHTML = `${icon("download")}${busy ? I18N.t("обновляю…") : I18N.t("обновить до {v}", { v: esc(u.version) })}`;
      top.disabled = busy;
    }

    // Настройки.
    const repo = $("upd-repo");
    if (repo && document.activeElement !== repo) repo.value = u.repo || "";

    $("upd-check").disabled = !!u.checking || busy;
    $("upd-check").textContent = u.checking ? I18N.t("Проверяю…") : I18N.t("Проверить обновления");

    const install = $("upd-install");
    install.hidden = !u.available;
    install.disabled = busy;
    install.textContent = busy ? I18N.t("Обновляю…") : I18N.t("Обновить до {v}", { v: u.version || "" });

    const page = $("upd-page");
    page.hidden = !u.page;
    if (u.page) page.href = u.page;

    const rows = [
      `<div class="sum-row"><span>Сейчас работает</span><b>${esc(u.current || "—")}</b></div>`,
    ];
    if (u.available) {
      rows.push(`<div class="sum-row good"><span>Вышла версия</span><b>${esc(u.version)}</b></div>`);
      if (u.size > 0) {
        rows.push(`<div class="sum-row"><span>Размер</span><b>${I18N.t("{n} МБ", { n: Math.round(u.size / (1 << 20)) })}</b></div>`);
      }
    }
    if (u.note) {
      rows.push(`<div class="sum-row ${u.note_code ? "bad" : ""}">
        <span>${u.note_code ? esc(u.note_code) : I18N.t("Последняя проверка")}</span>
        <b>${esc(u.note)}</b></div>`);
    }
    $("upd-sum").innerHTML = rows.join("");

    // Что изменилось — отдельным текстом под сводкой: в строку сводки это не
    // влезает, а читать его человек будет ровно один раз перед нажатием.
    const notes = u.available && u.notes ? plainNotes(u.notes) : "";
    let box = $("upd-notes");
    if (!box) {
      box = document.createElement("div");
      box.id = "upd-notes";
      box.className = "hint upd-notes";
      $("upd-sum").after(box);
    }
    box.hidden = !notes;
    box.textContent = notes.length > 600 ? notes.slice(0, 600) + "…" : notes;
  }

  // plainNotes превращает описание релиза с GitHub (Markdown) в обычный
  // текст: без решёток, звёздочек и ссылок, пункты списка — с «• », без
  // строки «Full Changelog» со служебной ссылкой.
  function plainNotes(md) {
    return String(md)
      .split(/\r?\n/)
      .filter((line) => !/full changelog/i.test(line))
      .map((line) =>
        line
          .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
          .replace(/^\s*#{1,6}\s*/, "")
          .replace(/^(\s*)[-*+]\s+/, "$1• ")
          .replace(/\*\*|__|`/g, "")
          .replace(/<[^>]+>/g, "")
          .trimEnd())
      .join("\n")
      .replace(/\n{3,}/g, "\n\n")
      .trim();
  }

  // Обновление — единственное место, где панель просит подтверждения у
  // необратимого действия: программа заменит себя и закроется. Посреди стрима
  // это надо делать осознанно.
  async function installUpdate(btn) {
    const u = (lastState && lastState.update) || {};
    if (!confirm(I18N.t("Обновить до версии {v}?", { v: u.version }) + "\n\n" +
        I18N.t("Программа скачает новую сборку, заменит себя и запустится заново. Музыка на это время прервётся. Настройки, очередь и входы останутся на месте."))) {
      return;
    }
    const d = await post("/api/update/install", btn);
    if (d && d.ok) {
      say(I18N.t("Обновился до {v}, перезапускаюсь…", { v: d.version }));
      // Дальше приложение закроется само, и сокет оборвётся. Панель это уже
      // умеет: покажет «приложение не отвечает» и будет ждать возвращения.
    }
  }

  // ── плейлисты, ждущие решения ──────────────────────────────────────
  //
  // Плейлист не идёт в эфир сам: за него платят одной наградой, а треков в
  // очередь ложится сразу несколько, и полчаса чужой музыки подряд — решение
  // хозяина эфира. Поэтому здесь ровно две кнопки и всё, что нужно для
  // решения: кто заказал, что там играет и сколько это займёт времени.
  //
  // Главное число — не количество треков, а длительность. «Пять треков» никому
  // ничего не говорит, «19 минут» говорит всё.

  function renderPlaylists(s) {
    const list = s.playlists || [];
    const box = $("playlists");
    box.hidden = list.length === 0;
    if (list.length === 0) {
      box.innerHTML = "";
      return;
    }

    box.innerHTML = `
      <div class="kicker">
        Ждут решения <span class="cnt">${list.length}</span>
      </div>
      ${list.map((p) => {
        const shown = p.tracks || [];
        const more = p.total > shown.length
          ? `<span class="pl-more">${I18N.t("из {n} в плейлисте", { n: p.total })}</span>` : "";
        return `
        <div class="pl">
          <div class="pl-head">
            <div>
              <div class="pl-title">${esc(p.title || I18N.t("Плейлист"))}</div>
              <div class="pl-sub">
                ${icon("user")}${I18N.t("заказал")} <b>${esc(p.requester)}</b>
                <span class="sep">/</span> ${esc(p.source)}
                <span class="sep">/</span> ${shown.length} ${
                  plural(shown.length, "трек", "трека", "треков", "track", "tracks")} ${more}
              </div>
            </div>
            <div class="pl-time">${minutesLeft(p.length_ms)}</div>
          </div>
          <ol class="pl-tracks">
            ${shown.map((t) => `<li>${esc(t)}</li>`).join("")}
          </ol>
          <div class="acts">
            <button class="act key small" data-pl="approve" data-id="${esc(p.id)}">
              ${icon("circle-check")}Одобрить
            </button>
            <button class="act small" data-pl="reject" data-id="${esc(p.id)}">
              ${icon("x")}Отклонить, вернуть баллы
            </button>
          </div>
        </div>`;
      }).join("")}`;
  }

  // ── заказы с YouTube, ждущие решения ────────────────────────────────
  //
  // Трека нет в Spotify, а на YouTube нашёлся. В режиме «спрашивать» он
  // ждёт здесь: рядом то, что написал зритель, и то, что нашлось, — чтобы
  // сразу было видно, то ли это. Обратный отсчёт — до решения «само».

  function renderYouTubeAsks(s) {
    const list = s.youtube_asks || [];
    const box = $("ytasks");
    box.hidden = list.length === 0;
    if (list.length === 0) {
      box.innerHTML = "";
      return;
    }
    box.innerHTML = `
      <div class="kicker">
        Нет в Spotify — включить с YouTube? <span class="cnt">${list.length}</span>
      </div>
      ${list.map((a) => `
        <div class="pl">
          <div class="pl-head">
            <div>
              <div class="pl-title">${esc(trackLine(a.artist, a.title))}</div>
              <div class="pl-sub">
                ${icon("user")}${I18N.t("заказал")} <b>${esc(a.requester)}</b>
                <span class="sep">/</span> ${I18N.t("написал «{text}»", { text: esc(a.request) })}
                <span class="sep">/</span> <a href="${esc(a.url)}" target="_blank" rel="noopener">открыть ролик</a>
              </div>
            </div>
            <div class="pl-time">${a.duration_ms > 0 ? mmss(a.duration_ms) : ""}</div>
          </div>
          <div class="acts">
            <button class="act key small" data-yta="play" data-id="${esc(a.id)}">
              ${icon("play")}Включить
            </button>
            <button class="act small" data-yta="reject" data-id="${esc(a.id)}">
              ${icon("x")}Отклонить, вернуть баллы
            </button>
            <span class="hint yt-left" data-until="${esc(a.until || "")}" data-else="${esc(a.else)}"
                  style="margin:0"></span>
          </div>
        </div>`).join("")}`;
    tickYouTubeAsks();
  }

  // tickYouTubeAsks обновляет «осталось N с» без перерисовки карточек.
  function tickYouTubeAsks() {
    document.querySelectorAll("#ytasks .yt-left").forEach((el) => {
      const until = Date.parse(el.dataset.until);
      if (!until || until < Date.UTC(2000, 0, 1)) {
        el.textContent = I18N.t("ждёт, пока не решишь");
        return;
      }
      const left = Math.max(0, Math.round((until - Date.now()) / 1000));
      const then = el.dataset.else === "play" ? I18N.t("включится сам") : I18N.t("пропустится");
      el.textContent = I18N.t("через {n} с {then}", { n: left, then });
    });
  }
  setInterval(tickYouTubeAsks, 1000);

  // Раздел «YouTube»: недавно сыгравшие с YouTube. Видно, что именно нашлось
  // по заказу, — промах здесь замечают раньше, чем в чате.
  function renderYouTubeRecent(list) {
    const box = $("yt-recent");
    if (!box) return;
    list = list || [];
    if (list.length === 0) {
      box.innerHTML = `<div class="hint" style="margin-top:0">Пока ничего: с YouTube за этот запуск не играло.</div>`;
      return;
    }
    box.innerHTML = `<ol class="pl-tracks">${list.map((r) => `
      <li><a href="${esc(r.url)}" target="_blank" rel="noopener">${esc(trackLine(r.artist, r.title))}</a>
        <span class="hint" style="margin:0">— ${esc(r.requester)}: «${esc(r.request)}»</span></li>`).join("")}</ol>`;
  }

  function fillYouTubeMode() {
    if (!config) return;
    const mode = config.youtube_mode || "ask";
    document.querySelectorAll('input[name="yt-mode"]').forEach((r) => { r.checked = r.value === mode; });
    $("yt-ask-opts").hidden = mode !== "ask";
    $("yt-ask-seconds").value = String(config.youtube_ask_seconds ?? 60);
    $("yt-ask-else").value = config.youtube_ask_else || "skip";
  }

  function renderOrders(s) {
    const list = s.queue;
    const box = $("orders");

    $("orders-count").textContent = list.length;
    $("orders-count").classList.toggle("zero", list.length === 0);

    const left = list.reduce((sum, i) => sum + i.duration_ms, 0);
    $("queue-rest").textContent = list.length ? I18N.t("ещё {time}", { time: minutesLeft(left) }) : "";

    $("queue-pause").textContent = s.paused ? I18N.t("возобновить заказы") : I18N.t("остановить заказы");
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
              <div class="preview-prompt">${I18N.t("Напиши артиста и название или ссылку. Если трек не найдётся — баллы вернутся.")}</div>
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
            <span class="ttl">${esc(trackLine(r.artist, r.title))}</span>
            <span class="sub">
              ${r.provider === "youtube" ? `<span class="tag miss">YouTube</span>` : ""}
              ${esc(r.requester)}
              <span class="tag">${r.starting ? "запускается…" : "заиграет сразу после трека стримера"}</span>
            </span>
          </span>
          <span class="len">${r.duration_ms > 0 ? mmss(r.duration_ms) : "—"}</span>
          <span class="deal"></span>
        </li>`;
      }
      return `<li class="${fresh.trim()}" draggable="true" data-id="${r.id}">
        <span class="grip" title="перетащи, чтобы поменять порядок">${icon("grip-vertical")}</span>
        <span class="idx">${String(i + 1).padStart(2, "0")}</span>
        <span class="body">
          <span class="ttl">${esc(trackLine(r.artist, r.title))}</span>
          <span class="sub">
            ${r.source === "donation" ? `<span class="chip money">донат</span>` : ""}
            ${r.uncertain ? `<span class="tag doubt" title="совпадение неточное">неточно</span>` : ""}
            ${r.provider === "youtube" ? `<span class="tag miss">YouTube</span>` : ""}
            ${esc(r.requester)}
            ${r.raw_request ? `<span class="asked" title="${esc(r.raw_request)}">${esc(r.raw_request)}</span>` : ""}
          </span>
        </span>
        <span class="len">${r.duration_ms > 0 ? mmss(r.duration_ms) : "—"}</span>
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
      // Пока тянут ползунок, полоса слушается руки, а не часов: иначе она
      // прыгала бы обратно каждую секунду прямо под пальцем.
      if (seeking) return;
      const at = Math.min(meterLength, Math.max(0, Date.now() - meterFrom));
      bar.style.width = (at / meterLength) * 100 + "%";
      $("at").textContent = mmss(at);
    };
    meterTick();

    // На паузе часы не идут. Полоса считается у нас, а не спрашивается у
    // Spotify, — значит и останавливать её надо самим, иначе трек «доиграет»
    // на экране, пока в колонках тишина.
    if (now.paused) return;
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
          .then((r) => { if (!r.ok) say(I18N.t("Не смог поменять порядок"), true); })
          .catch(() => say(I18N.t("Не смог поменять порядок"), true));
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
    confirmThen(btn, I18N.t("очистить и вернуть баллы?"),
      () => post("/api/queue/clear?refund=1", btn));
  };

  $("ban-add").onclick = (e) => {
    const login = $("ban-login").value.trim().replace(/^@/, "");
    if (!login) return;
    confirmThen(e.currentTarget, I18N.t("закрыть заказы для {login}?", { login }), async () => {
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
      ? `${minutes} ${plural(minutes, "минута", "минуты", "минут", "min", "min")}`
      : I18N.t("{h} ч {m} мин", { h: Math.floor(minutes / 60), m: minutes % 60 });

    const cell = (value, label) =>
      `<div class="cell"><div class="num">${value}</div><div class="lab">${label}</div></div>`;

    $("tally").innerHTML =
      cell(ses.orders, I18N.t("заказов")) +
      cell(ses.accepted, I18N.t("принято")) +
      cell(ses.refunded, I18N.t("возвращено")) +
      cell(uptime, I18N.t("в работе"));
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
      ? I18N.t("свернуть")
      : I18N.t("ещё {n} {entries}", { n: hidden, entries: plural(hidden, "запись", "записи", "записей", "entry", "entries") });
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
      <div class="what">${I18N.t("Открой")} <a href="${esc(safeURL(tw.pending_url))}" target="_blank"
        rel="noopener">${esc(tw.pending_url)}</a> ${I18N.t("на любом устройстве и введи код:")}</div>
      <div class="code">${esc(tw.pending_code)}</div>
      <div class="left" id="code-left"></div>`;

    // Единственный таймер в панели, и живёт он только пока висит код: без него
    // человек не понимает, сколько у него ещё есть времени.
    const tick = () => {
      const el = $("code-left");
      if (!el) return;
      const left = new Date(tw.pending_expires) - Date.now();
      if (left <= 0) {
        el.textContent = I18N.t("код истёк — нажми «Подключить Twitch» ещё раз");
        clearInterval(bannerTimer);
        return;
      }
      el.textContent = I18N.t("код действует ещё {time}", { time: minutesLeft(left) });
    };
    tick();
    bannerTimer = setInterval(tick, 20000);
  }

  // ── карточки аккаунтов ─────────────────────────────────────────────

  // Доступ к Twitch у публичных приложений живёт тридцать дней. Показываем
  // дату всегда, а не только перед концом: тогда это не сюрприз.
  function renewLine(tw) {
    if (!tw.renew_at) return "";
    const when = new Date(tw.renew_at).toLocaleDateString(I18N.locale(),
      { day: "numeric", month: "long" });
    return tw.renew_soon
      ? `<div class="note"><b>TW-04</b> ${I18N.t("Доступ кончается {when} — нажми «Подключить Twitch» ещё раз.", { when })}</div>`
      : `<div class="plan">${I18N.t("доступ действует до {when}", { when })}</div>`;
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
    const at = until.toLocaleTimeString(I18N.locale(), { hour: "2-digit", minute: "2-digit" });
    const what = I18N.t(pausedParts[sp.paused_part] || "часть возможностей пока не работает");
    return note("SP-08", I18N.t("Spotify ограничил приложение до {at}: {what}. ", { at, what }) +
      I18N.t("Это не поломка и не обход блокировок — Spotify снимет ограничение сам. ") +
      I18N.t("Кнопка «Проверить связь» пробует ещё раз."));
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
      <div class="who">${esc(sp.account || I18N.t("аккаунт без имени"))}</div>
      ${sp.email ? `<div class="mail">${esc(sp.email)}</div>` : ""}
      ${sp.proxy ? `<div class="mail">${I18N.t("через {proxy}", { proxy: esc(sp.proxy) })}</div>` : ""}
      <div class="plan">${esc(sp.plan_label)}${
        sp.country ? " · " + I18N.t("страна аккаунта {c}", { c: esc(sp.country) }) : ""}</div>
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
        ? I18N.t("ключ сохранён ({hint}) — вставлять заново не надо", { hint: t.hint })
        : I18N.t("vless://… , trojan://… , ss://… или https://…");
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
    const [wordRu, mood] = look[t.phase] || look.off;
    const word = I18N.t(wordRu);

    // Три шага подключения галочками и одна строчка «что это значит».
    //
    // Появилось 11.10: тестер не понимал, подключилось ли вообще, и боялся,
    // что приложение пустило через VPN весь компьютер. Отсюда прямая фраза
    // про остальной интернет — она правда: через обход идёт только Spotify.
    const step = (done, text) =>
      `<div class="plan">${done ? "✓" : "…"} ${text}</div>`;
    const live = t.phase === "connected";
    const meaningRu = {
      connected: "Spotify идёт через обход. Остальной интернет — напрямую, как обычно.",
      connecting: "Ищу рабочий сервер из твоего ключа. Первый раз — до пары минут.",
      reconnecting: "Сервер отвалился, ищу другой. Заказы подождут.",
      standby: "Spotify сейчас отвечает и без обхода. Перестанет — включу сам.",
      down: "Что сделать: нажми «Включить / перепроверить». Не помогло — ключ устарел, вставь новый.",
      off: "Нажми «Включить / перепроверить».",
    }[t.phase];
    const meaning = meaningRu ? I18N.t(meaningRu) : "";

    box.className = "account " + mood;
    box.innerHTML = `
      <div class="who">${word}</div>
      ${step(true, I18N.t("ключ сохранён · ") + esc(t.hint))}
      ${step(live && t.server, live && t.server ? I18N.t("сервер: ") + esc(t.server) : I18N.t("сервер ещё не выбран"))}
      ${step(live, live ? I18N.t("Spotify отвечает через обход") : I18N.t("Spotify через обход ещё не проверен"))}
      ${t.from_cache ? `<div class="plan">список серверов взят из памяти — сервис подписки не ответил</div>` : ""}
      ${meaning ? `<div class="plan"><b>${meaning}</b></div>` : ""}
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
      <div class="who">${esc(tw.channel || I18N.t("канал"))}</div>
      <div class="mail">${esc(tw.channel_type)}</div>
      <div class="plan">${tw.reward_ready
        ? I18N.t("награда «{title}» · {n} {points}", { title: esc(tw.reward_title), n: tw.reward_cost,
            points: plural(tw.reward_cost, "балл", "балла", "баллов", "point", "points") })
        : tw.has_points ? I18N.t("награда не создана") : I18N.t("заказы за баллы недоступны")}</div>
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
      select.innerHTML = `<option value="">${I18N.t("системное устройство")}</option>` +
        y.devices.map((d) => `<option value="${esc(d.id)}">${esc(d.name)}</option>`).join("");
    }
    // Что выбрано — знает конфиг. Поле y.device сервер заполняет один раз при
    // старте и только если yt-dlp с mpv на месте: без этого стример выбирал
    // виртуальный кабель, обновлял вкладку и снова видел «системное».
    const want = (config && config.audio_device) || y.device || "";
    if (want && select.value !== want) select.value = want;
  }

  $("yt-browser").onchange = async (e) => {
    if (await saveConfig({ youtube_browser: e.target.value })) {
      say(I18N.t("Сохранено. Применится к следующему заказу с YouTube."));
    }
  };

  $("yt-device").onchange = async (e) => {
    if (await saveConfig({ audio_device: e.target.value })) {
      say(I18N.t("Устройство сохранено. Оно применится к следующему заказу с YouTube."));
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

  const MENU_SECTIONS = ["settings", "youtube", "widget", "language", "log"];

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
      // У раздела «YouTube» своей кнопки в верхней полосе нет: он нужен
      // редко, и полоса и так полна.
      const btn = $(id + "-toggle");
      if (btn) {
        btn.classList.toggle("active", on);
        btn.setAttribute("aria-expanded", String(on));
      }
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

  // Справочник команд собирает приложение — знак команд настраивается, и
  // готовый текст в панели рано или поздно разошёлся бы с настоящим.
  // Показываем ровно то, что уйдёт файлом: стример должен видеть, что шлёт.
  let modsLoaded = false;

  async function loadMods() {
    if (modsLoaded) return;
    try {
      const text = await (await fetch("/api/mods")).text();
      $("mods-text").textContent = text;
      modsLoaded = true;
    } catch {
      $("mods-text").textContent = I18N.t("Не смог прочитать справочник");
    }
  }

  function fillTab(tab) {
    // Единственное здесь, что стоит запроса к Spotify, — список плейлистов.
    // Раньше он запрашивался при любом заходе в настройки, даже если человек
    // пришёл за громкостью YouTube. Норма запросов у Spotify тесная, и такие
    // «на всякий случай» из неё и складываются.
    if (tab === "fallback") loadPlaylists();
    if (tab === "orders") loadMods();
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
      if (config.language && config.language !== I18N.lang()) I18N.setLang(config.language);
      $("client-id").value = config.spotify_client_id || "";
      $("proxy").value = config.spotify_proxy || "";
      $("twitch-id").value = config.twitch_client_id || "";
      $("da-id").value = config.donationalerts_client_id || "";
      $("dp-key").value = config.donatepay_key || "";
      $("dx-key").value = config.donatex_key || "";
      $("donation-min").value = config.donation_min ?? "";
      $("command-prefix").value = config.command_prefix || "!";
      $("yt-browser").value = config.youtube_browser || "";
      $("yt-device").value = config.audio_device || "";
    $("yt-volume").value = config.youtube_volume ?? 100;
    $("yt-volume-val").textContent = ($("yt-volume").value) + "%";
      fillYouTubeMode();
      $("verbose-log").checked = !!config.verbose_log;

      $("reward-title").value = config.reward_title || "";
      $("reward-cost").value = config.reward_cost ?? "";
      $("auto-reward").checked = !!config.auto_create_reward;
      fillPlaylistReward();
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
      say(I18N.t("Не смог прочитать настройки"), true);
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
      ? I18N.t("Плейлист не выбран — пока работает как «ничего не включать».")
      : I18N.t("Самый предсказуемый вариант: заранее знаешь, что заиграет.");

    const hint = $("playlist-hint");
    hint.className = broken ? "hint warn" : "hint";
    hint.textContent = broken ? I18N.t("Выбери плейлист, иначе режим не сработает.") : "";
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
      // Цена плейлиста считается от цены трека и числа треков, то есть от
      // соседних полей. Без пересчёта здесь стример менял цену трека и видел
      // под ползунком старую арифметику — а на канал уезжала уже новая.
      if ($("pl-sum")) drawPlaylistSum();
      return true;
    } catch {
      say(I18N.t("Не смог сохранить настройки"), true);
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
      const what = p.error ? esc(p.error) : `${p.http || I18N.t("нет ответа")}`;
      return `<div>${ok ? "✅" : "❌"} ${esc(p.way)} — <b>${what}</b>` +
        `${ok ? I18N.t(", нашлось {n}", { n: p.found }) : ""}</div>`;
    }).join("");
    box.innerHTML = `<b>${I18N.t("Проверка поиска: прошло {ok} из {total}.", { ok: d.ok, total: d.total })}</b>` +
      `${rows}<br>${I18N.t("Теперь нажми «Сохранить лог и историю» и пришли архив.")}`;
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
        $("playlist-hint").textContent = errLine(list);
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
      $("playlist").innerHTML = `<option value="">${I18N.t("— не выбран —")}</option>` +
        list.map((p) => `<option value="${esc(p.id)}" data-name="${esc(p.name)}"${
          p.id === chosen ? " selected" : ""}>${esc(p.name)}${
          // Ноль здесь чаще значит «Spotify не сказал», чем «пусто»: писать
          // «0 треков» у плейлиста, который стример сам же и слушает, — врать.
          p.tracks > 0
            ? ` · ${p.tracks} ${plural(p.tracks, "трек", "трека", "треков", "track", "tracks")}`
            : ""}</option>`).join("");
      playlistsLoaded = true;
      refreshModeHints();
    } catch {
      say(I18N.t("Не смог получить список плейлистов"), true);
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
        ? I18N.t("Spotify отвечает и без обхода — держу обход наготове")
        : I18N.t("Обход работает: {server}", { server: d.server }));
    }
    return d;
  }

  const actions = {
    login: (btn) => saveThenConnect("client-id", "spotify_client_id",
      (b) => post("/api/spotify/login", b), btn).then((d) => {
      if (!d) return;
      // Показываем адрес возврата: если вход не пройдёт, первым делом сверяют
      // именно эту строку с тем, что вписано в настройках Spotify.
      $("redirect").textContent = I18N.t("Адрес возврата: {uri}", { uri: d.redirect_uri });
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
    // Ответ нужен и здесь: без него внизу оставалась надпись от прошлой
    // кнопки, и казалось, что «Выключить» не сработало.
    "tunnel-off": (btn) => post("/api/tunnel/off", btn).then((d) => {
      if (d) say(I18N.t("Обход выключен. Spotify идёт напрямую."));
    }),
    "tunnel-forget": (btn) => new Promise((done) => {
      confirmThen(btn, I18N.t("забыть ключ?"), async () => {
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
    check: (btn) => {
      // Spotify объявил паузу — стучаться сейчас можно, но не бесплатно.
      //
      // Кнопка нарочно сбрасывает объявленную паузу: человек её видел и решил
      // попробовать. Беда в том, что каждый такой стук ограничение продлевает,
      // а выглядит кнопка безобидно. В ночь на 10.09 их набралось тринадцать
      // за час, пока Spotify просил подождать двадцать один час. Теперь она
      // спрашивает.
      const until = lastState && lastState.spotify && lastState.spotify.paused_until;
      if (until && new Date(until) > new Date()) {
        const at = new Date(until).toLocaleString(I18N.locale(),
          { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
        const ask = I18N.t("Spotify ограничил приложение и просит подождать до {at}.", { at }) + "\n\n" +
          I18N.t("Каждая проверка сейчас может продлить ограничение. Само оно снимется, ничего делать не надо.") + "\n\n" + I18N.t("Всё равно проверить?");
        if (!confirm(ask)) return Promise.resolve(null);
      }
      return post("/api/spotify/check", btn).then((d) => {
        // Связь починили — список плейлистов имеет смысл собрать заново.
        if (playlistsFailed) {
          playlistsLoaded = false;
          loadPlaylists();
        }
        return d;
      });
    },
    // Пробник поиска: жмут его по моей просьбе, когда поиск отказывает без
    // видимой причины. Главное здесь — не картинка в панели, а строки «проба
    // поиска» в логе; на экране просто видно, что проверка дошла до конца.
    probe: (btn) => post("/api/spotify/probe", btn).then((d) => {
      showProbe(d);
      return d;
    }),
    snapshot: (btn) => post("/api/spotify/snapshot", btn),
    skip: (btn) => post("/api/queue/skip", btn),
    // Короткий сигнал на устройство заказов с YouTube: услышать, куда оно
    // ведёт, до того как на стриме заиграет настоящий заказ.
    "yt-test": (btn) => post("/api/youtube/test-sound", btn).then((d) => {
      if (d && d.ok) say(I18N.t("Сигнал отправлен. Не услышал — выбери другое устройство."));
    }),
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
    // Кнопки плеера. Каждая — одно действие человека и один запрос: см.
    // internal/server/playercontrol.go, там же про норму запросов Spotify.
    const play = e.target.closest("[data-play]");
    if (play) {
      const what = play.dataset.play;
      if (what === "mute") {
        const el = $("vol");
        if (!el) return;
        const now = parseInt(el.value, 10);
        // Второе нажатие возвращает туда, где было. Помним у себя: спрашивать
        // Spotify «а сколько было» — лишний запрос ради того, что мы и так
        // только что видели.
        const want = now > 0 ? 0 : (volumeWas || 100);
        if (now > 0) volumeWas = now;
        el.value = want;
        $("vol-val").textContent = want + "%";
        send("/api/player/volume", { percent: want });
        return;
      }
      send("/api/player/" + what);
      return;
    }

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

    // Заказы с YouTube, ждущие решения.
    const yta = e.target.closest("[data-yta]");
    if (yta) {
      post(`/api/youtube-asks/${encodeURIComponent(yta.dataset.id)}/${yta.dataset.yta}`, yta);
      return;
    }

    // Плейлисты, ждущие решения. Одобрение может занять секунды: у плейлиста
    // Яндекса каждый трек ещё надо найти в Spotify. Поэтому кнопка гаснет на
    // время запроса — post() делает это сам.
    const pl = e.target.closest("[data-pl]");
    if (pl) {
      const path = `/api/playlists/${encodeURIComponent(pl.dataset.id)}/${pl.dataset.pl}`;
      post(path, pl);
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
    $("fix-was").textContent = trackLine(item.artist, item.title);
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
    $("fix-note").textContent = I18N.t("Ищу…");

    try {
      const r = await fetch(`/api/search?q=${encodeURIComponent(query)}`);
      const data = await r.json();
      // Пока искали, стример успел набрать другое — старый ответ выкидываем.
      if (mine !== fixSeq) return;

      if (!r.ok) {
        $("fix-note").textContent = errLine(data);
        return;
      }
      if (!data.length) {
        $("fix-note").textContent = I18N.t("Ничего не нашлось. Попробуй иначе или вставь ссылку Spotify.");
        $("fix-results").innerHTML = "";
        return;
      }

      $("fix-note").textContent = "";
      $("fix-results").innerHTML = data.map((t) => `
        <li>
          <button class="pick" data-pick="${esc(t.id)}">
            <span class="pick-art">${t.cover_url
              ? `<img src="${esc(coverSrc(t.cover_url))}" alt="" loading="lazy">` : ""}</span>
            <span class="pick-text">
              <span class="pick-title">${esc(t.title)}</span>
              <span class="pick-sub">${esc(t.artist)}${t.album ? " · " + esc(t.album) : ""}</span>
            </span>
            <span class="pick-len">${t.duration_ms > 0 ? mmss(t.duration_ms) : "—"}</span>
          </button>
        </li>`).join("");
    } catch {
      if (mine === fixSeq) $("fix-note").textContent = I18N.t("Поиск не ответил");
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
        $("fix-note").textContent = data.error || I18N.t("Не получилось заменить трек");
        btn.disabled = false;
        return;
      }
      closeFix();
      say(I18N.t("Трек заменён. Такой же заказ впредь найдётся сразу."));
    } catch {
      $("fix-note").textContent = I18N.t("Приложение не ответило");
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
    if (e.key === "Escape" && !$("conn-pop").hidden) { toggleConns(false); return; }
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
          ? I18N.t("По этому запросу ничего нет.")
          : I18N.t("Пока пусто. Здесь окажется всё, что заказывали.");
        return;
      }
      $("hist-note").textContent = "";
      $("hist").innerHTML = rows.map((h) => {
        const [word, tone] = outcomeWord[h.outcome] || [h.outcome, ""];
        const name = h.artist || h.title ? esc(trackLine(h.artist, h.title)) : esc(h.raw);
        return `<li>
          <span class="body">
            <span class="ttl">${name}</span>
            <span class="sub">
              <span class="tag ${tone}">${esc(I18N.t(word))}</span>
              ${esc(h.requester)}
              ${h.reason ? `<span class="asked">${esc(h.reason)}</span>` : ""}
            </span>
          </span>
          <span class="len">${when(h.at)}</span>
        </li>`;
      }).join("");
    } catch {
      $("hist-note").textContent = I18N.t("Не смог прочитать хронику");
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
    const time = d.toLocaleTimeString(I18N.locale(), { hour: "2-digit", minute: "2-digit" });
    const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const diff = (day(new Date()) - day(d)) / 86400000;
    if (diff === 0) return time;
    if (diff === 1) return I18N.t("вчера {time}", { time });
    return d.toLocaleDateString(I18N.locale(), { day: "numeric", month: "short" }) + " " + time;
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

  $("conn-toggle").onclick = (e) => {
    e.stopPropagation();
    toggleConns();
  };
  // Клик мимо окошка закрывает его — так ведут себя все всплывающие окошки, и
  // отдельной кнопки «закрыть» для этого заводить не надо.
  document.addEventListener("click", (e) => {
    if (!$("conn-pop").hidden && !e.target.closest(".conn-slot")) toggleConns(false);
  });

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

  $("save-prefix").onclick = async () => {
    const prefix = $("command-prefix").value.trim() || "!";
    $("command-prefix").value = prefix;
    if (await saveConfig({ command_prefix: prefix })) {
      // Справочник собирает приложение, и знак в нём тоже сменился — читаем
      // заново, иначе стример отправит модераторам старый.
      modsLoaded = false;
      loadMods();
      say(I18N.t("Знак команд теперь «{p}»", { p: prefix }));
    }
  };

  $("save-id").onclick = async () => {
    // Пустое поле не сохраняем: раньше на это отвечало «сохранён» —
    // и человек шёл дальше, уверенный, что всё вставил.
    if (!$("client-id").value.trim()) {
      say(I18N.t("Поле пустое — сначала вставь Client ID Spotify."), true);
      return;
    }
    if (await saveConfig({ spotify_client_id: $("client-id").value.trim() })) {
      say(I18N.t("Client ID сохранён. Теперь нажми «Подключить Spotify»."));
    }
  };
  $("save-proxy").onclick = async () => {
    if (await saveConfig({ spotify_proxy: $("proxy").value.trim() }, "?proxy=1")) {
      say($("proxy").value.trim()
        ? I18N.t("Прокси включён. Самой программе Spotify он не нужен — нажми «Проверить связь».")
        : I18N.t("Прокси убран, Spotify идёт напрямую."));
    }
  };

  $("save-da-id").onclick = async () => {
    // Пустое поле не сохраняем: раньше на это отвечало «сохранён» —
    // и человек шёл дальше, уверенный, что всё вставил.
    if (!$("da-id").value.trim()) {
      say(I18N.t("Поле пустое — сначала вставь Client ID DonationAlerts."), true);
      return;
    }
    if (await saveConfig({ donationalerts_client_id: $("da-id").value.trim() })) {
      say(I18N.t("Сохранено. Теперь нажми «Подключить DonationAlerts»."));
    }
  };
  $("save-dp-key").onclick = async () => {
    if (await saveConfig({ donatepay_key: $("dp-key").value.trim() })) {
      // Пустое поле — это «убрать ключ», и сказать надо именно так.
      say($("dp-key").value.trim()
        ? I18N.t("Ключ DonatePay сохранён. Перезапусти приложение, чтобы он заработал.")
        : I18N.t("Ключ DonatePay убран."));
    }
  };
  $("save-dx-key").onclick = async () => {
    if (await saveConfig({ donatex_key: $("dx-key").value.trim() })) {
      // Пустое поле — это «убрать ключ», и сказать надо именно так.
      say($("dx-key").value.trim()
        ? I18N.t("Ключ DonateX сохранён. Перезапусти приложение, чтобы он заработал.")
        : I18N.t("Ключ DonateX убран."));
    }
  };

  $("donation-min").onchange = async (e) => {
    const value = parseFloat(e.target.value.replace(",", "."));
    if (!isFinite(value) || value < 0) {
      say(I18N.t("Сумма должна быть числом"), true);
      // Возвращаем то, что сохранено: иначе на экране остаётся мусор, а
      // человек уходит уверенным, что порог изменён.
      e.target.value = config.donation_min ?? "";
      return;
    }
    if (await saveConfig({ donation_min: value })) markSaved(e.target);
  };

  $("save-twitch-id").onclick = async () => {
    // Пустое поле не сохраняем: раньше на это отвечало «сохранён» —
    // и человек шёл дальше, уверенный, что всё вставил.
    if (!$("twitch-id").value.trim()) {
      say(I18N.t("Поле пустое — сначала вставь Client ID Twitch."), true);
      return;
    }
    if (await saveConfig({ twitch_client_id: $("twitch-id").value.trim() })) {
      say(I18N.t("Client ID Twitch сохранён. Теперь нажми «Подключить Twitch»."));
    }
  };

  document.querySelectorAll('input[name="yt-mode"]').forEach((radio) => {
    radio.onchange = async () => {
      $("yt-ask-opts").hidden = radio.value !== "ask";
      if (await saveConfig({ youtube_mode: radio.value })) {
        say(I18N.t({ ask: "Буду спрашивать про каждый заказ с YouTube.",
              auto: "Заказы с YouTube будут включаться сами.",
              off: "С YouTube больше не играем — такие заказы отклоняются." }[radio.value]));
      }
    };
  });
  $("verbose-log").onchange = async (e) => {
    if (await saveConfig({ verbose_log: e.target.checked })) {
      say(e.target.checked
        ? I18N.t("Подробный журнал включён. Первые цифры о компьютере — через полминуты.")
        : I18N.t("Подробный журнал выключен."));
    }
  };
  $("yt-ask-seconds").onchange = async (e) => {
    if (await saveConfig({ youtube_ask_seconds: parseInt(e.target.value, 10) })) markSaved(e.target);
  };
  $("yt-ask-else").onchange = async (e) => {
    if (await saveConfig({ youtube_ask_else: e.target.value })) markSaved(e.target);
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
        say(I18N.t("Нужно число от {min} до {max}", { min, max }), true);
        e.target.value = Math.round((config[key] || 0) / scale) || "";
        return;
      }
      if (await saveConfig({ [key]: value * scale })) {
        e.target.value = value;
        markSaved(e.target);
      }
    };
  }

  // ── награда за плейлист ────────────────────────────────────────────
  //
  // Цена не вписывается руками, а считается: цена трека × число треков −
  // скидка. Так она сама едет следом за настройками — поднял цену трека,
  // плейлист подорожал. Вписанная руками через неделю правок разошлась бы с
  // поштучной и стала бы либо подарком, либо грабежом, а заметил бы это
  // стример не сразу. Своя цена всё-таки возможна: круглое число красивее.
  //
  // Тот же расчёт живёт в internal/config/playlist.go. Здесь он повторён
  // ради подписи «во сколько это выходит за трек»: считать её на сервере и
  // гонять по сокету ради строчки под ползунком незачем.
  function playlistPrice() {
    if (config.playlist_cost > 0) return config.playlist_cost;
    const track = config.reward_cost || 0;
    const count = playlistCount();
    const off = Math.min(90, Math.max(0, config.playlist_discount ?? 20));
    return Math.max(track, Math.floor(track * count * (100 - off) / 100)) || 1;
  }

  function playlistCount() {
    return Math.min(20, Math.max(1, config.playlist_max_tracks || 5));
  }

  function fillPlaylistReward() {
    const on = !!config.playlist_reward;
    $("pl-on").checked = on;
    $("pl-fields").hidden = !on;

    $("pl-title").value = config.playlist_reward_title || "";
    $("pl-max").value = playlistCount();
    const off = config.playlist_discount ?? 20;
    $("pl-off").value = off;
    $("pl-off-val").textContent = off + "%";

    const own = (config.playlist_cost || 0) > 0;
    $("pl-own-cost").checked = own;
    $("pl-cost-field").hidden = !own;
    $("pl-cost").value = config.playlist_cost || playlistPrice();
    $("pl-off").disabled = own;

    drawPlaylistSum();
  }

  // Подпись под ползунком. Стример должен видеть, что он предлагает зрителю,
  // а не верить слову «скидка»: сравнение с поштучной ценой и есть весь смысл
  // этой награды.
  function drawPlaylistSum() {
    const count = playlistCount();
    const price = playlistPrice();
    const full = (config.reward_cost || 0) * count;
    const each = Math.floor(price / count);
    const save = full > 0 ? Math.round((1 - price / full) * 100) : 0;

    $("pl-sum").innerHTML = `
      <div class="sum-row"><span>Плейлист целиком</span><b>${price} ${
        plural(price, "балл", "балла", "баллов", "point", "points")}</b></div>
      <div class="sum-row"><span>Это за трек</span><b>${each}</b></div>
      <div class="sum-row"><span>${I18N.t("Те же {n} {tracks} поштучно", { n: count,
        tracks: plural(count, "трек", "трека", "треков", "track", "tracks") })}</span><b>${full}</b></div>
      <div class="sum-row ${save > 0 ? "good" : "bad"}">
        <span>${save > 0 ? I18N.t("Зрителю выгоднее на") : I18N.t("Выгоды нет")}</span>
        <b>${save > 0 ? save + "%" : "—"}</b>
      </div>`;
  }

  $("pl-on").onchange = (e) =>
    saveConfig({ playlist_reward: e.target.checked }).then(() => {
      $("pl-fields").hidden = !e.target.checked;
      markSaved(e.target);
    });

  $("pl-title").onchange = async (e) => {
    const title = e.target.value.trim();
    if (!title) {
      e.target.value = config.playlist_reward_title || "";
      return;
    }
    if (await saveConfig({ playlist_reward_title: title })) markSaved(e.target);
  };

  $("pl-own-cost").onchange = (e) => {
    const on = e.target.checked;
    $("pl-cost-field").hidden = !on;
    $("pl-off").disabled = on;
    // Снимая галочку, обнуляем свою цену — тогда считалка снова берёт своё.
    saveConfig({ playlist_cost: on ? (parseInt($("pl-cost").value, 10) || playlistPrice()) : 0 })
      .then(() => { fillPlaylistReward(); markSaved(e.target); });
  };

  // Ползунок скидки ведём у себя, а пишем по отпусканию: каждая правка цены
  // уезжает на Twitch, и слать её на каждое движение мыши нельзя.
  live($("pl-off"), (v) => {
    $("pl-off-val").textContent = v + "%";
    const was = config.playlist_discount;
    config.playlist_discount = v;
    drawPlaylistSum();
    config.playlist_discount = was;
  }, (v) => saveConfig({ playlist_discount: v }).then(() => drawPlaylistSum()));

  numberField("pl-max", "playlist_max_tracks", { min: 1, max: 20 });
  numberField("pl-cost", "playlist_cost", { min: 1, max: 1000000 });

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
  // Образец — это сам виджет во фрейме, а не похожая вёрстка. Прошлая
  // панель рисовала плашку у себя и масштабировала её своей переменной, на
  // которую умножалась только часть размеров. На 200% текст рос, а обложка
  // и отступы — нет, плашка разваливалась, и человек видел в панели одно,
  // а в OBS другое. Теперь фрейм размером с настоящий кадр (1920×1080 и так
  // далее) просто уменьшен целиком: каждый пиксель на своём месте.

  // Образцы треков. Обложки рисуются на месте — настоящих картинок у
  // панели нет, а без обложки не проверить ни цвета из обложки, ни
  // свечение, ни обложку фоном. Цвета у всех четырёх разные нарочно: так
  // видно, как плашка перекрашивается от трека к треку.
  const demoEN = () => I18N.lang() === "en";
  const WX_TRACKS = [
    // Русские образцы у англоязычного стримера подменяются (геттеры — язык
    // меняется на ходу, без перезагрузки).
    { title: "Bohemian Rhapsody", artist: "Queen", get by() { return demoEN() ? "viewer" : "zritel"; }, len: 354000, at: 149000,
      paint: ["#ff6a3d", "#b3125b", "#2a0a3a"], art: "sun" },
    { get title() { return demoEN() ? "Smells Like Teen Spirit" : "Группа крови"; },
      get artist() { return demoEN() ? "Nirvana" : "Кино"; }, by: "mikhail", len: 286000, at: 61000,
      paint: ["#e8352f", "#5a0b0b", "#0d0606"], art: "stripes" },
    { title: "Everything In Its Right Place", artist: "Radiohead", by: "nastya_k", len: 251000, at: 190000,
      paint: ["#3ad0ff", "#1f47c9", "#081232"], art: "rings" },
    { get title() { return demoEN() ? "Take On Me" : "Плот"; },
      get artist() { return demoEN() ? "a-ha" : "Юрий Лоза"; }, by: "dobryak2000", len: 232000, at: 35000,
      paint: ["#d6f04a", "#2f9e6e", "#0b2a20"], art: "waves" },
  ];
  let wxTrack = 0;
  // Каким показать образец: заказом или своей музыкой. Выбирать набор надо,
  // видя оба случая — строка под артистом у них разная.
  // «Сейчас играет» — образец берёт настоящий трек: свою обложку и
  // название проверить на живом примере нагляднее, чем на выдуманном.
  // Выбор помним между запусками — кто включил живой трек, хочет его и дальше.
  let wxSource = "order";
  try { wxSource = localStorage.getItem("wx-source") || "live"; } catch { wxSource = "live"; }
  // Образец на паузе — по кнопке «Пауза» под кадром.
  let wxPaused = false;
  let wxTab = "style";
  let galleryBuilt = false;
  // Временная правка поверх сохранённого: пока тянут ползунок, таскают
  // плашку или примеряют стиль наведением. На диск она не пишется.
  let wxLive = null;
  let wxApi = null;
  const wxCovers = {};

  function widget() {
    return (config && config.widget) || {};
  }
  const wxView = () => Object.assign({}, widget(), wxLive || {});

  // wxCover рисует обложку образца: градиент и простая фигура. Абстрактная
  // картинка, а не чужая обложка, — но с настоящими цветами, по которым
  // работает всё, что зависит от обложки.
  function wxCover(i) {
    if (wxCovers[i]) return wxCovers[i];
    const t = WX_TRACKS[i];
    const c = document.createElement("canvas");
    c.width = c.height = 240;
    const g = c.getContext("2d");
    const grad = g.createLinearGradient(0, 0, 240, 240);
    grad.addColorStop(0, t.paint[0]);
    grad.addColorStop(.55, t.paint[1]);
    grad.addColorStop(1, t.paint[2]);
    g.fillStyle = grad;
    g.fillRect(0, 0, 240, 240);
    g.globalAlpha = .9;
    if (t.art === "sun") {
      g.fillStyle = "#ffd36e";
      g.beginPath(); g.arc(150, 96, 52, 0, Math.PI * 2); g.fill();
      g.fillStyle = t.paint[2];
      for (let y = 112; y < 160; y += 9) g.fillRect(90, y, 130, 4);
    } else if (t.art === "stripes") {
      g.fillStyle = "#f4efe6";
      for (let x = -240; x < 240; x += 34) {
        g.beginPath(); g.moveTo(x, 240); g.lineTo(x + 240, 0); g.lineTo(x + 252, 0); g.lineTo(x + 12, 240); g.fill();
      }
      g.fillStyle = "#0d0606";
      g.beginPath(); g.arc(120, 120, 44, 0, Math.PI * 2); g.fill();
    } else if (t.art === "rings") {
      g.strokeStyle = "#bff3ff";
      for (let r = 18; r < 170; r += 16) {
        g.lineWidth = r % 32 ? 2 : 5;
        g.beginPath(); g.arc(80, 170, r, 0, Math.PI * 2); g.stroke();
      }
    } else {
      g.strokeStyle = "#f6ffd0";
      g.lineWidth = 6;
      for (let y = 70; y < 230; y += 26) {
        g.beginPath();
        for (let x = 0; x <= 240; x += 8) g.lineTo(x, y + Math.sin((x + y) / 22) * 10);
        g.stroke();
      }
    }
    wxCovers[i] = c.toDataURL("image/jpeg", .9);
    // Пиксельная копия — для плиток «Пикселя»; в кадре её делает сам виджет.
    const p = document.createElement("canvas");
    p.width = p.height = 20;
    p.getContext("2d").drawImage(c, 0, 0, 20, 20);
    wxCovers["px" + i] = p.toDataURL("image/png");
    return wxCovers[i];
  }

  function wxNow() {
    const live = wxSource === "live" && lastState && lastState.now;
    if (live) return Object.assign({}, lastState.now);
    const t = WX_TRACKS[wxTrack];
    return {
      title: t.title, artist: t.artist, requester: t.by,
      source: wxSource === "own" ? "own" : "order", provider: "spotify",
      cover_url: wxCover(wxTrack), duration_ms: t.len, position_ms: t.at,
      paused: wxPaused,
    };
  }

  // ── кадр ───────────────────────────────────────────────────────────

  // Форматы кадра и размер источника OBS для каждого. Высота у широких
  // форматов — 1080, как у обычного стрима: плашка в пикселях остаётся той
  // же, меняется только сколько места вокруг. Список тот же, что в
  // config/widget.go.
  const WX_FRAMES = {
    "16:9":  [1920, 1080],
    "16:10": [1920, 1200],
    "4:3":   [1440, 1080],
    "5:4":   [1350, 1080],
    "21:9":  [2560, 1080],
    "32:9":  [3840, 1080],
    "9:16":  [1080, 1920],
    "1:1":   [1080, 1080],
  };
  const wxFrame = (w) => WX_FRAMES[w.frame] || WX_FRAMES["16:9"];

  // Фрейм зовёт это, когда виджет внутри готов слушаться.
  window.wxReady = () => {
    try { wxApi = $("wx-frame").contentWindow.wxPreview; } catch { wxApi = null; }
    if (!wxApi) return;
    wxApi.setLang(I18N.lang());
    wxPaint();
    wxApi.track(wxNow());
    $("wx-source-pick").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b.dataset.source === wxSource));
    wxLiveNote();
  };

  function wxBoot() {
    const f = $("wx-frame");
    if (!f.getAttribute("src")) f.src = "/widget?preview=1";
  }

  // wxPaint отдаёт виджету во фрейме то, что сейчас видно в панели:
  // сохранённое плюс временную правку.
  function wxPaint() {
    if (wxApi) wxApi.dress(wxView());
    wxLayout();
  }

  // wxRatio — во сколько раз образец меньше настоящего кадра.
  function wxRatio() {
    const box = $("wx-stage").getBoundingClientRect();
    return box.width > 0 ? box.width / wxFrame(wxView())[0] : 1;
  }

  // Кадр образца целиком — честный, но плашка в нём мелкая: в кадре
  // 1920×1080 она и правда занимает пятую часть ширины. Чтобы разглядеть
  // шрифт и анимацию, кадр можно приблизить к плашке — «Крупно».
  // wxCam — как сейчас показан кадр: масштаб и сдвиг в пикселях окошка.
  let wxZoom = false;
  let wxCam = { k: 1, x: 0, y: 0 };
  // Пока плашку тащат, камера стоит: иначе кадр ехал бы за плашкой, а
  // плашка — за курсором, и они догоняли бы друг друга.
  let wxCamHold = false;

  // wxLayout подгоняет фрейм под окошко образца: фрейм ровно размером с
  // кадр OBS, а уменьшен целиком, одним преобразованием.
  function wxLayout() {
    const [fw, fh] = wxFrame(wxView());
    const stage = $("wx-stage");
    stage.style.setProperty("--wx-ar", fw + " / " + fh);
    stage.style.setProperty("--wx-arn", (fw / fh).toFixed(4));
    const f = $("wx-frame");
    f.style.width = fw + "px";
    f.style.height = fh + "px";

    if (!wxCamHold) {
      const box = stage.getBoundingClientRect();
      const k = wxRatio();
      wxCam = { k, x: 0, y: 0 };
      if (wxZoom && wxApi && box.width > 0) {
        const r = wxApi.slot.getBoundingClientRect();
        // Плашка занимает примерно половину окошка — крупнее уже не видно,
        // как она сидит в кадре.
        const z = Math.max(1, Math.min(5, (box.width * .5) / Math.max(1, r.width * k)));
        const K = k * z;
        let x = box.width / 2 - (r.left + r.width / 2) * K;
        let y = box.height / 2 - (r.top + r.height / 2) * K;
        x = Math.min(0, Math.max(box.width - fw * K, x));
        y = Math.min(0, Math.max(box.height - fh * K, y));
        wxCam = { k: K, x, y };
      }
    }
    $("wx-frame-box").style.transform =
      "translate(" + wxCam.x + "px, " + wxCam.y + "px) scale(" + wxCam.k + ")";
    stage.classList.toggle("zoomed", wxZoom);
    $("wx-zoom").classList.toggle("on", wxZoom);

    const w = wxView();
    $("wx-badge").textContent = fw + "×" + fh + " · " + Math.round((w.scale || 1) * 100) + "%" +
      (wxZoom ? " · " + I18N.t("крупно") : "");
    // В режиме своей картинки подсказка — про картинку: мышь в кадре
    // сейчас двигает её, а не плашку.
    try { if (mediaMode()) wxBadgeMedia(); } catch { /* до первой отрисовки */ }
  }
  if (window.ResizeObserver) new ResizeObserver(() => wxLayout()).observe($("wx-stage"));

  // wxPlay проигрывает появление, смену или уход в кадре.
  function wxPlay(kind) {
    if (!wxApi) return;
    if (kind === "next") {
      wxTrack = (wxTrack + 1) % WX_TRACKS.length;
      wxApi.next(wxNow());
      return;
    }
    wxApi.play(kind);
  }

  // Уход — и возврат: после ухода в кадре пусто, а настраивать дальше надо.
  let wxBackTimer = null;
  function wxLeaveAndBack() {
    clearTimeout(wxBackTimer);
    wxPlay("leave");
    wxBackTimer = setTimeout(() => wxPlay("enter"), 1100);
  }

  // ── сохранение ─────────────────────────────────────────────────────

  // Настройки виджета лежат одним блоком: сливаем поверх текущего, а не
  // собираем заново, иначе каждая правка стирала бы остальные.
  async function saveWidget(patch) {
    const next = Object.assign({}, widget(), patch);
    // В режиме настройки своего стиля каждая правка вида сразу пишется и в
    // сам стиль: человек настраивает стиль, а не «плашку, которую потом
    // надо не забыть сохранить».
    if (wxEditing && !("styles" in patch)) {
      next.styles = (next.styles || []).map((st) => (st.id === wxEditing
        ? Object.assign({ id: st.id, name: st.name }, lookOf(next)) : st));
      next.style = wxEditing;
    }
    wxLive = null;
    // Запоминаем сразу, не дожидаясь ответа: пока запись летит, следующая
    // правка (колесо после перетаскивания) бралась бы из старых настроек
    // и затирала только что сделанное.
    if (config) config.widget = next;
    const ok = await saveConfig({ widget: next });
    drawWidget();
    return ok;
  }

  // Ползунки двигают образец сразу, а на диск пишут по отпусканию: иначе
  // каждое движение мыши было бы записью файла и рассылкой в OBS.
  function live(input, onMove, onDone) {
    input.oninput = () => onMove(parseInt(input.value, 10));
    input.onchange = () => onDone(parseInt(input.value, 10));
  }

  function liveSet(patch) {
    wxLive = Object.assign({}, wxLive || {}, patch);
    wxPaint();
  }

  // ── вкладки ────────────────────────────────────────────────────────

  $("wx-tabs").onclick = (e) => {
    const b = e.target.closest("[data-wtab]");
    if (!b) return;
    wxTab = b.dataset.wtab;
    document.querySelectorAll("#wx-tabs [data-wtab]").forEach((t) => t.classList.toggle("on", t === b));
    document.querySelectorAll("#wx-side [data-wpane]").forEach((p) => { p.hidden = p.dataset.wpane !== wxTab; });
    $("wx-side").scrollTop = 0;
    if (wxTab === "media") loadFiles();
    drawTools(curMedia());
    if (!mediaMode()) wxLayout();
  };

  // ── стиль ──────────────────────────────────────────────────────────

  // Мини-плашка для плитки. Та же вёрстка и тот же widget.css, что в кадре.
  function miniCard(family, variant) {
    return `<span class="card show" data-family="${family}" data-variant="${variant}"
        data-pos="left-bottom" data-art="on" data-bar="on" data-align="left" style="--progress:.46">
      <span class="aura"><i></i></span>
      <span class="fx"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>
      <span class="art"></span>
      <span class="text">
        <span class="title">${esc(WX_TRACKS[1].title)}</span>
        <span class="artist">${esc(WX_TRACKS[1].artist)}</span>
        <span class="by">${esc(I18N.t("заказал {who}", { who: "mikhail" }))}</span>
        <span class="bar"><i style="width:52%"></i></span>
        <span class="meta"><span class="eq"><i></i><i></i><i></i><i></i><i></i></span></span>
      </span>
    </span>`;
  }

  function buildGallery() {
    if (galleryBuilt) return;
    const cover = wxCover(1);

    // Семнадцать плиток подряд не читаются. Делим на две полки: «живые» —
    // те, что двигаются и в покое (снег, частицы, дрожащая линия), и
    // классика — спокойные плашки. Это и есть главный выбор: нужен ли в
    // кадре живой элемент или тихая подпись.
    const tile = (f) => `
      <button class="wxe-fam" data-family="${f.id}" title="${esc(I18N.t(f.note))}">
        <span class="wxe-fam-stage">${miniCard(f.id, f.variants[0].id)}</span>
        <span class="wxe-fam-name">${esc(I18N.t(f.name))}${f.fresh ? `<em>${I18N.t("новый")}</em>` : ""}</span>
      </button>`;
    const shelf = (title, note, list) => `
      <div class="wxe-shelf">
        <div class="wxe-shelf-head"><b>${esc(I18N.t(title))}</b><span>${esc(I18N.t(note))}</span></div>
        <div class="wxe-fams">${list.map(tile).join("")}</div>
      </div>`;
    $("wx-gallery").innerHTML =
      shelf("Живые", "двигаются и в покое: свет, снег, дрожащая линия", WIDGET_FAMILIES.filter((f) => f.fresh)) +
      shelf("Классика", "спокойные плашки", WIDGET_FAMILIES.filter((f) => !f.fresh));

    // Обложка в плитках — та же картинка, что у образца: пустой квадрат
    // читался бы как «оформление сломалось».
    $("wx-gallery").querySelectorAll(".art").forEach((a) => {
      a.innerHTML = `<img src="${cover}" alt=""><img class="px" src="${wxCovers.px1}" alt="">`;
    });
    $("wx-gallery").querySelectorAll(".aura i").forEach((a) => { a.style.backgroundImage = `url(${cover})`; });

    const gallery = $("wx-gallery");
    gallery.onclick = (e) => {
      const tile = e.target.closest("[data-family]");
      if (!tile) return;
      const fam = WIDGET_FAMILIES.find((f) => f.id === tile.dataset.family);
      // Смена набора сбрасывает ручную правку: иначе розовый акцент от
      // прошлого набора переехал бы в новый и испортил его.
      const hadTweaks = Object.keys(widget().tweaks || {}).length > 0;
      // На первом экране из своего стиля в готовый — включаем готовый
      // начисто: без чужих гифок, шрифта и поворотов. Иначе в кадре была бы
      // смесь, а наверху всё ещё стояло имя своего стиля.
      const w0 = widget();
      const fromOwn = !wxEditing && (w0.styles || []).some((s) => s.id === w0.style);
      const patch = { preset: fam.id + "/" + fam.variants[0].id, tweaks: null };
      if (!wxEditing) patch.style = "";
      if (fromOwn) {
        Object.assign(patch, STYLE_EMPTY, { preset: patch.preset });
        // Все поля явно: сервер кладёт присланное поверх сохранённого.
        const t0 = { scale: 100, x: 0, y: 0, rot: 0, flip: false };
        patch.media = Object.assign({}, w0.media || {}, {
          bg: "", cover: "", fallback: "", sticker: "", bg_dim: 35, bg_blur: 0,
          bg_t: t0, cover_t: t0, fallback_t: t0, sticker_t: t0,
        });
      }
      saveWidget(patch).then(() => {
        wxPlay("enter");
        // Молча стирать свои цвета нельзя: человек решит, что настройка
        // сломалась. Говорим, что случилось.
        if (hadTweaks) say(I18N.t("Свои цвета сброшены: у нового стиля свои. Задать заново — во вкладке «Цвет»."));
      });
    };
    // Примерка: навёл — стиль встал в кадре, увёл — вернулся выбранный.
    // Перебрать тринадцать стилей так быстрее, чем щёлкать каждый и
    // щёлкать обратно.
    gallery.onmouseover = (e) => {
      const tile = e.target.closest("[data-family]");
      if (!tile) return;
      const fam = WIDGET_FAMILIES.find((f) => f.id === tile.dataset.family);
      const preset = fam.id + "/" + fam.variants[0].id;
      if (wxLive && wxLive.preset === preset) return;
      if (widgetPreset(widget().preset).family.id === fam.id) { wxLive = null; wxPaint(); return; }
      wxLive = { preset, tweaks: null };
      wxPaint();
    };
    gallery.onmouseleave = () => {
      if (!wxLive) return;
      wxLive = null;
      wxPaint();
    };

    galleryBuilt = true;
  }

  // wxProbe — вычисленные цвета набора без отрисовки плашки: для кружков
  // окраски. Берём их у невидимой плашки на той же странице стилей.
  function wxProbe(family, variant) {
    let probe = $("wx-probe");
    if (!probe) {
      probe = document.createElement("div");
      probe.id = "wx-probe";
      probe.className = "card";
      probe.style.cssText = "position:absolute;left:-9999px;top:0;visibility:hidden";
      document.body.appendChild(probe);
    }
    probe.dataset.family = family;
    probe.dataset.variant = variant;
    const cs = getComputedStyle(probe);
    return {
      bg: cs.getPropertyValue("--w-bg").trim(),
      accent: cs.getPropertyValue("--w-accent").trim(),
      ink: cs.getPropertyValue("--w-ink").trim(),
    };
  }

  function drawVariants(w) {
    const { family, variant } = widgetPreset(w.preset);
    $("wx-fam-name").textContent = "· " + I18N.t(family.name);
    $("wx-variants").innerHTML = family.variants.map((v) => {
      const c = wxProbe(family.id, v.id);
      // Подложка бывает «none» (открытый текст, неон) — тогда кружок рисуем
      // на шахматке, как прозрачное в любом графическом редакторе.
      const bg = !c.bg || c.bg === "none" || c.bg === "transparent" ? "" : c.bg;
      return `<button class="wxe-sw${v.id === variant.id ? " on" : ""}" data-variant="${v.id}">
        <span class="wxe-sw-dot${bg ? "" : " clear"}" style="${bg ? "--sw-bg:" + esc(bg) + ";" : ""}--sw-ac:${esc(c.accent)};--sw-ink:${esc(c.ink)}"><i></i></span>
        <span>${esc(I18N.t(v.name))}</span>
      </button>`;
    }).join("");
  }

  $("wx-variants").onclick = (e) => {
    const b = e.target.closest("[data-variant]");
    if (!b) return;
    const fam = widgetPreset(widget().preset).family;
    saveWidget({ preset: fam.id + "/" + b.dataset.variant });
  };
  $("wx-variants").onmouseover = (e) => {
    const b = e.target.closest("[data-variant]");
    if (!b) return;
    const fam = widgetPreset(widget().preset).family;
    const preset = fam.id + "/" + b.dataset.variant;
    if (wxLive && wxLive.preset === preset) return;
    wxLive = { preset };
    wxPaint();
  };
  $("wx-variants").onmouseleave = () => { if (wxLive) { wxLive = null; wxPaint(); } };

  // ── обложка ────────────────────────────────────────────────────────

  function drawShapes(w) {
    const fam = widgetPreset(w.preset).family.id;
    const can = widgetCan(fam, "shape");
    const now = w.cover_shape || "";
    $("wx-shapes").innerHTML = WIDGET_SHAPES.map((s) => `
      <button class="wxe-shape${s.id === now ? " on" : ""}" data-shape="${s.id}" ${can ? "" : "disabled"}>
        <span class="wxe-shape-ico" data-s="${s.id || "style"}"></span>
        <span>${esc(I18N.t(s.name))}</span>
      </button>`).join("");
    $("wx-shape-hint").textContent = can ? "" :
      I18N.t("У этого стиля обложка — часть предмета, её форма не меняется.");

    // Обложка выключена — свечению и размеру обложки не к чему относиться.
    // Гасим их, а не оставляем крутиться вхолостую.
    const art = w.show_art !== false;
    const glow = widgetCan(fam, "glow") && art;
    const bg = widgetCan(fam, "coverbg");
    $("wx-glow-row").classList.toggle("off", !glow);
    $("wx-coverbg-row").classList.toggle("off", !bg);
    $("wx-glow").disabled = !glow;
    $("wx-coverbg").disabled = !bg;
    $("wx-glow").checked = !!w.cover_glow && glow;
    // У «Ауры» обложка фоном — сама суть стиля, выключатель всегда включён.
    $("wx-coverbg").checked = fam === "aura" || (!!w.cover_bg && bg);
    document.querySelector('[data-tweak-slider="--w-art"]').classList.toggle("off", !art);

    // Серый выключатель без объяснения выглядит как поломка. Говорим, почему.
    $("wx-glow-note").textContent = !art
      ? I18N.t("Обложка выключена — светиться нечему.")
      : glow ? I18N.t("Мягкий ореол вокруг обложки её же цветом.")
      : I18N.t("У этого стиля свой свет — ореол с ним спорил бы.");
    $("wx-coverbg-note").textContent = fam === "aura"
      ? I18N.t("В «Ауре» это основа стиля — включено всегда.")
      : bg ? I18N.t("Размытая обложка под плашкой: каждый трек плашка перекрашивается сама.")
      : I18N.t("У этого стиля своя подложка — размытой обложке тут не место.");
  }

  $("wx-shapes").onclick = (e) => {
    const b = e.target.closest("[data-shape]");
    if (b && !b.disabled) saveWidget({ cover_shape: b.dataset.shape });
  };
  $("wx-art-on").onchange = (e) => saveWidget({ show_art: e.target.checked });
  $("wx-glow").onchange = (e) => saveWidget({ cover_glow: e.target.checked });
  $("wx-coverbg").onchange = (e) => saveWidget({ cover_bg: e.target.checked });

  // ── цвет ───────────────────────────────────────────────────────────

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

  // Цвет набора без ручных правок: у невидимой плашки, а не у той, что в
  // кадре, — на той уже лежат правки и цвета из обложки.
  function presetToken(w, key) {
    const { family, variant } = widgetPreset(w.preset);
    wxProbe(family.id, variant.id);
    return getComputedStyle($("wx-probe")).getPropertyValue(key).trim();
  }

  // Тонкая настройка — всегда поверх набора: нетронутое остаётся набором,
  // а не застывшим слепком его текущих значений.
  function drawTweaks(w) {
    const tweaks = w.tweaks || {};
    $("wx-tweaks").innerHTML = WIDGET_TWEAKS.filter((t) => t.kind === "color").map((t) => {
      const set = tweaks[t.key] !== undefined;
      const now = (tweaks[t.key] || presetToken(w, t.key) || "").trim();
      const locked = t.key === "--w-accent" && w.magic;
      return `<label class="wxe-color${set ? " set" : ""}${locked ? " locked" : ""}" title="${esc(I18N.t(t.hint || t.name))}">
        <input type="color" data-tweak="${t.key}" value="${esc(asHex(now))}" ${locked ? "disabled" : ""}>
        <span class="wxe-color-name">${esc(I18N.t(t.name))}<small>${locked ? esc(I18N.t("из обложки")) : set ? esc(asHex(now)) : esc(I18N.t("как в стиле"))}</small></span>
        ${set ? `<button class="wxe-x" data-clear="${t.key}" title="${esc(I18N.t("вернуть как в стиле"))}">×</button>` : ""}
      </label>`;
    }).join("");

    // Ползунки ручной настройки разложены по вкладкам (размер обложки — в
    // «Обложке», кегль — в «Цвете»), но устроены одинаково.
    document.querySelectorAll("[data-tweak-slider]").forEach((box) => {
      const key = box.dataset.tweakSlider;
      const set = tweaks[key] !== undefined;
      const px = parseInt(tweaks[key] || presetToken(w, key), 10) || 0;
      box.classList.toggle("set", set);
      box.querySelector("input").value = px;
      box.querySelector("b").textContent = px + " px";
    });
  }

  function setTweak(key, value) {
    const tweaks = Object.assign({}, widget().tweaks || {});
    if (value === null) delete tweaks[key];
    else tweaks[key] = value;
    saveWidget({ tweaks: Object.keys(tweaks).length ? tweaks : null });
  }
  function liveTweak(key, value) {
    const tweaks = Object.assign({}, widget().tweaks || {}, { [key]: value });
    liveSet({ tweaks });
  }

  $("wx-tweaks").oninput = (e) => {
    const key = e.target.dataset.tweak;
    if (key) liveTweak(key, e.target.value);
  };
  $("wx-tweaks").onchange = (e) => {
    const key = e.target.dataset.tweak;
    if (key) setTweak(key, e.target.value);
  };
  document.querySelectorAll("[data-tweak-slider]").forEach((box) => {
    const key = box.dataset.tweakSlider;
    live(box.querySelector("input"), (v) => {
      box.querySelector("b").textContent = v + " px";
      liveTweak(key, v + "px");
    }, (v) => setTweak(key, v + "px"));
  });
  // Крестик «вернуть как в стиле» — у цветов и у ползунков одинаковый.
  $("widget").addEventListener("click", (e) => {
    const clear = e.target.closest("[data-clear]");
    if (!clear) return;
    e.preventDefault();
    setTweak(clear.dataset.clear, null);
  });
  $("wx-reset").onclick = () => saveWidget({ tweaks: null, font: "" });
  $("wx-magic").onchange = (e) => saveWidget({ magic: e.target.checked });

  function drawFonts(w) {
    const now = w.font || "";
    $("wx-fonts").innerHTML = WIDGET_FONTS.map((f) => `
      <button class="wxe-font${f.id === now ? " on" : ""}" data-font="${f.id}">
        <span class="wxe-font-aa" style="${f.css ? "font-family:" + esc(f.css) : ""}">Aa</span>
        <span>${esc(I18N.t(f.name))}</span>
      </button>`).join("");
  }
  $("wx-fonts").onclick = (e) => {
    const b = e.target.closest("[data-font]");
    if (b) saveWidget({ font: b.dataset.font });
  };

  live($("wx-op"), (v) => {
    $("wx-op-val").textContent = v + "%";
    liveSet({ opacity: v });
  }, (v) => saveWidget({ opacity: v }));

  // ── анимация ───────────────────────────────────────────────────────

  function drawAnims(w) {
    const chips = (now, out) => WIDGET_ANIMS.map((a) => {
      const name = out ? a.out : a.name;
      return `<button class="wxe-anim${a.id === now ? " on" : ""}" data-anim="${a.id}" title="${esc(I18N.t(a.hint || name))}">
        <span class="wxe-anim-ico${out ? " out" : ""}" data-a="${a.id || "style"}"><i></i></span>
        <span>${esc(I18N.t(name))}</span>
      </button>`;
    }).join("");
    $("wx-in").innerHTML = chips(w.anim_in || "", false);
    $("wx-out").innerHTML = chips(w.anim_out || "", true);
  }
  $("wx-in").onclick = async (e) => {
    const b = e.target.closest("[data-anim]");
    if (!b) return;
    await saveWidget({ anim_in: b.dataset.anim });
    wxPlay("enter");
  };
  $("wx-out").onclick = async (e) => {
    const b = e.target.closest("[data-anim]");
    if (!b) return;
    await saveWidget({ anim_out: b.dataset.anim });
    wxLeaveAndBack();
  };
  $("wx-motion-on").onchange = (e) => saveWidget({ motion: e.target.checked });

  // ── своё: картинки, ролики, встроенные анимации ───────────────────

  // Четыре места на плашке. «Если обложки нет» — страховка: у заказов с
  // YouTube и у редких треков обложки не бывает, и вместо серой ноты
  // встаёт своя картинка.
  const WX_SLOTS = [
    { id: "bg",       field: "bg",       name: "Фон плашки",       note: "во всю плашку, под текстом" },
    { id: "cover",    field: "cover",    name: "Вместо обложки",   note: "всегда, на каждом треке" },
    { id: "fallback", field: "fallback", name: "Если обложки нет", note: "страховка: обложка не пришла" },
    { id: "sticker",  field: "sticker",  name: "Наклейка",         note: "у края плашки, торчит наружу" },
  ];
  let wxSlot = "bg";
  // Загруженные файлы. Список спрашиваем у приложения, когда открыли
  // вкладку, и после каждой загрузки или удаления.
  let wxFiles = [];

  const wxMedia = (w) => Object.assign({ bg_dim: 0, sticker_side: "right", sticker_size: 72 }, (w || widget()).media || {});
  const isVid = (ref) => /\.(webm|mp4)$/.test(ref);

  // Превью картинки для плиток. Ролик в плитке тоже играет — без звука:
  // по застывшему первому кадру не понять, что это за анимация.
  function mediaThumb(el, ref) {
    if (el._fx) { el._fx.stop(); el._fx = null; }
    el.innerHTML = "";
    if (!ref) return;
    if (ref.indexOf("fx:") === 0) {
      el._fx = WidgetFX.mount(el, ref.slice(3), { accent: wxAccent() });
      return;
    }
    const src = "/media/" + encodeURIComponent(ref);
    el.innerHTML = isVid(ref)
      ? `<video src="${esc(src)}" muted loop autoplay playsinline></video>`
      : `<img src="${esc(src)}" alt="">`;
  }

  // Акцент плашки в кадре — им окрашены встроенные анимации.
  function wxAccent() {
    const v = presetToken(wxView(), "--w-accent");
    const t = (widget().tweaks || {})["--w-accent"];
    return asHex(t || v || "#c8f751");
  }

  function mediaName(ref) {
    if (!ref) return I18N.t("пусто");
    if (ref.indexOf("fx:") === 0) {
      const fx = WidgetFX.list.find((f) => f.id === ref.slice(3));
      return fx ? I18N.t(fx.name) : ref;
    }
    return isVid(ref) ? I18N.t("свой ролик") : I18N.t("своя картинка");
  }

  function drawSlots(w) {
    const m = wxMedia(w);
    const box = $("wx-slots");
    // Плитки мест перерисовываем, только если поменялось содержимое:
    // иначе ролик в превью начинал бы сначала на каждой правке ползунка.
    const sig = WX_SLOTS.map((s) => m[s.field] || "").join("|") + "|" + wxSlot + "|" + I18N.lang();
    if (box.dataset.sig !== sig) {
      box.dataset.sig = sig;
      box.innerHTML = WX_SLOTS.map((s) => `
        <button class="wxe-slot${s.id === wxSlot ? " on" : ""}${m[s.field] ? " full" : ""}" data-slot="${s.id}">
          <span class="wxe-slot-thumb" data-thumb="${s.id}"></span>
          <span class="wxe-slot-text"><b>${esc(I18N.t(s.name))}</b><small>${esc(m[s.field] ? mediaName(m[s.field]) : I18N.t(s.note))}</small></span>
          ${m[s.field] ? `<span class="wxe-x" data-unset="${s.field}" title="${esc(I18N.t("убрать"))}">×</span>` : ""}
        </button>`).join("");
      WX_SLOTS.forEach((s) => mediaThumb(box.querySelector(`[data-thumb="${s.id}"]`), m[s.field] || ""));
    }

    drawTools(m);
    drawLib(m);
  }

  // drawLib — библиотека: свои файлы и встроенные анимации. Подсвечено
  // то, что стоит в выбранном месте.
  function drawLib(m) {
    const now = m[WX_SLOTS.find((s) => s.id === wxSlot).field] || "";
    const own = $("wx-lib-own");
    const ownSig = wxFiles.map((f) => f.name).join("|");
    if (own.dataset.sig !== ownSig) {
      own.dataset.sig = ownSig;
      own.innerHTML = wxFiles.map((f) => `
        <button class="wxe-pick" data-ref="${esc(f.name)}" title="${esc((f.size / 1048576).toFixed(1) + " " + I18N.t("МБ"))}">
          <span class="wxe-pick-thumb" data-thumb="${esc(f.name)}"></span>
          <span class="wxe-x" data-del="${esc(f.name)}" title="${esc(I18N.t("удалить файл"))}">×</span>
        </button>`).join("");
      wxFiles.forEach((f) => mediaThumb(own.querySelector(`[data-thumb="${f.name}"]`), f.name));
    }
    const fx = $("wx-lib-fx");
    const fxSig = I18N.lang() + wxAccent();
    if (fx.dataset.sig !== fxSig) {
      fx.dataset.sig = fxSig;
      fx.innerHTML = WidgetFX.list.map((f) => `
        <button class="wxe-pick" data-ref="fx:${f.id}">
          <span class="wxe-pick-thumb" data-thumb="fx:${f.id}"></span>
          <span class="wxe-pick-name">${esc(I18N.t(f.name))}</span>
        </button>`).join("");
      WidgetFX.list.forEach((f) => mediaThumb(fx.querySelector(`[data-thumb="fx:${f.id}"]`), "fx:" + f.id));
    }
    document.querySelectorAll("#wx-lib-own [data-ref], #wx-lib-fx [data-ref]").forEach((b) => {
      b.classList.toggle("on", b.dataset.ref === now);
    });
  }

  async function loadFiles() {
    try {
      wxFiles = await (await fetch("/api/widget/media")).json();
    } catch { wxFiles = []; }
    drawLib(wxMedia());
  }

  function saveMedia(patch) {
    return saveWidget({ media: Object.assign(wxMedia(), patch) });
  }
  function liveMedia(patch) {
    liveSet({ media: Object.assign(wxMedia(), (wxLive || {}).media || {}, patch) });
  }

  $("wx-slots").onclick = (e) => {
    const unset = e.target.closest("[data-unset]");
    if (unset) {
      e.stopPropagation();
      saveMedia({ [unset.dataset.unset]: "" });
      return;
    }
    const b = e.target.closest("[data-slot]");
    if (!b) return;
    wxSlot = b.dataset.slot;
    drawSlots(widget());
  };

  // Клик по картинке ставит её в выбранное место. Повторный клик по уже
  // стоящей — убирает: так не надо искать крестик.
  async function pickMedia(e) {
    const del = e.target.closest("[data-del]");
    if (del) {
      e.stopPropagation();
      const r = await fetch("/api/widget/media/" + encodeURIComponent(del.dataset.del) + "/delete", { method: "POST" });
      if (r.ok) config = await r.json();
      await loadFiles();
      drawWidget();
      return;
    }
    const b = e.target.closest("[data-ref]");
    if (!b) return;
    const field = WX_SLOTS.find((s) => s.id === wxSlot).field;
    const ref = b.dataset.ref === wxMedia()[field] ? "" : b.dataset.ref;
    // Фон без затемнения на пёстрой гифке не читается — первый раз ставим
    // треть, дальше человек решает сам.
    const patch = { [field]: ref };
    if (field === "bg" && ref && !wxMedia().bg && !wxMedia().bg_dim) patch.bg_dim = 35;
    await saveMedia(patch);
    // Фон плашки не видно, если обложка уходит за него — проигрываем
    // появление, чтобы было видно, что встало.
    wxPlay("enter");
  }
  $("wx-lib-own").onclick = pickMedia;
  $("wx-lib-fx").onclick = pickMedia;

  // Загрузка: и через выбор файла, и перетаскиванием в рамку.
  async function upload(files) {
    let last = "";
    for (const f of files) {
      if (f.size > 10 * 1048576) {
        say(I18N.t("Файл больше 10 МБ. Сожмите его или обрежьте ролик."), true);
        continue;
      }
      $("wx-drop").classList.add("busy");
      try {
        const r = await fetch("/api/widget/media", { method: "POST", body: f });
        if (!r.ok) {
          say((await r.text()).trim() || I18N.t("Не получилось сохранить файл."), true);
          continue;
        }
        last = (await r.json()).name;
      } catch {
        say(I18N.t("Не получилось сохранить файл."), true);
      } finally {
        $("wx-drop").classList.remove("busy");
      }
    }
    await loadFiles();
    // Только что загруженное сразу встаёт в выбранное место: человек
    // загружал его ровно для этого.
    if (last) {
      const field = WX_SLOTS.find((s) => s.id === wxSlot).field;
      const patch = { [field]: last };
      if (field === "bg" && !wxMedia().bg_dim) patch.bg_dim = 35;
      await saveMedia(patch);
      wxPlay("enter");
    }
  }
  $("wx-file").onchange = (e) => {
    upload([...e.target.files]);
    e.target.value = "";
  };
  const drop = $("wx-drop");
  drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("over"); };
  drop.ondragleave = () => drop.classList.remove("over");
  drop.ondrop = (e) => {
    e.preventDefault();
    drop.classList.remove("over");
    upload([...e.dataTransfer.files]);
  };

  // ── инструменты картинки под кадром ────────────────────────────────

  const slotField = () => WX_SLOTS.find((s) => s.id === wxSlot).field;
  const T0 = {
    scale: 100, x: 0, y: 0, rot: 0, flip: false, fit: "", opacity: 100, key: "",
    bright: 100, contrast: 100, sat: 100, gray: false, speed: 100, mask: "", back: false,
  };
  const T_BOOL = { flip: true, gray: true, back: true };
  // Медиа «как сейчас в кадре»: сохранённое плюс то, что ещё тянут.
  const curMedia = () => Object.assign(wxMedia(), (wxLive || {}).media || {});
  const curT = () => Object.assign({}, T0, curMedia()[slotField() + "_t"] || {});

  // Правка сразу видна в кадре, а на диск ложится, когда рука отпустила:
  // иначе каждое движение мыши было бы записью файла.
  let mSaveTimer = null;
  function mSet(patch, when) {
    liveMedia(patch);
    clearTimeout(mSaveTimer);
    const save = () => saveWidget({ media: curMedia() });
    if (when === "now") save();
    else if (when === "later") mSaveTimer = setTimeout(save, 450);
    drawTools(curMedia(), true);
  }
  function tSet(change, when) {
    const t = Object.assign(curT(), change);
    t.scale = Math.round(Math.min(500, Math.max(10, t.scale)));
    t.x = Math.round(Math.min(150, Math.max(-150, t.x)));
    t.y = Math.round(Math.min(150, Math.max(-150, t.y)));
    t.rot = Math.round(((t.rot % 360) + 540) % 360 - 180);
    mSet({ [slotField() + "_t"]: t }, when);
  }

  function parseK(k, raw) {
    const [ns, name] = k.split(":");
    let v = raw;
    if (T_BOOL[name]) v = raw === "1" || raw === true;
    else if (/^-?\d+$/.test(String(raw))) v = Number(raw);
    return [ns, name, v];
  }
  function setK(k, raw, when) {
    const [ns, name, v] = parseK(k, raw);
    if (ns === "t") tSet({ [name]: v }, when);
    else mSet({ [name]: v }, when);
  }

  // Режим «картинка в кадре»: открыта вкладка «Своё» и в выбранном месте
  // что-то стоит. Тогда мышь в кадре двигает картинку, а не плашку.
  const mediaMode = () => wxScreen === "edit" && wxTab === "media" && !!curMedia()[slotField()];

  function drawTools(m, quick) {
    const box = $("wx-tools");
    const ref = m[slotField()] || "";
    const on = wxScreen === "edit" && wxTab === "media" && !!ref;
    box.hidden = !on;
    $("wx-stage").classList.toggle("media-mode", on);
    if (!on) return;
    const slot = WX_SLOTS.find((s) => s.id === wxSlot);
    if (!quick) {
      $("wx-tools-title").textContent = I18N.t(slot.name) + " · " + mediaName(ref);
      const th = $("wx-tools-thumb");
      if (th.dataset.ref !== ref) { th.dataset.ref = ref; mediaThumb(th, ref); }
    }
    const t = Object.assign({}, T0, m[slot.field + "_t"] || {});
    // «Как ложится» по умолчанию у места своё: фон заполняет, наклейка
    // видна целиком. Подсвечиваем то, что на деле в кадре.
    if (!t.fit) t.fit = wxSlot === "sticker" ? "contain" : "cover";
    box.querySelectorAll("[data-k]").forEach((el) => {
      const [ns, name] = el.dataset.k.split(":");
      const v = ns === "t" ? t[name] : m[name];
      if (el.classList.contains("wxe-slider")) {
        const input = el.querySelector("input");
        if (document.activeElement !== input) input.value = v;
        el.querySelector("b").textContent = v + (el.dataset.unit || "");
        el.classList.toggle("set", v !== (ns === "t" ? T0[name] : v));
      } else {
        const want = T_BOOL[name] ? (v ? "1" : "") : String(v === undefined ? "" : v);
        el.querySelectorAll("[data-v]").forEach((b) => b.classList.toggle("on", b.dataset.v === want));
      }
    });
    box.querySelectorAll("[data-slots]").forEach((sec) => { sec.hidden = sec.dataset.slots !== wxSlot; });
    const anim = /^fx:|\.(webm|mp4)$/.test(ref);
    box.querySelectorAll('[data-only="anim"]').forEach((el) => { el.hidden = !anim; });
    if (!quick) wxBadgeMedia();
  }

  function wxBadgeMedia() {
    if (mediaMode()) $("wx-badge").textContent = I18N.t("тащи картинку в кадре · колесо — размер · Shift — поворот");
  }

  const tools = $("wx-tools");
  tools.addEventListener("input", (e) => {
    const k = e.target.closest("[data-k]");
    if (k && e.target.type === "range") setK(k.dataset.k, e.target.value, "");
  });
  tools.addEventListener("change", (e) => {
    const k = e.target.closest("[data-k]");
    if (k && e.target.type === "range") setK(k.dataset.k, e.target.value, "now");
  });
  tools.addEventListener("click", (e) => {
    const v = e.target.closest("[data-v]");
    if (v) { setK(v.closest("[data-k]").dataset.k, v.dataset.v, "now"); return; }
    const a = e.target.closest("[data-tact]");
    if (!a) return;
    const t = curT();
    switch (a.dataset.tact) {
      case "rot-l": tSet({ rot: t.rot - 90 }, "now"); break;
      case "rot-r": tSet({ rot: t.rot + 90 }, "now"); break;
      case "flip": tSet({ flip: !t.flip }, "now"); break;
      case "center": tSet({ x: 0, y: 0 }, "now"); break;
      case "reset": tSet(Object.assign({}, T0), "now"); break;
      case "remove": saveMedia({ [slotField()]: "" }); break;
    }
  });

  // Картинка прямо в кадре: тащить — сдвиг, колесо — размер, с Shift —
  // поворот. Перехватываем мышь раньше, чем её получит перетаскивание
  // самой плашки: пока открыта вкладка «Своё», двигать плашку незачем.
  (function () {
    const hit = $("wx-hit");
    let md = null;
    hit.addEventListener("pointerdown", (e) => {
      if (!mediaMode() || !wxApi || !wxApi.places) return;
      e.stopImmediatePropagation();
      e.preventDefault();
      const r = wxApi.places[wxSlot].getBoundingClientRect();
      md = { x0: e.clientX, y0: e.clientY, w: r.width || 1, h: r.height || 1, t: curT() };
      wxCamHold = true;
      try { hit.setPointerCapture(e.pointerId); } catch { /* мышь уже ушла — дотащим и так */ }
      hit.style.cursor = "grabbing";
    }, true);
    hit.addEventListener("pointermove", (e) => {
      if (!mediaMode()) return;
      e.stopImmediatePropagation();
      hit.style.cursor = md ? "grabbing" : "move";
      if (!md) return;
      // Сдвиг мыши в окошке → в пиксели настоящего кадра → в проценты места.
      const dx = (e.clientX - md.x0) / wxCam.k / md.w * 100;
      const dy = (e.clientY - md.y0) / wxCam.k / md.h * 100;
      tSet({ x: md.t.x + dx, y: md.t.y + dy }, "");
    }, true);
    const end = (e) => {
      if (!md) return;
      e.stopImmediatePropagation();
      md = null;
      wxCamHold = false;
      hit.style.cursor = "move";
      tSet({}, "now");
    };
    hit.addEventListener("pointerup", end, true);
    hit.addEventListener("pointercancel", end, true);
    hit.addEventListener("wheel", (e) => {
      if (!mediaMode()) return;
      e.preventDefault();
      const t = curT();
      const up = e.deltaY < 0;
      if (e.shiftKey) tSet({ rot: t.rot + (up ? 5 : -5) }, "later");
      else tSet({ scale: t.scale * (up ? 1.06 : 1 / 1.06) }, "later");
    }, { passive: false });
  })();

  // ── свои стили ─────────────────────────────────────────────────────

  // Что входит в стиль. Место в кадре, размер и расписание показа — нет:
  // это про сцену, и переключение стиля не должно уводить плашку в другой
  // угол.
  const STYLE_FIELDS = ["preset", "tweaks", "font", "cover_shape", "cover_glow", "cover_bg",
    "magic", "anim_in", "anim_out", "show_eq", "show_art", "opacity", "media"];

  // Пустое пишем явно: настройки ложатся поверх сохранённых, и поле,
  // которого в присланном нет, осталось бы от прошлого стиля — свой шрифт
  // или форма обложки «прилипали» бы ко всем следующим.
  const STYLE_EMPTY = {
    tweaks: null, font: "", cover_shape: "", cover_glow: false, cover_bg: false,
    magic: false, anim_in: "", anim_out: "", show_eq: false, show_art: true, opacity: 100,
  };
  function lookOf(w) {
    const look = {};
    for (const k of STYLE_FIELDS) look[k] = w[k] === undefined ? STYLE_EMPTY[k] : w[k];
    look.media = Object.assign({}, w.media || {});
    return look;
  }

  function drawMine(w) {
    const box = $("wx-mine");
    const styles = w.styles || [];
    const sig = JSON.stringify(styles) + "|" + I18N.lang();
    if (box.dataset.sig !== sig) {
      box.dataset.sig = sig;
      const cover = wxCover(1);
      box.innerHTML = styles.map((st) => {
        const { family, variant } = widgetPreset(st.preset);
        return `<button class="wxe-fam" data-style="${esc(st.id)}">
          <span class="wxe-fam-stage">${miniCard(family.id, variant.id)}
            <span class="wxe-mine-acts">
              <span class="wxe-mine-act" data-edit="${esc(st.id)}" title="${esc(I18N.t("войти в настройку этого стиля"))}">${esc(I18N.t("настроить"))}</span>
              <span class="wxe-x" data-unstyle="${esc(st.id)}" title="${esc(I18N.t("удалить стиль"))}">×</span>
            </span></span>
          <span class="wxe-fam-name"><span class="wxe-fam-label">${esc(st.name)}</span></span>
        </button>`;
      }).join("") + `<button class="wxe-fam wxe-mine-new" data-new-style>
          <span class="wxe-fam-stage"><span class="wxe-mine-plus">+</span></span>
          <span class="wxe-fam-name">${esc(I18N.t("Создать свой стиль"))}</span>
        </button>`;
      // Плитка своего стиля — та же мини-плашка, одетая в его цвета,
      // шрифт и фон. Иначе все свои стили одного семейства выглядели бы
      // одинаково, и выбирать пришлось бы по названию.
      styles.forEach((st) => {
        const card = box.querySelector(`[data-style="${CSS.escape(st.id)}"] .card`);
        for (const [k, v] of Object.entries(st.tweaks || {})) card.style.setProperty(k, v);
        const font = WIDGET_FONTS.find((f) => f.id === st.font);
        if (font && font.css) { card.style.setProperty("--w-title-font", font.css); card.dataset.font = st.font; }
        if (st.cover_shape && widgetCan(card.dataset.family, "shape")) card.dataset.shape = st.cover_shape;
        const art = card.querySelector(".art");
        const m = st.media || {};
        if (m.cover) mediaThumb(art, m.cover);
        else art.innerHTML = `<img src="${cover}" alt=""><img class="px" src="${wxCovers.px1}" alt="">`;
        if (m.bg) {
          const bg = document.createElement("span");
          bg.className = "mbg on";
          bg.style.setProperty("--m-dim", ((m.bg_dim || 0) / 100).toFixed(2));
          card.prepend(bg);
          card.dataset.mbg = "on";
          mediaThumb(bg, m.bg);
        }
      });
    }
    // Выбранный стиль, который потом поправили, помечаем: в кадре уже не
    // совсем он, и «обновить» на плитке запишет правку в стиль.
    box.querySelectorAll("[data-style]").forEach((t) => {
      t.classList.toggle("on", t.dataset.style === w.style);
      t.classList.toggle("editing", t.dataset.style === wxEditing);
    });
    drawEditing(w);
  }

  function openStyleForm(open) {
    $("wx-style-form").hidden = !open;
    if (open) {
      $("wx-style-name").value = "";
      $("wx-style-name").focus();
    }
  }
  $("wx-style-cancel").onclick = () => openStyleForm(false);
  $("wx-style-form").onsubmit = async (e) => {
    e.preventDefault();
    const w = widget();
    const name = $("wx-style-name").value.trim() ||
      I18N.t("Мой стиль {n}", { n: (w.styles || []).length + 1 });
    const id = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
    const styles = (w.styles || []).concat([Object.assign({ id, name }, lookOf(w))]);
    if (await saveWidget({ styles, style: id })) {
      openStyleForm(false);
      openEdit(id);
      say(I18N.t("Вы в настройке стиля: всё, что поменяете во вкладках, сразу запишется в него."));
    }
  };

  $("wx-mine").onclick = async (e) => {
    const w = widget();
    const del = e.target.closest("[data-unstyle]");
    if (del) {
      e.stopPropagation();
      const styles = (w.styles || []).filter((s) => s.id !== del.dataset.unstyle);
      if (wxEditing === del.dataset.unstyle) wxEditing = null;
      saveWidget({ styles, style: w.style === del.dataset.unstyle ? "" : w.style });
      return;
    }
    const edit = e.target.closest("[data-edit]");
    if (edit) {
      e.stopPropagation();
      const st = (w.styles || []).find((s) => s.id === edit.dataset.edit);
      if (!st) return;
      wxEditing = null;
      await saveWidget(Object.assign(lookOf(st), { style: st.id }));
      openEdit(st.id);
      return;
    }
    if (e.target.closest("[data-new-style]")) { openStyleForm(true); return; }
    const tile = e.target.closest("[data-style]");
    if (!tile) return;
    const st = (w.styles || []).find((s) => s.id === tile.dataset.style);
    if (!st) return;
    const look = lookOf(st);
    // Выбрали другой стиль — из настройки прежнего выходим, иначе его
    // молча переписало бы видом нового.
    if (wxEditing && wxEditing !== st.id) wxEditing = null;
    await saveWidget(Object.assign(look, { style: st.id }));
    wxPlay("enter");
  };
  // Примерка наведением — как у встроенных стилей.
  $("wx-mine").onmouseover = (e) => {
    const tile = e.target.closest("[data-style]");
    if (!tile) return;
    const st = (widget().styles || []).find((s) => s.id === tile.dataset.style);
    if (!st || (wxLive && wxLive._style === st.id)) return;
    wxLive = Object.assign(lookOf(st), { _style: st.id });
    wxPaint();
  };
  $("wx-mine").onmouseleave = () => { if (wxLive) { wxLive = null; wxPaint(); } };

  // ── два экрана: «Оформление» и настройка ───────────────────────────

  // wxEditing — свой стиль, который сейчас настраивают (его id), или null:
  // тогда настройка правит просто то, что в кадре.
  let wxEditing = null;
  let wxScreen = "home";

  // Галерея готовых стилей одна, а нужна на обоих экранах: на первом — чтобы
  // выбрать, в настройке — как «Основа» своего стиля. Переносим её туда,
  // где она сейчас видна, вместо того чтобы строить вторую.
  function showScreen(name) {
    wxScreen = name;
    $("wx-home").hidden = name !== "home";
    $("wx-edit").hidden = name !== "edit";
    const gallery = $("wx-gallery");
    (name === "home" ? $("wx-gallery-home") : $("wx-gallery-group")).appendChild(gallery);
    $("wx-side").scrollTop = 0;
    if (name === "home") openStyleForm(false);
    drawWidget();
  }

  // openEdit открывает настройку. С id — настройку своего стиля: всё, что
  // поменяют во вкладках, сразу пишется в него.
  function openEdit(id) {
    wxEditing = id || null;
    $("wx-saveas").hidden = true;
    showScreen("edit");
  }
  function enterEditing(id) { openEdit(id); }

  function drawEditing(w) {
    const st = wxEditing && (w.styles || []).find((s) => s.id === wxEditing);
    if (wxEditing && !st) wxEditing = null;
    const { family, variant } = widgetPreset(w.preset);
    const builtin = I18N.t(family.name) + " · " + I18N.t(variant.name);

    // Первый экран: что сейчас в кадре.
    const own = (w.styles || []).find((s) => s.id === w.style);
    $("wx-cur-name").textContent = own ? own.name : I18N.t(family.name);
    $("wx-cur-sub").textContent = own ? I18N.t("свой стиль · основа {base}", { base: builtin }) : I18N.t(variant.name);
    $("wx-open-edit").lastChild.textContent = own ? I18N.t("Настроить «{name}»", { name: own.name }) : I18N.t("Настроить");

    // Шапка настройки.
    $("wx-topbar").classList.toggle("own", !!st);
    $("wx-edit-tag").textContent = st ? I18N.t("Свой стиль") : I18N.t("Настройка вида");
    $("wx-edit-name").hidden = !st;
    $("wx-edit-title").hidden = !!st;
    $("wx-edit-title").textContent = builtin;
    $("wx-saveas-open").hidden = !!st || !$("wx-saveas").hidden;
    if (st && document.activeElement !== $("wx-edit-name")) $("wx-edit-name").value = st.name;
  }

  // «Настроить» на первом экране: выбран свой стиль — настраиваем его,
  // иначе просто то, что в кадре.
  $("wx-open-edit").onclick = () => {
    const w = widget();
    const own = (w.styles || []).some((s) => s.id === w.style);
    openEdit(own ? w.style : null);
  };

  // Открываемся с первого экрана.
  $("wx-gallery-home").appendChild($("wx-gallery"));

  function leaveEdit() {
    const was = wxEditing;
    wxEditing = null;
    showScreen("home");
    if (was) say(I18N.t("Стиль сохранён — он на полке «Мои стили»."));
  }
  $("wx-edit-done").onclick = leaveEdit;
  $("wx-back").onclick = leaveEdit;

  $("wx-edit-name").onchange = (e) => {
    const name = e.target.value.trim();
    if (!name || !wxEditing) return;
    const styles = (widget().styles || []).map((st) => (st.id === wxEditing ? Object.assign({}, st, { name }) : st));
    saveWidget({ styles });
  };

  // Настроили вид без своего стиля и понравилось — сохранить прямо отсюда,
  // не возвращаясь на первый экран.
  $("wx-saveas-open").onclick = () => {
    $("wx-saveas").hidden = false;
    $("wx-saveas-open").hidden = true;
    $("wx-saveas-name").value = "";
    $("wx-saveas-name").focus();
  };
  $("wx-saveas").onsubmit = async (e) => {
    e.preventDefault();
    const w = widget();
    const name = $("wx-saveas-name").value.trim() ||
      I18N.t("Мой стиль {n}", { n: (w.styles || []).length + 1 });
    const id = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
    const styles = (w.styles || []).concat([Object.assign({ id, name }, lookOf(w))]);
    if (await saveWidget({ styles, style: id })) {
      $("wx-saveas").hidden = true;
      wxEditing = id;
      drawWidget();
      say(I18N.t("Стиль «{name}» сохранён — дальше правки пишутся в него.", { name }));
    }
  };

  // ── место ──────────────────────────────────────────────────────────

  // Ноль — честный отступ «вплотную к краю», а не «не задано»: настройки
  // приходят уже приведёнными приложением.
  const wxGapX = (w) => (Number.isFinite(w.gap_x) ? w.gap_x : 40);
  const wxGapY = (w) => (Number.isFinite(w.gap_y) ? w.gap_y : 40);

  // posName — место в кадре по-человечески. Внутри оно записано словами
  // «left-bottom»: так его понимают и виджет, и настройки. Показывать это
  // человеку нельзя — он читает подсказку, а не исходник.
  function posName(pos) {
    const [h, v] = String(pos || "left-bottom").split("-");
    const across = { left: "слева", center: "по центру", right: "справа" }[h] || "слева";
    const down = { top: "сверху", middle: "посередине", bottom: "снизу" }[v] || "снизу";
    if (h === "center" && v === "middle") return I18N.t("ровно посередине");
    if (I18N.lang() === "en") return I18N.t(down) + " " + I18N.t(across);
    return across + " " + down;
  }

  // wxDrawFrame подсказывает размер источника. Без этого стример ставил
  // источник 800×600 по умолчанию OBS, и плашка в кадре стояла не там, где
  // он её поставил в панели.
  function wxDrawFrame(w) {
    const [fw, fh] = wxFrame(w);
    const frame = WX_FRAMES[w.frame] ? w.frame : "16:9";
    document.querySelectorAll("#wx-frame-pick button").forEach((b) => {
      b.classList.toggle("on", b.dataset.frame === frame);
    });
    $("wx-anchors").style.setProperty("--wx-arn", (fw / fh).toFixed(4));
    $("wx-anchors").style.setProperty("--wx-ar", fw + " / " + fh);
    $("wx-frame-hint").innerHTML = I18N.t(
      "В OBS у источника «Браузер» поставь ширину <b>{w}</b> и высоту <b>{h}</b> " +
      "и растяни его на весь кадр. Или просто перетащи кнопку «Перетащить в OBS» " +
      "в окно сцены — размер подставится сам.", { w: fw, h: fh });
    // OBS понимает эти параметры у брошенной в сцену ссылки и сразу создаёт
    // источник нужного размера. Самому виджету они не мешают.
    $("wx-drag").href = location.origin + "/widget?layer-name=" +
      encodeURIComponent(I18N.t("Заказ музыки")) + "&layer-width=" + fw + "&layer-height=" + fh;
  }

  $("wx-anchors").onclick = (e) => {
    const btn = e.target.closest("[data-pos]");
    if (btn) saveWidget({ position: btn.dataset.pos });
  };
  $("wx-align").onclick = (e) => {
    const b = e.target.closest("[data-align]");
    if (b) saveWidget({ align: b.dataset.align });
  };
  $("wx-frame-pick").onclick = async (e) => {
    const b = e.target.closest("button[data-frame]");
    if (!b) return;
    await saveWidget({ frame: b.dataset.frame });
    // Связь с OBS включена — источник в OBS сразу становится того же размера.
    if (obsOn) obsShow(await post("/api/obs/fit"));
  };

  live($("wx-scale"), (v) => {
    $("wx-scale-val").textContent = v + "%";
    liveSet({ scale: v / 100 });
  }, (v) => saveWidget({ scale: v / 100 }));
  $("wx-scale-quick").onclick = (e) => {
    const b = e.target.closest("[data-scale]");
    if (b) saveWidget({ scale: Number(b.dataset.scale) / 100 });
  };

  live($("wx-gapx"), (v) => {
    $("wx-gapx-val").textContent = v + " px";
    liveSet({ gap_x: v });
  }, (v) => saveWidget({ gap_x: v, gap: v }));
  live($("wx-gapy"), (v) => {
    $("wx-gapy-val").textContent = v + " px";
    liveSet({ gap_y: v });
  }, (v) => saveWidget({ gap_y: v }));

  // Перетаскивание плашки прямо по кадру.
  //
  // Зачем оно вдобавок к девяти точкам. Точки отвечают на вопрос «к чему
  // прижать», а отступы — «на сколько отодвинуть», и человек, который просто
  // хочет поставить плашку вот сюда, вынужден переводить своё «вот сюда» в
  // две цифры. Здесь он ставит её мышью, а цифры подставляются сами.
  //
  // Привязка при этом сохраняется: плашка прилипает к ближайшей стороне, а не
  // повисает в кадре координатами. Кадр у OBS может быть другого размера, и
  // координаты уехали бы, а привязка нет.
  //
  // Мышь ловит прозрачный слой поверх фрейма: в сам фрейм события не
  // попадают, а плашку внутри мы меряем через его документ.
  (() => {
    const stage = $("wx-stage");
    const hit = $("wx-hit");
    let drag = null;

    // cardBox — где плашка стоит в кадре, в пикселях настоящего кадра.
    function cardBox() {
      if (!wxApi) return null;
      const r = wxApi.slot.getBoundingClientRect();
      return { x: r.left, y: r.top, w: r.width, h: r.height };
    }
    function at(e) {
      const box = stage.getBoundingClientRect();
      return { x: (e.clientX - box.left - wxCam.x) / wxCam.k, y: (e.clientY - box.top - wxCam.y) / wxCam.k };
    }
    const inside = (p, c) => c && p.x >= c.x && p.x <= c.x + c.w && p.y >= c.y && p.y <= c.y + c.h;

    hit.addEventListener("pointermove", (e) => {
      if (drag) {
        const p = dragPlace(at(e), drag);
        liveSet(p);
        hintDrag(p);
        return;
      }
      const ticker = widgetPreset(widget().preset).family.id === "ticker";
      hit.style.cursor = !ticker && inside(at(e), cardBox()) ? "grab" : "";
    });

    hit.addEventListener("pointerdown", (e) => {
      if (widgetPreset(widget().preset).family.id === "ticker") return;
      const p = at(e);
      const c = cardBox();
      if (!inside(p, c)) return;
      const [fw, fh] = wxFrame(widget());
      // Держим плашку за ту же точку, за которую взяли: иначе она прыгает
      // серединой под курсор, и человек промахивается мимо задуманного.
      drag = { dx: p.x - c.x, dy: p.y - c.y, w: c.w, h: c.h, fw, fh };
      wxCamHold = true;
      hit.setPointerCapture(e.pointerId);
      stage.classList.add("dragging");
      hit.style.cursor = "grabbing";
      e.preventDefault();
    });

    const finish = (e) => {
      if (!drag) return;
      const p = dragPlace(at(e), drag);
      drag = null;
      wxCamHold = false;
      stage.classList.remove("dragging");
      hit.style.cursor = "grab";
      saveWidget(p);
    };
    hit.addEventListener("pointerup", finish);
    hit.addEventListener("pointercancel", () => {
      drag = null;
      wxCamHold = false;
      stage.classList.remove("dragging");
      wxLive = null;
      drawWidget();
    });

    // dragPlace переводит место под курсором в «к чему прижать и на сколько».
    function dragPlace(p, d) {
      let x = p.x - d.dx;
      let y = p.y - d.dy;
      // Не выпускаем плашку за кадр: за краем её всё равно не будет видно, а
      // вернуть обратно человек уже не сможет — тащить будет не за что.
      x = Math.min(Math.max(0, x), Math.max(0, d.fw - d.w));
      y = Math.min(Math.max(0, y), Math.max(0, d.fh - d.h));

      // Ближайшая сторона по каждой оси. Середина берётся, только когда
      // плашка и правда стоит посередине: иначе «почти по центру» щёлкало бы
      // туда-сюда между центром и краем.
      const cx = (x + d.w / 2) / d.fw;
      const cy = (y + d.h / 2) / d.fh;
      const h = cx < 0.36 ? "left" : cx > 0.64 ? "right" : "center";
      const v = cy < 0.36 ? "top" : cy > 0.64 ? "bottom" : "middle";

      const out = { position: h + "-" + v };
      if (h !== "center") out.gap_x = Math.round(h === "left" ? x : d.fw - x - d.w);
      if (v !== "middle") out.gap_y = Math.round(v === "top" ? y : d.fh - y - d.h);
      return out;
    }

    // Пока тащат — говорим словами, куда попали и на сколько отступили.
    // Человек должен видеть, что он настраивает, а не только куда ткнул.
    function hintDrag(p) {
      const parts = [posName(p.position)];
      if (p.gap_x !== undefined) parts.push(I18N.t("отступ сбоку {n} px", { n: p.gap_x }));
      if (p.gap_y !== undefined) parts.push(I18N.t("сверху-снизу {n} px", { n: p.gap_y }));
      $("wx-badge").textContent = parts.join(" · ");
    }
  })();

  // ── показ ──────────────────────────────────────────────────────────

  $("wx-by-on").onchange = (e) => saveWidget({ show_requester: e.target.checked });
  $("wx-bar-on").onchange = (e) => saveWidget({ show_bar: e.target.checked });
  $("wx-time-on").onchange = (e) => saveWidget({ show_time: e.target.checked });
  $("wx-eq-on").onchange = (e) => saveWidget({ show_eq: e.target.checked });
  $("wx-own-on").onchange = (e) => saveWidget({ show_own: e.target.checked });
  $("wx-pause-on").onchange = (e) => saveWidget({ hide_on_pause: e.target.checked });
  $("wx-onchange").onchange = (e) => saveWidget({ show_on_change: e.target.checked });

  // Режим показа. «Всё время» — это ноль секунд удержания; так это и
  // хранится в настройках, а человеку показывается словами.
  //
  // Раньше это был один ползунок «убирать через N секунд» с нулём в
  // значении «не убирать». Ноль-как-выключатель посреди шкалы человек не
  // читает: он видит ползунок в крайнем положении и не понимает, что тот
  // сейчас означает совсем другой режим работы.
  $("wx-mode").onclick = (e) => {
    const b = e.target.closest("[data-mode]");
    if (!b) return;
    const flash = b.dataset.mode === "flash";
    saveWidget(flash
      ? { hold_seconds: parseInt($("wx-hold").value, 10) || 15, show_on_change: true }
      : { hold_seconds: 0, show_every: 0 });
  };

  live($("wx-hold"), (v) => {
    $("wx-hold-val").textContent = I18N.t("{n} с", { n: v });
  }, (v) => saveWidget({ hold_seconds: v }));

  live($("wx-every"), (v) => {
    $("wx-every-val").textContent = v
      ? I18N.t("каждые {n} {min}", { n: v, min: plural(v, "минуту", "минуты", "минут", "minute", "minutes") })
      : I18N.t("не напоминать");
  }, (v) => saveWidget({ show_every: v * 60 }));

  // «Показать, как это выглядит» — появление, ожидание, уход, всё подряд.
  //
  // Без этой кнопки настройку «всплывать на 15 секунд» проверить можно было
  // бы только на живом стриме: в образце плашка висит всегда, иначе её не
  // видно во время настройки.
  let tryTimer = null;
  $("wx-flash-try").onclick = () => {
    const hold = parseInt($("wx-hold").value, 10) || 15;
    clearTimeout(tryTimer);
    wxPlay("enter");
    // Ждём столько же, сколько будет ждать виджет, но не дольше десяти
    // секунд: смотреть на неподвижную плашку минуту незачем, а увидеть надо
    // именно уход.
    tryTimer = setTimeout(() => {
      wxPlay("leave");
      tryTimer = setTimeout(() => wxPlay("enter"), 1400);
    }, Math.min(hold, 10) * 1000);
  };

  // ── кадр: сцена, образец, проигрыш ─────────────────────────────────

  $("wx-scene-pick").onclick = (e) => {
    const btn = e.target.closest("[data-scene]");
    if (!btn) return;
    $("wx-stage").dataset.scene = btn.dataset.scene;
    $("wx-scene-pick").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b === btn));
  };
  $("wx-source-pick").onclick = (e) => {
    const btn = e.target.closest("[data-source]");
    if (!btn) return;
    wxSource = btn.dataset.source;
    try { localStorage.setItem("wx-source", wxSource); } catch { /* не запомнится — не беда */ }
    $("wx-source-pick").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b === btn));
    if (wxApi) wxApi.next(wxNow());
    wxOwnCheck();
    wxLiveNote();
  };
  $("wx-play").onclick = () => wxPlay("enter");
  // Пауза образца: столбики ложатся, полоса стоит, а с «Прятать на паузе»
  // плашка уходит — ровно как на стриме.
  $("wx-pause").onclick = () => {
    wxPaused = !wxPaused;
    $("wx-pause").classList.toggle("on", wxPaused);
    if (wxApi) wxApi.track(wxNow());
  };
  $("wx-zoom").onclick = () => {
    wxZoom = !wxZoom;
    // Отъезд обратно тоже плавный: класс держим, пока камера едет.
    const stage = $("wx-stage");
    stage.classList.add("zooming");
    wxLayout();
    setTimeout(() => stage.classList.remove("zooming"), 550);
  };
  $("wx-leave").onclick = () => wxLeaveAndBack();
  $("wx-next").onclick = () => wxPlay("next");

  $("wx-copy").onclick = async () => {
    try {
      await navigator.clipboard.writeText($("wx-url").value);
      say(I18N.t("Адрес скопирован — вставь его в источник «Браузер» в OBS."));
    } catch {
      // Буфер обмена доступен не везде; выделенный текст скопируется вручную.
      $("wx-url").select();
      say(I18N.t("Скопируй выделенный адрес: Ctrl+C"), true);
    }
  };

  let wxLiveKey = null;
  // Ничего не играет — честно говорим, что в кадре пример.
  function wxLiveNote() {
    const idle = wxSource === "live" && !(lastState && lastState.now);
    $("wx-badge").classList.toggle("warn", idle);
    if (idle) $("wx-badge").textContent = I18N.t("сейчас ничего не играет — показан пример");
    else wxLayout();
  }

  // Своя музыка выключена, а образец показывает её — значит, на стриме
  // плашки в этот момент не будет. Образец обязан показать то же самое.
  function wxOwnCheck() {
    if (!wxApi) return;
    const hidden = wxSource === "own" && widget().show_own === false;
    const shown = wxApi.card.classList.contains("show") && !wxApi.card.classList.contains("leave");
    $("wx-badge").classList.toggle("warn", hidden);
    if (hidden && shown) wxApi.play("leave");
    else if (!hidden && !shown) wxPlay("enter");
    if (hidden) $("wx-badge").textContent = I18N.t("своя музыка не показывается — включи во вкладке «Показ»");
  }

  // ── всё вместе ─────────────────────────────────────────────────────

  const seg = (id, attr, value) => {
    document.querySelectorAll("#" + id + " [data-" + attr + "]").forEach((b) => {
      b.classList.toggle("on", b.dataset[attr] === String(value));
    });
  };

  function drawWidget() {
    wxBoot();
    const w = widget();
    const family = widgetPreset(w.preset).family;

    wxPaint();
    wxDrawFrame(w);

    // Стиль
    $("wx-gallery").querySelectorAll("[data-family]").forEach((t) => {
      t.classList.toggle("on", t.dataset.family === family.id);
    });
    drawVariants(w);

    // Обложка
    $("wx-art-on").checked = w.show_art !== false;
    $("wx-shape-group").classList.toggle("off", w.show_art === false);
    drawShapes(w);

    // Цвет
    $("wx-magic").checked = !!w.magic;
    drawTweaks(w);
    drawFonts(w);
    const op = Number.isFinite(w.opacity) ? w.opacity : 100;
    $("wx-op").value = op;
    $("wx-op-val").textContent = op + "%";

    // Своё и свои стили
    drawSlots(w);
    drawMine(w);

    // Анимация
    drawAnims(w);
    $("wx-motion-on").checked = w.motion !== false;

    // Место. Тикер занимает всю ширину — выбор угла для него ничего не
    // значит, и делать вид, что значит, нечестно.
    const ticker = family.id === "ticker";
    $("wx-anchors").classList.toggle("narrow", ticker);
    // Отступ от края по оси, где плашка стоит посередине, ни на что не
    // влияет. Живой ползунок, который ничего не делает, хуже серого.
    const [ph, pv] = (w.position || "left-bottom").split("-");
    $("wx-gapx").disabled = ticker || ph === "center";
    $("wx-gapy").disabled = ticker || pv === "middle";
    $("wx-gapx").closest(".wxe-slider").classList.toggle("off", $("wx-gapx").disabled);
    $("wx-gapy").closest(".wxe-slider").classList.toggle("off", $("wx-gapy").disabled);
    $("wx-pos-hint").textContent = ticker
      ? I18N.t("Тикер занимает всю ширину кадра: остаётся только выбрать верх или низ.")
      : I18N.t("Плашку можно просто перетащить мышью прямо по кадру — точка привязки и отступы подставятся сами.");
    const chosenPos = w.position || "left-bottom";
    $("wx-anchors").querySelectorAll("button").forEach((b) => b.classList.toggle("on", b.dataset.pos === chosenPos));
    const percent = Math.round((w.scale || 1) * 100);
    $("wx-scale").value = percent;
    $("wx-scale-val").textContent = percent + "%";
    seg("wx-scale-quick", "scale", percent);
    const gx = wxGapX(w), gy = wxGapY(w);
    $("wx-gapx").value = gx;
    $("wx-gapx-val").textContent = gx + " px";
    $("wx-gapy").value = gy;
    $("wx-gapy-val").textContent = gy + " px";
    seg("wx-align", "align", w.align || "auto");

    // Показ
    $("wx-by-on").checked = w.show_requester !== false;
    $("wx-bar-on").checked = w.show_bar !== false;
    $("wx-time-on").checked = !!w.show_time;
    $("wx-eq-on").checked = !!w.show_eq;
    $("wx-own-on").checked = w.show_own !== false;
    $("wx-pause-on").checked = !!w.hide_on_pause;

    const hold = w.hold_seconds || 0;
    const flash = hold > 0;
    seg("wx-mode", "mode", flash ? "flash" : "always");
    $("wx-flash").hidden = !flash;
    $("wx-mode-hint").textContent = flash
      ? I18N.t("Плашка появляется, висит сколько сказано и уходит.")
      : I18N.t("Плашка стоит в кадре, пока играет музыка.");
    $("wx-hold").value = flash ? hold : 15;
    $("wx-hold-val").textContent = I18N.t("{n} с", { n: flash ? hold : 15 });
    $("wx-onchange").checked = w.show_on_change !== false;
    // «Не всплывать на смене» без напоминаний — плашка не появится никогда.
    // Приложение такую пару всё равно чинит, но пусть человек видит это
    // здесь, а не удивляется пустому кадру.
    const never = !w.show_every;
    $("wx-onchange").disabled = never;
    $("wx-onchange").closest(".wxe-switch").classList.toggle("locked", never);
    $("wx-onchange-note").textContent = never
      ? I18N.t("Выключить можно, только если плашка напоминает о себе, — иначе она не появится вообще.")
      : I18N.t("Главный повод показать плашку: заиграло что-то новое.");
    const everyMin = Math.round((w.show_every || 0) / 60);
    $("wx-every").value = everyMin;
    $("wx-every-val").textContent = everyMin
      ? I18N.t("каждые {n} {min}", { n: everyMin, min: plural(everyMin, "минуту", "минуты", "минут", "minute", "minutes") })
      : I18N.t("не напоминать");

    const url = location.origin + "/widget";
    $("wx-url").value = url;
    $("wx-open").href = url + "?demo=1";

    if (wxApi) wxApi.setLang(I18N.lang());
    wxOwnCheck();
  }

  // ── кнопки обновления ──────────────────────────────────────────────

  $("update-now").onclick = (e) => installUpdate(e.currentTarget);
  $("upd-install").onclick = (e) => installUpdate(e.currentTarget);

  $("upd-check").onclick = async (e) => {
    const d = await post("/api/update/check", e.currentTarget);
    if (!d) return;
    say(d.available
      ? I18N.t("Вышла версия {v} — кнопка «Обновить» рядом", { v: d.version })
      : I18N.t("У тебя последняя версия: {v}", { v: d.current }));
  };

  $("upd-repo").onchange = async (e) => {
    const repo = e.target.value.trim();
    if (await saveConfig({ update_repo: repo })) markSaved(e.target);
  };

  $("widget-toggle").onclick = () => toggleWidget();

  // ── связь с OBS ────────────────────────────────────────────────────
  // Панель только показывает итог: подключается и подгоняет приложение.
  let obsOn = false;
  function obsShow(d) {
    if (!d) return;
    obsOn = !!d.enabled;
    if (d.port && document.activeElement !== $("obs-port")) $("obs-port").value = d.port;
    const st = $("obs-state");
    st.className = "obs-state " + (d.level === "ok" ? "ok" : d.level === "fail" ? "fail" : "");
    $("obs-state-text").textContent =
      d.level === "ok" ? I18N.t("подключено") :
      d.level === "fail" ? I18N.t("ошибка {code}", { code: d.code || "" }) :
      I18N.t("не подключено");
    $("obs-connect").textContent = d.level === "ok" ? I18N.t("Переподключить") : I18N.t("Подключить OBS");
    $("obs-connect").classList.toggle("key", d.level !== "ok");
    $("obs-fit").hidden = !(d.level === "ok" && !d.no_source);
    $("obs-add").hidden = !(d.level === "ok" && d.no_source);
    $("obs-how").hidden = d.level === "ok";
    const note = $("obs-note");
    let text = "";
    note.classList.remove("warn");
    if (d.level === "fail") {
      text = errLine(d);
      note.classList.add("warn");
    } else if (d.level === "ok" && d.no_source) {
      text = I18N.t("В сценах OBS виджета нет — нажми «Добавить в OBS», и он встанет в текущую сцену.");
    } else if (d.level === "ok") {
      text = I18N.t("Виджет растянут на весь кадр OBS {canvas}.", { canvas: d.canvas || "" });
    }
    if (d.level === "ok" && d.suggest) {
      text += " " + I18N.t("Кадр OBS сейчас {canvas} — формат {frame} подходит лучше.",
        { canvas: d.canvas, frame: d.suggest });
      note.classList.add("warn");
    }
    note.textContent = text.trim();
  }
  $("obs-connect").onclick = async () => {
    const port = parseInt($("obs-port").value, 10) || 4455;
    const d = await post("/api/obs/connect", $("obs-connect"), { port, password: $("obs-pass").value });
    $("obs-pass").value = "";
    obsShow(d);
  };
  $("obs-fit").onclick = async () => obsShow(await post("/api/obs/fit", $("obs-fit")));
  $("obs-add").onclick = async () => obsShow(await post("/api/obs/add", $("obs-add")));
  fetch("/api/obs").then((r) => r.json()).then((d) => {
    obsShow(d);
    // Связь включена раньше — проверяем её тихо, без ожидания.
    if (d.enabled) post("/api/obs/fit").then(obsShow);
  }).catch(() => {});
  // Щелчок по кнопке перетаскивания открыл бы виджет во вкладке — это не то,
  // что от неё ждут. Её надо тащить мышью.
  $("wx-drag").onclick = (e) => {
    e.preventDefault();
    say(I18N.t("Эту кнопку надо не нажать, а перетащить мышью в окно сцены OBS."));
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
      else say(I18N.t("Мастер настройки не загрузился — перезапусти приложение"), true);
    };
  }

  // ── сборка ─────────────────────────────────────────────────────────

  let lastState = null;

  function render(s) {
    lastState = s;
    // Язык сменили в другом окне или в мастере — догоняем. setLang сам
    // вызовет перерисовку через onChange.
    if (s.lang && s.lang !== I18N.lang()) {
      I18N.setLang(s.lang);
      return;
    }
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
    // Живой образец в редакторе виджета идёт за настоящим треком: сменился
    // трек, встал на паузу — образец делает то же самое.
    if (wxSource === "live" && wxApi) {
      const key = s.now ? nowKey(s.now) + "|" + !!s.now.paused : "";
      if (key !== wxLiveKey) { wxLiveKey = key; wxApi.track(wxNow()); wxLiveNote(); }
    }
    draw("orders", sig([s.queue, s.paused, s.twitch.reward_title,
                        s.twitch.reward_cost, s.twitch.reward_ready]), () => renderOrders(s));
    draw("update", sig(s.update), () => renderUpdate(s.update || {}));
    draw("playlists", sig(s.playlists), () => renderPlaylists(s));
    draw("ytasks", sig(s.youtube_asks), () => renderYouTubeAsks(s));
    draw("yt-recent", sig(s.youtube_recent), () => renderYouTubeRecent(s.youtube_recent));
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
    if (tb) tb.textContent = s.now ? trackLine(s.now.artist, s.now.title) : "";

    // Заголовок вкладки — тоже часть панели: стример держит её в фоне.
    document.title = s.now
      ? trackLine(s.now.artist, s.now.title)
      : s.queue.length > 0
        ? I18N.t("{n} в очереди · Заказ музыки", { n: s.queue.length })
        : I18N.t("Заказ музыки");
  }

  // ── язык ───────────────────────────────────────────────────────────
  //
  // Выбор хранится в настройках приложения (поле language) и приходит в
  // снимке состояния (lang). Подписи кнопок не переводятся: каждая на своём языке.
  function drawLangPick() {
    const box = $("lang-pick");
    if (!box) return;
    box.querySelectorAll("[data-lang]").forEach((b) => {
      b.textContent = b.dataset.lang === "en" ? "English" : "Русский";
      b.classList.toggle("on", b.dataset.lang === I18N.lang());
    });
  }
  drawLangPick();
  if ($("lang-pick")) {
    $("lang-pick").onclick = async (e) => {
      const b = e.target.closest("[data-lang]");
      if (!b || b.dataset.lang === I18N.lang()) return;
      I18N.setLang(b.dataset.lang);
      await saveConfig({ language: b.dataset.lang });
    };
  }
  // Смена языка — полная перерисовка: draw() иначе не тронет блоки, у которых
  // данные не поменялись.
  // Раздел «Обход блокировок» нужен там, где Spotify заблокирован. Русским
  // он открыт, англоязычным — свёрнут: VPN-советы им только мешают. Ключ уже
  // вставлен — открыт при любом языке.
  function syncUnblock() {
    const box = $("unblock-box");
    if (!box) return;
    const hasKey = !!(lastState && lastState.tunnel && lastState.tunnel.has_key);
    box.open = I18N.lang() !== "en" || hasKey;
  }
  syncUnblock();

  I18N.onChange(() => {
    drawn.clear();
    drawLangPick();
    syncUnblock();
    modsLoaded = false;
    if (!$("settings").hidden && settingsTab === "orders") loadMods();
    if (lastState) render(lastState);
    if (config) { refreshModeHints(); if (!$("widget").hidden) drawWidget(); }
    if (!$("log").hidden) { loadHistory(); loadModLog(); }
    if (playlistsLoaded && !playlistsFailed) { playlistsLoaded = false; loadPlaylists(); }
  });

  fetch("/static/icons/sprite.svg")
    .then((r) => r.text())
    .then((svg) => { $("sprite").innerHTML = svg; });

  loadConfig();
  connect();
})();
