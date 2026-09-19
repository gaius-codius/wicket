//go:build tools

package tools

import (
	_ "charm.land/bubbles/v2"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
	_ "github.com/BurntSushi/toml"
	_ "github.com/godbus/dbus/v5"
	_ "golang.org/x/term"
	_ "golang.org/x/tools/go/packages"
)
