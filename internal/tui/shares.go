package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/wicket/internal/config"
)

// Share rows sit above "add folder" in SHARING. Their ids are outside
// fieldCount so the fixed label/input tables stay sized for real fields.
const fieldShareRow0 = 1000

func shareRow(i int) int { return fieldShareRow0 + i }

func shareIndex(id int) (int, bool) {
	if id >= fieldShareRow0 && id < fieldShareRow0+1000 {
		return id - fieldShareRow0, true
	}
	return 0, false
}

func (f formState) label(id int) string {
	if i, ok := shareIndex(id); ok {
		if i < len(f.p.Shares) && f.p.Shares[i].Name != "" {
			return f.p.Shares[i].Name
		}
		return "folder"
	}
	if id >= 0 && id < fieldCount {
		return formLabels[id]
	}
	return ""
}

func (f formState) help(id int) string {
	if _, ok := shareIndex(id); ok {
		if f.shareEdit >= 0 {
			if f.sharePart == "name" {
				return "Drive name on the remote machine. Empty derives it from the path. `enter` keeps it."
			}
			return "Local folder to share, absolute or ~/…. `enter` keeps it."
		}
		return "`enter` edits the path, `n` the name, `d` removes the folder."
	}
	if id == fieldShareAdd {
		return "Share another local folder as a drive. `enter` adds one."
	}
	if id >= 0 && id < fieldCount {
		return formHelp[id]
	}
	return ""
}

func (f *formState) beginShareEdit(i int, part string) {
	if i < 0 || i >= len(f.p.Shares) {
		return
	}
	f.shareEdit = i
	f.sharePart = part
	val := f.p.Shares[i].Path
	if part == "name" {
		val = f.p.Shares[i].Name
	}
	in := f.shareInput
	in.SetValue(val)
	in.Focus()
	in.CursorEnd()
	f.shareInput = in
	f.field = shareRow(i)
	f.clearError()
}

func (f *formState) endShareEdit(keep bool) {
	if f.shareEdit < 0 || f.shareEdit >= len(f.p.Shares) {
		f.shareEdit = -1
		f.sharePart = ""
		f.shareInput.Blur()
		return
	}
	if keep {
		v := strings.TrimSpace(f.shareInput.Value())
		s := f.p.Shares[f.shareEdit]
		if f.sharePart == "name" {
			s.Name = v
		} else {
			s.Path = v
		}
		f.p.Shares[f.shareEdit] = s
	}
	f.shareEdit = -1
	f.sharePart = ""
	f.shareInput.Blur()
}

func (f *formState) addShare() {
	f.p.Shares = append(f.p.Shares, config.Share{})
	f.beginShareEdit(len(f.p.Shares)-1, "path")
}

func (f *formState) removeShare(i int) {
	if i < 0 || i >= len(f.p.Shares) {
		return
	}
	if f.shareEdit == i {
		f.endShareEdit(false)
	} else if f.shareEdit > i {
		f.shareEdit--
	}
	f.p.Shares = append(f.p.Shares[:i], f.p.Shares[i+1:]...)
	if len(f.p.Shares) == 0 {
		f.focus(fieldShareAdd)
		return
	}
	if i >= len(f.p.Shares) {
		i = len(f.p.Shares) - 1
	}
	f.focus(shareRow(i))
}

func (f formState) shareValue(i int) string {
	if i < 0 || i >= len(f.p.Shares) {
		return ""
	}
	s := f.p.Shares[i]
	if s.Path == "" {
		return "set a path"
	}
	if s.Name != "" {
		return s.Path
	}
	return s.Path
}

func (f *formState) editShareText(msg tea.Msg) {
	in, err := updateInput(f.shareInput, msg, false)
	if err != nil {
		f.err, f.errField = err.Error(), f.field
		return
	}
	f.shareInput = in
	if f.err != "" && f.errField == f.field {
		f.clearError()
	}
}

func newShareInput() textinput.Model {
	in := textinput.New()
	in.CharLimit = 512
	in.Placeholder = "~/Documents"
	return in
}

func shareSummary(shares []config.Share) string {
	if len(shares) == 0 {
		return ""
	}
	parts := make([]string, 0, len(shares))
	for _, s := range shares {
		if s.Name != "" {
			parts = append(parts, s.Name)
			continue
		}
		if s.Path != "" {
			parts = append(parts, config.ShareNameFromPath(s.Path))
			continue
		}
		parts = append(parts, "folder")
	}
	return strings.Join(parts, ", ")
}

func shareCountLabel(n int) string {
	if n == 1 {
		return "1 folder"
	}
	return fmt.Sprintf("%d folders", n)
}
