// Package logx — логирование с уровнями и маскировкой секретов.
//
// Приложение работает на чужом компьютере, а чинит его другой человек, поэтому
// лог подробный: при любой ошибке от Spotify или Twitch в него уходит полный
// ответ сервиса. Чтобы такой лог можно было переслать, все секреты в нём
// затираются на лету — и по известным значениям, и по форме записи.
package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Секреты узнаём двумя способами. Первый — по форме: так ловятся токены,
// которых мы ещё не видели, например внутри чужого JSON-ответа.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:access_token|refresh_token|code_verifier|code|id_token|client_secret)"\s*:\s*")([^"]{4,})(")`),
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9._\-]{12,})`),
	regexp.MustCompile(`(?i)((?:access_token|refresh_token|code_verifier|client_secret)=)([^&\s]{4,})`),
}

const mask = "***СКРЫТО***"

// Redactor помнит конкретные секреты этого запуска и вычищает их из строк.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// Add запоминает значение, которое нельзя показывать. Короткие значения
// игнорируем: затирать строку из трёх символов — значит испортить весь лог.
func (r *Redactor) Add(secrets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range secrets {
		if len(s) < 8 {
			continue
		}
		if !contains(r.secrets, s) {
			r.secrets = append(r.secrets, s)
		}
	}
}

// Clean затирает в строке всё, что не должно попасть в лог.
func (r *Redactor) Clean(s string) string {
	r.mu.RLock()
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, mask)
	}
	r.mu.RUnlock()

	for _, re := range patterns {
		s = re.ReplaceAllString(s, "${1}"+mask+"${3}")
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// handler оборачивает обычный обработчик slog и чистит все строковые значения.
// Чистка на этом уровне важнее, чем в местах вызова: секрет может приехать
// внутри текста чужой ошибки, о которой мы даже не думали.
type handler struct {
	inner slog.Handler
	red   *Redactor
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, h.red.Clean(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(h.cleanAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *handler) cleanAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.red.Clean(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok && err != nil {
			return slog.String(a.Key, h.red.Clean(err.Error()))
		}
		return slog.String(a.Key, h.red.Clean(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		out := make([]any, 0, len(attrs))
		for _, sub := range attrs {
			out = append(out, h.cleanAttr(sub))
		}
		return slog.Group(a.Key, out...)
	default:
		return a
	}
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		cleaned = append(cleaned, h.cleanAttr(a))
	}
	return &handler{inner: h.inner.WithAttrs(cleaned), red: h.red}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name), red: h.red}
}

// Logger — логгер приложения вместе с редактором секретов и путём к файлу.
type Logger struct {
	*slog.Logger
	Redactor *Redactor
	Path     string

	file  *os.File
	level *slog.LevelVar
}

// LogFileName — имя файла лога внутри папки с данными.
const LogFileName = "songrequest.log"

// maxLogBytes — при старте лог обрезается, если разросся. Хранить историю за
// год незачем, а вот переслать разработчику файл на 200 МБ уже не выйдет.
const maxLogBytes = 8 << 20

// New создаёт логгер, пишущий в консоль и в файл dir/songrequest.log.
func New(dir string, debug bool) (*Logger, error) {
	path := filepath.Join(dir, LogFileName)
	rotate(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}

	level := new(slog.LevelVar)
	red := &Redactor{}

	base := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{
		Level: level,
	})
	l := &Logger{
		Logger:   slog.New(&handler{inner: base, red: red}),
		Redactor: red,
		Path:     path,
		file:     f,
		level:    level,
	}
	l.SetDebug(debug)
	return l, nil
}

// SetDebug переключает подробность на ходу: стример жмёт галочку в панели, и
// следующая же ошибка пишется со всеми подробностями, без перезапуска.
func (l *Logger) SetDebug(on bool) {
	if on {
		l.level.Set(slog.LevelDebug)
	} else {
		l.level.Set(slog.LevelInfo)
	}
}

// DebugEnabled сообщает, включён ли подробный режим. Имя не Debug, чтобы не
// перекрывать одноимённый метод slog.Logger, который мы встраиваем.
func (l *Logger) DebugEnabled() bool { return l.level.Level() == slog.LevelDebug }

// Close закрывает файл лога.
func (l *Logger) Close() error { return l.file.Close() }

// rotate переименовывает разросшийся лог в .1 и начинает новый.
func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogBytes {
		return
	}
	os.Remove(path + ".1")
	os.Rename(path, path+".1")
}
