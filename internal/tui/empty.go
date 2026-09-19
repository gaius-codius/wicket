package tui

func (m Model) viewEmpty(lo layout) string {
	body := m.styles.header.Render("CONNECTIONS") + "\n" +
		m.styles.muted.Render("No saved connections. Press n to add one.") + "\n" +
		m.listFooter(lo)
	return body
}
