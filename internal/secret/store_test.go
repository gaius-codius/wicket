package secret

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_CRUD(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	id := Identity{Service: "wicket", Config: "/a", Profile: "work", Host: "h", User: "u"}
	if _, err := m.Lookup(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup: %v", err)
	}
	pw, _ := NewPassword("s3cret")
	if err := m.Upsert(id, pw); err != nil {
		t.Fatal(err)
	}
	got, err := m.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Multiple {
		t.Fatal("multiple")
	}
	other := id
	other.Config = "/b"
	if _, err := m.Lookup(other); !errors.Is(err, ErrNotFound) {
		t.Fatal("isolation")
	}
	if err := m.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(id); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted")
	}
}

func TestMemoryStore_MostRecentWins(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	n := 0
	m.clock = func() time.Time {
		n++
		return time.Unix(int64(n), 0)
	}
	id := Identity{Service: "wicket", Config: "/a", Profile: "work", Host: "h", User: "u"}
	pw1, _ := NewPassword("one")
	pw2, _ := NewPassword("two")
	_ = m.Upsert(id, pw1)
	m.items = append(m.items, memItem{id: id, secret: pw2, modTime: time.Unix(99, 0)})
	got, err := m.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Multiple {
		t.Fatal("want multiple warning")
	}
	var buf []byte
	_ = got.Password.WriteLine(writerFunc(func(p []byte) (int, error) {
		buf = append(buf, p...)
		return len(p), nil
	}))
	if string(buf) != "two\n" {
		t.Fatalf("got %q", buf)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
