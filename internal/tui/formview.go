package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/rdp"
)

// formBlock is a run of form lines that the viewport keeps or drops whole: a
// row together with any error under it, a section heading, or the gap
// between two sections.
type formBlock struct {
	lines []string
	row   bool
	gap   bool
}

// formValueMin is the narrowest value column the labels leave: room for a
// short hostname or "● 100%", the scale's six-cell marked form.
const formValueMin = 10

// formLabelMin is the narrowest the label column gets, so a label still says
// which field it is.
const formLabelMin = 8

// formColumns sizes the label and value columns for a panel inner cells
// wide. The label column is sized to the panel, not the other way round: a
// narrow terminal should truncate labels rather than render rows wider than
// the frame.
func formColumns(inner int) (labelW, valueW int) {
	longest := 0
	for _, l := range formLabels {
		longest = max(longest, lipgloss.Width(l)+1)
	}
	labelW = min(longest, max(inner-2-2-formValueMin, formLabelMin), max(inner-2-2-1, 1))
	valueW = max(inner-2-labelW-2, 1)
	return labelW, valueW
}

// scaleChoices is the width the three scale choices need side by side;
// below it the row shows only the current one. Each choice is six cells
// ("● 100%" or "○ 140%") and a space separates them.
const scaleChoices = 3*6 + 2

// viewForm draws the form in lo.Budget lines.
//
// The form is taller than a short panel, so it scrolls: a window of blocks
// is kept around the focused row, and whatever it leaves out is counted by a
// cue above or below. Lines are handed out in order of what the user can
// least do without -- the focused row, its error, the scroll cues, the help
// line, the focused section's heading -- and the window gets the rest. The
// old one-line-per-field window clipped from the bottom, so an error could
// cost the panel the very row it was about.
func (m Model) viewForm(lo layout) string {
	f := &m.form
	inner := lo.Inner
	labelW, valueW := formColumns(inner)
	blocks, focusAt, headAt := m.formBlocks(inner, labelW, valueW)
	room := max(lo.Budget, 1)

	// A pending "discard?" is the question the keys now answer, so it
	// outranks even the focused row.
	var tail []string
	if f.confirmDiscard {
		tail = []string{m.styles.warning.Render("▲ ") +
			m.styles.primary.Render(truncate("Discard unsaved changes?", max(inner-2, 1)))}
		room--
		if room < 1 {
			return strings.Join(tail, "\n")
		}
	}

	// The focused row, then its error, squeezed onto one line if the
	// wrapped message does not fit.
	focused := blocks[focusAt]
	room--
	if errLines := focused.lines[1:]; len(errLines) > room {
		focused.lines = append([]string{focused.lines[0]}, m.formErrorLine(f.err, inner, errIndent(labelW, valueW), room)...)
	}
	room -= len(focused.lines) - 1
	blocks[focusAt] = focused

	// An error that belongs to no row goes below the form.
	if f.err != "" && f.errField == fieldNone && !f.confirmDiscard {
		wrapped := m.wrapError(f.err, inner, 0)
		if len(wrapped) > room {
			wrapped = m.formErrorLine(f.err, inner, 0, room)
		}
		tail = append(tail, wrapped...)
		room -= len(wrapped)
	}

	help := ""
	if !f.confirmDiscard {
		help = m.formHelpLine(f.field, inner)
	}

	var (
		cues, above, below int
		s, e               int
		showHelp, sticky   bool
	)
	// Each pass may hide more rows, which can call for another cue line, so
	// repeat until the cues cover what is hidden or there is no room left.
	for {
		r := room - cues
		showHelp = help != "" && r >= 1
		if showHelp {
			r--
		}
		headRes := 0
		if r >= 1 {
			headRes = 1
		}
		s, e = growForm(blocks, focusAt, r-headRes)
		if headRes == 1 && headAt >= s {
			// The heading is in the window after all, so its line goes back.
			s, e = growForm(blocks, focusAt, r)
		}
		sticky = headRes == 1 && headAt < s
		above, below = countRows(blocks[:s]), countRows(blocks[e:])
		need := btoi(above > 0) + btoi(below > 0)
		if need <= cues || cues >= room {
			break
		}
		cues = min(need, room)
	}

	var top, bottom []string
	switch {
	case cues == 1 && above > 0 && below > 0:
		bottom = append(bottom, m.cue("▲ "+strconv.Itoa(above)+" above · ▼ "+strconv.Itoa(below)+" below", inner))
	default:
		if cues > 0 && above > 0 {
			top = append(top, m.cue("▲ "+strconv.Itoa(above)+" more above", inner))
		}
		if cues > 0 && below > 0 && (above == 0 || cues > 1) {
			bottom = append(bottom, m.cue("▼ "+strconv.Itoa(below)+" more below", inner))
		}
	}
	if sticky {
		top = append(top, blocks[headAt].lines...)
	}

	out := top
	for _, b := range blocks[s:e] {
		out = append(out, b.lines...)
	}
	out = append(out, bottom...)
	// Gaps are the last thing to get a line: only a panel with room to
	// spare separates the help and the tail from the rows.
	spare := max(lo.Budget, 1) - len(out) - btoi(showHelp) - len(tail)
	if showHelp {
		if spare > 1 || (spare == 1 && len(tail) == 0) {
			out = append(out, "")
			spare--
		}
		out = append(out, help)
	}
	if len(tail) > 0 && spare > 0 {
		out = append(out, "")
	}
	return strings.Join(append(out, tail...), "\n")
}

// growForm widens a window of blocks around the one at focusAt, a block at
// a time on alternate sides, while room lines remain. A gap left at either
// edge is dropped, since it separates nothing.
func growForm(blocks []formBlock, focusAt, room int) (s, e int) {
	s, e = focusAt, focusAt+1
	for {
		grew := false
		if s > 0 && len(blocks[s-1].lines) <= room {
			s--
			room -= len(blocks[s].lines)
			grew = true
		}
		if e < len(blocks) && len(blocks[e].lines) <= room {
			room -= len(blocks[e].lines)
			e++
			grew = true
		}
		if !grew {
			break
		}
	}
	for s < focusAt && blocks[s].gap {
		s++
	}
	for e-1 > focusAt && blocks[e-1].gap {
		e--
	}
	return s, e
}

func countRows(blocks []formBlock) int {
	n := 0
	for _, b := range blocks {
		if b.row {
			n++
		}
	}
	return n
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// formBlocks lays the form out in full. focusAt is the focused row's block
// and headAt its section heading's.
//
// It and the helpers below take a pointer: the model holds a text input per
// field, and copying it for every row made a tall sweep of sizes slow.
func (m *Model) formBlocks(inner, labelW, valueW int) (blocks []formBlock, focusAt, headAt int) {
	f := &m.form
	for i, sec := range f.sections() {
		if i > 0 {
			blocks = append(blocks, formBlock{lines: []string{""}, gap: true})
		}
		head := len(blocks)
		blocks = append(blocks, formBlock{lines: []string{m.sectionHeading(sec.title, inner)}})
		for _, id := range sec.fields {
			lines := []string{m.formRow(id, labelW, valueW)}
			if f.err != "" && f.errField == id {
				lines = append(lines, m.wrapError(f.err, inner, errIndent(labelW, valueW))...)
			}
			if id == f.field {
				focusAt, headAt = len(blocks), head
			}
			blocks = append(blocks, formBlock{lines: lines, row: true})
		}
	}
	return blocks, focusAt, headAt
}

// sectionHeading is a section's title and a rule to the edge of the panel.
func (m Model) sectionHeading(title string, width int) string {
	t := truncate(title, width)
	out := m.styles.muted.Bold(true).Render(t)
	if rule := width - lipgloss.Width(t) - 1; rule > 0 {
		out += " " + m.styles.divider.Render(strings.Repeat("─", rule))
	}
	return out
}

func (m *Model) formRow(id, labelW, valueW int) string {
	f := &m.form
	mark := "  "
	labelStyle := m.styles.muted
	if f.field == id {
		mark = m.styles.accent.Render("▌ ")
		labelStyle = m.styles.accent
	}
	if f.err != "" && f.errField == id {
		labelStyle = m.styles.danger
	}
	label := labelStyle.Render(padRight(truncate(formLabels[id]+":", labelW), labelW))
	return mark + label + "  " + m.formValue(id, valueW)
}

// formValue draws field id's value in width cells.
func (m *Model) formValue(id, width int) string {
	f := &m.form
	switch id {
	case fieldFullscreen:
		return m.onOff(f.p.Fullscreen, width)
	case fieldMultimon:
		return m.onOff(f.p.Multimon, width)
	case fieldDynamic:
		return m.onOff(f.p.DynamicResolution, width)
	case fieldClipboard:
		return m.onOff(f.p.Clipboard, width)
	case fieldShareHome:
		return m.onOff(f.p.ShareHome, width)
	case fieldForget:
		return m.onOff(f.forget, width)
	case fieldScale:
		return m.scaleValue(f.p.Scale, width)
	case fieldClient:
		return m.clientValue(width)
	case fieldName:
		if f.nameSelected && f.field == fieldName && f.p.Name != "" {
			return m.styles.onSelection(m.styles.primary).Render(truncate(f.p.Name, width))
		}
	}
	return inputView(f.inputs[id], width)
}

// customChoice is the client row's last choice, which makes it a text input.
const customChoice = "custom…"

// notFound marks a configured client that PATH did not have when the form
// opened.
const notFound = "not found"

// clientValue draws the client row: the choices with the current one marked
// with ●/○ like on/off and scale, or only the current one when they do not
// fit; or, on "custom…", the text input. A configured client PATH did not
// have is marked, from the search made as the form opened.
func (m *Model) clientValue(width int) string {
	f := &m.form
	if f.clientCustom() {
		in := f.inputs[fieldClient]
		mark := "  " + notFound
		if f.clientMissing != "" && in.Value() == f.clientMissing &&
			width-lipgloss.Width(mark) > lipgloss.Width(in.Value()) {
			return inputView(in, width-lipgloss.Width(mark)) + m.styles.muted.Render(mark)
		}
		return inputView(in, width)
	}
	choices := append(slices.Clip(f.clients), customChoice)
	full := len(choices) - 1
	for _, c := range choices {
		full += lipgloss.Width(c) + 2
		if c == f.clientMissing {
			full += 1 + lipgloss.Width(notFound)
		}
	}
	if full <= width {
		parts := make([]string, 0, len(choices))
		for i, c := range choices {
			var part string
			if i == f.clientAt {
				part = m.styles.onSelection(m.styles.accent.Bold(true)).Render("● " + c)
			} else {
				part = m.styles.muted.Render("○ " + c)
			}
			if c == f.clientMissing {
				part += " " + m.styles.muted.Render(notFound)
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, " ")
	}
	// Only the current choice, in the tightest form that still marks it,
	// and with its marker while there is room for one.
	cur := choices[f.clientAt]
	forms := []string{"● " + cur, cur}
	if cur == f.clientMissing {
		forms = []string{"● " + cur + " " + notFound, "● " + cur, cur}
	}
	for _, s := range forms {
		if lipgloss.Width(s) <= width {
			if head, ok := strings.CutSuffix(s, " "+notFound); ok {
				return m.styles.accent.Render(head) + " " + m.styles.muted.Render(notFound)
			}
			return m.styles.accent.Render(s)
		}
	}
	return m.styles.accent.Render(truncate(cur, width))
}

// onOff draws a boolean. The words carry the value, so it still reads with
// NO_COLOR, where the dots alone would not.
func (m Model) onOff(v bool, width int) string {
	if v {
		return m.styles.accent.Render(truncate("● on", width))
	}
	return m.styles.muted.Render(truncate("○ off", width))
}

// scaleValue shows the three scales with the current one marked, or only the
// current one when the three do not fit. The ●/○ dots match on/off fields so
// the live value still reads without colour.
func (m Model) scaleValue(cur, width int) string {
	if width < scaleChoices {
		// Tighter forms of the current choice before any cut: a bare "1…" did
		// not say which scale it was.
		pct := strconv.Itoa(cur) + "%"
		for _, s := range []string{"● " + pct, pct} {
			if lipgloss.Width(s) <= width {
				return m.styles.accent.Render(s)
			}
		}
		return m.styles.accent.Render(truncate(pct, width))
	}
	parts := make([]string, 0, 3)
	for _, s := range []int{100, 140, 180} {
		txt := strconv.Itoa(s) + "%"
		if s == cur {
			parts = append(parts, m.styles.onSelection(m.styles.accent.Bold(true)).Render("● "+txt))
		} else {
			parts = append(parts, m.styles.muted.Render("○ "+txt))
		}
	}
	return strings.Join(parts, " ")
}

// errIndent lines an error up under the value it is about, unless the value
// column is too narrow to hold a message.
func errIndent(labelW, valueW int) int {
	if valueW >= 24 {
		return 2 + labelW + 2
	}
	return 2
}

// wrapError renders "✗ msg" wrapped to the panel, each line indented.
func (m Model) wrapError(msg string, inner, indent int) []string {
	w := max(inner-indent, 1)
	wrapped := lipgloss.NewStyle().Width(w).Render("✗ " + msg)
	lines := strings.Split(wrapped, "\n")
	for i, ln := range lines {
		lines[i] = strings.Repeat(" ", indent) + m.styles.danger.Render(strings.TrimRight(ln, " "))
	}
	return lines
}

// formErrorLine is a field's error cut to fit room lines, which is one line
// or none.
func (m Model) formErrorLine(msg string, inner, indent, room int) []string {
	if room < 1 {
		return nil
	}
	return []string{strings.Repeat(" ", indent) + m.styles.danger.Render(truncate("✗ "+msg, max(inner-indent, 1)))}
}

func (m Model) cue(text string, width int) string {
	return m.styles.muted.Render(truncate(text, width))
}

// formHelp says what each field is for. Text between backquotes is a key.
var formHelp = [fieldCount]string{
	fieldName:       "What the list and wicket connect call it.",
	fieldHost:       "IP address or hostname of the machine, with an optional :port.",
	fieldUser:       "The account to sign in as.",
	fieldDomain:     "The Windows domain of the account, if it has one.",
	fieldSize:       "WIDTHxHEIGHT or N%. Empty uses the client default.",
	fieldFullscreen: "Start the session full screen. `space` switches.",
	fieldMultimon:   "Full screen across every monitor. `space` switches.",
	fieldDynamic:    "Resize the remote desktop with the window. `space` switches.",
	fieldScale:      "`←/→` to choose.",
	fieldClipboard:  "Copy and paste between here and there. `space` switches.",
	fieldShareHome:  "All of your home folder, read-write, as a drive there. `space` switches.",
	fieldPassword:   "Saved in the keyring on `ctrl+s`. Empty keeps what is stored.",
	fieldForget:     "Deletes the stored password on save. `space` switches.",
	fieldClient:     "`←/→` to choose.",
}

// clientHelp says what the client row's current choice is.
func (f *formState) clientHelp() string {
	const choose = " `←/→` to choose."
	switch {
	case f.detected == 0:
		return "No FreeRDP client found on PATH. Type a binary name."
	case f.clientCustom():
		return "Any FreeRDP-compatible binary on PATH. `←` from the start goes back."
	case f.clients[f.clientAt] == f.clientMissing:
		return "Not found on PATH; kept until you choose another." + choose
	}
	if about, ok := rdp.AboutClient(f.clients[f.clientAt]); ok {
		return about + "." + choose
	}
	return "The client this profile names, kept as it is." + choose
}

// formHelpLine is the help for field id on one line of width cells.
func (m *Model) formHelpLine(id, width int) string {
	text := formHelp[id]
	if id == fieldPassword && m.form.oldName == "" {
		text = "Saved in the keyring on `ctrl+s`. Empty asks when connecting."
	}
	if id == fieldClient {
		text = m.form.clientHelp()
	}
	var b strings.Builder
	left := width
	for i, seg := range strings.Split(text, "`") {
		if left <= 0 {
			break
		}
		cut := truncate(seg, left)
		style := m.styles.muted
		if i%2 == 1 {
			style = m.styles.accent.Bold(true)
		}
		b.WriteString(style.Render(cut))
		left -= lipgloss.Width(cut)
		if cut != seg {
			break
		}
	}
	return b.String()
}
