package i18n

// Тексты, добавленные по итогам аудита 21.10: обход блокировок, мастер,
// статусы Twitch, возврат музыки, хроника модераторов. Отдельным файлом,
// чтобы не толкаться в общих словарях с параллельными правками.
//
// В английском варианте нарочно нет советов про VPN и обход: англоязычному
// стримеру Spotify не заблокирован, а «включи VPN» его только запутает.
func init() {
	register(map[string]string{
		"Запускается заказ: %s": "Starting request: %s",
		"Этот плейлист уже одобряют — треки ставятся в очередь.": "This playlist is already being approved — its tracks are being queued.",
		// обход: итог перебора серверов
		"Ни один из %d серверов ключа не пустил к Spotify. " +
			"1) Проверь, жива ли подписка, и возьми свежий ключ в кабинете VPN. " +
			"2) Не помогло — выключи обход и включи свой VPN целиком. " +
			"3) Или поставь Psiphon и впиши его адрес в «Прокси для Spotify» (кнопка «Найти автоматически»).": "None of the %d servers in the key let us reach Spotify. " +
			"1) Check that your VPN subscription is active and get a fresh key from your VPN account. " +
			"2) Still no luck — turn the bypass off and run your VPN for the whole computer instead.",
		"Серверы ключа пускают в интернет, но Spotify отказывает им по стране (проверено %d). " +
			"1) Возьми в кабинете VPN ключ другой страны (Европа, США). " +
			"2) Не помогло — выключи обход и включи свой VPN целиком. " +
			"3) Или поставь Psiphon и впиши его адрес в «Прокси для Spotify» (кнопка «Найти автоматически»).": "The servers in the key work, but Spotify refuses them by country (%d checked). " +
			"1) Get a key for another country (Europe, US) from your VPN account. " +
			"2) Still no luck — turn the bypass off and run your VPN for the whole computer instead.",
		"Сервер обхода сбоит, беру другой.":                               "The bypass server is misbehaving, trying another one.",
		"Ключ скопирован не целиком — скопируй его заново одной строкой.": "The key was not copied in full — copy it again as one line.",
		"Программу обхода (xray.exe) не скачать. Можно положить программу руками: скачай %s (через VPN или с другого компьютера), " +
			"достань из архива xray.exe и положи в папку %s. Потом снова включи обход.": "Could not download the bypass program (xray.exe). You can add it by hand: download %s, " +
			"take xray.exe out of the archive and put it into %s. Then turn the bypass on again.",
		"Spotify не пускает из этой страны. Вставь ключ VPN в «Обход блокировок» и включи обход — или включи свой VPN целиком.":            "Spotify is not available from this country. Check the country of your Spotify account (Account → Edit profile).",
		"Spotify не пускает из этой страны, а обход выключен. Включи его в «Обход блокировок».":                                            "Spotify is not available from this country and the bypass is off. Turn it on under Bypass.",
		"Обход отвалился и пока не поднимается: %s Приложение продолжает пробовать само. Проверь интернет и не кончилась ли подписка VPN.": "The bypass dropped and is not coming back yet: %s The app keeps trying on its own. Check your internet and your VPN subscription.",

		// ошибки
		"%s отказал. Попробуй ещё раз через минуту, а если повторяется — перезапусти приложение.":  "%s refused. Try again in a minute; if it keeps happening, restart the app.",
		"Spotify не работает из этой страны. Вставь ключ VPN в «Обход блокировок» и включи обход.": "Spotify is not available in your account's country. Check the country in your Spotify account settings (Account → Edit profile).",

		// Spotify: подписка и снимок
		"без подписки":                       "no subscription",
		"подписка не определена":             "subscription unknown",
		"снимок не снят":                     "no snapshot taken",
		"в момент снимка ничего не играло":   "nothing was playing at the time",
		"устройство неизвестно":              "unknown device",
		"ничего не играло":                   "nothing was playing",
		"без источника — вернём только трек": "no source — only the track comes back",
		"плейлист — вернётся полностью":      "playlist — comes back in full",
		"альбом — вернётся полностью":        "album — comes back in full",
		"радио артиста — вернём только трек": "artist radio — only the track comes back",
		"любимые треки — вернём только трек": "liked songs — only the track comes back",
		"%s — вернём только трек":            "%s — only the track comes back",

		// возврат музыки
		"Вернул трек, но источник восстановить нельзя — после него музыка остановится.": "Brought the track back, but its source can't be restored — music will stop after it.",
		"Источник вернуть нельзя, поэтому дальше %s.":                                   "The source can't be restored, so next up: %s.",
		"Возвращать было нечего, поэтому %s.":                                           "There was nothing to bring back, so %s.",
		"играет запасной плейлист":                                                      "the fallback playlist is playing",
		"играет «%s»":      "“%s” is playing",
		"играет музыка %s": "music by %s is playing",

		// Twitch: статус и тип канала
		"команды в чате не работают — подключи Twitch заново": "chat commands don't work — reconnect Twitch",
		"заказы принимаются, но команды в чате не работают":   "taking requests, but chat commands don't work",
		"чат подключён, баллов на канале нет":                 "chat connected, no channel points",
		"заказы принимаются":                                  "taking requests",
		"неизвестно":                                          "unknown",
		"партнёр":                                             "partner",
		"аффилиат":                                            "affiliate",
		"обычный канал, баллов нет":                           "regular channel, no points",

		// награды
		"Заказ трека":     "Song Request",
		"Заказ плейлиста": "Playlist Request",
		"Напиши артиста и название, ссылку на Spotify, YouTube, " +
			"Яндекс.Музыку или VK. Если трек не найдётся — баллы вернутся.": "Type the artist and title, or paste a Spotify or YouTube link. If the song isn't found, you get your points back.",
		"Кинь ссылку на плейлист или альбом Spotify либо Яндекс.Музыки. " +
			"Сыграют первые %d треков, и только после того, как заказ одобрит " +
			"стример или модератор. Не одобрят — баллы вернутся.": "Paste a link to a Spotify playlist or album. The first %d tracks will play once the streamer or a moderator approves. If not approved, you get your points back.",
		"из плейлиста «%s»": "from playlist “%s”",

		// хроника модераторов: кто и что сделал
		"стример": "streamer",
		"остановил приём заказов":            "paused requests",
		"возобновил приём заказов":           "resumed requests",
		"вернул скипнутое":                   "restored the skipped track",
		"поднял заказ в начало":              "moved a request to the top",
		"поставил громкость":                 "set the volume",
		"снял с паузы":                       "resumed",
		"поставил на паузу":                  "paused",
		"снял с паузы музыку канала":         "resumed the channel music",
		"поставил на паузу музыку канала":    "paused the channel music",
		"закрыл заказы":                      "blocked requests from",
		"вернул заказы":                      "unblocked requests from",
		"исправил трек":                      "fixed a track",
		"не стал ждать конца трека":          "didn't wait for the track to end",
		"скипнул":                            "skipped",
		"удалил":                             "removed",
		"удалил и вернул баллы":              "removed and refunded",
		"очистил очередь":                    "cleared the queue",
		"одобрил плейлист":                   "approved a playlist",
		"одобрил плейлист, но играть нечего": "approved a playlist, but nothing to play",
		"отклонил плейлист":                  "rejected a playlist",
		"включил с YouTube":                  "played from YouTube",
		"не стал включать с YouTube":         "declined to play from YouTube",
		"поменял порядок очереди":            "reordered the queue",
	})
}
