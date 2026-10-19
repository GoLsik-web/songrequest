// Package youtube играет то, чего нет в Spotify.
//
// Работает через два чужих бинарника: yt-dlp достаёт метаданные и поток,
// mpv воспроизводит без видео на отдельное устройство. Приложение скачивает
// их само при первом запуске — просить нетехнического человека «установить
// yt-dlp и добавить в PATH» бессмысленно.
package youtube

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

// Tools — пути к бинарникам.
type Tools struct {
	log *logx.Logger
	dir string

	ytdlp string
	mpv   string
}

// NewTools создаёт хранилище инструментов в папке приложения.
func NewTools(log *logx.Logger, dataDir string) *Tools {
	return &Tools{log: log, dir: filepath.Join(dataDir, "tools")}
}

// downloads — откуда берём бинарники. Только Windows: приложение писалось
// под неё, а на других системах эти программы обычно уже стоят из пакетов.
var downloads = map[string]string{
	"yt-dlp": "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe",
}

// Ensure находит инструменты: сначала указанные вручную, потом свои,
// потом системные, и только затем качает.
//
// overrideYtdlp и overrideMpv — пути из настроек. Пустые значения означают
// «разбирайся сам».
func (t *Tools) Ensure(ctx context.Context, overrideYtdlp, overrideMpv string) error {
	t.ytdlp = t.resolve("yt-dlp", overrideYtdlp)
	t.mpv = t.resolve("mpv", overrideMpv)

	// yt-dlp — только свой и свежий (на Windows, где мы умеем его качать).
	//
	// 12.10: у владельца нашёлся yt-dlp из Python трёхмесячной давности, и
	// приложение брало его. YouTube такому режет скорость: звук на 1 МБ
	// качался 18 секунд, а через mpv обрывался. Свежий yt-dlp.exe тот же трек
	// отдавал за 7. YouTube ломает старые версии постоянно, поэтому своя
	// копия ещё и обновляется (см. refreshYtdlp).
	if overrideYtdlp == "" && runtime.GOOS == "windows" {
		own := filepath.Join(t.dir, "yt-dlp.exe")
		if _, err := os.Stat(own); err == nil {
			t.ytdlp = own
			go t.refreshYtdlp(context.WithoutCancel(ctx), own)
		} else {
			t.ytdlp = ""
		}
	}

	if t.ytdlp == "" {
		path, err := t.download(ctx, "yt-dlp")
		if err != nil {
			return err
		}
		t.ytdlp = path
	}

	if t.mpv == "" {
		// Раньше здесь стояла просьба поставить mpv самому через winget.
		// Тестер её не выполнил — и заказ по ссылке на YouTube у него просто
		// не играл, а зритель получал «ты не написал, что заказываешь».
		// Просить нетехнического человека что-то устанавливать бесполезно:
		// ставим сами, как уже ставим yt-dlp.
		path, err := t.installMpv(ctx)
		if err != nil {
			return err
		}
		t.mpv = path
	}
	return nil
}

// Ready сообщает, всё ли на месте.
func (t *Tools) Ready() bool { return t.ytdlp != "" && t.mpv != "" }

// resolve ищет программу: указанный путь, своя папка, затем системная.
func (t *Tools) resolve(name, override string) string {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
		t.log.Warn("указанный путь не существует", "программа", name, "путь", override)
	}

	own := filepath.Join(t.dir, name+exeSuffix())
	if _, err := os.Stat(own); err == nil {
		return own
	}

	// Системная установка — самый частый случай для mpv.
	for _, candidate := range systemNames(name) {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func systemNames(name string) []string {
	if name == "mpv" {
		// mpv.net ставится под своим именем и умеет те же ключи.
		return []string{"mpv", "mpv.net", "mpvnet"}
	}
	return []string{name}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// download качает бинарник в папку приложения.
func (t *Tools) download(ctx context.Context, name string) (string, error) {
	url, ok := downloads[name]
	if !ok || runtime.GOOS != "windows" {
		return "", errs.Newf(errs.YouTubeNoTool,
			"Не найдена программа %s. Установи её и укажи путь в настройках.", name)
	}

	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return "", errs.Wrap(errs.YouTubeNoTool, "Не смог создать папку для программ.", err)
	}
	path := filepath.Join(t.dir, name+exeSuffix())

	t.log.Info("качаю программу", "название", name, "куда", path)
	if err := t.fetch(ctx, url, path, errs.YouTubeNoTool, name); err != nil {
		return "", err
	}

	t.log.Info("программа скачана", "название", name, "путь", path)
	return path, nil
}

// Paths отдаёт найденные пути — панель показывает их в настройках.
func (t *Tools) Paths() (ytdlp, mpv string) { return t.ytdlp, t.mpv }

// fetch качает файл по адресу и кладёт его на место одним движением.
//
// Через временный файл: оборванная загрузка не должна оставить битый
// бинарник, который потом молча не запустится.
func (t *Tools) fetch(ctx context.Context, url, path string, code errs.Code, name string) error {
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return errs.Wrapf(code, err, "Не смог сохранить %s.", name)
	}
	fail := func(e error, format string, args ...any) error {
		out.Close()
		os.Remove(tmp)
		return errs.Wrapf(code, e, format, args...)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(err, "Не получилось скачать %s.", name)
	}

	// Срок щедрый: mpv весит три десятка мегабайт, а интернет бывает разный.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fail(err, "Не получилось скачать %s. Проверь интернет.", name)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fail(nil, "Не получилось скачать %s (ответ %d).", name, resp.StatusCode)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fail(err, "Загрузка %s оборвалась.", name)
	}
	out.Close()

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return errs.Wrapf(code, err, "Не смог сохранить %s.", name)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.log.Warn("не выставил права на файл", "путь", path, "ошибка", err)
	}
	return nil
}

// ytdlpFresh — сколько своя копия yt-dlp считается свежей.
const ytdlpFresh = 3 * 24 * time.Hour

// refreshYtdlp тихо скачивает свежий yt-dlp поверх старого.
//
// В фоне: заказы идут и на старом, а новый заменит его одним движением (см.
// fetch). Не вышло — не беда, попробуем при следующем запуске.
func (t *Tools) refreshYtdlp(ctx context.Context, path string) {
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) < ytdlpFresh {
		return
	}
	t.log.Info("обновляю yt-dlp", "возраст_дней", int(time.Since(info.ModTime()).Hours()/24))
	if err := t.fetch(ctx, downloads["yt-dlp"], path, errs.YouTubeNoTool, "yt-dlp"); err != nil {
		t.log.Warn("yt-dlp не обновился", "ошибка", err)
		return
	}
	t.log.Info("yt-dlp обновлён")
}
