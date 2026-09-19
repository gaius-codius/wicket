package tui

func (m Model) viewEmpty(lo layout) string {
	_ = lo
	return m.styles.primary.Render("No saved connections.") + "\n" +
		m.styles.muted.Render("Press ") + m.styles.key.Render("n") + m.styles.muted.Render(" to add one.")
}
