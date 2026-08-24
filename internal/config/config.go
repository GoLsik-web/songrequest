// Package config хранит настройки приложения в JSON-файле рядом с базой данных.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ResumeFailMode — что делать, если вернуть Spotify в исходное состояние не удалось.
type ResumeFailMode string

const (
	// ResumeNothing — ничего не включать, только показать уведомление в панели.
	ResumeNothing ResumeFailMode = "nothing"
	// ResumeFallbackPlaylist — включить запасной плейлист, выбранный в настройках.
	// Режим по умолчанию: он самый предсказуемый, стример заранее знает, что заиграет.
	ResumeFallbackPlaylist ResumeFailMode = "playlist"
	// ResumeArtistRadio — включить треки последнего игравшего артиста.
	// Настоящее радио Spotify через API недоступно: эндпоинт рекомендаций
	// закрыт для новых приложений. Это ближайшая замена, которая работает.
	ResumeArtistRadio ResumeFailMode = "radio"
)

// Config — всё, что стример может настроить. Поля с тегом json попадают в файл.
type Config struct {
	// Локальный сервер
	Port int `json:"port"` // 0 = выбрать свободный порт

	// Spotify
	SpotifyClientID string `json:"spotify_client_id"`

	// Twitch
	TwitchClientID   string `json:"twitch_client_id"`
	RewardTitle      string `json:"reward_title"`
	RewardCost       int    `json:"reward_cost"`
	CommandPrefix    string `json:"command_prefix"`
	AutoCreateReward bool   `json:"auto_create_reward"`

	// Донаты
	DonationAlertsClientID string  `json:"donationalerts_client_id"`
	DonatePayKey           string  `json:"donatepay_key"`
	DonationMin            float64 `json:"donation_min"` // от какой суммы принимаем заказ

	// Лимиты заказов
	MaxTrackSeconds  int  `json:"max_track_seconds"`
	MaxPerUser       int  `json:"max_per_user"`
	DonationPriority bool `json:"donation_priority"`

	// Возврат контекста Spotify
	ResumeFail         ResumeFailMode `json:"resume_fail_mode"`
	FallbackPlaylistID string         `json:"fallback_playlist_id"`
	FallbackPlaylist   string         `json:"fallback_playlist_name"` // для показа в панели
	ResumeDelaySeconds int            `json:"resume_delay_seconds"`

	// Матчинг: пороги уверенности и веса. Вынесены наружу, чтобы крутить без пересборки.
	MatchAccept float64      `json:"match_accept"` // выше — берём молча
	MatchMaybe  float64      `json:"match_maybe"`  // между — «неточное совпадение»
	MatchWeight MatchWeights `json:"match_weights"`

	// YouTube-фоллбэк
	AudioDevice string `json:"audio_device"` // имя устройства для mpv, пусто = системное
	YtDlpPath   string `json:"ytdlp_path"`   // пусто = встроенная копия
	MpvPath     string `json:"mpv_path"`

	// Фильтр «это не музыка»
	RejectKeywords []string `json:"reject_keywords"`
}

// MatchWeights — веса слагаемых в оценке кандидата из Spotify.
type MatchWeights struct {
	Title      float64 `json:"title"`
	Artist     float64 `json:"artist"`
	Duration   float64 `json:"duration"`
	Popularity float64 `json:"popularity"`
	Version    float64 `json:"version"`
}

// Defaults возвращает конфиг со значениями по умолчанию.
func Defaults() Config {
	return Config{
		Port:               8977,
		RewardTitle:        "Заказ трека",
		RewardCost:         1000,
		CommandPrefix:      "!",
		AutoCreateReward:   true,
		MaxTrackSeconds:    8 * 60,
		MaxPerUser:         3,
		DonationPriority:   true,
		DonationMin:        100,
		ResumeFail:         ResumeFallbackPlaylist,
		ResumeDelaySeconds: 3,
		MatchAccept:        0.80,
		MatchMaybe:         0.55,
		MatchWeight: MatchWeights{
			Title:      1.0,
			Artist:     0.8,
			Duration:   0.6,
			Popularity: 0.1,
			Version:    1.0,
		},
		RejectKeywords: []string{
			"нарезка", "нарезки", "подкаст", "стрим", "compilation", "mix",
			"1 hour", "1 час", "10 hours", "podcast", "livestream", "best moments",
		},
	}
}

// File — конфиг на диске. Все обращения идут через него, поэтому чтение из
// панели и запись из настроек не мешают друг другу.
type File struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

// Load читает конфиг из dir/config.json. Если файла нет — создаёт со значениями
// по умолчанию. Неизвестные поля в файле игнорируются, отсутствующие остаются
// дефолтными, поэтому старый конфиг переживает обновление приложения.
func Load(dir string) (*File, error) {
	f := &File{cfg: Defaults(), path: filepath.Join(dir, "config.json")}

	data, err := os.ReadFile(f.path)
	switch {
	case os.IsNotExist(err):
		if err := f.Save(); err != nil {
			return nil, err
		}
		return f, nil
	case err != nil:
		return nil, fmt.Errorf("чтение настроек: %w", err)
	}

	if err := json.Unmarshal(data, &f.cfg); err != nil {
		return nil, fmt.Errorf("настройки повреждены (%s): %w", f.path, err)
	}
	return f, nil
}

// Save записывает конфиг атомарно: сначала во временный файл, потом переименование.
// Так недописанный файл не заменит рабочий, если приложение убьют посреди записи.
func (f *File) Save() error {
	f.mu.RLock()
	data, err := json.MarshalIndent(f.cfg, "", "  ")
	f.mu.RUnlock()
	if err != nil {
		return err
	}

	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("запись настроек: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("запись настроек: %w", err)
	}
	return nil
}

// Update меняет поля под блокировкой и сразу сохраняет результат на диск.
func (f *File) Update(fn func(*Config)) error {
	f.mu.Lock()
	fn(&f.cfg)
	f.mu.Unlock()
	return f.Save()
}

// Get отдаёт копию настроек для чтения без гонок.
func (f *File) Get() Config {
	f.mu.RLock()
	defer f.mu.RUnlock()
	cp := f.cfg
	cp.RejectKeywords = append([]string(nil), f.cfg.RejectKeywords...)
	return cp
}

// EffectiveResumeMode — режим, который действительно сработает.
//
// Если выбран запасной плейлист, но сам плейлист не выбран, включать нечего:
// молча деградируем до «ничего не включать». Панель подсветит это в настройках,
// но посреди стрима приложение не должно ругаться на настройку.
func (c Config) EffectiveResumeMode() ResumeFailMode {
	if c.ResumeFail == ResumeFallbackPlaylist && c.FallbackPlaylistID == "" {
		return ResumeNothing
	}
	return c.ResumeFail
}

// ResumeModeIncomplete сообщает панели, что выбранный режим не настроен до конца.
func (c Config) ResumeModeIncomplete() bool {
	return c.ResumeFail == ResumeFallbackPlaylist && c.FallbackPlaylistID == ""
}

// Path — путь к файлу настроек, показываем его в панели.
func (f *File) Path() string { return f.path }
