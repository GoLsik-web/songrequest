package match

import "strings"

// Маркеры версии — то, что делает трек другой записью той же песни: ремикс,
// кавер, концертник, ускоренная версия.
//
// Правило простое и важное: если зритель маркер не писал, значит он хочет
// оригинал. Без этого правила поиск отдаёт самое популярное, а популярнее
// оригинала часто оказывается ускоренная версия из тиктока.
//
// «Remastered» и «Radio Edit» маркерами не считаются: для человека это та же
// самая песня, и штрафовать за них нельзя. Они разбираются в StripEditionSuffix.
var markerWords = map[string][]string{
	"remix":      {"remix", "rmx", "ремикс", "ремиx"},
	"cover":      {"cover", "кавер", "кover"},
	"live":       {"live", "концерт", "концертная", "живьем", "живьём", "unplugged"},
	"sped":       {"sped up", "speed up", "spedup", "ускоренная", "ускоренный", "ускорено", "nightcore"},
	"slowed":     {"slowed", "slow down", "замедленная", "замедленный", "slowed reverb"},
	"reverb":     {"reverb", "реверб"},
	"acoustic":   {"acoustic", "акустика", "акустическая"},
	"instrument": {"instrumental", "инструментал", "минус", "минусовка", "karaoke", "караоке"},
	"8d":         {"8d", "8д"},
	"bass":       {"bass boosted", "bassboosted", "бас буст"},
	"demo":       {"demo", "демо"},
	"mashup":     {"mashup", "мэшап", "машап"},
	"edit":       {"vip edit", "club edit", "extended mix", "extended version"},
}

// FindMarkers ищет в тексте пометки о версии и возвращает их виды.
//
// Возвращается именно вид («remix»), а не написание: заказ со словом «ремикс»
// должен совпасть с кандидатом, у которого написано «Remix».
func FindMarkers(s string) []string {
	norm := Normalize(s)
	if norm == "" {
		return nil
	}
	padded := " " + norm + " "

	var found []string
	seen := map[string]bool{}
	for kind, words := range markerWords {
		for _, w := range words {
			if strings.Contains(padded, " "+Normalize(w)+" ") {
				if !seen[kind] {
					seen[kind] = true
					found = append(found, kind)
				}
				break
			}
		}
	}
	return found
}

// SameMarkers сравнивает наборы пометок.
func SameMarkers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if !set[y] {
			return false
		}
	}
	return true
}

// sharedMarkers считает, сколько пометок совпало.
func sharedMarkers(a, b []string) int {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	n := 0
	for _, y := range b {
		if set[y] {
			n++
		}
	}
	return n
}
