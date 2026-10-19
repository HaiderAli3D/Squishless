package caire

// squishless: declarations shared by the GUI and non-GUI builds, moved out of draw.go
// and gui.go (which now need the caire_gui build tag).

// ShapeType is the shape used to draw seams in debug mode.
type ShapeType string

const (
	Circle ShapeType = "circle"
	Line   ShapeType = "line"
)

// The maximum size of the preview window.
var (
	maxScreenX float32 = 1280
	maxScreenY float32 = 720
)
