package tui

import (
	"context"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

// presenceState is what the list knows about a profile's stored password.
type presenceState int

const (
	// presenceChecking means a check is in flight, or about to be.
	presenceChecking presenceState = iota
	presenceSaved
	presenceNotSaved
	// presenceUnavailable means the keyring could not answer. It is never
	// drawn as "asks when connecting": a keyring that did not answer has not
	// said there is no password.
	presenceUnavailable
)

// presenceEntry is one cached answer. seq identifies the check that will
// fill it, so a reply to a check that has since been superseded is dropped.
type presenceEntry struct {
	state presenceState
	seq   int
}

// presenceMsg carries a finished check back to Update.
type presenceMsg struct {
	id  secret.Identity
	seq int
	got secret.Presence
	err error
}

// presenceTimeout bounds a check. A wedged bus then reads as "keyring
// unavailable" rather than "checking…" for as long as the list is open.
const presenceTimeout = 2 * time.Second

func (m Model) identity(p config.Profile) secret.Identity {
	return secret.IdentityFor(m.app.Cfg.Path(), p)
}

// ensurePresence starts a check for the selected profile when nothing is
// known about it yet. It runs after every Update rather than from the view:
// painting the list must never touch the keyring, and a D-Bus round trip per
// frame would stall the UI behind a slow bus.
func (m Model) ensurePresence() (Model, tea.Cmd) {
	if m.session != nil || m.quit || (m.view != viewList && m.view != viewRetry) {
		return m, nil
	}
	if m.app == nil || m.app.Cfg == nil || m.app.Secrets == nil {
		return m, nil
	}
	// The compact layout shows no details, so it has nothing to check for.
	if lo := newLayout(m.width, m.height); lo.Compact || lo.Tiny {
		return m, nil
	}
	p, ok := m.selected()
	if !ok {
		return m, nil
	}
	id := m.identity(p)
	if _, known := m.presence[id]; known {
		return m, nil
	}
	m.presenceSeq++
	seq := m.presenceSeq
	m.setPresence(id, presenceEntry{state: presenceChecking, seq: seq})
	store := m.app.Secrets
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
		defer cancel()
		got, err := store.Presence(ctx, id)
		return presenceMsg{id: id, seq: seq, got: got, err: err}
	}
}

// handlePresence records a finished check, unless the entry it was for has
// been dropped or asked for again since: that reply describes a keyring from
// before a save or delete and may no longer be true.
func (m Model) handlePresence(msg presenceMsg) (tea.Model, tea.Cmd) {
	e, ok := m.presence[msg.id]
	if !ok || e.seq != msg.seq {
		return m, nil
	}
	// The error is checked first: Presence answers NotSaved alongside one.
	state := presenceUnavailable
	switch {
	case msg.err != nil:
	case msg.got == secret.Saved:
		state = presenceSaved
	default:
		state = presenceNotSaved
	}
	m.setPresence(msg.id, presenceEntry{state: state, seq: msg.seq})
	return m, nil
}

// setPresence writes to a copy of the cache. Models are values, and a map
// shared between an old model and a new one would let one change the other.
func (m *Model) setPresence(id secret.Identity, e presenceEntry) {
	next := maps.Clone(m.presence)
	if next == nil {
		next = map[secret.Identity]presenceEntry{}
	}
	next[id] = e
	m.presence = next
}

// forgetPresence drops what is known about ids, after anything that may have
// changed what the keyring holds for them. The next Update checks again.
func (m *Model) forgetPresence(ids ...secret.Identity) {
	if len(m.presence) == 0 {
		return
	}
	next := maps.Clone(m.presence)
	for _, id := range ids {
		delete(next, id)
	}
	m.presence = next
}

// presenceLine is the password detail's text and style for p. It reads only
// the cache.
func (m Model) presenceLine(p config.Profile) (string, lipgloss.Style) {
	e, ok := m.presence[m.identity(p)]
	if !ok {
		e.state = presenceChecking
	}
	switch e.state {
	case presenceSaved:
		return "● saved in keyring", m.styles.success
	case presenceNotSaved:
		return "asks when connecting", m.styles.muted
	case presenceUnavailable:
		return "keyring unavailable", m.styles.muted
	default:
		return "checking…", m.styles.muted
	}
}
