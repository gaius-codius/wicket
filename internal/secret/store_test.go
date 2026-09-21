package secret

import (
	"context"
	"errors"
	"testing"
	"time"
)

var bg = context.Background()

func TestMemoryStore_CRUD(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	id := Identity{Service: "wicket", Config: "/a", Profile: "work", Host: "h", User: "u"}
	if _, err := m.Lookup(bg, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup: %v", err)
	}
	pw, _ := NewPassword("s3cret")
	if err := m.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}
	got, err := m.Lookup(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Multiple {
		t.Fatal("multiple")
	}
	other := id
	other.Config = "/b"
	if _, err := m.Lookup(bg, other); !errors.Is(err, ErrNotFound) {
		t.Fatal("isolation")
	}
	if err := m.Delete(bg, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(bg, id); !errors.Is(err, ErrNotFound) {
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
	_ = m.Upsert(bg, id, pw1)
	m.items = append(m.items, memItem{id: id, secret: pw2, modTime: time.Unix(99, 0)})
	got, err := m.Lookup(bg, id)
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

func TestMemoryStore_Presence(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	ctx := context.Background()
	id := Identity{Service: "wicket", Config: "/a", Profile: "work", Host: "h", User: "u"}
	if got, err := m.Presence(ctx, id); err != nil || got != NotSaved {
		t.Fatalf("empty store: %v, %v", got, err)
	}
	pw, _ := NewPassword("s3cret")
	if err := m.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Presence(ctx, id); err != nil || got != Saved {
		t.Fatalf("after upsert: %v, %v", got, err)
	}
	// The whole identity is the key: a changed host is a different entry.
	other := id
	other.Host = "h2"
	if got, err := m.Presence(ctx, other); err != nil || got != NotSaved {
		t.Fatalf("other host: %v, %v", got, err)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Presence(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled: %v, want ErrUnavailable", err)
	}
}

// A caller that has given up gets an error that reads as "unavailable" from
// every method, never a silent success or "not found".
func TestMemoryStore_HonoursTheContext(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	id := Identity{Service: "wicket", Config: "/a", Profile: "work", Host: "h", User: "u"}
	pw, _ := NewPassword("s3cret")
	if err := m.Upsert(bg, id, pw); err != nil {
		t.Fatal(err)
	}
	done, cancel := context.WithCancel(bg)
	cancel()
	if _, err := m.Lookup(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Lookup: %v, want ErrUnavailable", err)
	}
	if err := m.Upsert(done, id, pw); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Upsert: %v, want ErrUnavailable", err)
	}
	if err := m.Delete(done, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete: %v, want ErrUnavailable", err)
	}
	if _, err := m.Lookup(bg, id); err != nil {
		t.Fatalf("a cancelled Delete removed the item: %v", err)
	}
}
