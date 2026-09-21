package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
	"github.com/gaius-codius/wicket/internal/secret"
)

// Every keyring call that can wait on the user -- reading a password to
// connect, and every write or delete -- runs off the update loop, as a
// keyringOp. They once ran inside Update, and a keyring that stopped
// answering froze Wicket for good: Ctrl+C, q, SIGTERM and SIGHUP all arrive
// through the same loop, so nothing short of SIGKILL could end it, and that
// left the terminal in raw mode. Now the loop keeps running, the status line
// says what Wicket is waiting for, and Esc or Ctrl+C stop the wait.
//
// Only one operation runs at a time, and while it does every other key is
// ignored: a second Enter must not start a second lookup, and an edit must
// not change the profile a save is half way through writing.

// keyringWait bounds one keyring operation. It is minutes rather than
// seconds because the wait may be for a person: a desktop unlock prompt, or a
// keyring that holds a search open until its database is unlocked. A short
// deadline would make a locked keyring unusable, so the wait is made
// cancellable instead, and the status line says how to cancel it. The bound
// only guarantees that an operation nobody cancels still ends.
const keyringWait = secret.OpTimeout

// keyringSlowAfter is when the status line stops saying Wicket is checking
// and starts saying it is waiting, with a word about unlock prompts: the
// presence check gives up at the same point.
const keyringSlowAfter = presenceTimeout

// keyringOp is the keyring operation running now.
type keyringOp struct {
	id     int
	cancel context.CancelFunc
	// slow is the status line once the operation has taken a while.
	slow string
	// keys are the footer while it runs.
	keys []keyHint
	// stopped puts the model right once the user stops waiting, with the
	// key they stopped it with. The operation's reply is then ignored.
	stopped func(m Model, key string) (tea.Model, tea.Cmd)
}

// keyringReplyMsg carries a finished operation's result, one of the *Msg
// types handled in handleKeyringReply, back to the loop.
type keyringReplyMsg struct {
	id    int
	reply tea.Msg
}

// keyringSlowMsg fires when operation id has run for keyringSlowAfter.
type keyringSlowMsg struct{ id int }

// credResolvedMsg is the password lookup for a connect.
type credResolvedMsg struct {
	p   config.Profile
	res credResult
}

// secretStoredMsg is the password dialog's ctrl+s store.
type secretStoredMsg struct {
	p   config.Profile
	err error
}

// saveCarriedMsg is a save's first keyring step, before the config moves.
type saveCarriedMsg struct {
	plan savePlan
	c    carry
	err  error
}

// saveFinishedMsg is a save's last keyring step, after the config moved, or
// the undoing of its first step when the config could not be written.
type saveFinishedMsg struct {
	plan  savePlan
	c     carry
	warns []string
	// failed is the config write's error when this is an undo.
	failed error
}

// carryUndoneMsg is the removal of a copy made by a save the user stopped
// waiting for before the config moved.
type carryUndoneMsg struct {
	plan  savePlan
	warns []string
}

// deleteFinishedMsg is the removal of a deleted profile's password.
type deleteFinishedMsg struct {
	name  string
	id    secret.Identity
	warns []string
}

var stopWaitingKeys = []keyHint{{"esc", "stop waiting", intentNormal}}

// runKeyring starts op off the loop, saying what on the status line.
func (m Model) runKeyring(what, slow string, keys []keyHint, stopped func(Model, string) (tea.Model, tea.Cmd), op func(context.Context) tea.Msg) (tea.Model, tea.Cmd) {
	m.keyringSeq++
	id := m.keyringSeq
	ctx, cancel := m.app.keyringContext(keyringWait)
	m.keyring = &keyringOp{id: id, cancel: cancel, slow: slow, keys: keys, stopped: stopped}
	m.setStatus(what, statusInfo)
	run := func() tea.Msg {
		defer cancel()
		return keyringReplyMsg{id: id, reply: op(ctx)}
	}
	return m, tea.Batch(run, tea.Tick(keyringSlowAfter, func(time.Time) tea.Msg { return keyringSlowMsg{id: id} }))
}

// keyringHints is the footer while an operation runs.
func (m Model) keyringHints() []keyHint {
	return m.keyring.keys
}

func (m Model) handleKeyringSlow(msg keyringSlowMsg) (tea.Model, tea.Cmd) {
	if m.keyring != nil && m.keyring.id == msg.id {
		m.setStatus(m.keyring.slow, statusInfo)
	}
	return m, nil
}

// handleKeyringKey takes the keys while an operation runs. Esc and Ctrl+C
// stop waiting; nothing else does anything.
func (m Model) handleKeyringKey(key string) (tea.Model, tea.Cmd) {
	if key != "esc" && key != "ctrl+c" {
		return m, nil
	}
	return m.stopKeyring(key)
}

// stopKeyring cancels the running operation and forgets it, so its reply,
// when it comes, is ignored.
func (m Model) stopKeyring(key string) (tea.Model, tea.Cmd) {
	op := m.keyring
	op.cancel()
	m.keyring = nil
	m.setStatus("", statusInfo)
	return op.stopped(m, key)
}

// handleKeyringReply takes a finished operation's result. A reply to an
// operation the user stopped waiting for is dropped: the model has moved on,
// and acting on it now would, say, start a connection nobody is waiting for.
func (m Model) handleKeyringReply(msg keyringReplyMsg) (tea.Model, tea.Cmd) {
	if m.keyring == nil || m.keyring.id != msg.id {
		if r, ok := msg.reply.(credResolvedMsg); ok {
			if pw, ok := r.res.Cred.(secret.Password); ok {
				pw.Clear()
			}
		}
		return m, nil
	}
	m.keyring = nil
	m.setStatus("", statusInfo)
	switch r := msg.reply.(type) {
	case credResolvedMsg:
		return m.handleCredResolved(r)
	case secretStoredMsg:
		return m.handleSecretStored(r)
	case saveCarriedMsg:
		return m.handleSaveCarried(r)
	case saveFinishedMsg:
		return m.handleSaveFinished(r)
	case carryUndoneMsg:
		return m.handleCarryUndone(r)
	case deleteFinishedMsg:
		return m.handleDeleteFinished(r)
	}
	return m, nil
}

// --- connect ---

// resolveCredential looks up p's password off the loop, then connects or
// asks for one. Esc stops waiting and asks for the password instead, which
// is the way past a keyring that has stopped answering; Ctrl+C goes back to
// the list.
func (m Model) resolveCredential(p config.Profile) (tea.Model, tea.Cmd) {
	app := m.app
	name := truncate(p.Name, statusNameWidth)
	return m.runKeyring(
		"Checking the keyring for "+name+"…",
		"Waiting for the keyring… answer its unlock prompt if one is showing.",
		[]keyHint{{"esc", "type the password", intentNormal}, {"ctrl+c", "cancel", intentNormal}},
		func(m Model, key string) (tea.Model, tea.Cmd) {
			if key == "esc" {
				return m.openModal(p, context.Canceled, false)
			}
			m.view = viewList
			m.clearUseOnce()
			m.setStatus("Stopped waiting for the keyring.", statusInfo)
			return m, nil
		},
		func(ctx context.Context) tea.Msg {
			return credResolvedMsg{p: p, res: app.ResolveCredential(ctx, p, nil)}
		})
}

func (m Model) handleCredResolved(r credResolvedMsg) (tea.Model, tea.Cmd) {
	if r.res.NeedModal {
		return m.openModal(r.p, r.res.Err, false)
	}
	extra := ""
	if r.res.Multiple {
		extra = "multiple matching secrets; using the most recently modified"
	}
	return m.runConnect(r.p, r.res.Cred, false, extra)
}

// --- the password dialog's ctrl+s ---

// storeAndConnect saves the typed password, then connects with it whatever
// the keyring said. The dialog stays up, holding the password, until the
// keyring has answered; stopping the wait goes back to it, so Enter still
// connects without saving.
func (m Model) storeAndConnect(p config.Profile, pw secret.Password) (tea.Model, tea.Cmd) {
	app := m.app
	return m.runKeyring(
		"Saving the password for "+truncate(p.Name, statusNameWidth)+"…",
		"Waiting for the keyring… answer its unlock prompt if one is showing.",
		stopWaitingKeys,
		func(m Model, _ string) (tea.Model, tea.Cmd) {
			// The write may already have landed, or land yet: a keyring
			// can finish a request its caller gave up on. "Connects without
			// saving" said otherwise about a password that was stored.
			m.forgetPresence(m.identity(p))
			m.modal.err = "stopped waiting; the password may have been saved. enter connects"
			return m, nil
		},
		func(ctx context.Context) tea.Msg {
			return secretStoredMsg{p: p, err: app.StoreSecret(ctx, p, pw)}
		})
}

func (m Model) handleSecretStored(r secretStoredMsg) (tea.Model, tea.Cmd) {
	m.forgetPresence(m.identity(r.p))
	warn := ""
	if r.err != nil {
		warn = "password not saved: " + keyringProblem(r.err)
	}
	return m.modalStart(warn)
}

// --- saving a profile ---

// beginSave saves the form's profile. Validation and the config write run on
// the loop; the keyring steps either side of the write run off it.
func (m Model) beginSave(f formState, intent PasswordIntent) (tea.Model, tea.Cmd) {
	m.form = f
	plan, err := m.app.planSave(f.oldName, f.p, intent)
	if err != nil {
		return m.saveFailed(err)
	}
	if !plan.carries() {
		return m.commitSave(plan, carry{})
	}
	app := m.app
	// done hands the carry's result to the undo, should the user stop
	// waiting: the reply itself is dropped once they have.
	done := make(chan carry, 1)
	return m.runKeyring(
		"Moving the saved password for "+truncate(plan.p.Name, statusNameWidth)+"…",
		"Waiting for the keyring… answer its unlock prompt if one is showing.",
		stopWaitingKeys,
		func(m Model, _ string) (tea.Model, tea.Cmd) {
			// The config has not been touched, so the form is exactly as
			// the user left it.
			m.forgetPresence(plan.oldID, plan.newID)
			return m.undoStoppedCarry(plan, done)
		},
		func(ctx context.Context) tea.Msg {
			c, err := app.carrySecret(ctx, plan)
			done <- c
			return saveCarriedMsg{plan: plan, c: c, err: err}
		})
}

// undoStoppedCarry puts the keyring back after the user stopped waiting for
// a save's carry. The config was never written, but the copy under the new
// identity may have been: it can finish before the stop, with its reply
// still on its way, or land in the keyring after Wicket gave up on it. It
// used to be left there, a second copy of the password under details no
// profile had.
//
// The undo is a keyring operation of its own, so nothing else runs until it
// is done: a save started now could copy to the same identity, and an undo
// still running would then delete a password the config had come to need.
// It waits for the carry to end before it looks, and deletes only what the
// carry may have written.
func (m Model) undoStoppedCarry(plan savePlan, done <-chan carry) (tea.Model, tea.Cmd) {
	var got *carry
	select {
	case c := <-done:
		got = &c
	default:
	}
	if got != nil && !got.attempted {
		// Nothing was written; there is nothing to put back.
		m.setStatus("Not saved: stopped waiting for the keyring.", statusError)
		return m, nil
	}
	app := m.app
	return m.runKeyring("Not saved; putting the keyring back…",
		"Not saved; waiting for the keyring to put the password back…",
		stopWaitingKeys,
		func(m Model, _ string) (tea.Model, tea.Cmd) {
			m.forgetPresence(plan.newID)
			m.setStatus("Not saved: stopped waiting for the keyring; a copy of its password may be left in the keyring.", statusError)
			return m, nil
		},
		func(ctx context.Context) tea.Msg {
			c := got
			if c == nil {
				select {
				case r := <-done:
					c = &r
				case <-ctx.Done():
					return carryUndoneMsg{plan: plan, warns: []string{"a copy of its password may be left in the keyring"}}
				}
			}
			if !c.attempted {
				return carryUndoneMsg{plan: plan}
			}
			return carryUndoneMsg{plan: plan, warns: app.undoCarry(ctx, plan)}
		})
}

func (m Model) handleCarryUndone(r carryUndoneMsg) (tea.Model, tea.Cmd) {
	m.forgetPresence(r.plan.oldID, r.plan.newID)
	msg := "Not saved: stopped waiting for the keyring."
	if len(r.warns) > 0 {
		msg = "Not saved: stopped waiting for the keyring; " + strings.Join(r.warns, "; ") + "."
	}
	m.setStatus(msg, statusError)
	return m, nil
}

func (m Model) handleSaveCarried(r saveCarriedMsg) (tea.Model, tea.Cmd) {
	m.forgetPresence(r.plan.oldID, r.plan.newID)
	if r.err != nil {
		return m.saveFailed(carryFailed(r.err))
	}
	return m.commitSave(r.plan, r.c)
}

// commitSave writes the config, then hands the rest of the keyring work off
// the loop.
func (m Model) commitSave(plan savePlan, c carry) (tea.Model, tea.Cmd) {
	warns, err := m.app.commitSave(plan)
	// A save can move, replace or delete a password, and a rename or a new
	// host, user or domain is a new identity, so what the list knew about
	// either is checked again once the keyring work is done.
	m.forgetPresence(plan.oldID, plan.newID)
	m.refreshUsed()
	app := m.app
	if err != nil {
		if !c.copied {
			return m.saveFailed(err)
		}
		// The password was copied for a config that was never written;
		// the copy goes, and the original was never touched.
		return m.runKeyring("Not saved; putting the keyring back…",
			"Not saved; waiting for the keyring to put the password back…",
			stopWaitingKeys,
			func(m Model, _ string) (tea.Model, tea.Cmd) {
				m, cmd := m.saveFailed(err)
				m.setStatus("a copy of its password may be left in the keyring", statusError)
				return m, cmd
			},
			func(ctx context.Context) tea.Msg {
				return saveFinishedMsg{plan: plan, c: c, warns: app.undoCarry(ctx, plan), failed: err}
			})
	}
	if !plan.finishes() {
		return m.saveDone(plan, c, warns)
	}
	what := "Saved " + truncate(plan.p.Name, statusNameWidth) + "; updating the keyring…"
	return m.runKeyring(what,
		"Saved; waiting for the keyring… answer its unlock prompt if one is showing.",
		stopWaitingKeys,
		func(m Model, _ string) (tea.Model, tea.Cmd) {
			return m.saveDone(plan, c, append(warns, unfinishedSave(plan)))
		},
		func(ctx context.Context) tea.Msg {
			return saveFinishedMsg{plan: plan, c: c, warns: append(warns, app.finishSave(ctx, plan, c)...)}
		})
}

// unfinishedSave is what a save the user stopped waiting for may have left
// undone in the keyring.
func unfinishedSave(plan savePlan) string {
	switch {
	case plan.intent.set():
		return "stopped waiting for the keyring; the new password may not be saved"
	case plan.intent.forget():
		return "stopped waiting for the keyring; its saved password may be left in place"
	default:
		return "stopped waiting for the keyring; a copy of its password may be left under its old details"
	}
}

func (m Model) handleSaveFinished(r saveFinishedMsg) (tea.Model, tea.Cmd) {
	m.forgetPresence(r.plan.oldID, r.plan.newID)
	if r.failed != nil {
		nm, cmd := m.saveFailed(r.failed)
		if len(r.warns) > 0 {
			nm.setStatus(strings.Join(r.warns, "; "), statusError)
		}
		return nm, cmd
	}
	return m.saveDone(r.plan, r.c, r.warns)
}

// saveFailed leaves the form up with the error under the field at fault.
func (m Model) saveFailed(err error) (Model, tea.Cmd) {
	f := m.form
	f.setError(err)
	// The invalid field may be scrolled out of a short panel, so focus goes
	// to it: the viewport follows focus, and the fix is typed there anyway.
	if f.errField != fieldNone {
		f.focus(f.errField)
	}
	m.form = f
	return m, nil
}

// saveDone closes the form and says what the save did.
func (m Model) saveDone(plan savePlan, c carry, warns []string) (tea.Model, tea.Cmd) {
	name := plan.p.Name
	m.form.password = ""
	m.form = formState{}
	m.view = viewList
	m.clearFilter()
	m.selectName(name)
	// Every warning is something that did not happen, so a save with any of
	// them is not reported as a success.
	msg, kind := outcome("Saved", name, warns)
	if kind == statusSuccess {
		switch {
		case plan.intent.forget():
			msg = "Saved " + truncate(name, statusNameWidth) + " and forgot its password."
		case c.copied && plan.accountChanged:
			// Worth saying: the password now goes to a different account
			// or host than it was saved for.
			msg = "Saved " + truncate(name, statusNameWidth) + "; its saved password moved with it."
		}
	}
	m.setStatus(msg, kind)
	return m, nil
}

// --- deleting a profile ---

// finishDelete removes a deleted profile's password off the loop. The
// profile itself is already gone from the config.
func (m Model) finishDelete(name string, id secret.Identity, warns []string) (tea.Model, tea.Cmd) {
	app := m.app
	return m.runKeyring(
		"Deleted "+truncate(name, statusNameWidth)+"; removing its saved password…",
		"Deleted; waiting for the keyring… answer its unlock prompt if one is showing.",
		stopWaitingKeys,
		func(m Model, _ string) (tea.Model, tea.Cmd) {
			m.forgetPresence(id)
			m.setStatus(outcome("Deleted", name, append(warns, "stopped waiting for the keyring; any saved password was left in place")))
			return m, nil
		},
		func(ctx context.Context) tea.Msg {
			return deleteFinishedMsg{name: name, id: id, warns: append(warns, app.forgetSecret(ctx, id)...)}
		})
}

func (m Model) handleDeleteFinished(r deleteFinishedMsg) (tea.Model, tea.Cmd) {
	m.forgetPresence(r.id)
	m.setStatus(outcome("Deleted", r.name, r.warns))
	return m, nil
}
