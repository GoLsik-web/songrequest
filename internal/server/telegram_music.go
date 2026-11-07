package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tg "songrequest/internal/telegram"
)

type cachedMusic struct {
	tg.MusicInfo
	At time.Time
}

func (s *Server) cachedMusic(kind, artist, title string) (cachedMusic, bool) {
	var result cachedMusic
	raw, ok, err := s.db.GetKV(musicKey(kind, artist, title))
	if err != nil || !ok || json.Unmarshal([]byte(raw), &result) != nil {
		return result, false
	}
	return result, time.Since(result.At) < 30*24*time.Hour
}
func musicKey(kind, artist, title string) string {
	if kind == "artist" {
		title = ""
	}
	return "telegram_music:" + kind + ":" + strings.ToLower(strings.TrimSpace(artist)) + "|" + strings.ToLower(strings.TrimSpace(title))
}
func (s *Server) warmTelegramMusic(ctx context.Context) {
	client := &http.Client{Timeout: 8 * time.Second}
	for ctx.Err() == nil {
		rows, err := s.db.SQL().QueryContext(ctx, `SELECT DISTINCT artist,title FROM history WHERE played_at>=? AND outcome IN ('played','skipped') AND artist<>'' AND title<>'' ORDER BY played_at DESC LIMIT 200`, s.telegramSince())
		var tracks [][2]string
		if err == nil {
			for rows.Next() {
				var a, t string
				if rows.Scan(&a, &t) == nil {
					tracks = append(tracks, [2]string{a, t})
				}
			}
			rows.Close()
		}
		failed := false
		for _, track := range tracks {
			for _, kind := range []string{"artist", "recording"} {
				if _, ok := s.cachedMusic(kind, track[0], track[1]); ok {
					continue
				}
				if !telegramWait(ctx, nil, 1200*time.Millisecond) {
					return
				}
				call, cancel := context.WithTimeout(ctx, 8*time.Second)
				info, err := tg.MusicLookup(call, client, "https://musicbrainz.org/ws/2", kind, track[0], track[1])
				cancel()
				if err != nil {
					failed = true
					break
				}
				raw, _ := json.Marshal(cachedMusic{MusicInfo: info, At: time.Now()})
				_ = s.db.SetKV(musicKey(kind, track[0], track[1]), string(raw))
			}
			if failed {
				break
			}
		}
		if !telegramWait(ctx, nil, time.Minute) {
			return
		}
	}
}
func (s *Server) telegramTaste(ctx context.Context) string {
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT artist,title,COUNT(*) FROM history WHERE played_at>=? AND outcome IN ('played','skipped') AND artist<>'' GROUP BY artist,title`, s.telegramSince())
	if err != nil {
		return ""
	}
	type track struct {
		artist, title string
		n             int
	}
	var tracks []track
	for rows.Next() {
		var t track
		if rows.Scan(&t.artist, &t.title, &t.n) == nil {
			tracks = append(tracks, t)
		}
	}
	rows.Close()
	genres := map[string]int{}
	years, dated, known, total := 0, 0, 0, 0
	for _, t := range tracks {
		total += t.n
		if data, ok := s.cachedMusic("artist", t.artist, t.title); ok && data.Genre != "" {
			genres[data.Genre] += t.n
			known += t.n
		}
		if data, ok := s.cachedMusic("recording", t.artist, t.title); ok && data.Year > 0 {
			years += data.Year * t.n
			dated += t.n
		}
	}
	if total == 0 {
		return ""
	}
	if known == 0 && dated == 0 {
		return "\nМузыкальный вкус: сведения о жанрах и годах ещё уточняются.\n"
	}
	var list []string
	for g := range genres {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if genres[list[i]] == genres[list[j]] {
			return list[i] < list[j]
		}
		return genres[list[i]] > genres[list[j]]
	})
	text := "\nМузыкальный вкус (MusicBrainz)\n"
	for i, g := range list {
		if i >= 3 {
			break
		}
		text += fmt.Sprintf("%s: %d%%\n", g, genres[g]*100/known)
	}
	if dated > 0 {
		text += fmt.Sprintf("Средний год трека: %d\n", years/dated)
	}
	if known > 0 {
		text += fmt.Sprintf("Жанр определён для %d из %d треков.\n", known, total)
	}
	if len(list) > 0 {
		text += "Вердикт: " + map[string]string{"Рок": "рок-клуб собран — гитары погромче!", "Метал": "чат пришёл трясти головой.", "Классика": "у вас не чат, а филармония.", "Фонк": "чат дрифтует под фонк.", "Джаз": "джазовый клуб открыт.", "Электроника": "не стрим, а рейв.", "Поп": "вечер знакомых припевов.", "Хип-хоп": "бит качает, баллы летят.", "Фолк": "душевный музыкальный вечер.", "R&B / соул": "всё на груве и настроении."}[list[0]] + "\n"
	}
	return text
}
