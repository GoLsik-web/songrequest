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
	// SpotifyProxy — посредник только для запросов к Spotify. Пусто — прямое
	// соединение. Нужен там, где до Spotify не достучаться напрямую; всё
	// остальное (Twitch, чат, донаты) при этом идёт своим путём и не теряет
	// в скорости, в отличие от системного VPN.
	SpotifyProxy string `json:"spotify_proxy"`

	// Twitch
	TwitchClientID   string `json:"twitch_client_id"`
	RewardTitle      string `json:"reward_title"`
	RewardCost       int    `json:"reward_cost"`
	CommandPrefix    string `json:"command_prefix"`
	AutoCreateReward bool   `json:"auto_create_reward"`

	// Донаты
	DonationAlertsClientID string  `json:"donationalerts_client_id"`
	DonatePayKey           string  `json:"donatepay_key"`
	DonateXKey             string  `json:"donatex_key"`
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
	// WaitForCurrent — не обрывать трек, который играет у стримера: первый
	// заказ дожидается его конца. По умолчанию включено — обрывать музыку
	// на середине хуже, чем подождать.
	WaitForCurrent bool `json:"wait_for_current"`

	// Матчинг: пороги уверенности и веса. Вынесены наружу, чтобы крутить без пересборки.
	MatchAccept float64      `json:"match_accept"` // выше — берём молча
	MatchMaybe  float64      `json:"match_maybe"`  // между — «неточное совпадение»
	MatchWeight MatchWeights `json:"match_weights"`

	// YouTube-фоллбэк
	AudioDevice string `json:"audio_device"` // имя устройства для mpv, пусто = системное
	// YouTubeBrowser — откуда брать куки, когда YouTube требует подтвердить,
	// что запросы не от робота. Пусто = приложение переберёт браузеры само.
	YouTubeBrowser string `json:"youtube_browser"`
	YtDlpPath      string `json:"ytdlp_path"` // пусто = встроенная копия
	MpvPath        string `json:"mpv_path"`

	// Фильтр «это не музыка»
	RejectKeywords []string `json:"reject_keywords"`

	// Виджет для OBS
	Widget Widget `json:"widget"`
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
		ResumeDelaySeconds: 1,
		WaitForCurrent:     true,
		MatchAccept:        0.80,
		MatchMaybe:         0.55,
		MatchWeight: MatchWeights{
			Title:      1.0,
			Artist:     0.8,
			Duration:   0.6,
			Popularity: 0.1,
			Version:    1.0,
		},
		Widget: DefaultWidget(),
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

	// saveMu отдельный: он держится на всю запись файла, а mu — только на
	// снятие копии настроек. Один замок на оба дела заставил бы панель ждать
	// диска ради обычного чтения.
	saveMu sync.Mutex
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
	// Конфиг мог быть от прошлой версии или поправлен руками: приводим в
	// рабочий вид сразу, а не когда виджет уже висит на стриме.
	f.cfg.Widget.Normalize()
	return f, nil
}

// Save записывает конфиг атомарно: сначала во временный файл, потом переименование.
// Так недописанный файл не заменит рабочий, если приложение убьют посреди записи.
func (f *File) Save() error {
	// Запись целиком под своим замком, и вот почему. Панель сохраняет
	// настройки целым файлом, а стример успевает нажать две галочки подряд.
	// Без замка две записи шли одновременно в один и тот же .tmp: одна правка
	// молча пропадала, а на Windows переименование ещё и упиралось в чужой
	// открытый файл — «не смог сохранить настройки» на ровном месте.
	f.saveMu.Lock()
	defer f.saveMu.Unlock()

	f.mu.RLock()
	data, err := json.MarshalIndent(f.cfg, "", "  ")
	f.mu.RUnlock()
	if err != nil {
		return err
	}

	// Имя временного файла своё у каждой записи: если приложение убьют
	// посреди сохранения, чужой огрызок не помешает следующему запуску.
	tmp := fmt.Sprintf("%s.%d.tmp", f.path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("запись настроек: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		os.Remove(tmp)
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
