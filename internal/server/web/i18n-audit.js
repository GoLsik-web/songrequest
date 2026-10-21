// Английские тексты, добавленные по итогам аудита 21.10: мастер, раздел
// обхода в настройках, причины «почему с YouTube». Отдельным файлом, чтобы не
// толкаться в общих словарях с параллельными правками.
//
// Слово для обхода в английском одно — bypass.
(function () {
  I18N.add({
    // мастер
    "Нужен Spotify Premium:": "Spotify Premium is required:",
    "без него Spotify не даёт программам управлять плеером.": "without it Spotify doesn't let apps control the player.",
    "Обход не включён. Из России страница входа Spotify может не открыться — включи обход (шаг «Обход блокировок») или свой VPN на время входа.": "The bypass is off. If Spotify is blocked where you are, the login page may not open — turn on your VPN while logging in.",
    "На канале нет баллов, шаг пропущен. Заказы за донат работают.": "The channel has no channel points, step skipped. Donation requests still work.",
    "Награды пока нет. Нажми «Сохранить» — приложение создаст её само.": "No reward yet. Press “Save” — the app will create it itself.",
    "Адрес возврата для приложения DonationAlerts (поле Redirect URL):": "Redirect address for your DonationAlerts app (the Redirect URL field):",
    "Не похоже на Client ID: он из 32 знаков — цифры и латинские буквы a–f. Скопируй его заново из Settings.": "That doesn't look like a Client ID: it is 32 characters — digits and letters a–f. Copy it again from Settings.",
    "Ключ сохранён. Если вход потом ругается «Invalid client», похоже, вставлен Client secret — нужен Client ID.": "Key saved. If login later says “Invalid client”, you probably pasted the Client secret — the Client ID is needed.",
    "Не похоже на Client ID Twitch: это около 30 латинских букв и цифр. Скопируй его заново в Manage.": "That doesn't look like a Twitch Client ID: it's about 30 letters and digits. Copy it again under Manage.",
    "Сохранено. Награда появится на канале через пару секунд.": "Saved. The reward will appear on the channel in a few seconds.",
    "Сохранено. Награду приложение создаст, когда подключишь Twitch.": "Saved. The app will create the reward once you connect Twitch.",

    // настройки: раздел обхода
    "Обход блокировок — только если Spotify заблокирован в твоей стране": "Bypass — only if Spotify is blocked in your country",
    "Включить / перепроверить": "Turn on / recheck",
    "Вставь ключ от своего VPN или ссылку на подписку — приложение само подберёт сервер, через который Spotify отвечает.": "Paste your VPN key or subscription link — the app picks a server through which Spotify responds.",
    "подробнее": "more",
    "Вход в Spotify.": "Spotify login.",
    "При включённом обходе вход открывается в окне приложения, VPN не нужен. Без обхода — включи VPN на время входа.": "With the bypass on, the login opens in the app window, no VPN needed. Without it, turn on a VPN while logging in.",
    "«Обход наготове»": "“Bypass on standby”",
    "значит: ключ сохранён, но сейчас Spotify отвечает и без него. Как только перестанет — приложение включит обход само.": "means the key is saved, but Spotify responds without it right now. As soon as it stops, the app turns the bypass on itself.",

    // почему трек играет с YouTube (приходит с сервера готовой фразой)
    "по ссылке зрителя": "viewer's link",
    "в Spotify такого нет": "not on Spotify",
    "Spotify не ответил на поиск": "Spotify search didn't answer",
    "по ссылке на Яндекс.Музыку": "Yandex Music link",
    "Spotify не сыграл этот трек": "Spotify couldn't play this track",
    "первый ролик не заиграл": "the first video didn't play",
  });
})();
