package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestState_RecordAndLastUsed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.FixedZone("AEST", 10*3600))
	path := filepath.Join(t.TempDir(), "nested", "state.toml")
	st, err := OpenState(path, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("work"); ok {
		t.Fatal("absent")
	}
	if err := st.Record("work"); err != nil {
		t.Fatal(err)
	}
	got, ok := st.LastUsed("work")
	if !ok {
		t.Fatal("missing after record")
	}
	if !got.Equal(now) && got.Format(time.RFC3339) != now.Format(time.RFC3339) {
		t.Fatalf("got %v want %v", got, now)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %04o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %04o", dirInfo.Mode().Perm())
	}
}

func TestState_GarbageTimestampAbsent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.toml")
	body := "[last_used]\nwork = \"not-a-time\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenState(path, sysClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("work"); ok {
		t.Fatal("garbage should be absent")
	}
}

func TestState_UnparseableWriteFails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.toml")
	orig := []byte("this is not toml {{{")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenState(path, sysClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("work"); ok {
		t.Fatal("unparseable read should be empty")
	}
	if err := st.Record("work"); err == nil {
		t.Fatal("write should fail")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(orig) {
		t.Fatalf("clobbered:\n%s", got)
	}
}

func TestState_RenameForget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "state.toml")
	st, err := OpenState(path, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Record("work"); err != nil {
		t.Fatal(err)
	}
	if err := st.Rename("work", "office"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("work"); ok {
		t.Fatal("old key")
	}
	if _, ok := st.LastUsed("office"); !ok {
		t.Fatal("new key")
	}
	if err := st.Forget("office"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.LastUsed("office"); ok {
		t.Fatal("forgotten")
	}
}

func TestState_LockMerge(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.toml")
	a, err := OpenState(path, fixedClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenState(path, fixedClock{time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := a.Record("one"); err != nil {
			t.Errorf("a: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := b.Record("two"); err != nil {
			t.Errorf("b: %v", err)
		}
	}()
	wg.Wait()
	if _, ok := a.LastUsed("one"); !ok {
		t.Fatal("missing one")
	}
	if _, ok := a.LastUsed("two"); !ok {
		t.Fatal("missing two")
	}
}
