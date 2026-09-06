package tunnel

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"songrequest/internal/errs"
)

// Здесь приложение добывает себе xray.exe — программу, которая и умеет
// разговаривать с сервером обхода.
//
// Качаем с GitHub, как уже качаем yt-dlp и mpv. Просить нетехнического
// человека «скачай архив, распакуй, положи рядом» бесполезно: тестер один раз
// такую просьбу не выполнил, и заказы с YouTube у него просто не играли.
//
// Отдельная тонкость: качаем ровно в тот момент, когда обход ещё не работает,
// а значит интернет уже чем-то ограничен. GitHub из России обычно открыт (и
// mpv с yt-dlp оттуда качаются), но если и он закрыт — честно говорим об этом
// и даём положить файл руками: путь к нему есть в настройках.

// xrayDownloads — где лежит нужная сборка. Ключ — архитектура компьютера.
var xrayDownloads = map[string]string{
	"amd64": "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-windows-64.zip",
	"arm64": "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-windows-arm64-v8a.zip",
	"386":   "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-windows-32.zip",
}

// ensureTool находит программу обхода: сначала указанную в настройках, потом
// свою скачанную, и только затем качает.
func (t *Tunnel) ensureTool(ctx context.Context, override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		t.log.Warn("указанный путь к программе обхода не существует", "путь", override)
	}

	own := filepath.Join(t.dir, "xray"+exeSuffix())
	if _, err := os.Stat(own); err == nil {
		return own, nil
	}

	url, ok := xrayDownloads[runtime.GOARCH]
	if !ok || runtime.GOOS != "windows" {
		return "", errs.New(errs.TunnelNoTool,
			"Для этого компьютера у приложения нет программы обхода.")
	}

	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return "", errs.Wrap(errs.TunnelNoTool, "Не смог создать папку для программ.", err)
	}

	t.log.Info("качаю программу обхода", "откуда", url)
	if err := download(ctx, url, own); err != nil {
		return "", err
	}
	t.log.Info("программа обхода скачана", "путь", own)
	return own, nil
}

// download качает архив с Xray и достаёт из него только сам xray.exe.
//
// В архиве, кроме него, лежат списки стран (geoip.dat, geosite.dat) на
// несколько десятков мегабайт. Нам они не нужны: правил маршрутизации у нас
// нет, всё уходит в один выход, — и класть их на диск незачем.
func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errs.Wrap(errs.TunnelNoTool, "Не получилось скачать программу обхода.", err)
	}
	// Срок щедрый: архив весит около двадцати мегабайт, а интернет бывает
	// разный — тем более в тот момент, когда обход ещё не работает.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return errs.Wrap(errs.TunnelNoTool,
			"Не получилось скачать программу обхода. Проверь интернет: возможно, "+
				"без включённого VPN не открывается и GitHub.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errs.New(errs.TunnelNoTool,
			fmt.Sprintf("Не получилось скачать программу обхода (ответ %d).", resp.StatusCode))
	}

	// Читаем архив в память, а не на диск: распаковщику нужен произвольный
	// доступ, а двадцать мегабайт того не стоят, чтобы городить временный файл.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return errs.Wrap(errs.TunnelNoTool, "Загрузка программы обхода оборвалась.", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errs.Wrap(errs.TunnelNoTool, "Скачанный архив не читается.", err)
	}
	for _, f := range zr.File {
		if !strings.EqualFold(filepath.Base(f.Name), "xray"+exeSuffix()) {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return errs.Wrap(errs.TunnelNoTool, "Не смог достать программу из архива.", err)
		}
		defer src.Close()

		// Через временный файл: оборванная распаковка не должна оставить
		// битую программу, которая потом молча не запустится.
		tmp := dest + ".part"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return errs.Wrap(errs.TunnelNoTool, "Не смог сохранить программу обхода.", err)
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			os.Remove(tmp)
			return errs.Wrap(errs.TunnelNoTool, "Не смог сохранить программу обхода.", err)
		}
		out.Close()
		if err := os.Rename(tmp, dest); err != nil {
			os.Remove(tmp)
			return errs.Wrap(errs.TunnelNoTool, "Не смог сохранить программу обхода.", err)
		}
		return nil
	}
	return errs.New(errs.TunnelNoTool, "В скачанном архиве нет программы обхода.")
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// ToolPath — где лежит программа обхода. Панель показывает этот путь, чтобы
// человек мог положить файл руками, если скачать не вышло.
func (t *Tunnel) ToolPath() string {
	return filepath.Join(t.dir, "xray"+exeSuffix())
}
