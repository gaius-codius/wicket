package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
)

func (m Model) handleListKey(key string) (tea.Model, tea.Cmd) {
	ps := m.profiles()
	empty := len(ps) == 0
	switch key {
	case "q":
		return m.quitNow()
	case "?":
		return m.openHelp()
	case "esc":
		return m, nil
	case "j", "down":
		if m.cursor < len(ps)-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "n":
		return m.openForm("", config.Profile{
			Client:            config.DefaultClient,
			Scale:             config.DefaultScale,
			DynamicResolution: config.DefaultDynamicResolution,
		})
	case "e":
		if empty {
			return m, nil
		}
		p, ok := m.selected()
		if !ok {
			return m, nil
		}
		return m.openForm(p.Name, p)
	case "D", "shift+d":
		if empty {
			return m, nil
		}
		p, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.delName = p.Name
		m.view = viewDelete
		return m, nil
	case "enter":
		if empty {
			return m, nil
		}
		return m.beginConnect()
	default:
		return m, nil
	}
}

func (m Model) viewList(lo layout) string {
	ps := m.profiles()
	if len(ps) == 0 {
		return m.viewEmpty(lo)
	}
	selLines := 4
	if lo.Compact {
		selLines = 1
	}
	start, end := listWindow(len(ps), m.cursor, m.listCardBudget(lo), selLines)
	var b strings.Builder
	b.WriteString(m.styles.header.Render("CONNECTIONS"))
	b.WriteByte('\n')
	for i := start; i < end; i++ {
		b.WriteString(m.renderCard(ps[i], i == m.cursor, lo))
		b.WriteByte('\n')
	}
	b.WriteString(m.listFooter(lo))
	return b.String()
}

func (m Model) listCardBudget(lo layout) int {
	n := lo.Height - 8
	if m.status != "" {
		n--
	}
	if m.view == viewRetry {
		n -= 4
	}
	if n < 1 {
		return 1
	}
	return n
}

// listWindow returns a half-open [start, end) of profiles that fit in budget
// lines, always including the selected card (selLines tall; others 1 line).
func listWindow(n, cursor, budget, selLines int) (start, end int) {
	if n <= 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	if selLines < 1 {
		selLines = 1
	}
	if budget < 1 {
		budget = 1
	}
	start, end = cursor, cursor+1
	used := selLines
	for used < budget {
		progressed := false
		if start > 0 && used+1 <= budget {
			start--
			used++
			progressed = true
		}
		if end < n && used+1 <= budget {
			end++
			used++
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return start, end
}

func (m Model) renderCard(p config.Profile, selected bool, lo layout) string {
	name := p.Name
	if selected {
		bar := m.styles.accent.Render("▌ ")
		if lo.Compact {
			line := name
			if p.Host != "" {
				line += "  " + p.Host
			}
			return bar + m.styles.primary.Render(line)
		}
		var lines []string
		lines = append(lines, bar+m.styles.primary.Render(name))
		detail := "  host " + p.Host + "  user " + p.User
		if p.Domain != "" {
			detail += "  domain " + p.Domain
		}
		lines = append(lines, m.styles.muted.Render(detail))
		used := lastUsedLabel(m.app.State, p.Name)
		lines = append(lines, m.styles.muted.Render("  last-used "+used))
		lines = append(lines, m.styles.muted.Render("  "+displayLine(p)))
		return strings.Join(lines, "\n")
	}
	return "  " + m.styles.muted.Render(name)
}

func displayLine(p config.Profile) string {
	size := p.Size
	if size == "" {
		size = "default"
	}
	mode := "window"
	if p.Fullscreen {
		mode = "fullscreen"
	}
	dyn := ""
	if p.DynamicResolution {
		dyn = " dynamic"
	}
	return fmt.Sprintf("%s  %s%s  scale %d", size, mode, dyn, p.Scale)
}

func (m Model) listFooter(lo layout) string {
	if lo.Compact {
		return m.styles.footer.Render("[enter] connect  [n] new  [e] edit  [D] delete  [?] help  [q] quit")
	}
	return m.styles.footer.Render("[enter] connect  [n] new  [e] edit  [D] delete  [?] help  [q] quit")
}
