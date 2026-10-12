package match

import (
	"sync"
	"testing"
)

// Разбор заказов идёт параллельно: два зрителя нажимают награду в одну
// секунду. 12.10 общая цепочка transform роняла приложение целиком.
// Запускать с -race не обязательно: падение ловится и так, за сотни кругов.
func TestNormalizeConcurrent(t *testing.T) {
	inputs := []string{"Beyoncé Halo", "Björk Army of Me", "Кино Группа крови", "Måneskin Beggin", "!скип"}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				in := inputs[(g+i)%len(inputs)]
				if Normalize(in) == "" {
					t.Errorf("пустой результат для %q", in)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
