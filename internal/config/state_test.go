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

// The list renders and sorts every row from one Snapshot, so it has to agree
// with LastUsed entry for entry: the same garbage is dropped, and a missing or
// unreadable file is an empty map, not a nil one a caller would have to guard.
func TestState_SnapshotMatchesLastUsed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing, err := OpenState(filepath.Join(dir, "missing.toml"), sysClock{})
	if err != nil {
		t.Fatal(err)
	}
	if m := missing.Snapshot(); m == nil || len(m) != 0 {
		t.Fatalf("missing file: %v", m)
	}

	garbled := filepath.Join(dir, "garbled.toml")
	if err := os.WriteFile(garbled, []byte("this is not toml {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err := OpenState(garbled, sysClock{})
	if err != nil {
		t.Fatal(err)
	}
	if m := bad.Snapshot(); m == nil || len(m) != 0 {
		t.Fatalf("unparseable file: %v", m)
	}

	path := filepath.Join(dir, "state.toml")
	body := "[last_used]\n" +
		"work = \"2026-09-18T14:00:00Z\"\n" +
		"home = 2026-09-17T09:30:00Z\n" +
		"lab = \"not-a-time\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	st, err := OpenState(path, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot %v, want work and home only", snap)
	}
	for _, name := range []string{"work", "home", "lab"} {
		want, wantOK := st.LastUsed(name)
		got, ok := snap[name]
		if ok != wantOK || !got.Equal(want) {
			t.Fatalf("%s: snapshot %v,%v; LastUsed %v,%v", name, got, ok, want, wantOK)
		}
	}

	// The map is the caller's: changing it must not leak into later reads,
	// and a later write must show up in the next snapshot.
	delete(snap, "work")
	if err := st.Record("lab"); err != nil {
		t.Fatal(err)
	}
	again := st.Snapshot()
	if _, ok := again["work"]; !ok {
		t.Fatal("a caller's edit to one snapshot reached the next")
	}
	if got := again["lab"]; !got.Equal(now) {
		t.Fatalf("lab = %v after Record, want %v", got, now)
	}
}

// The list polls Stamp to notice a connect made from another shell, so the
// stamp has to move on every write and stay put when nothing was written.
func TestState_StampMovesOnlyWhenTheFileChanges(t *testing.T) {
	t.Parallel()
	st, err := OpenState(filepath.Join(t.TempDir(), "state.toml"), sysClock{})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Stamp(); got != (StateStamp{}) {
		t.Fatalf("missing file stamp %+v, want zero", got)
	}
	if err := st.Record("work"); err != nil {
		t.Fatal(err)
	}
	first := st.Stamp()
	if first == (StateStamp{}) {
		t.Fatal("stamp did not move after the first write")
	}
	if again := st.Stamp(); again != first {
		t.Fatalf("stamp moved without a write: %+v then %+v", first, again)
	}
	if err := st.Record("a-much-longer-profile-name"); err != nil {
		t.Fatal(err)
	}
	if st.Stamp() == first {
		t.Fatal("stamp did not move after a second write")
	}
}
