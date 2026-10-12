package queue

import "testing"

// Заказ, вынутый из очереди, переживает перезапуск, пока не закончится.
func TestInflightSurvivesUntilDone(t *testing.T) {
	q := newQueue(t)
	add(t, q, "а", "первый", SourcePoints, false)

	it, err := q.Next()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := q.TakeInflight()
	if !ok || got.Title != it.Title {
		t.Fatalf("незаконченный заказ не нашёлся: %v %+v", ok, got)
	}
	if _, ok := q.TakeInflight(); ok {
		t.Fatal("взятый незаконченный заказ отдан второй раз")
	}

	add(t, q, "б", "второй", SourcePoints, false)
	if _, err := q.Next(); err != nil {
		t.Fatal(err)
	}
	q.Done()
	if _, ok := q.TakeInflight(); ok {
		t.Fatal("законченный заказ остался незаконченным")
	}
}

// Снимок Spotify лежит в kv и стирается по nil — иначе после перезапуска
// музыка возвращалась бы на место прошлого стрима.
func TestKVJSON(t *testing.T) {
	q := newQueue(t)
	type snap struct{ Track string }
	q.PutJSON("snapshot", snap{"плейлист стримера"})
	var got snap
	if !q.GetJSON("snapshot", &got) || got.Track != "плейлист стримера" {
		t.Fatalf("снимок не прочитался: %+v", got)
	}
	q.PutJSON("snapshot", nil)
	if q.GetJSON("snapshot", &got) {
		t.Fatal("стёртый снимок всё ещё читается")
	}
}
