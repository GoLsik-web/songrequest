package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type MusicInfo struct {
	Genre string `json:"genre"`
	Year  int    `json:"year"`
}
type musicTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func Genre(tags []musicTag) string {
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Count > tags[j].Count })
	groups := []struct {
		name  string
		words []string
	}{
		{"Классика", []string{"classical", "baroque", "opera"}}, {"Фонк", []string{"phonk"}},
		{"Метал", []string{"metal"}}, {"Джаз", []string{"jazz"}}, {"Хип-хоп", []string{"hip hop", "hip-hop", "rap"}},
		{"Рок", []string{"rock", "punk"}}, {"Электроника", []string{"electronic", "techno", "house", "trance", "dubstep", "drum and bass"}},
		{"Поп", []string{"pop"}}, {"Фолк", []string{"folk", "country"}}, {"R&B / соул", []string{"rhythm and blues", "r&b", "soul"}},
	}
	for _, tag := range tags {
		for _, g := range groups {
			for _, word := range g.words {
				if strings.Contains(strings.ToLower(tag.Name), word) {
					return g.name
				}
			}
		}
	}
	return ""
}
func queryQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
func score(raw json.RawMessage) int {
	v := strings.Trim(string(raw), `"`)
	n, _ := strconv.Atoi(v)
	return n
}

// MusicLookup makes one request. Its caller serializes and throttles all calls.
func MusicLookup(ctx context.Context, client *http.Client, base, kind, artist, title string) (MusicInfo, error) {
	q := "artist:" + queryQuote(artist)
	if kind == "recording" {
		q = "recording:" + queryQuote(title) + " AND " + q
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+kind+"?"+url.Values{"query": {q}, "fmt": {"json"}, "limit": {"5"}}.Encode(), nil)
	if err != nil {
		return MusicInfo{}, err
	}
	req.Header.Set("User-Agent", "songrequest/0.62.0 (https://github.com/GoLsik-web/songrequest)")
	resp, err := client.Do(req)
	if err != nil {
		return MusicInfo{}, fmt.Errorf("MusicBrainz unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return MusicInfo{}, fmt.Errorf("MusicBrainz: %d", resp.StatusCode)
	}
	var result struct {
		Artists []struct {
			Score json.RawMessage `json:"score"`
			Tags  []musicTag      `json:"tags"`
		} `json:"artists"`
		Recordings []struct {
			Score json.RawMessage `json:"score"`
			Date  string          `json:"first-release-date"`
		} `json:"recordings"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return MusicInfo{}, err
	}
	info := MusicInfo{}
	for _, a := range result.Artists {
		if score(a.Score) >= 95 {
			if g := Genre(a.Tags); g != "" {
				info.Genre = g
				break
			}
		}
	}
	for _, r := range result.Recordings {
		if score(r.Score) >= 95 && len(r.Date) >= 4 {
			year, _ := strconv.Atoi(r.Date[:4])
			if year >= 1000 && year <= 2100 && (info.Year == 0 || year < info.Year) {
				info.Year = year
			}
		}
	}
	return info, nil
}
