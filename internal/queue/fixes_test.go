package queue

import (
	"errors"
	"reflect"
	"testing"
)

// Remove говорит, что заказа уже нет: по этому ответу сервер решает, можно
// ли возвращать баллы (плеер мог забрать заказ через Next).
func TestRemoveMissingIsNotFound(t *testing.T) {
	q := newQueue(t)
	it := add(t, q, "а", "один", SourcePoints, false)
	if _, err := q.Next(); err != nil {
		t.Fatal(err)
	}
	if err := q.Remove(it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("удаление забранного заказа: %v, ждали ErrNotFound", err)
	}
}

func TestMoveTopMissing(t *testing.T) {
	q := newQueue(t)
	add(t, q, "а", "один", SourcePoints, false)
	if err := q.MoveTop(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("подъём несуществующего: %v", err)
	}
}

// Неполный список не даёт совпадающих позиций: пропущенные встают в конец в
// прежнем порядке.
func TestReorderPartial(t *testing.T) {
	q := newQueue(t)
	a := add(t, q, "а", "a", SourcePoints, false)
	add(t, q, "б", "b", SourcePoints, false)
	c := add(t, q, "в", "c", SourcePoints, false)
	add(t, q, "г", "d", SourcePoints, false)
	if err := q.Reorder([]int64{c.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "a", "b", "d"}
	if got := titles(t, q); !reflect.DeepEqual(got, want) {
		t.Fatalf("порядок %v, ждали %v", got, want)
	}
}
