// Package i18n — перевод того, что видит человек: стример в панели и
// трее, зрители в чате. Журнал остаётся русским.
//
// Ключ сообщения — сам русский текст (или русская строка формата для
// fmt). Так русский режим остаётся байт-в-байт прежним: T просто
// возвращает ключ, а читать код по-прежнему удобно — видно, что скажут.
// Английские тексты лежат в словарях en_*.go, по файлу на область
// (чат, ошибки, трей…), чтобы правки не наступали друг на друга.
package i18n

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Языки. Русский — по умолчанию.
const (
	RU = "ru"
	EN = "en"
)

var (
	lang atomic.Value // string

	mu sync.RWMutex
	en = map[string]string{}
)

func init() { lang.Store(RU) }

// Normalize приводит значение из настроек к известному языку.
func Normalize(l string) string {
	if l == EN {
		return EN
	}
	return RU
}

// SetLang переключает язык сразу для всего приложения.
func SetLang(l string) { lang.Store(Normalize(l)) }

// Lang — текущий язык.
func Lang() string { return lang.Load().(string) }

// IsEN — включён ли английский.
func IsEN() bool { return Lang() == EN }

// register добавляет английские тексты. Вызывается из init() файлов en_*.go.
func register(m map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for k, v := range m {
		en[k] = v
	}
}

// T переводит русский текст на текущий язык. Нет перевода — отдаёт как есть.
func T(ru string) string {
	if !IsEN() {
		return ru
	}
	return English(ru)
}

// English — английский вариант независимо от текущего языка.
func English(ru string) string {
	mu.RLock()
	s, ok := en[ru]
	mu.RUnlock()
	if ok {
		return s
	}
	return ru
}

// Has — есть ли английский текст для ключа (для тестов).
func Has(ru string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := en[ru]
	return ok
}

// F — T для строки формата: F("%s заказал %s", a, b).
func F(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// Plural выбирает форму по числу: в русском три формы (1 трек, 2 трека,
// 5 треков), в английском две (one/other). ru — три русские формы, en —
// две английские.
func Plural(n int, ru [3]string, en [2]string) string {
	if IsEN() {
		if n == 1 || n == -1 {
			return en[0]
		}
		return en[1]
	}
	a := n
	if a < 0 {
		a = -a
	}
	if a%10 == 1 && a%100 != 11 {
		return ru[0]
	}
	if a%10 >= 2 && a%10 <= 4 && (a%100 < 10 || a%100 >= 20) {
		return ru[1]
	}
	return ru[2]
}
