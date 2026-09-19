package tui

const (
	widthCompact = 60
	widthWide    = 100
	heightTiny   = 5
	widthTiny    = 20

	// panelNormal and panelWide cap the panel width so it does not stretch
	// across very wide terminals.
	panelNormal = 80
	panelWide   = 120
)

type layout struct {
	Width, Height int
	// Panel is the framed panel's outer width; Inner is the text width
	// inside its border and padding.
	Panel, Inner        int
	Compact, Wide, Tiny bool
	// Budget is the number of body lines the list may use. render sets it
	// once the header, status, and footer heights are known.
	Budget int
}

func newLayout(width, height int) layout {
	lo := layout{
		Width:   width,
		Height:  height,
		Compact: width < widthCompact,
		Wide:    width >= widthWide,
		Tiny:    width < widthTiny || height < heightTiny,
	}
	switch {
	case lo.Wide:
		lo.Panel = min(width, panelWide)
	case lo.Compact:
		lo.Panel = width
	default:
		lo.Panel = min(width, panelNormal)
	}
	lo.Inner = max(lo.Panel-4, 1)
	return lo
}
