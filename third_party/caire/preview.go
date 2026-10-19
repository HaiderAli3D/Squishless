//go:build caire_gui

// squishless: the Gio preview window is opt-in (-tags caire_gui), so the library
// builds without cgo or GUI system libraries. See preview_nogui.go.

package caire

import (
	"os"
)

// squishless: tells Process that the preview window is built in.
const previewAvailable = true

// showPreview spawns a new Gio GUI window and updates its content with the resized image received from a channel.
func (p *Processor) showPreview(
	imgWorker <-chan worker,
	errChan chan<- error,
	guiWindow struct {
		width  int
		height int
	},
) {
	var gui = NewGUI(guiWindow.width, guiWindow.height)
	gui.proc = p
	gui.process.worker = imgWorker

	// Run the Gio GUI app in a separate goroutine
	go func() {
		if err := gui.Run(); err != nil {
			errChan <- err
		}
		// It's important to call os.Exit(0) in order to terminate
		// the execution of the GUI app when pressing ESC key.
		os.Exit(0)
	}()
}
