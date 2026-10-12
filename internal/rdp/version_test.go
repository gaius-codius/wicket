package rdp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseFreeRDPMajor(t *testing.T) {
	for _, tc := range []struct {
		out   string
		major int
		ok    bool
	}{
		{"This is FreeRDP version 3.31.1 (63b948ca5c)\n", 3, true},
		{"This is FreeRDP version 2.11.7 (2.11.7)\n", 2, true},
		{"This is FreeRDP version 2.10.0 (n/a)\n", 2, true},
		// Log lines before the version do not hide it.
		{"[12:00:00:000] [1:1] [WARN][com.freerdp.core] - something\nThis is FreeRDP version 3.5.0 (dev)\n", 3, true},
		{"", 0, false},
		{"This is FreeRDP version\n", 0, false},
		{"This is FreeRDP version x.1\n", 0, false},
		{"This is FreeRDP version 3\n", 0, false},
		{"This is FreeRDP version 3.\n", 0, false},
		{"This is FreeRDP version 3.garbage\n", 0, false},
		{"xfreerdp: unknown option --version\n", 0, false},
		{"FreeRDP 3.31.1\n", 0, false},
	} {
		major, ok := ParseFreeRDPMajor([]byte(tc.out))
		if major != tc.major || ok != tc.ok {
			t.Errorf("%q: %d %v, want %d %v", tc.out, major, ok, tc.major, tc.ok)
		}
	}
}

// fakeClient writes a script that stands in for a FreeRDP client.
func fakeClient(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xfreerdp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFreeRDPMajor_RunsTheClient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		major int
		ok    bool
	}{
		{"version 3", `[ "$1" = --version ] || exit 9; echo "This is FreeRDP version 3.31.1 (63b948ca5c)"`, 3, true},
		{"version 2", `echo "This is FreeRDP version 2.11.7 (2.11.7)"`, 2, true},
		// A build may report its version and still exit non-zero.
		{"version 3, exit 1", `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"; exit 1`, 3, true},
		{"on stderr", `echo "This is FreeRDP version 3.0.0 (x)" >&2`, 3, true},
		{"malformed", `echo "This is FreeRDP version unknown"`, 0, false},
		{"silent failure", `exit 1`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			major, ok := FreeRDPMajor(fakeClient(t, tc.body))
			if major != tc.major || ok != tc.ok {
				t.Fatalf("%d %v, want %d %v", major, ok, tc.major, tc.ok)
			}
		})
	}
	if _, ok := FreeRDPMajor(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("a missing executable qualified")
	}
}

// A client that has printed a good version but does not exit in time is not
// one to offer.
func TestFreeRDPMajor_Timeout(t *testing.T) {
	path := fakeClient(t, `echo "This is FreeRDP version 3.31.1 (63b948ca5c)"; exec sleep 10`)
	start := time.Now()
	if _, ok := probeMajor(path, 200*time.Millisecond); ok {
		t.Fatal("a client that timed out qualified")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("probe took %v", d)
	}
}

// Output past the cap is dropped, not an error, and what was kept still reads.
func TestFreeRDPMajor_CapsOutput(t *testing.T) {
	path := fakeClient(t, `echo "This is FreeRDP version 3.1.0 (x)"; head -c 1000000 /dev/zero`)
	if major, ok := FreeRDPMajor(path); major != 3 || !ok {
		t.Fatalf("%d %v", major, ok)
	}
}
