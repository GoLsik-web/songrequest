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
	// Запись живёт, пока заказ не вернули в очередь: сбой между «взял» и
	// «поставил обратно» не должен его терять.
	if again, ok := q.TakeInflight(); !ok || again.Title != it.Title {
		t.Fatal("незаконченный заказ стёрся до того, как его вернули в очередь")
	}
	q.ForgetInflight(got)
	if _, ok := q.TakeInflight(); ok {
		t.Fatal("забытый незаконченный заказ отдан второй раз")
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

// ForgetInflight не стирает чужую запись: пока заказ прошлого запуска
// возвращали в очередь, плеер мог уже взять новый.
func TestForgetInflightKeepsNewer(t *testing.T) {
	q := newQueue(t)
	add(t, q, "а", "старый", SourcePoints, false)
	old, _ := q.Next()
	add(t, q, "б", "новый", SourcePoints, false)
	if _, err := q.Next(); err != nil {
		t.Fatal(err)
	}
	q.ForgetInflight(old)
	got, ok := q.TakeInflight()
	if !ok || got.Title != "новый" {
		t.Fatalf("запись нового заказа стёрта: %v %+v", ok, got)
	}
}
