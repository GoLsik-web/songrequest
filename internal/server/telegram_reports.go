package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"songrequest/internal/telegram"
)

const telegramHistoryPageSize = 10

func telegramOnOff(value bool) string {
	if value {
		return "включены"
	}
	return "выключены"
}

func telegramMutatingAction(action string) bool {
	switch action {
	case "skip", "pause", "play", "open", "close":
		return true
	}
	return strings.HasPrefix(action, "top:") || strings.HasPrefix(action, "remove:") || strings.HasPrefix(action, "setting:")
}

func (s *Server) telegramHistoryPage(ctx context.Context, page int) (string, bool, error) {
	if page < 0 || page > 100000 {
		return "", false, fmt.Errorf("invalid page")
	}
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT requester,COALESCE(title,''),COALESCE(artist,''),outcome FROM history WHERE played_at>=? ORDER BY played_at DESC,id DESC LIMIT ? OFFSET ?`, s.telegramSince(), telegramHistoryPageSize+1, page*telegramHistoryPageSize)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "📜 Заказы эфира · страница %d\n", page+1)
	count := 0
	for rows.Next() {
		var user, title, artist, outcome string
		if err := rows.Scan(&user, &title, &artist, &outcome); err != nil {
			return "", false, err
		}
		if count == telegramHistoryPageSize {
			return b.String(), true, nil
		}
		label := map[string]string{"played": "сыграно", "skipped": "пропущено", "rejected": "отклонено"}[outcome]
		if label == "" {
			label = outcome
		}
		fmt.Fprintf(&b, "%d. %s: %s — %s (%s)\n", page*telegramHistoryPageSize+count+1, user, artist, title, label)
		count++
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if count == 0 {
		b.WriteString("На этой странице нет завершённых заказов.\n")
	}
	b.WriteString("\n/export — полный список за эфир в CSV")
	return b.String(), false, nil
}

func (s *Server) sendTelegramHistory(ctx context.Context, c *telegram.Client, chat int64, page int) error {
	text, more, err := s.telegramHistoryPage(ctx, page)
	if err != nil {
		return s.telegramSend(ctx, c, chat, "Не удалось прочитать историю. Попробуй ещё раз.")
	}
	var navigation []map[string]string
	if page > 0 {
		navigation = append(navigation, tgButton("← Назад", fmt.Sprintf("history:%d", page-1)))
	}
	if more {
		navigation = append(navigation, tgButton("Дальше →", fmt.Sprintf("history:%d", page+1)))
	}
	keyboard := tgKeyboard()
	if len(navigation) > 0 {
		keyboard["inline_keyboard"] = append([][]map[string]string{navigation}, keyboard["inline_keyboard"].([][]map[string]string)...)
	}
	parts := telegram.SplitText(text)
	for i, part := range parts {
		payload := map[string]any{"chat_id": chat, "text": part}
		if i == len(parts)-1 {
			payload["reply_markup"] = keyboard
		}
		if err := c.Call(ctx, "sendMessage", payload, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) telegramHistoryCSV(ctx context.Context) ([]byte, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `SELECT played_at,requester,COALESCE(artist,''),COALESCE(title,''),provider,outcome,COALESCE(reason,'') FROM history WHERE played_at>=? ORDER BY played_at,id`, s.telegramSince())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf") // Excel: UTF-8 с кириллицей.
	w := csv.NewWriter(&b)
	w.Comma = ';'
	if err := w.Write([]string{"Когда", "Зритель", "Исполнитель", "Трек", "Источник", "Результат", "Причина"}); err != nil {
		return nil, err
	}
	for rows.Next() {
		var at int64
		var user, artist, title, provider, outcome, reason string
		if err := rows.Scan(&at, &user, &artist, &title, &provider, &outcome, &reason); err != nil {
			return nil, err
		}
		cells := []string{time.Unix(at, 0).Format(time.RFC3339), user, artist, title, provider, outcome, reason}
		for i, cell := range cells {
			trimmed := strings.TrimLeft(cell, " \t\r\n")
			if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
				cells[i] = "'" + cell
			}
		}
		if err := w.Write(cells); err != nil {
			return nil, err
		}
		if b.Len() > 8<<20 {
			return nil, fmt.Errorf("report too large")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func (s *Server) sendTelegramExport(ctx context.Context, c *telegram.Client, chat int64) error {
	data, err := s.telegramHistoryCSV(ctx)
	if err != nil {
		return s.telegramSend(ctx, c, chat, "Не удалось собрать файл истории. Попробуй ещё раз.")
	}
	return c.Document(ctx, strconv.FormatInt(chat, 10), "Все завершённые заказы за текущий эфир", "songrequest-orders.csv", data)
}
