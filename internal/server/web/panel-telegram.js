(function () {
  "use strict";
  I18N.add({
    "Telegram-компаньон": "Telegram companion",
    "Итоги эфира, номинации, карточка и пульт музыки прямо в приложении. Отдельная программа и Python не нужны.": "Stream recap, awards, a shareable card and music controls built into the app. No separate program or Python required.",
    "Создай своего бота через": "Create your own bot using",
    "и вставь токен ниже.": "and paste its token below.",
    "Включи компаньона и сохрани настройки. Открой своего бота и отправь /id: он покажет твой Telegram ID.": "Enable the companion and save. Open your bot and send /id to find your Telegram ID.",
    "Впиши свой ID ниже и сохрани ещё раз. Затем нажми «Старт» у бота. Пока ID не указан, управление недоступно.": "Enter your ID below and save again, then press Start in your bot. Controls stay locked until an owner ID is set.",
    "Токен бота": "Bot token", "Токен от BotFather": "Token from BotFather", "Токен сохранён": "Token saved",
    "Хранится в хранилище паролей Windows. Пустое поле сохраняет прежний токен.": "Stored in Windows Credential Manager. Leave blank to keep the saved token.",
    "Твой Telegram ID": "Your Telegram ID", "Включить Telegram-компаньона": "Enable Telegram companion",
    "Присылать итоги после окончания эфира": "Send a recap when the stream ends",
    "Уведомлять о начале заказанного трека": "Notify when a requested track starts",
    "Канал для карточки итогов (необязательно)": "Channel for recap cards (optional)", "@канал или -100…": "@channel or -100…",
    "Бот должен быть администратором канала с правом публикации. Карточка публикуется вместе с автоматическими итогами.": "The bot needs posting permission as a channel admin. Cards are published with automatic recaps.",
    "Сохранить Telegram": "Save Telegram", "Отключить и удалить токен": "Disable and delete token", "Открыть своего бота": "Open your bot",
    "Доступ к управлению есть только у указанного владельца в личном чате. Приложение должно работать на компьютере. Не запускай того же бота одновременно в другой программе.": "Only the specified owner can use controls in a private chat. Keep the desktop app running. Do not run the same bot in another program at the same time.",
    "Поддержка": "Support", "Telegram выключен": "Telegram disabled", "Telegram подключён": "Telegram connected", "Подключаюсь к Telegram…": "Connecting to Telegram…",
    "Проверь свой Telegram ID: нужны только цифры.": "Check your Telegram ID: digits only.",
    "Отключить Telegram и удалить сохранённый токен?": "Disable Telegram and delete the saved token?", "Настройки Telegram сохранены": "Telegram settings saved",
    "Не разобрал настройки Telegram": "Could not read Telegram settings", "Проверь ID владельца и канал": "Check the owner ID and channel",
    "Канал: @имя или числовой ID вида -100…": "Channel: @name or a numeric ID starting with -100…",
    "Проверь токен от BotFather": "Check your BotFather token", "Хранилище паролей недоступно": "Credential Manager is unavailable",
    "Не удалось удалить токен": "Could not delete the token", "Не удалось сохранить токен": "Could not save the token",
    "Укажи токен бота": "Enter your bot token", "Не удалось сохранить настройки": "Could not save settings",
    "Токен Telegram недействителен": "Invalid Telegram token",
    "Этот бот уже запущен в другой программе. Останови её или создай отдельного бота.": "This bot is running in another program. Stop it or create a separate bot.",
    "Telegram просит подождать; подключение повторится": "Telegram requested a delay; reconnection will follow",
    "Telegram недоступен; подключение повторится автоматически": "Telegram is unavailable; reconnection is automatic",
    "У бота включён webhook. Используй отдельного бота для приложения.": "This bot has a webhook. Use a separate bot for the app.",
    "Открой своего бота и нажми «Старт»": "Open your bot and press Start",
  });
  const $ = (id) => document.getElementById(id);
  const t = (text) => I18N.t(text);
  let dirty = false, busy = false;
  const box = $("telegram-settings");
  const status = $("tg-status");
  box.addEventListener("input", () => { dirty = true; });
  function note(text, bad) { status.textContent = text; status.className = "state " + (bad ? "bad" : ""); }
  async function load() {
    if (busy) return;
    try {
      const res = await fetch("/api/telegram");
      if (!res.ok) throw new Error(t("Не получилось связаться с приложением."));
      const d = await res.json(), s = d.settings;
      if (!dirty) {
        $("tg-enabled").checked = s.enabled;
        $("tg-owner").value = s.owner_id || "";
        $("tg-auto").checked = s.auto_recap;
        $("tg-notify").checked = s.notify_orders;
        $("tg-channel").value = s.publish_channel || "";
      }
      $("tg-token").placeholder = t(d.has_token ? "Токен сохранён" : "Токен от BotFather");
      const open = $("tg-open");
      open.hidden = !/^[a-zA-Z0-9_]+$/.test(d.username || "");
      if (!open.hidden) open.href = "https://t.me/" + d.username;
      note(d.note ? t(d.note) : t(!s.enabled ? "Telegram выключен" : d.connected ? "Telegram подключён" : "Подключаюсь к Telegram…"), !!d.note);
    } catch (e) { note(e.message, true); }
  }
  async function save(forget) {
    if (busy) return;
    if (forget && !confirm(t("Отключить Telegram и удалить сохранённый токен?"))) return;
    const owner = $("tg-owner").value.trim();
    if (owner && (!/^\d+$/.test(owner) || !Number.isSafeInteger(Number(owner)) || Number(owner) <= 0)) {
      note(t("Проверь свой Telegram ID: нужны только цифры."), true); return;
    }
    busy = true;
    $("tg-save").disabled = $("tg-forget").disabled = true;
    try {
      const response = await fetch("/api/telegram", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
        enabled: !forget && $("tg-enabled").checked, owner_id: Number(owner) || 0,
        auto_recap: $("tg-auto").checked, notify_orders: $("tg-notify").checked,
        publish_channel: $("tg-channel").value.trim(), token: forget ? "" : $("tg-token").value.trim(), forget,
      }) });
      if (!response.ok) throw new Error(t((await response.text()).trim()));
      $("tg-token").value = ""; dirty = false;
      note(t("Настройки Telegram сохранены"), false);
    } catch (e) { note(e.message, true); }
    finally { busy = false; $("tg-save").disabled = $("tg-forget").disabled = false; }
    await load();
  }
  $("tg-save").onclick = () => save(false);
  $("tg-forget").onclick = () => save(true);
  new MutationObserver(() => { if (!$("settings").hidden) load(); }).observe($("settings"), { attributes: true, attributeFilter: ["hidden"] });
  setInterval(() => { if (!document.hidden && !$("settings").hidden) load(); }, 10000);
  load();
  I18N.onChange(load);
})();
