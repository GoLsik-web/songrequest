// Package secrets хранит токены в системном хранилище учётных данных
// (на Windows — «Диспетчер учётных данных»), а не в файле рядом с настройками.
//
// Запасного варианта с открытым файлом сознательно нет: тихо положить токен
// Spotify в текстовый файл на чужом компьютере — хуже, чем честно сказать,
// что хранилище недоступно.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "songrequest"

// ErrNotFound означает, что вход ещё не выполнялся.
var ErrNotFound = errors.New("сохранённых данных нет")

// Store — доступ к системному хранилищу.
type Store struct{}

// New создаёт хранилище.
func New() *Store { return &Store{} }

// partSize — сколько знаков класть в одну запись хранилища.
//
// У хранилища Windows предел — две с половиной тысячи байт на запись. Токены в
// него влезают, а список серверов обхода (двадцать серверов с ключами) — нет:
// приложение каждый раз писало «data passed to Set was too big», список не
// запоминался, и при каждом включении обхода подписка скачивалась заново.
// Большое кладём по частям. Тысяча — с запасом: Windows хранит текст
// двухбайтовым.
const partSize = 1000

// partsMark — чем начинается главная запись, если данные лежат по частям.
const partsMark = "parts:"

func partName(name string, i int) string { return fmt.Sprintf("%s#%d", name, i) }

// PutJSON кладёт структуру под именем name.
func (s *Store) PutJSON(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	text := string(data)
	// Прошлые части больше не нужны: новая запись может быть короче.
	s.deleteParts(name)
	if len(text) <= partSize {
		return set(name, text)
	}
	n := 0
	for start := 0; start < len(text); start += partSize {
		end := min(start+partSize, len(text))
		if err := set(partName(name, n), text[start:end]); err != nil {
			return err
		}
		n++
	}
	return set(name, fmt.Sprintf("%s%d", partsMark, n))
}

func set(name, text string) error {
	if err := keyring.Set(service, name, text); err != nil {
		return fmt.Errorf("не смог сохранить данные входа в хранилище Windows: %w", err)
	}
	return nil
}

// GetJSON читает структуру; ErrNotFound, если её там нет.
func (s *Store) GetJSON(name string, v any) error {
	data, err := get(name)
	if err != nil {
		return err
	}
	var n int
	if _, scanErr := fmt.Sscanf(data, partsMark+"%d", &n); scanErr == nil && strings.HasPrefix(data, partsMark) {
		var b strings.Builder
		for i := 0; i < n; i++ {
			part, err := get(partName(name, i))
			if err != nil {
				return err
			}
			b.WriteString(part)
		}
		data = b.String()
	}
	return json.Unmarshal([]byte(data), v)
}

func get(name string) (string, error) {
	data, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("не смог прочитать данные входа из хранилища Windows: %w", err)
	}
	return data, nil
}

// deleteParts убирает части большой записи, если она была большой.
func (s *Store) deleteParts(name string) {
	data, err := keyring.Get(service, name)
	if err != nil || !strings.HasPrefix(data, partsMark) {
		return
	}
	var n int
	fmt.Sscanf(data, partsMark+"%d", &n)
	for i := 0; i < n; i++ {
		keyring.Delete(service, partName(name, i))
	}
}

// Delete забывает сохранённое: нужно для кнопки «Отключить».
func (s *Store) Delete(name string) error {
	s.deleteParts(name)
	err := keyring.Delete(service, name)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
