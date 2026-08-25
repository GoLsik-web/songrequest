// Package diag собирает архив для разбора проблем на чужом компьютере.
//
// Приложение стоит у стримера, а чинит его разработчик, поэтому нужен один
// файл, который стример просто перешлёт: лог, настройки без секретов и
// сведения о системе. Ничего сверх этого в архив не попадает.
package diag

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

// Info — сведения о запуске, которые кладём в архив рядом с логом.
type Info struct {
	Version     string            `json:"версия_приложения"`
	OS          string            `json:"ос"`
	ExportedAt  string            `json:"время_выгрузки"`
	DataDir     string            `json:"папка_данных"`
	Connections map[string]string `json:"подключения"`
}

// Build собирает zip в память и возвращает его вместе с именем файла.
// В память, а не на диск: архив уходит в браузер и нигде не остаётся.
//
// extra — готовые файлы, которые кладём рядом с логом: история заказов с
// возвратами баллов и список действий в панели. Их собирает сервер, потому
// что живут они в базе, а этот пакет про базу ничего не знает.
func Build(dataDir, configPath string, red *logx.Redactor, info Info, extra map[string][]byte) ([]byte, string, error) {
	info.OS = runtime.GOOS + " " + runtime.GOARCH
	info.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	info.DataDir = dataDir

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}

	// Лог и его предыдущая часть, если она есть.
	logPath := filepath.Join(dataDir, logx.LogFileName)
	for _, item := range []struct{ path, name string }{
		{logPath, logx.LogFileName},
		{logPath + ".1", logx.LogFileName + ".1"},
	} {
		data, err := os.ReadFile(item.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, "", errs.Wrap(errs.DiagExport, "Не смог прочитать файл лога.", err)
		}
		// Лог уже чистится при записи, но перед отправкой наружу проверяем ещё
		// раз: файл мог остаться от старой версии приложения.
		if err := add(item.name, []byte(red.Clean(string(data)))); err != nil {
			return nil, "", errs.Wrap(errs.DiagExport, "Не смог упаковать лог.", err)
		}
	}

	// Настройки без опознавательных знаков.
	if cfgData, err := os.ReadFile(configPath); err == nil {
		if err := add("config.json", sanitizeConfig(cfgData)); err != nil {
			return nil, "", errs.Wrap(errs.DiagExport, "Не смог упаковать настройки.", err)
		}
	}

	// Порядок обхода карты в Go случайный, а имена файлов в архиве должны
	// идти одинаково от выгрузки к выгрузке — иначе их неудобно сравнивать.
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(extra[name]) == 0 {
			continue
		}
		if err := add(name, []byte(red.Clean(string(extra[name])))); err != nil {
			return nil, "", errs.Wrap(errs.DiagExport, "Не смог упаковать "+name+".", err)
		}
	}

	infoData, _ := json.MarshalIndent(info, "", "  ")
	if err := add("система.json", infoData); err != nil {
		return nil, "", errs.Wrap(errs.DiagExport, "Не смог упаковать сведения о системе.", err)
	}

	if err := zw.Close(); err != nil {
		return nil, "", errs.Wrap(errs.DiagExport, "Не смог закрыть архив.", err)
	}

	name := fmt.Sprintf("songrequest-лог-%s.zip", time.Now().Format("2006-01-02-1504"))
	return buf.Bytes(), name, nil
}

// sanitizeConfig прячет идентификаторы приложений. Client ID при PKCE не
// секрет, но это всё-таки чужой ключ: для разбора проблемы достаточно знать,
// что он заполнен и какой длины.
func sanitizeConfig(data []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return []byte("(настройки не разобрались, поэтому в архив не попали)")
	}

	for key, value := range raw {
		if !strings.HasSuffix(key, "client_id") {
			continue
		}
		s, _ := value.(string)
		// Режем по рунам, а не по байтам: если в поле по ошибке окажется
		// кириллица, обрезка по байтам даст битый текст в архиве.
		r := []rune(s)
		switch {
		case s == "":
			raw[key] = "(не заполнен)"
		case len(r) <= 6:
			raw[key] = "(заполнен, подозрительно короткий)"
		default:
			raw[key] = fmt.Sprintf("%s… (всего символов: %d)", string(r[:6]), len(r))
		}
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return []byte("(настройки не разобрались, поэтому в архив не попали)")
	}
	return out
}
