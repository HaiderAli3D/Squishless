//go:build !caire_gui

package caire

// squishless: stand-in for preview.go when the Gio preview window is not built in.
// Process turns Preview off (with a warning) before this could be called.

const previewAvailable = false

func (p *Processor) showPreview(
	imgWorker <-chan worker,
	errChan chan<- error,
	guiWindow struct {
		width  int
		height int
	},
) {
}
