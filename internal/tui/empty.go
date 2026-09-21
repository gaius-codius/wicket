package tui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/wicket/internal/theme"
)

// emptyItem is one piece of the empty state: its lines, the rank at which it
// is given up when the panel is short (lower survives longer), and the group
// it is drawn in. Groups are separated by a blank line when there is room.
type emptyItem struct {
	lines       []string
	rank, group int
}

// viewEmpty is the first-run screen. It is drawn in reading order but shed by
// rank, so a short terminal keeps the heading and the key that gets the user
// started, and gives up the path, the theme and the shell hint first. A plain
// clip from the bottom kept the explanation and lost the key.
func (m Model) viewEmpty(lo layout) string {
	st := m.styles
	wrap := lipgloss.NewStyle().Width(lo.Inner)
	items := []emptyItem{
		{rank: 1, group: 0, lines: []string{st.primary.Bold(true).Render(truncate("No saved connections yet.", lo.Inner))}},
		{rank: 3, group: 0, lines: strings.Split(st.muted.Render(wrap.Render("Save a FreeRDP profile once, then open it with one key.")), "\n")},
		{rank: 2, group: 1, lines: []string{st.accent.Bold(true).Render("n") + "  " +
			st.primary.Render(truncate("add your first connection", max(lo.Inner-3, 1)))}},
	}
	labelW := len("config")
	detail := func(rank int, label, value string) {
		items = append(items, emptyItem{rank: rank, group: 2, lines: []string{
			st.muted.Render(padRight(label, labelW)) + "  " + st.primary.Render(value)}})
	}
	valueW := max(lo.Inner-labelW-2, 1)
	if path := m.configPath(); path != "" {
		detail(4, "config", truncateLeft(sanitize(path), valueW))
	}
	detail(5, "theme", truncate(themeLine(m.theme.Mode, m.look), valueW))
	items = append(items, emptyItem{rank: 6, group: 2, lines: []string{
		st.muted.Render(truncate("from a shell or launcher: ", lo.Inner)) +
			st.primary.Render(truncate("wicket connect <profile>", max(lo.Inner-26, 0)))}})
	return strings.Join(layoutEmpty(items, lo.Budget), "\n")
}

// layoutEmpty keeps the items that fit in budget, best rank first, each whole
// or not at all, and spends what is left on the gaps between groups.
func layoutEmpty(items []emptyItem, budget int) []string {
	keep := make([]bool, len(items))
	used, worst := 0, 0
	for _, it := range items {
		worst = max(worst, it.rank)
	}
	for rank := 1; rank <= worst; rank++ {
		for i, it := range items {
			if it.rank == rank && used+len(it.lines) <= budget {
				keep[i] = true
				used += len(it.lines)
			}
		}
	}
	var out []string
	group := -1
	for i, it := range items {
		if !keep[i] {
			continue
		}
		if group >= 0 && it.group != group && used < budget {
			out = append(out, "")
			used++
		}
		group = it.group
		out = append(out, it.lines...)
	}
	return out
}

func (m Model) configPath() string {
	if m.app != nil && m.app.Cfg != nil {
		return m.app.Cfg.Path()
	}
	return m.loadPath
}

// themeLine names the look being drawn and where it came from, so a user who
// sees unexpected colours knows which setting to change.
func themeLine(mode theme.Mode, look theme.Look) string {
	switch look.Kind {
	case theme.KindOmarchy:
		return "omarchy"
	case theme.KindWicket:
		shade := "light"
		if look.Dark {
			shade = "dark"
		}
		if mode == theme.ModeWicketDark || mode == theme.ModeWicketLight {
			return "wicket · " + shade
		}
		return "wicket · " + shade + ", from your terminal"
	default:
		return "terminal colours"
	}
}

// sanitize drops control characters, so a path read from the environment
// cannot move the cursor or recolour the panel.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// truncateLeft shortens plain text to width cells from the left, keeping the
// end: the file name is the part of a path worth seeing.
func truncateLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[1:]
	}
	return "…" + string(r)
}
