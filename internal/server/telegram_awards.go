package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Server) telegramOrderAwards(ctx context.Context) string {
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT requester,artist,title,outcome,played_at FROM history WHERE played_at>=? ORDER BY played_at,requester`, s.telegramSince())
	if err != nil {
		return ""
	}
	type entry struct {
		user, artist, title, outcome string
		at                           int64
	}
	var tracks []entry
	for rows.Next() {
		var e entry
		if rows.Scan(&e.user, &e.artist, &e.title, &e.outcome, &e.at) == nil {
			tracks = append(tracks, e)
		}
	}
	rows.Close()
	if len(tracks) == 0 {
		return ""
	}
	counts, fans, skips, misses, nights := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	genres := map[string]int{}
	var oldest, newest entry
	oldYear, newYear, dated := 0, 0, 0
	for _, e := range tracks {
		counts[e.user]++
		if e.outcome == "skipped" {
			skips[e.user]++
		}
		if e.outcome == "rejected" {
			misses[e.user]++
			continue
		}
		if e.artist != "" {
			fans[e.user+"\x00"+e.artist]++
		}
		if time.Unix(e.at, 0).Hour() < 5 {
			nights[e.user]++
		}
		if info, ok := s.cachedMusic("artist", e.artist, e.title); ok && info.Genre != "" {
			genres[info.Genre]++
		}
		if info, ok := s.cachedMusic("recording", e.artist, e.title); ok && info.Year > 0 {
			dated++
			if oldYear == 0 || info.Year < oldYear {
				oldYear = info.Year
				oldest = e
			}
			if info.Year > newYear {
				newYear = info.Year
				newest = e
			}
		}
	}
	best := func(m map[string]int) (string, int) {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		winner, n := "", 0
		for _, k := range keys {
			if m[k] > n {
				winner, n = k, m[k]
			}
		}
		return winner, n
	}
	var b strings.Builder
	b.WriteString("\n🏆 Номинации эфира\n")
	if who, n := best(counts); n > 0 {
		fmt.Fprintf(&b, "Диджей вечера: %s (%d)\n", who, n)
	}
	if key, n := best(fans); n >= 2 {
		v := strings.SplitN(key, "\x00", 2)
		fmt.Fprintf(&b, "Верный фанат: %s — %s (%d)\n", v[0], v[1], n)
	}
	if who, n := best(skips); n >= 2 {
		fmt.Fprintf(&b, "Скип-магнит: %s (%d)\n", who, n)
	}
	if who, n := best(misses); n >= 2 {
		fmt.Fprintf(&b, "Искатель редкостей: %s (%d)\n", who, n)
	}
	if who, n := best(nights); n > 0 {
		fmt.Fprintf(&b, "Ночной меломан: %s (%d)\n", who, n)
	}
	if dated >= 3 {
		fmt.Fprintf(&b, "Хранитель винила: %s — %s (%d)\n", oldest.user, oldest.title, oldYear)
		if newYear != oldYear {
			fmt.Fprintf(&b, "Свежий слух: %s — %s (%d)\n", newest.user, newest.title, newYear)
		}
	}
	main, _ := best(genres)
	if main != "" {
		for _, e := range tracks {
			if e.outcome == "rejected" {
				continue
			}
			if info, ok := s.cachedMusic("artist", e.artist, e.title); ok && info.Genre != "" && info.Genre != main && genres[info.Genre] == 1 {
				fmt.Fprintf(&b, "Неожиданный вкус: %s — %s (%s)\n", e.user, e.title, info.Genre)
				break
			}
		}
	}
	return b.String()
}
