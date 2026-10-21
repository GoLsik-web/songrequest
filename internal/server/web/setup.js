// Знакомство: своя полоса заголовка, заставка при запуске и мастер первой
// настройки.
//
// Зачем это отдельный файл. app.js — панель, которую стример видит каждый
// день; здесь — первые полторы минуты знакомства с программой. Эти две вещи
// живут по разным правилам и меняются в разное время, поэтому и лежат
// врозь. Общее у них ровно одно: снимок состояния. Панель отдаёт его сюда
// вызовом window.srSetupState.
//
// Ни одной новой ручки у приложения мастер не просит: он нажимает те же самые
// кнопки, что и панель, — «включить обход», «подключить Spotify», «сохранить
// настройки». Поэтому пройденный мастер и настроенная руками панель дают
// ровно одно и то же состояние.
(() => {
  const $ = (id) => document.getElementById(id);

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));

  // Перевод (см. i18n.js). В русском режиме t() возвращает ключ как есть.
  const t = (ru, vars) => (window.I18N ? window.I18N.t(ru, vars) : ru);
  const en = () => !!(window.I18N && window.I18N.lang() === "en");
  // tt — для многострочных кусков разметки: ключ словаря — тот же текст со
  // схлопнутыми пробелами. По-русски отдаёт исходник байт в байт.
  const tt = (s) => (en() ? t(s.replace(/\s+/g, " ").trim()) : s);

  // ── своя полоса заголовка ──────────────────────────────────────────
  //
  // Показывается только в окне программы. Проверяем не по названию браузера,
  // а по тому, есть ли у страницы связь с окном: имена srWindow* вставляет
  // само приложение. В браузере их нет — и полосы там нет, потому что двигать
  // и закрывать нечего.

  function setupTitlebar() {
    const bar = $("titlebar");
    if (!bar || typeof window.srWindowDrag !== "function") return;

    document.body.classList.add("has-titlebar");

    // Тянем окно за всю полосу, кроме кнопок.
    const grip = bar.querySelector(".tb-grip");
    grip.addEventListener("mousedown", (e) => {
      // Только левая кнопка: правой Windows показывает своё меню окна.
      if (e.button !== 0) return;
      window.srWindowDrag();
    });
    // Двойной щелчок по полосе разворачивает окно и возвращает обратно —
    // так делает любое окно Windows, и отучать от этого никого нельзя.
    grip.addEventListener("dblclick", () => window.srWindowMaxToggle());

    bar.querySelector(".win-min").onclick = () => window.srWindowMinimize();
    bar.querySelector(".win-max").onclick = () => window.srWindowMaxToggle();
    bar.querySelector(".win-close").onclick = () => window.srWindowHide();

    // Приложение сообщает сюда, развёрнуто ли окно: значок на кнопке разный,
    // а сама страница про это не знает — для неё разворот ничем не отличается
    // от того, что окно растянули мышью.
    window.srMaximized = (on) => {
      const btn = bar.querySelector(".win-max");
      btn.dataset.on = on ? "1" : "";
      const label = tt(on ? "Свернуть в окно" : "Развернуть");
      btn.title = label;
      btn.setAttribute("aria-label", label);
    };
    if (typeof window.srWindowIsMaximized === "function") {
      window.srWindowIsMaximized().then(window.srMaximized, () => {});
    }
  }

  // ── заставка ───────────────────────────────────────────────────────

  const INTRO_MS = 3000;

  function playIntro() {
    return new Promise((done) => {
      const box = $("intro");
      if (!box) return done();

      box.hidden = false;
      let closed = false;
      const close = () => {
        if (closed) return;
        closed = true;
        box.classList.add("gone");
        // Ждём, пока доиграет исчезновение, и только потом убираем совсем:
        // hidden посреди перехода — это моргание вместо ухода.
        setTimeout(() => { box.hidden = true; done(); }, 460);
      };

      // Пропустить можно щелчком по заставке или клавишей Escape.
      //
      // Отдельной кнопки «пропустить интро» больше нет: она висела в углу
      // заставки и была единственным что ни есть элементом управления на экране,
      // который должен быть просто красивым. Закрывается заставка и без неё:
      // щелчком куда угодно, Escape или сама по себе через INTRO_MS.
      // Enter и пробел сюда намеренно не входят: приложение запускают, не
      // выходя из игры, и случайное нажатие пробела не должно ни закрывать
      // заставку, ни проваливаться дальше — на кнопку первого шага настройки.
      box.onclick = (e) => { if (e.target === box || e.target.closest(".intro-inner")) close(); };
      const key = (e) => { if (e.key === "Escape") close(); };
      document.addEventListener("keydown", key, { once: true });
      setTimeout(close, INTRO_MS);
    });
  }

  // ── рисованные схемы ───────────────────────────────────────────────
  //
  // Вместо снимков чужих сайтов. Снимок понятнее ровно до того дня, когда
  // Spotify или Twitch поменяют свою страницу: тогда он начинает врать, а
  // человек ищет кнопку, которой больше нет. Схема показывает устройство
  // страницы — где адрес, где поле, где кнопка, — и стареет медленнее. Плюс
  // она весит килобайты, а не мегабайты внутри .exe.

  const C = {
    bg: "#0b0d0e", raise: "#14171a", line: "#282c30",
    ink: "#e8ebed", dim: "#8b9298", lime: "#c8f751",
  };

  // window рисует окно браузера с адресной строкой и содержимым.
  function browserArt(url, inner, h = 210) {
    return `<svg viewBox="0 0 640 ${h}" role="img" aria-label="${esc(t("Схема страницы: {url}", { url }))}">
      <rect x="1" y="1" width="638" height="${h - 2}" rx="12" fill="${C.bg}" stroke="${C.line}"/>
      <rect x="1" y="1" width="638" height="34" rx="12" fill="${C.raise}"/>
      <rect x="1" y="24" width="638" height="12" fill="${C.raise}"/>
      <circle cx="22" cy="18" r="4" fill="${C.line}"/>
      <circle cx="38" cy="18" r="4" fill="${C.line}"/>
      <circle cx="54" cy="18" r="4" fill="${C.line}"/>
      <rect x="72" y="8" width="548" height="20" rx="10" fill="${C.bg}" stroke="${C.line}"/>
      <text x="86" y="22" font-family="Manrope, sans-serif" font-size="11" fill="${C.dim}">${esc(url)}</text>
      ${inner}
    </svg>`;
  }

  // field рисует строку «подпись — поле», при hot=true поле подсвечено лаймом.
  function fieldArt(x, y, w, label, value, hot) {
    const stroke = hot ? C.lime : C.line;
    const ink = hot ? C.lime : C.dim;
    return `
      <text x="${x}" y="${y}" font-family="Manrope, sans-serif" font-size="10"
            fill="${C.dim}" letter-spacing="1">${esc(t(label))}</text>
      <rect x="${x}" y="${y + 8}" width="${w}" height="26" rx="7"
            fill="${C.bg}" stroke="${stroke}" ${hot ? 'stroke-width="1.5"' : ""}/>
      <text x="${x + 10}" y="${y + 25}" font-family="ui-monospace, Consolas, monospace"
            font-size="11" fill="${ink}">${esc(t(value))}</text>`;
  }

  function buttonArt(x, y, w, text, hot) {
    return `
      <rect x="${x}" y="${y}" width="${w}" height="26" rx="13"
            fill="${hot ? C.lime : "none"}" stroke="${hot ? C.lime : C.line}"/>
      <text x="${x + w / 2}" y="${y + 17}" text-anchor="middle"
            font-family="Manrope, sans-serif" font-size="11" font-weight="600"
            fill="${hot ? "#0d1204" : C.dim}">${esc(t(text))}</text>`;
  }

  // ── шаги ───────────────────────────────────────────────────────────
  //
  // Порядок не случайный: сначала обход, иначе вход в Spotify не пройдёт
  // вовсе и человек упрётся в непонятную ошибку на первом же шаге.

  const host = location.host;
  // Адреса возврата берутся с сервера (/api/setup): он знает, на каком адресе
  // реально слушает. Здесь — только запасной вариант до ответа.
  let redirectURI = `http://127.0.0.1:${location.port || 8977}/callback`;
  let donationsURI = `http://127.0.0.1:${location.port || 8977}/donations/callback`;

  // tunnelNeeded — Spotify недавно отказал напрямую «не работает из этой
  // страны». Только тогда шаг обхода важный: у кого Spotify открывается и так,
  // тому обход не нужен вовсе.
  const tunnelNeeded = () => {
    const at = config && config.tunnel_needed_at ? Date.parse(config.tunnel_needed_at) : 0;
    return at > 0 && Date.now() - at < 7 * 24 * 3600 * 1000;
  };
  const tunnelReady = () => {
    const tn = (state && state.tunnel) || {};
    return !!(tn.on || tn.standby);
  };
  const widgetURL = `http://${host}/widget`;

  const STEPS = [
    // Язык — самым первым: дальше весь мастер уже на выбранном.
    {
      id: "lang",
      name: "Язык / Language",
      must: false,
      noTag: true,
      short: "Русский · English",
      title: "Язык / Language",
      why: "На каком языке показывать приложение. Поменять можно в любой момент в настройках.",
      art: null,
      body: () => `
        <div class="preset-pick lang-pick" data-no-i18n>
          <button data-act="lang" data-lang="ru" class="${en() ? "" : "on"}">Русский</button>
          <button data-act="lang" data-lang="en" class="${en() ? "on" : ""}">English</button>
        </div>
        <div class="check" id="su-check"></div>`,
      check: () => ({ ok: true, text: t("Язык: русский") }),
    },

    {
      id: "tunnel",
      name: "Обход блокировок",
      // Англоязычным обход не нужен: Spotify у них работает напрямую.
      hidden: () => en(),
      must: () => tunnelNeeded(),
      short: "ключ от VPN",
      title: "Обход блокировок",
      why: "Spotify не отвечает приложениям из России. Вставь сюда ключ от своего VPN или ссылку на подписку — приложение само скачает всё нужное, переберёт серверы и оставит тот, через который Spotify работает. Отдельных программ запускать не надо.",
      art: () => `<svg viewBox="0 0 640 150" role="img" aria-label="Схема: приложение ходит к Spotify через сервер обхода">
        <rect x="14" y="46" width="150" height="58" rx="12" fill="${C.raise}" stroke="${C.line}"/>
        <text x="89" y="72" text-anchor="middle" font-family="Manrope" font-size="12" fill="${C.ink}">Это приложение</text>
        <text x="89" y="90" text-anchor="middle" font-family="Manrope" font-size="10" fill="${C.dim}">твой компьютер</text>

        <path d="M170 75 h70" stroke="${C.lime}" stroke-width="1.5" stroke-dasharray="4 4"/>
        <path d="M236 70 l8 5 -8 5 z" fill="${C.lime}"/>

        <rect x="246" y="40" width="150" height="70" rx="12" fill="${C.raise}" stroke="${C.lime}"/>
        <text x="321" y="66" text-anchor="middle" font-family="Manrope" font-size="12" fill="${C.lime}">Сервер обхода</text>
        <text x="321" y="84" text-anchor="middle" font-family="Manrope" font-size="10" fill="${C.dim}">по твоему ключу</text>
        <text x="321" y="100" text-anchor="middle" font-family="Manrope" font-size="10" fill="${C.dim}">выбирается сам</text>

        <path d="M402 75 h70" stroke="${C.lime}" stroke-width="1.5" stroke-dasharray="4 4"/>
        <path d="M468 70 l8 5 -8 5 z" fill="${C.lime}"/>

        <rect x="478" y="46" width="148" height="58" rx="12" fill="${C.raise}" stroke="${C.line}"/>
        <text x="552" y="72" text-anchor="middle" font-family="Manrope" font-size="12" fill="${C.ink}">Spotify</text>
        <text x="552" y="90" text-anchor="middle" font-family="Manrope" font-size="10" fill="${C.dim}">заказы и плеер</text>

        <text x="14" y="26" font-family="Manrope" font-size="10" fill="${C.dim}" letter-spacing="1">ЧЕРЕЗ ОБХОД ИДУТ ТОЛЬКО ЗАПРОСЫ К SPOTIFY — ИГРА, ЧАТ И СТРИМ ОСТАЮТСЯ НА ПРЯМОМ КАНАЛЕ</text>
      </svg>`,
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Открой личный кабинет своего VPN и найди там <b>ссылку на подписку</b>
              («Subscription», «Импорт в v2rayN») или отдельный ключ вида
              <span class="quiet">vless://…</span>, <span class="quiet">trojan://…</span>,
              <span class="quiet">ss://…</span>`)}</li>
          <li>${tt(`Скопируй её целиком и вставь в поле ниже`)}</li>
          <li>${tt(`Нажми <b>Включить обход</b> и подожди. В первый раз приложение качает
              программу обхода и перебирает серверы — до пары минут`)}</li>
        </ol>
        <label class="setup-field">
          <span>Ключ от VPN или ссылка на подписку</span>
          <input type="password" id="su-tunnel" spellcheck="false" autocomplete="off"
                 placeholder="vless://… или https://…">
        </label>
        <div class="setup-acts">
          <button class="s-btn key" data-act="tunnel-on">Включить обход</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Ключ никому не виден: он ложится в хранилище паролей Windows.
          В настройках приложения и в архиве диагностики его нет.
          <br>Если Spotify у тебя открывается без всякого VPN — этот шаг можно пропустить.`)}</p>`,
      check: (s) => {
        const tn = (s && s.tunnel) || {};
        if (tn.on) return { ok: true, text: t("Обход работает: {server}", { server: tn.server || t("сервер выбран") }) };
        // Ключ вставлен, а обход не поднят, потому что Spotify отвечает и без
        // него: шаг сдан, чинить нечего.
        if (tn.standby) {
          return { ok: true, text: tn.note || t("Ключ сохранён. Обход наготове — сейчас Spotify отвечает и без него.") };
        }
        if (tn.note) return { ok: false, text: tn.note };
        return { ok: false, text: t("Обход пока не включён.") };
      },
    },

    {
      id: "spotify-id",
      name: "Ключ Spotify",
      must: true,
      short: "Client ID",
      title: "Spotify: свой ключ приложения",
      why: "Spotify не разрешает чужим программам управлять твоей музыкой просто так. Нужен собственный ключ — Client ID. Заводится один раз, бесплатно, на том же аккаунте, где у тебя Premium.",
      art: () => browserArt("developer.spotify.com/dashboard → Create app", `
        ${fieldArt(30, 60, 260, "APP NAME", "Заказ музыки", false)}
        ${fieldArt(30, 118, 260, "APP DESCRIPTION", "музыка по заказам зрителей", false)}
        ${fieldArt(320, 60, 290, "REDIRECT URI  ← самое важное", redirectURI, true)}
        ${buttonArt(320, 118, 70, "Add", true)}
        <text x="400" y="136" font-family="Manrope" font-size="10" fill="${C.dim}">не нажмёшь Add — адрес не сохранится</text>
        ${buttonArt(540, 170, 70, "Save", false)}
        <text x="30" y="180" font-family="Manrope" font-size="10" fill="${C.dim}">☑ Web API</text>
      `),
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Открой <b>developer.spotify.com/dashboard</b> и войди
              <b>тем же аккаунтом Spotify</b>, на котором у тебя Premium`)}</li>
          <li>${tt(`Нажми <b>Create app</b>. Название и описание — любые`)}</li>
          <li>${tt(`В поле <b>Redirect URI</b> вставь строку ниже и обязательно нажми
              <b>Add</b> справа от поля`)}</li>
          <li>${tt(`Поставь галочку <b>Web API</b>, согласись с условиями и нажми <b>Save</b>`)}</li>
          <li>${tt(`Зайди в <b>Settings</b> приложения и скопируй <b>Client ID</b>.
              <span class="quiet">Client secret не нужен — не показывай его никому.</span>`)}</li>
        </ol>
        <div class="copyline">
          <code>${esc(redirectURI)}</code>
          <button class="s-btn" data-act="copy" data-text="${esc(redirectURI)}">Скопировать</button>
        </div>
        <label class="setup-field">
          <span>Client ID Spotify</span>
          <input type="text" id="su-spotify-id" spellcheck="false" autocomplete="off"
                 placeholder="набор букв и цифр">
        </label>
        <div class="setup-acts">
          <button class="s-btn key" data-act="save-spotify-id">Сохранить</button>
        </div>
        <div class="check" id="su-check"></div>`,
      fill: (cfg) => { $("su-spotify-id").value = (cfg && cfg.spotify_client_id) || ""; },
      check: (s, cfg) => cfg && cfg.spotify_client_id
        ? { ok: true, text: t("Ключ сохранён.") }
        : { ok: false, text: t("Ключ пока не вставлен.") },
    },

    {
      id: "spotify-login",
      name: "Вход в Spotify",
      must: true,
      short: "разрешить доступ",
      title: "Spotify: вход",
      why: "Теперь разреши приложению управлять твоим плеером. Откроется страница входа Spotify — если обход включён, она откроется прямо в окне программы и закроется сама.",
      art: () => browserArt("accounts.spotify.com", `
        <text x="320" y="86" text-anchor="middle" font-family="Unbounded, Manrope" font-size="14" fill="${C.ink}">Заказ музыки хочет:</text>
        <text x="320" y="112" text-anchor="middle" font-family="Manrope" font-size="11" fill="${C.dim}">управлять воспроизведением · видеть, что играет</text>
        ${buttonArt(270, 140, 100, "Agree", true)}
      `, 190),
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Нажми <b>Подключить Spotify</b>`)}</li>
          <li>${tt(`На странице Spotify нажми <b>Agree</b>`)}</li>
          <li>${tt(`Окно закроется само, а здесь появится твой аккаунт`)}</li>
        </ol>
        <p class="why"><b>${t("Нужен Spotify Premium:")}</b> ${t("без него Spotify не даёт программам управлять плеером.")}</p>
        ${!en() && !tunnelReady() ? `<p class="why warn">${t("Обход не включён. Из России страница входа Spotify может не открыться — включи обход (шаг «Обход блокировок») или свой VPN на время входа.")}</p>` : ""}
        <div class="setup-acts">
          <button class="s-btn key" data-act="spotify-login">Подключить Spotify</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Не открылось или ругается на адрес возврата — вернись на шаг назад и
          сверь <b>Redirect URI</b> в настройках приложения Spotify: он должен совпадать
          до последнего знака.`)}</p>`,
      check: (s) => s && s.spotify && s.spotify.connected
        ? { ok: true, text: t("Spotify подключён") + (s.spotify.account ? ": " + s.spotify.account : "") }
        : { ok: false, text: t("Вход пока не выполнен.") },
    },

    {
      id: "twitch-id",
      name: "Ключ Twitch",
      must: true,
      short: "Client ID",
      title: "Twitch: свой ключ приложения",
      why: "То же самое, что со Spotify, только на Twitch: нужен собственный Client ID. Заводится на том аккаунте, с которого ты стримишь.",
      art: () => browserArt("dev.twitch.tv/console/apps → Register Your Application", `
        ${fieldArt(30, 60, 260, "NAME", "Заказ музыки", false)}
        ${fieldArt(30, 118, 260, "CATEGORY", "Broadcasting Suite", false)}
        ${fieldArt(320, 60, 290, "OAUTH REDIRECT URLS", "http://localhost", true)}
        ${buttonArt(320, 118, 70, "Add", true)}
        ${buttonArt(540, 170, 70, "Create", false)}
        <text x="30" y="180" font-family="Manrope" font-size="10" fill="${C.dim}">Client Type: Public (если такого поля нет — ничего не делай)</text>
      `),
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Включи на аккаунте Twitch <b>двухфакторную защиту</b> — без неё Twitch не даст
              зарегистрировать приложение. <span class="quiet">Настройки → Безопасность и приватность.</span>`)}</li>
          <li>${tt(`Открой <b>dev.twitch.tv/console/apps</b> и нажми <b>Register Your Application</b>`)}</li>
          <li>${tt(`Имя — любое, но уникальное на весь Twitch. Ругнётся «занято» — допиши свой ник`)}</li>
          <li>${tt(`В <b>OAuth Redirect URLs</b> вставь строку ниже и нажми <b>Add</b>.
              <span class="quiet">Приложению этот адрес не нужен, но поле обязательное.</span>`)}</li>
          <li>${tt(`Category — <b>Broadcasting Suite</b>, Client Type — <b>Public</b>, капча,
              <b>Create</b>`)}</li>
          <li>${tt(`Нажми <b>Manage</b> у созданного приложения и скопируй <b>Client ID</b>.
              <span class="quiet">Кнопку New Secret не трогай.</span>`)}</li>
        </ol>
        <div class="copyline">
          <code>http://localhost</code>
          <button class="s-btn" data-act="copy" data-text="http://localhost">Скопировать</button>
        </div>
        <label class="setup-field">
          <span>Client ID Twitch</span>
          <input type="text" id="su-twitch-id" spellcheck="false" autocomplete="off"
                 placeholder="набор букв и цифр">
        </label>
        <div class="setup-acts">
          <button class="s-btn key" data-act="save-twitch-id">Сохранить</button>
        </div>
        <div class="check" id="su-check"></div>`,
      fill: (cfg) => { $("su-twitch-id").value = (cfg && cfg.twitch_client_id) || ""; },
      check: (s, cfg) => cfg && cfg.twitch_client_id
        ? { ok: true, text: t("Ключ сохранён.") }
        : { ok: false, text: t("Ключ пока не вставлен.") },
    },

    {
      id: "twitch-login",
      name: "Вход в Twitch",
      must: true,
      short: "код на сайте",
      title: "Twitch: вход по коду",
      why: "Twitch пускает по короткому коду: приложение показывает код, ты вводишь его на сайте Twitch — с этого же компьютера или с телефона.",
      art: () => `<svg viewBox="0 0 640 150" role="img" aria-label="Схема: код для входа в Twitch">
        <rect x="1" y="1" width="638" height="148" rx="12" fill="${C.bg}" stroke="${C.line}"/>
        <text x="320" y="46" text-anchor="middle" font-family="Manrope" font-size="11" fill="${C.dim}">открой адрес и введи код</text>
        <text x="320" y="84" text-anchor="middle" font-family="Unbounded, Manrope" font-size="26" fill="${C.lime}" letter-spacing="4">WXYZ-7788</text>
        <text x="320" y="112" text-anchor="middle" font-family="ui-monospace, Consolas, monospace" font-size="12" fill="${C.ink}">twitch.tv/activate</text>
        <text x="320" y="134" text-anchor="middle" font-family="Manrope" font-size="10" fill="${C.dim}">код живёт полчаса · не успел — нажми «Подключить Twitch» ещё раз</text>
      </svg>`,
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Нажми <b>Подключить Twitch</b>`)}</li>
          <li>${tt(`Наверху панели появится крупный код и адрес рядом с ним`)}</li>
          <li>${tt(`Открой адрес, введи код и нажми <b>Authorize</b>`)}</li>
          <li>${tt(`Через несколько секунд код исчезнет сам`)}</li>
        </ol>
        <div class="setup-acts">
          <button class="s-btn key" data-act="twitch-login">Подключить Twitch</button>
        </div>
        <div class="check" id="su-check"></div>`,
      check: (s) => {
        const tw = (s && s.twitch) || {};
        if (tw.connected) {
          return { ok: true, text: t("Twitch подключён") + (tw.channel ? ": " + tw.channel : "") };
        }
        // Код Twitch обычно показан крупной полосой наверху панели — но панель
        // сейчас закрыта мастером, и человек его просто не увидит. Значит
        // показываем здесь же.
        if (tw.pending_code) {
          return {
            ok: false,
            html: `<span>Открой <a href="${esc(tw.pending_url || "https://www.twitch.tv/activate")}"
                     target="_blank" rel="noopener">${esc(tw.pending_url || "twitch.tv/activate")}</a>
                   и введи код <b class="code-big">${esc(tw.pending_code)}</b></span>`,
          };
        }
        return { ok: false, text: t("Вход пока не выполнен.") };
      },
    },

    {
      id: "reward",
      name: "Награда за баллы",
      // Канал без баллов награду не получит никогда — требовать её нечестно.
      must: () => !noPoints(),
      short: "как зритель заказывает",
      title: "Награда за баллы канала",
      why: "Приложение само создаст на твоём канале награду: зритель тратит баллы, пишет название трека — заказ прилетает сюда. Руками награду создавать нельзя: за чужую награду Twitch не даёт вернуть баллы, если трек не подошёл.",
      art: () => `<svg viewBox="0 0 640 150" role="img" aria-label="Схема: награда на канале">
        <rect x="1" y="1" width="638" height="148" rx="12" fill="${C.bg}" stroke="${C.line}"/>
        <rect x="30" y="34" width="250" height="82" rx="10" fill="${C.raise}" stroke="${C.line}"/>
        <rect x="46" y="52" width="46" height="46" rx="8" fill="${C.lime}" opacity=".18"/>
        <text x="69" y="81" text-anchor="middle" font-family="Manrope" font-size="14" fill="${C.lime}">♪</text>
        <text x="108" y="70" font-family="Manrope" font-size="13" fill="${C.ink}">Заказ трека</text>
        <text x="108" y="90" font-family="Manrope" font-size="11" fill="${C.dim}">1000 баллов</text>
        <path d="M292 75 h60" stroke="${C.line}" stroke-width="1.5"/>
        <path d="M348 70 l8 5 -8 5 z" fill="${C.dim}"/>
        <rect x="360" y="34" width="250" height="82" rx="10" fill="${C.raise}" stroke="${C.line}"/>
        <text x="380" y="64" font-family="Manrope" font-size="11" fill="${C.dim}">зритель пишет</text>
        <text x="380" y="86" font-family="Manrope" font-size="13" fill="${C.ink}">queen bohemian rhapsody</text>
        <text x="380" y="104" font-family="Manrope" font-size="10" fill="${C.dim}">не нашлось — баллы вернутся сами</text>
      </svg>`,
      body: () => `
        <label class="setup-field">
          <span>Название награды</span>
          <input type="text" id="su-reward-title" spellcheck="false" placeholder="Заказ трека">
        </label>
        <label class="setup-field">
          <span>Сколько баллов стоит заказ</span>
          <input type="number" id="su-reward-cost" min="1" max="1000000" placeholder="1000">
        </label>
        <div class="setup-acts">
          <button class="s-btn key" data-act="save-reward">Сохранить</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Баллы канала есть только у аффилиатов и партнёров Twitch.
          Если у тебя обычный канал, награду создать невозможно — это ограничение Twitch.
          Заказы за донат при этом работают.`)}</p>`,
      fill: (cfg) => {
        $("su-reward-title").value = (cfg && cfg.reward_title) || t("Заказ трека");
        $("su-reward-cost").value = (cfg && cfg.reward_cost) || 1000;
      },
      check: (s) => {
        if (s && s.twitch && s.twitch.reward_ready) return { ok: true, text: t("Награда на канале создана.") };
        if (noPoints()) return { ok: true, text: t("На канале нет баллов, шаг пропущен. Заказы за донат работают.") };
        return { ok: false, text: s && s.twitch && s.twitch.connected
          ? t("Награды пока нет. Нажми «Сохранить» — приложение создаст её само.")
          : t("Награды пока нет. Она появится сразу после подключения Twitch.") };
      },
    },

    {
      id: "playlist",
      name: "Запасной плейлист",
      must: false,
      short: "что играет в тишине",
      title: "Запасной плейлист",
      why: "Когда очередь заказов кончилась, приложение возвращает твою музыку туда, где ты её остановил. Если вернуть не вышло — включит вот этот плейлист, чтобы в эфире не повисла тишина.",
      art: null,
      body: () => `
        <label class="setup-field">
          <span>Плейлист</span>
          <select id="su-playlist"><option value="">— загружаю… —</option></select>
        </label>
        <div class="setup-acts">
          <button class="s-btn key" data-act="save-playlist">Сохранить</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Список берётся из твоего Spotify — значит шаг работает только после
          входа. Пропустил вход — пропусти и этот шаг, потом настроишь в панели.`)}</p>`,
      fill: (cfg) => loadPlaylists(cfg),
      check: (s, cfg) => cfg && cfg.fallback_playlist_id
        ? { ok: true, text: (cfg.fallback_playlist_name ? t("Выбран: {name}", { name: cfg.fallback_playlist_name }) : t("Выбран плейлист")) }
        : { ok: false, text: t("Плейлист не выбран — можно и потом.") },
    },

    {
      id: "donations",
      name: "Донаты",
      must: false,
      short: "заказы за деньги",
      title: "Заказы за донат",
      why: "Кроме баллов канала, заказ можно принимать за донат. Работает с DonationAlerts, DonatePay и DonateX — настраивай те, которыми пользуешься, остальные оставь пустыми.",
      art: null,
      body: () => `
        <label class="setup-field">
          <span>Client ID DonationAlerts</span>
          <input type="text" id="su-da" spellcheck="false" autocomplete="off" placeholder="если пользуешься">
        </label>
        <label class="setup-field">
          <span>Ключ DonatePay</span>
          <input type="text" id="su-dp" spellcheck="false" autocomplete="off" placeholder="если пользуешься">
        </label>
        <label class="setup-field">
          <span>Ключ DonateX</span>
          <input type="text" id="su-dx" spellcheck="false" autocomplete="off" placeholder="если пользуешься">
        </label>
        <label class="setup-field">
          <span>Донат от какой суммы считается заказом (в валюте твоего счёта в сервисе)</span>
          <input type="number" id="su-donation-min" min="1" placeholder="100">
        </label>
        <p class="why">${t("Адрес возврата для приложения DonationAlerts (поле Redirect URL):")}</p>
        <div class="copyline">
          <code>${esc(donationsURI)}</code>
          <button class="s-btn" data-act="copy" data-text="${esc(donationsURI)}">${t("Скопировать")}</button>
        </div>
        <div class="setup-acts">
          <button class="s-btn key" data-act="save-donations">Сохранить</button>
          <button class="s-btn" data-act="da-login">Подключить DonationAlerts</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Ключи лежат в личных кабинетах самих сервисов, в разделе
          для разработчиков. Это единственный шаг, который спокойно делается потом,
          посреди недели.`)}</p>`,
      fill: (cfg) => {
        $("su-da").value = (cfg && cfg.donationalerts_client_id) || "";
        $("su-dp").value = (cfg && cfg.donatepay_key) || "";
        $("su-dx").value = (cfg && cfg.donatex_key) || "";
        $("su-donation-min").value = (cfg && cfg.donation_min) || 100;
      },
      check: (s, cfg) => {
        const any = cfg && (cfg.donationalerts_client_id || cfg.donatepay_key || cfg.donatex_key);
        return any
          ? { ok: true, text: t("Донаты настроены.") }
          : { ok: false, text: t("Ничего не настроено — заказы будут только за баллы канала.") };
      },
    },

    {
      id: "widget",
      name: "Виджет в OBS",
      // Проверить отсюда, что адрес правда вставлен в OBS, нельзя — поэтому
      // шаг не требуем, а засчитываем по нажатию «Скопировать».
      must: false,
      short: "плашка в кадре",
      title: "Виджет: что играет — на экране",
      why: "Маленькая плашка в углу кадра: обложка, название, артист и кто заказал. Это не отдельная программа, а страница, которую отдаёт само приложение.",
      art: () => `<svg viewBox="0 0 640 210" role="img" aria-label="Схема: источник «Браузер» в OBS">
        <rect x="1" y="1" width="638" height="208" rx="12" fill="${C.bg}" stroke="${C.line}"/>
        <text x="24" y="30" font-family="Manrope" font-size="11" fill="${C.dim}" letter-spacing="1">OBS · ИСТОЧНИКИ</text>
        <rect x="24" y="42" width="230" height="146" rx="10" fill="${C.raise}" stroke="${C.line}"/>
        <text x="40" y="68" font-family="Manrope" font-size="12" fill="${C.dim}">Игра</text>
        <text x="40" y="92" font-family="Manrope" font-size="12" fill="${C.dim}">Камера</text>
        <text x="40" y="116" font-family="Manrope" font-size="12" fill="${C.lime}">Заказ музыки  ← Браузер</text>
        <circle cx="230" cy="170" r="12" fill="none" stroke="${C.lime}"/>
        <text x="230" y="175" text-anchor="middle" font-family="Manrope" font-size="14" fill="${C.lime}">+</text>

        <rect x="280" y="42" width="336" height="146" rx="10" fill="${C.raise}" stroke="${C.line}"/>
        ${fieldArt(298, 60, 300, "URL", widgetURL, true)}
        ${fieldArt(298, 118, 140, "ШИРИНА", "1920", false)}
        ${fieldArt(458, 118, 140, "ВЫСОТА", "1080", false)}
      </svg>`,
      body: () => `
        <ol class="steps-list">
          <li>${tt(`Скопируй адрес виджета`)}</li>
          <li>${tt(`В OBS в списке <b>Источники</b> нажми <b>+</b> и выбери <b>Браузер</b>`)}</li>
          <li>${tt(`В поле <b>URL</b> вставь адрес, сотри то, что было по умолчанию`)}</li>
          <li>${tt(`Поставь ширину <b>1920</b>, высоту <b>1080</b>, нажми ОК и растяни источник на весь кадр`)}</li>
        </ol>
        <div class="copyline">
          <code>${esc(widgetURL)}</code>
          <button class="s-btn key" data-act="copy-widget" data-text="${esc(widgetURL)}">Скопировать адрес</button>
        </div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Адрес один и навсегда: оформление живёт в настройках приложения, а не в
          адресе. Поменял вид — в OBS он поменяется сам, переклеивать ничего не надо.
          <br>Плашка появится, когда заиграет музыка. Чтобы выставить её заранее, допиши
          в конец адреса <b>?demo=1</b> — виджет покажет выдуманный трек. Потом убери.`)}</p>`,
      check: () => copiedWidget
        ? { ok: true, text: t("Адрес скопирован — вставь его в OBS.") }
        : { ok: false, text: t("Скопируй адрес и добавь источник в OBS.") },
    },

    {
      id: "look",
      name: "Оформление виджета",
      must: false,
      short: "как плашка выглядит",
      title: "Оформление виджета",
      why: "Семнадцать стилей, у каждого несколько окрасок. Стиль задаёт устройство плашки, окраска — только цвета. Меняется на лету: в OBS видно сразу, ничего перезапускать не надо.",
      art: null,
      body: () => `
        <div class="preset-stage" id="su-stage">
          <div class="card show" id="su-card" style="left:24px; bottom:24px">
            <div class="aura"><i></i></div>
            <div class="fx"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></div>
            <div class="art" id="su-art"></div>
            <div class="text">
              <div class="title">Bohemian Rhapsody</div>
              <div class="artist">Queen</div>
              <div class="by">заказал zritel</div>
              <div class="bar"><i style="width:46%"></i></div>
              <div class="meta"><span class="eq"><i></i><i></i><i></i><i></i><i></i></span></div>
            </div>
          </div>
        </div>
        <div class="preset-pick" id="su-families"></div>
        <div class="preset-pick" id="su-variants"></div>
        <div class="check" id="su-check"></div>
        <p class="why">${tt(`Здесь только семейства и краски. Тонкая настройка — размер, угол кадра,
          свои цвета — живёт в панели, во вкладке <b>виджет</b>.`)}</p>`,
      fill: (cfg) => setupPresets(cfg),
      check: (s, cfg) => ({
        ok: true,
        text: t("Выбрано: {name}", { name: presetName((cfg && cfg.widget && cfg.widget.preset) || "efir/lime") }),
      }),
    },
  ];

  // steps — видимые шаги: часть прячется по языку (обход у англоязычных).
  const steps = () => STEPS.filter((st) => !(st.hidden && st.hidden()));
  const mustOf = (st) => (typeof st.must === "function" ? st.must() : st.must);
  const noPoints = () => {
    const tw = (state && state.twitch) || {};
    return !!(tw.connected && !tw.has_points);
  };

  // ── состояние мастера ──────────────────────────────────────────────

  let state = null;      // снимок от панели
  let config = null;     // настройки
  let at = 0;            // на каком шаге стоим
  let skipped = {};      // какие шаги пропущены
  let done = {};         // какие сданы (по последней проверке)
  let finished = false;  // дошли до экрана «готово»
  let copiedWidget = false;
  let open = false;

  // Панель отдаёт сюда каждый снимок состояния. Пока мастер закрыт, просто
  // запоминаем: он может открыться в любую секунду.
  window.srSetupState = (s) => {
    state = s;
    if (open) refresh();
  };

  async function loadConfig() {
    try {
      config = await (await fetch("/api/config")).json();
    } catch { config = config || {}; }
    return config;
  }

  async function saveConfig(patch) {
    try {
      const current = await (await fetch("/api/config")).json();
      Object.assign(current, typeof patch === "function" ? patch(current) : patch);
      const r = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current),
      });
      if (!r.ok) throw new Error();
      config = await r.json();
      return true;
    } catch {
      return false;
    }
  }

  // post зовёт ручку приложения и разбирает ответ так же, как панель: у
  // отказа есть код (SP-19, OB-02) и человеческий текст.
  async function post(path, body) {
    const r = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    });
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : {}; } catch { data = { error: text }; }
    if (!r.ok) throw new Error((data && (data.error || data.text)) || t("не получилось"));
    return data;
  }

  // ── рисование ──────────────────────────────────────────────────────

  function railHTML() {
    return steps().map((st, i) => {
      const cls = [
        "rail-step",
        i === at && !finished ? "at" : "",
        done[st.id] ? "ok" : "",
        skipped[st.id] && !done[st.id] ? "skip" : "",
      ].join(" ");
      const mark = done[st.id] ? "✓" : skipped[st.id] ? "–" : String(i + 1);
      return `<button class="${cls}" data-go="${i}">
          <span class="mark">${mark}</span>
          <span class="who"><b>${esc(t(st.name))}</b><i>${esc(t(st.short))}</i></span>
        </button>`;
    }).join("");
  }

  function stepHTML(st) {
    return `
      ${st.noTag ? "" : `<span class="tag ${mustOf(st) ? "must" : ""}">${mustOf(st) ? t("важный шаг") : t("можно потом")}</span>`}
      <h1>${esc(t(st.title))}</h1>
      <p class="why">${t(st.why)}</p>
      ${st.art ? `<div class="scheme">${st.art()}</div>` : ""}
      ${st.body()}
      <div class="setup-foot">
        ${at > 0 ? `<button class="ghost" data-act="back">${t("← назад")}</button>` : ""}
        <span class="spacer"></span>
        <button class="ghost" data-act="skip">${mustOf(st) ? t("пропустить (вернусь потом)") : t("пропустить")}</button>
        <button class="s-btn key" data-act="next" id="su-next">${t("Дальше")}</button>
      </div>`;
  }

  function doneHTML() {
    // Необязательные шаги в «незакрытое» не попадают: это не долг.
    const left = steps().filter((s) => !done[s.id] && mustOf(s));
    return `<div class="setup-done">
      <div class="tick">
        <svg viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor"
             stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>
      </div>
      <h1>${t("Готово")}</h1>
      <p>${left.length === 0
        ? t("Настроено всё. Можно включать стрим.")
        : t("Осталось незакрытым: {list}. Это можно доделать в настройках или пройти мастер заново.",
          { list: esc(left.map((s) => t(s.name)).join(", ")) })}</p>
      <div class="copyline">
        <code>${esc(widgetURL)}</code>
        <button class="s-btn" data-act="copy" data-text="${esc(widgetURL)}">${t("Скопировать")}</button>
      </div>
      <div class="setup-acts" style="justify-content:center">
        <button class="s-btn key" data-act="finish">${t("Открыть панель")}</button>
        ${left.length ? `<button class="s-btn" data-act="go-left">${t("Вернуться к пропущенному")}</button>` : ""}
      </div>
    </div>`;
  }

  function paint() {
    const box = $("setup");
    $("setup-rail-steps").innerHTML = railHTML();
    const card = $("setup-card");
    if (at >= steps().length) at = steps().length - 1;
    card.innerHTML = finished ? doneHTML() : stepHTML(steps()[at]);
    card.classList.remove("out");
    // Перезапуск появления: без сброса анимация второй раз не играет.
    void card.offsetWidth;

    const st = steps()[at];
    if (!finished && st.fill) {
      try { st.fill(config); } catch { /* поле могло не успеть появиться */ }
    }
    $("setup-progress-line").style.width =
      Math.round(((finished ? steps().length : at) / steps().length) * 100) + "%";
    refresh();
    box.querySelector(".setup-body").scrollTop = 0;
  }

  // refresh пересчитывает живую проверку текущего шага. Зовётся на каждый
  // снимок состояния: человек нажал кнопку в другом окне, вошёл в Spotify —
  // и «Дальше» должно загореться само, без единого нажатия здесь.
  function refresh() {
    if (finished) return;
    checkAll();
    const st = steps()[at];
    const res = st.check(state, config) || { ok: false, text: "" };
    done[st.id] = res.ok;

    const line = $("su-check");
    if (line) {
      // Шаг сдан — старая ошибка больше не правда, стираем её.
      if (res.ok) line.dataset.bad = "";
      line.className = "check " + (res.ok ? "ok" : line.dataset.bad ? "bad" : "");
      if (line.dataset.bad) {
        line.textContent = line.dataset.bad;
      } else if (res.html) {
        // Разметка нужна ровно одному месту — коду для входа в Twitch, где
        // рядом с кодом стоит ссылка. Всё остальное — простой текст.
        line.innerHTML = res.html;
      } else {
        line.textContent = res.text || "";
      }
    }
    const next = $("su-next");
    if (next) {
      next.disabled = false;
      next.textContent = res.ok ? t("Дальше") : mustOf(st) ? t("Дальше (шаг не сдан)") : t("Дальше");
      next.classList.toggle("key", res.ok);
    }
    $("setup-rail-steps").innerHTML = railHTML();
  }

  // checkAll прогоняет проверки всех шагов: иначе дорожка слева показывает
  // галочки только у тех шагов, где человек уже побывал в этот раз.
  function checkAll() {
    for (const st of STEPS) {
      try { done[st.id] = !!(st.check(state, config) || {}).ok; } catch { done[st.id] = false; }
    }
  }

  // say пишет в строку проверки то, чего в снимке состояния нет: ошибку от
  // ручки приложения или «жду ответа».
  function say(text, bad) {
    const line = $("su-check");
    if (!line) return;
    line.dataset.bad = bad ? text : "";
    line.className = "check " + (bad ? "bad" : "ok");
    line.textContent = text;
  }

  function goto(i) {
    const card = $("setup-card");
    card.classList.add("out");
    setTimeout(() => {
      at = Math.max(0, Math.min(steps().length - 1, i));
      finished = false;
      paint();
    }, 170);
  }

  function finish(save) {
    const card = $("setup-card");
    card.classList.add("out");
    setTimeout(() => { finished = true; paint(); }, 170);
    if (save) post("/api/setup/done", { done: true }).catch(() => {});
  }

  function close() {
    const box = $("setup");
    box.classList.remove("on");
    setTimeout(() => { box.hidden = true; open = false; }, 350);
  }

  // ── кнопки внутри шагов ────────────────────────────────────────────

  async function busy(btn, run) {
    btn.classList.add("busy");
    btn.disabled = true;
    try {
      await run();
    } catch (e) {
      say(String((e && e.message) || e), true);
    } finally {
      btn.classList.remove("busy");
      btn.disabled = false;
    }
  }

  const acts = {
    back: () => goto(at - 1),
    skip: () => {
      skipped[steps()[at].id] = true;
      if (at === steps().length - 1) finish(true);
      else goto(at + 1);
    },
    next: () => {
      if (at === steps().length - 1) finish(true);
      else goto(at + 1);
    },
    finish: () => { post("/api/setup/done", { done: true }).catch(() => {}); close(); },
    // «Закрыть и настроить потом» — мастер не пройден, отмечать его
    // пройденным нельзя: при следующем запуске он должен открыться снова.
    close: () => close(),
    "go-left": () => {
      const i = steps().findIndex((s) => !done[s.id] && mustOf(s));
      goto(i < 0 ? 0 : i);
    },

    copy: (btn) => copy(btn.dataset.text),
    "copy-widget": (btn) => {
      copy(btn.dataset.text);
      copiedWidget = true;
      refresh();
    },

    "tunnel-on": (btn) => busy(btn, async () => {
      const key = $("su-tunnel").value.trim();
      say(t("Включаю обход… в первый раз это до пары минут."));
      const d = await post("/api/tunnel/on", key ? { key } : {});
      $("su-tunnel").value = "";
      say(d.standby
        ? t("Ключ сохранён. Сейчас Spotify отвечает и без обхода — подниму его, когда понадобится.")
        : t("Обход работает: {server}", { server: d.server || "" }));
      refresh();
    }),

    "save-spotify-id": (btn) => busy(btn, async () => {
      const id = $("su-spotify-id").value.trim();
      // Client ID у Spotify — ровно 32 знака из цифр и букв a–f. Secret
      // выглядит так же, поэтому напоминаем про него в любом случае.
      if (!/^[0-9a-f]{32}$/i.test(id)) {
        say(t("Не похоже на Client ID: он из 32 знаков — цифры и латинские буквы a–f. Скопируй его заново из Settings."), true);
        return;
      }
      const ok = await saveConfig({ spotify_client_id: id });
      say(ok ? t("Ключ сохранён. Если вход потом ругается «Invalid client», похоже, вставлен Client secret — нужен Client ID.") : t("Не смог сохранить."), !ok);
      refresh();
    }),

    "spotify-login": (btn) => busy(btn, async () => {
      const d = await post("/api/spotify/login", {});
      // Приложение уже открыло страницу входа в своём окне (так бывает, когда
      // работает обход) — второй раз открывать её в браузере нельзя: из
      // России она там просто не загрузится, и человек решит, что сломалось.
      if (!d.in_app) window.open(d.url, "_blank", "noopener");
      say(t("Открыл страницу входа. Нажми там Agree и вернись сюда."));
    }),

    "save-twitch-id": (btn) => busy(btn, async () => {
      const id = $("su-twitch-id").value.trim();
      // У Twitch Client ID — около тридцати латинских букв и цифр без пробелов.
      if (!/^[a-z0-9]{20,40}$/i.test(id)) {
        say(t("Не похоже на Client ID Twitch: это около 30 латинских букв и цифр. Скопируй его заново в Manage."), true);
        return;
      }
      const ok = await saveConfig({ twitch_client_id: id });
      say(ok ? t("Ключ сохранён.") : t("Не смог сохранить."), !ok);
      refresh();
    }),

    "twitch-login": (btn) => busy(btn, async () => {
      await post("/api/twitch/login", {});
      // Ничего не говорим сами: код придёт со следующим снимком состояния, и
      // проверка шага покажет его вместе со ссылкой.
      say("");
      refresh();
    }),

    "save-reward": (btn) => busy(btn, async () => {
      const cost = parseInt($("su-reward-cost").value, 10);
      const ok = await saveConfig({
        reward_title: $("su-reward-title").value.trim() || t("Заказ трека"),
        reward_cost: Number.isFinite(cost) && cost > 0 ? cost : 1000,
      });
      const tw = (state && state.twitch) || {};
      say(!ok ? t("Не смог сохранить.")
        : tw.connected ? t("Сохранено. Награда появится на канале через пару секунд.")
          : t("Сохранено. Награду приложение создаст, когда подключишь Twitch."), !ok);
      refresh();
    }),

    "save-playlist": (btn) => busy(btn, async () => {
      const sel = $("su-playlist");
      const ok = await saveConfig({
        fallback_playlist_id: sel.value,
        fallback_playlist_name: sel.value ? sel.options[sel.selectedIndex].text : "",
      });
      say(ok ? t("Плейлист сохранён.") : t("Не смог сохранить."), !ok);
      refresh();
    }),

    "save-donations": (btn) => busy(btn, async () => {
      const min = parseInt($("su-donation-min").value, 10);
      const ok = await saveConfig({
        donationalerts_client_id: $("su-da").value.trim(),
        donatepay_key: $("su-dp").value.trim(),
        donatex_key: $("su-dx").value.trim(),
        donation_min: Number.isFinite(min) && min > 0 ? min : 100,
      });
      say(ok ? t("Сохранено.") : t("Не смог сохранить."), !ok);
      refresh();
    }),

    // Язык: сохраняем в настройки и сразу перерисовываем мастер. Названия
    // наград, оставленные по умолчанию, переводим вместе с языком.
    lang: (btn) => busy(btn, async () => {
      const l = btn.dataset.lang === "en" ? "en" : "ru";
      const names = [["reward_title", "Заказ трека", "Song Request"],
        ["playlist_reward_title", "Заказ плейлиста", "Playlist Request"]];
      await saveConfig((cur) => {
        const patch = { language: l };
        for (const [k, ru, enName] of names) {
          if (l === "en" && cur[k] === ru) patch[k] = enName;
          if (l === "ru" && cur[k] === enName) patch[k] = ru;
        }
        return patch;
      });
      if (window.I18N && !window.I18N.setLang(l) && open) paint();
    }),

    "da-login": (btn) => busy(btn, async () => {
      // Сначала сохраняем поля: иначе вставленный Client ID не дойдёт до
      // приложения, и вход откажет «Client ID не вставлен».
      const min = parseInt($("su-donation-min").value, 10);
      const saved = await saveConfig({
        donationalerts_client_id: $("su-da").value.trim(),
        donatepay_key: $("su-dp").value.trim(),
        donatex_key: $("su-dx").value.trim(),
        donation_min: Number.isFinite(min) && min > 0 ? min : 100,
      });
      if (!saved) throw new Error(t("Не смог сохранить."));
      const d = await post("/api/donations/login", {});
      if (d && d.url && !d.in_app) window.open(d.url, "_blank", "noopener");
      say(t("Открыл страницу DonationAlerts."));
    }),
  };

  function copy(text) {
    // Буфер обмена в старых движках и без защищённого соединения бывает
    // недоступен, поэтому запасной путь через невидимое поле — тот же приём,
    // что в панели.
    const fallback = () => {
      const box = document.createElement("textarea");
      box.value = text;
      document.body.appendChild(box);
      box.select();
      try { document.execCommand("copy"); } catch { /* ничего не поделать */ }
      box.remove();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).catch(fallback);
    } else {
      fallback();
    }
    say(t("Скопировано: {text}", { text }));
  }

  // ── плейлисты и оформление ─────────────────────────────────────────

  async function loadPlaylists(cfg) {
    const sel = $("su-playlist");
    if (!sel) return;
    try {
      const r = await fetch("/api/spotify/playlists");
      if (!r.ok) throw new Error();
      const list = await r.json();
      const items = Array.isArray(list) ? list : (list.items || []);
      sel.innerHTML = `<option value="">${t("— не выбран —")}</option>` +
        items.map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("");
      sel.value = (cfg && cfg.fallback_playlist_id) || "";
    } catch {
      sel.innerHTML = `<option value="">${t("— список не загрузился —")}</option>`;
    }
  }

  function presetName(preset) {
    const p = window.widgetPreset ? window.widgetPreset(preset) : null;
    // Неизвестный ключ (оформление из старой версии) не показываем как есть.
    return p ? `${t(p.family.name)} · ${t(p.variant.name)}` : t("своё оформление");
  }

  function setupPresets(cfg) {
    const card = $("su-card");
    if (!card || !window.WIDGET_FAMILIES) return;

    let preset = (cfg && cfg.widget && cfg.widget.preset) || "efir/lime";

    // Вместо обложки — значок ноты: настоящей картинки в образце нет, а пустой
    // серый квадрат читается как «оформление сломалось».
    const art = $("su-art");
    if (art) {
      art.innerHTML = `<svg class="no" viewBox="0 0 24 24" fill="none" stroke="currentColor"
        stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18V5l12-2v13"/>
        <circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`;
    }

    const dress = () => {
      const { family, variant } = window.widgetPreset(preset);
      card.dataset.family = family.id;
      card.dataset.variant = variant.id;
      card.dataset.art = "on";
      card.dataset.bar = "on";
      card.dataset.motion = "off";
      card.style.zoom = .9;

      $("su-families").innerHTML = window.WIDGET_FAMILIES.map((f) =>
        `<button data-family="${f.id}" class="${f.id === family.id ? "on" : ""}">${esc(f.name)}</button>`).join("");
      $("su-variants").innerHTML = family.variants.map((v) =>
        `<button data-variant="${v.id}" class="${v.id === variant.id ? "on" : ""}">${esc(v.name)}</button>`).join("");
    };

    const pick = async (next) => {
      preset = next;
      dress();
      const widget = Object.assign({}, (config && config.widget) || {}, { preset });
      await saveConfig({ widget });
      refresh();
    };

    $("su-families").onclick = (e) => {
      const b = e.target.closest("button[data-family]");
      if (!b) return;
      const fam = window.WIDGET_FAMILIES.find((f) => f.id === b.dataset.family);
      pick(`${fam.id}/${fam.variants[0].id}`);
    };
    $("su-variants").onclick = (e) => {
      const b = e.target.closest("button[data-variant]");
      if (!b) return;
      pick(`${preset.split("/")[0]}/${b.dataset.variant}`);
    };

    dress();
  }

  // ── запуск ─────────────────────────────────────────────────────────

  function openWizard(step) {
    const box = $("setup");
    box.hidden = false;
    open = true;
    at = step || 0;
    finished = false;
    // Появление отдельным кадром: без него переход не играет, потому что
    // элемент только что был hidden.
    requestAnimationFrame(() => box.classList.add("on"));
    paint();
  }

  // Мастер можно позвать из панели («пройти настройку заново»).
  window.srSetupOpen = async () => {
    await loadConfig();
    await post("/api/setup/done", { done: false }).catch(() => {});
    skipped = {};
    copiedWidget = false;
    openWizard(0);
  };

  // Enter — «Дальше», Esc — закрыть. В выпадающем списке и на кнопке Enter
  // остаётся своим: там он уже что-то значит.
  document.addEventListener("keydown", (e) => {
    if (!open || e.isComposing) return;
    if (e.key === "Escape") { e.preventDefault(); close(); return; }
    if (e.key !== "Enter") return;
    const tag = (e.target && e.target.tagName) || "";
    if (tag === "BUTTON" || tag === "SELECT" || tag === "TEXTAREA" || tag === "A") return;
    e.preventDefault();
    if (finished) acts.finish();
    else acts.next();
  });

  // Язык сменился (здесь или в панели) — перерисовываем открытый мастер.
  if (window.I18N) window.I18N.onChange(() => { if (open) paint(); });

  $("setup").addEventListener("click", (e) => {
    const go = e.target.closest("[data-go]");
    if (go) return goto(parseInt(go.dataset.go, 10));
    const btn = e.target.closest("[data-act]");
    if (!btn) return;
    const run = acts[btn.dataset.act];
    if (run) run(btn);
  });

  async function boot() {
    setupTitlebar();

    let info = { intro: false, done: true };
    try { info = await (await fetch("/api/setup")).json(); } catch { /* мастер не покажем */ }
    if (info.spotify_redirect) redirectURI = info.spotify_redirect;
    if (info.donations_redirect) donationsURI = info.donations_redirect;

    await loadConfig();
    // Первый снимок состояния берём сами: панель пришлёт свой по сокету, но
    // это может занять секунду, а проверки шага должны работать сразу.
    if (!state) {
      try { state = await (await fetch("/api/state")).json(); } catch { /* переживём */ }
    }

    if (info.intro) await playIntro();
    if (!info.done) openWizard(0);
  }

  boot();
})();
