package caire

// squishless: additions that let the library be driven programmatically
// (live per-seam frames, repeatable runs, in-memory masks) without the Gio GUI.

import (
	"fmt"
	"image"

	pigo "github.com/esimov/pigo/core"
)

// Frame is a snapshot of the image after a single seam has been removed or inserted.
// When Vertical is true the image (and the seam coordinates) are rotated 90° counter
// clockwise, exactly as the carver sees them; use Upright to get the natural orientation.
type Frame struct {
	Img      *image.NRGBA
	Seams    []Seam // the seam that was just processed, in Img's coordinate space
	Vertical bool
}

// Upright returns the frame image and seam in the image's natural orientation.
// The returned image may share memory with f.Img when no rotation is needed.
func (f Frame) Upright() (*image.NRGBA, []Seam) {
	if !f.Vertical {
		return f.Img, f.Seams
	}
	h := f.Img.Bounds().Dy()
	seams := make([]Seam, len(f.Seams))
	for i, s := range f.Seams {
		// Inverse of rotateImage90: rotateImage270 maps src(x, y) to dst(h-1-y, x).
		seams[i] = Seam{X: h - 1 - s.Y, Y: s.X}
	}
	return rotateImage270(f.Img), seams
}

// emitFrame forwards the current state to the OnFrame hook, if any.
func (p *Processor) emitFrame(img *image.NRGBA, seams []Seam) error {
	if p.OnFrame == nil {
		return nil
	}
	return p.OnFrame(Frame{Img: img, Seams: seams, Vertical: p.vRes})
}

// resetState resets the package level variables mutated during a resize operation,
// so that multiple resize operations can run one after the other in the same process.
// Concurrent resize operations are still not supported.
func resetState() {
	resizeXY = false
	sobel = nil
	energySeams = make([][]Seam, 0)
	vEnergySeams = make([][]Seam, 0)
	detAttempts = 0
	isFaceDetected = false
}

// insertedSeams returns the table of seams inserted so far in the given orientation.
func insertedSeams(vertical bool) *[][]Seam {
	if vertical {
		return &vEnergySeams
	}
	return &energySeams
}

// recordInsertedSeam stores a seam that was just inserted by AddSeam, so the following
// energy maps of the same orientation avoid picking the same path again. Upstream recorded
// every seam (removed ones too, and across orientations); this keeps only insertions and
// shifts the earlier seams of the same orientation to account for the inserted column.
func recordInsertedSeam(vertical bool, seams []Seam) {
	table := insertedSeams(vertical)
	if len(*table) > 0 && len(seams) > 0 {
		inserted := make(map[int]int, len(seams)) // row -> inserted column
		for _, s := range seams {
			inserted[s.Y] = s.X
		}
		for _, old := range *table {
			for i, s := range old {
				if x, ok := inserted[s.Y]; ok && s.X >= x {
					old[i].X++
				}
			}
		}
	}
	*table = append(*table, append([]Seam(nil), seams...))
}

// rotateMasks applies rotate to the protective and removal masks, keeping them in the
// same orientation as the image while it is rotated for a vertical pass.
func (p *Processor) rotateMasks(rotate func(*image.NRGBA) *image.NRGBA) {
	if p.Mask != nil {
		p.Mask = rotate(p.Mask)
	}
	if p.RMask != nil {
		p.RMask = rotate(p.RMask)
	}
}

// SetMasks converts the protective (mask) and removal (rmask) images to black & white
// masks, the same way the CLI does for the -mask and -rmask flags. Either may be nil.
// The masks must have the same size as the source image.
func (p *Processor) SetMasks(mask, rmask image.Image) {
	p.Mask, p.RMask, p.DebugMask = nil, nil, nil
	if mask != nil {
		p.Mask = dither(imgToNRGBA(mask))
		p.DebugMask = p.Mask
	}
	if rmask != nil {
		p.RMask = dither(imgToNRGBA(rmask))
		p.DebugMask = p.RMask
	}
}

// Run resizes an already decoded image and returns the result without encoding it.
// It is the in-memory counterpart of Process: it loads the face detector when
// FaceDetect is set and resets the package state left over by earlier runs.
// Runs must not overlap (the library relies on package level state).
func (p *Processor) Run(src image.Image) (image.Image, error) {
	if p.FaceDetect {
		det, err := pigo.NewPigo().Unpack(cascadeFile)
		if err != nil {
			return nil, fmt.Errorf("error unpacking the cascade file: %v", err)
		}
		p.FaceDetector = det
	}

	resetState()
	if p.NewWidth != 0 && p.NewHeight != 0 {
		resizeXY = true
	}
	return p.Resize(imgToNRGBA(src))
}
