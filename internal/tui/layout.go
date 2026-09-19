package tui

const (
	widthCompact = 60
	widthWide    = 100
	heightTiny   = 5
	widthTiny    = 20
)

type layout struct {
	Width, Height, ContentWidth int
	Compact, Wide, Tiny         bool
}

func newLayout(width, height int) layout {
	cw := width - 6
	if cw < 1 {
		cw = 1
	}
	return layout{
		Width:        width,
		Height:       height,
		ContentWidth: cw,
		Compact:      width < widthCompact,
		Wide:         width >= widthWide,
		Tiny:         width < widthTiny || height < heightTiny,
	}
}
