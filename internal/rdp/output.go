package rdp

import "sync"

// OutputLimit is how much of a client's output a Tail keeps by default.
// FreeRDP logs freely; the end of the log is where the reason a session
// failed is written, and all that is ever shown of it.
const OutputLimit = 64 << 10

// Tail keeps the last bytes written to it, up to a limit. It stands in for
// the terminal as the client's stdout and stderr when something else is
// drawing there, so the client can log as much as it likes without painting
// over the screen or growing without bound. It is safe for concurrent use:
// exec copies stdout and stderr from separate goroutines.
type Tail struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

// NewTail returns a Tail that keeps the last limit bytes, or OutputLimit
// when limit is not positive.
func NewTail(limit int) *Tail {
	if limit <= 0 {
		limit = OutputLimit
	}
	return &Tail{limit: limit}
}

func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return n, nil
	}
	if over := len(t.buf) + len(p) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

// Bytes returns a copy of what the Tail holds. The bytes are raw client
// output: anything drawn from them must have its control characters removed
// first.
func (t *Tail) Bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}
