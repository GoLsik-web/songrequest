package server

import (
	"net/http"
	"net/url"
	"strings"

	"songrequest/internal/i18n"
)

// Справочник команд для модераторов.
//
// Зачем он вообще. Команды есть с самого начала, но узнать о них было неоткуда:
// в чате их видит только тот, кто уже знает про !помощь, а инструкции в docs/
// написаны для стримера и про настройку. Модератор в них не заглядывает — ему
// нужен один листок с тем, что можно набрать в чате.
//
// Почему текст собирает приложение, а не лежит готовым файлом в docs/. Знак
// команды настраивается (`command_prefix`), и в готовом файле он рано или
// поздно разошёлся бы с настоящим: у стримера другой бот занял «!», он поменял
// знак у себя — а модераторы читают листок с «!скип» и пишут не то. Здесь
// подставляется тот знак, который стоит прямо сейчас.
//
// Формат — обычный текст, а не HTML: этот файл стример пересылает модераторам в
// личные сообщения, и открыться он должен где угодно, хоть на телефоне.

// modsText собирает справочник с нужным знаком команд.
func modsText(prefix string) string {
	if prefix == "" {
		prefix = "!"
	}
	p := prefix
	if i18n.IsEN() {
		return modsTextEN(p)
	}

	lines := []string{
		"КОМАНДЫ ДЛЯ МОДЕРАТОРОВ",
		"",
		"Пишутся в чат канала. Работают у стримера и у модераторов; обычным",
		"зрителям они недоступны — музыку заказывают только за баллы или донат.",
		"",
		p + "очередь            что играет сейчас и что дальше",
		p + "трек               что играет прямо сейчас, одной строкой",
		"",
		p + "скип               убрать то, что играет",
		p + "скип 2             убрать второй заказ из очереди",
		p + "скип bad guy       убрать заказ по названию или артисту",
		p + "вернуть            поставить обратно то, что скипнули зря",
		"",
		p + "удалить 2          то же, что «скип 2»",
		p + "вверх 3            поднять третий заказ в начало очереди",
		p + "очистить           убрать всю очередь",
		"",
		p + "плейлисты          какие плейлисты ждут решения",
		p + "одобрить           пустить плейлист в очередь",
		p + "одобрить 2         если ждёт несколько — по номеру",
		p + "отклонить 2        отказать, баллы вернутся зрителю",
		"",
		p + "пауза              остановить музыку, которая звучит",
		p + "продолжить         снять с паузы",
		p + "громкость 40       громкость играющего заказа, 0–100",
		"",
		p + "бан ник            закрыть человеку заказы музыки",
		p + "бан ник причина    то же самое, причина попадёт в хронику",
		p + "разбан ник         вернуть человеку заказы",
		"",
		p + "стоп               перестать принимать новые заказы",
		p + "старт              снова принимать заказы",
		p + "помощь             этот список одной строкой в чат",
		"",
		"По-английски работает то же самое:",
		"queue (q), skip (s), undo, remove (rm), top, clear, volume (vol),",
		"pause, resume (play), playlists (pl), approve, reject,",
		"ban, unban, stop, start, help.",
		"",
		"ЧТО ВАЖНО ЗНАТЬ",
		"",
		"БАЛЛЫ ВОЗВРАЩАЮТСЯ САМИ. Любой скип, удаление и очистка возвращают",
		"баллы заказчику — отдельно ничего делать не надо. В чат при этом",
		"пишется, какой именно трек убрали и что баллы вернулись.",
		"",
		"КАК ВЫБРАТЬ, ЧТО СКИПНУТЬ. Тремя способами, как удобнее:",
		"  " + p + "скип           то, что звучит прямо сейчас",
		"  " + p + "скип 0         то же самое",
		"  " + p + "скип 1         следующий заказ, 2 — тот, что за ним, и так далее",
		"  " + p + "скип название  можно просто скопировать название из чата",
		"Номера берутся из «" + p + "очередь» — там заказы пронумерованы. Номер",
		"меняется, когда очередь двигается, поэтому смотреть очередь надо",
		"прямо перед скипом. Если под названием подходит сразу несколько",
		"заказов, приложение ничего не трогает и просит назвать номер.",
		"",
		"«" + p + "СТОП» — ЭТО ПРО ЗАКАЗЫ, А НЕ ПРО МУЗЫКУ. Приложение",
		"перестаёт принимать новые заказы; то, что уже играет, доиграет.",
		"Остановить звук — это «" + p + "пауза».",
		"",
		"ПЛЕЙЛИСТЫ ЖДУТ ВАШЕГО СЛОВА. Заказ плейлиста — отдельная награда, и",
		"сам в эфир он не идёт: сразу пять треков подряд от одного зрителя",
		"решает хозяин эфира. «" + p + "плейлисты» показывает, что ждёт, вместе",
		"со временем звучания; «" + p + "одобрить» пускает в очередь,",
		"«" + p + "отклонить» отказывает и возвращает баллы. Пока ждёт один",
		"плейлист, номер писать не надо.",
		"",
		"«" + p + "ВЕРНУТЬ» отменяет последний скип: трек встаёт в начало",
		"очереди. Баллы за него уже вернулись зрителю, и списать их обратно",
		"Twitch не даёт — так что этот раз он играет бесплатно.",
		"",
		"«" + p + "бан» — это только про заказы музыки. В чате человек не забанен",
		"и продолжает писать как обычно.",
		"",
		"Если заказ ждёт конца трека стримера, «" + p + "скип» означает",
		"«не жди, включай сейчас», а не «выброси заказ».",
		"",
		"Всё, что вы сделали, стример видит у себя в разделе «Хроника» —",
		"кто, что и когда. Это не слежка, а способ разобраться, если зритель",
		"придёт жаловаться на пропавшие баллы.",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// handleMods отдаёт справочник.
//
// Без ?save=1 — обычный текст: панель показывает его прямо на месте, чтобы
// стример видел, что именно он отправляет. С ?save=1 браузер предложит
// сохранить файл — его и пересылают модераторам.
func (s *Server) handleMods(w http.ResponseWriter, r *http.Request) {
	text := modsText(s.cfg.Get().CommandPrefix)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("save") == "1" {
		// Два имени сразу: латиницей — для тех, кто не понимает кодировок, и
		// по-русски через filename* — его и увидит человек. Без второго файл
		// сохранился бы под нечитаемой абракадаброй, а стример пересылает его
		// людям, которые должны понять, что это, ещё до открытия.
		const name = "Команды-модераторам.txt"
		if i18n.IsEN() {
			w.Header().Set("Content-Disposition", `attachment; filename="moderator-commands.txt"`)
		} else {
			w.Header().Set("Content-Disposition",
				`attachment; filename="komandy-moderatoram.txt"; filename*=UTF-8''`+
					url.PathEscape(name))
		}
	}
	w.Write([]byte(text))
}

// modsTextEN — тот же справочник по-английски, с английскими командами.
func modsTextEN(p string) string {
	lines := []string{
		"COMMANDS FOR MODERATORS",
		"",
		"Type them in the channel chat. They work for the streamer and moderators;",
		"regular viewers can't use them - they request music only with points or donations.",
		"",
		p + "queue (" + p + "q)         what is playing now and what's next",
		p + "now (" + p + "np)          what is playing right now, in one line",
		"",
		p + "skip (" + p + "s)          remove what is playing",
		p + "skip 2             remove the second request in the queue",
		p + "skip bad guy       remove a request by title or artist",
		p + "undo               put back what was skipped by mistake",
		"",
		p + "remove 2           same as \"" + p + "skip 2\" (also " + p + "rm)",
		p + "top 3              move the third request to the front of the queue",
		p + "clear              clear the whole queue",
		"",
		p + "playlists (" + p + "pl)    playlists waiting for a decision",
		p + "approve            let a playlist into the queue",
		p + "approve 2          if several are waiting - by number",
		p + "reject 2           decline; the viewer gets the points back",
		"",
		p + "pause              pause the music that is playing",
		p + "resume (" + p + "play)     unpause",
		p + "volume 40          volume of the playing request, 0-100 (also " + p + "vol)",
		"",
		p + "ban nick           block someone from requesting music",
		p + "ban nick reason    same, the reason goes into the activity log",
		p + "unban nick         let them request again",
		"",
		p + "stop               stop taking new requests",
		p + "start              take requests again",
		p + "help               this list as one line in chat",
		"",
		"WHAT YOU SHOULD KNOW",
		"",
		"POINTS ARE REFUNDED AUTOMATICALLY. Any skip, removal or clear refunds",
		"the points to the viewer - nothing else to do. The chat is told which",
		"track was removed and that the points were refunded.",
		"",
		"HOW TO PICK WHAT TO SKIP. Three ways, whichever is easier:",
		"  " + p + "skip           what is playing right now",
		"  " + p + "skip 0         the same",
		"  " + p + "skip 1         the next request, 2 - the one after it, and so on",
		"  " + p + "skip title     you can just copy the title from chat",
		"Numbers come from \"" + p + "queue\" - requests are numbered there. Numbers",
		"change as the queue moves, so check the queue right before skipping.",
		"If a title matches several requests, the app touches nothing",
		"and asks for a number.",
		"",
		"\"" + p + "STOP\" IS ABOUT REQUESTS, NOT MUSIC. The app stops taking new",
		"requests; whatever is already playing finishes. To stop the sound,",
		"use \"" + p + "pause\".",
		"",
		"PLAYLISTS WAIT FOR YOUR DECISION. A playlist request is a separate reward",
		"and doesn't go on air by itself: five tracks in a row from one viewer",
		"is the host's call. \"" + p + "playlists\" shows what's waiting along with",
		"its play time; \"" + p + "approve\" lets it into the queue,",
		"\"" + p + "reject\" declines it and refunds the points. If only one playlist",
		"is waiting, no number is needed.",
		"",
		"\"" + p + "UNDO\" reverts the last skip: the track goes back to the front",
		"of the queue. Its points were already refunded and Twitch doesn't let",
		"us take them back - so this time it plays for free.",
		"",
		"\"" + p + "ban\" is only about music requests. The person isn't banned",
		"from chat and can keep chatting as usual.",
		"",
		"If a request is waiting for the streamer's own track to end, \"" + p + "skip\"",
		"means \"don't wait, play it now\", not \"throw the request away\".",
		"",
		"Everything you do is shown to the streamer in the \"Activity\" section -",
		"who, what and when. It's not surveillance, just a way to sort things out",
		"if a viewer complains about missing points.",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}
