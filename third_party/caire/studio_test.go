package caire

// caire-studio: tests for the programmatic hooks added in studio.go / processor.go.

import (
	"errors"
	"image"
	"image/color"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noiseImage(w, h int, seed int64) *image.NRGBA {
	r := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func newStudioProcessor() *Processor {
	return &Processor{
		BlurRadius:     4,
		SobelThreshold: 2,
		ShapeType:      Circle,
		SeamColor:      "#ff0000",
	}
}

func TestStudio_WidthShrinkFramesAndStart(t *testing.T) {
	proc := newStudioProcessor()
	proc.NewWidth = 50

	var starts [][4]int
	frames := 0
	proc.OnStart = func(w, h, tw, th int) { starts = append(starts, [4]int{w, h, tw, th}) }
	proc.OnFrame = func(f Frame) error {
		frames++
		assert.False(t, f.Vertical)
		assert.Equal(t, 40, len(f.Seams))
		assert.Equal(t, 60-frames, f.Img.Bounds().Dx())
		return nil
	}

	out, err := proc.Run(noiseImage(60, 40, 1))
	require.NoError(t, err)
	assert.Equal(t, 10, frames)
	require.Len(t, starts, 1)
	assert.Equal(t, [4]int{60, 40, 50, 40}, starts[0])
	assert.Equal(t, image.Pt(50, 40), out.Bounds().Size())
}

func TestStudio_ConsecutiveRunsResetState(t *testing.T) {
	src := noiseImage(60, 40, 2)

	first := newStudioProcessor()
	first.NewWidth, first.NewHeight = 50, 30
	out, err := first.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(50, 30), out.Bounds().Size())

	second := newStudioProcessor()
	second.NewWidth = 45
	frames := 0
	second.OnFrame = func(f Frame) error {
		frames++
		assert.False(t, f.Vertical)
		return nil
	}
	out, err = second.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(45, 40), out.Bounds().Size())
	assert.Equal(t, 15, frames)

	third := newStudioProcessor()
	third.NewHeight = 35
	out, err = third.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(60, 35), out.Bounds().Size())
}

func TestStudio_HeightShrinkVerticalFrames(t *testing.T) {
	proc := newStudioProcessor()
	proc.NewHeight = 32

	var starts [][4]int
	frames := 0
	proc.OnStart = func(w, h, tw, th int) { starts = append(starts, [4]int{w, h, tw, th}) }
	proc.OnFrame = func(f Frame) error {
		frames++
		assert.True(t, f.Vertical)
		up, seams := f.Upright()
		// natural orientation: width unchanged, height reduced by one per seam
		assert.Equal(t, 60, up.Bounds().Dx())
		assert.Equal(t, 40-frames, up.Bounds().Dy())
		assert.Len(t, seams, 60)
		preHeight := up.Bounds().Dy() + 1 // seams are in pre-removal coordinates
		for _, s := range seams {
			assert.True(t, s.X >= 0 && s.X < 60, "seam x out of bounds: %v", s)
			assert.True(t, s.Y >= 0 && s.Y < preHeight, "seam y out of bounds: %v", s)
		}
		// one seam point per column
		cols := map[int]bool{}
		for _, s := range seams {
			cols[s.X] = true
		}
		assert.Len(t, cols, 60)
		return nil
	}

	out, err := proc.Run(noiseImage(60, 40, 3))
	require.NoError(t, err)
	assert.Equal(t, 8, frames)
	require.Len(t, starts, 1)
	assert.Equal(t, [4]int{60, 40, 60, 32}, starts[0])
	assert.Equal(t, image.Pt(60, 32), out.Bounds().Size())
}

func TestStudio_UprightPixelMapping(t *testing.T) {
	// Natural 3x2 image, rotated like the carver does for vertical passes.
	nat := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	nat.Set(2, 1, color.NRGBA{R: 255, A: 255})
	rot := rotateImage90(nat)
	// find the marked pixel in rotated space
	var mark Seam
	for y := 0; y < rot.Bounds().Dy(); y++ {
		for x := 0; x < rot.Bounds().Dx(); x++ {
			if rot.NRGBAAt(x, y).R == 255 {
				mark = Seam{X: x, Y: y}
			}
		}
	}
	up, seams := Frame{Img: rot, Seams: []Seam{mark}, Vertical: true}.Upright()
	assert.Equal(t, image.Pt(3, 2), up.Bounds().Size())
	assert.Equal(t, []Seam{{X: 2, Y: 1}}, seams)
	assert.Equal(t, uint8(255), up.NRGBAAt(2, 1).R)
}

func TestStudio_EnlargeWidth(t *testing.T) {
	proc := newStudioProcessor()
	proc.NewWidth = 70
	frames := 0
	proc.OnFrame = func(f Frame) error { frames++; return nil }

	out, err := proc.Run(noiseImage(60, 40, 4))
	require.NoError(t, err)
	assert.Equal(t, image.Pt(70, 40), out.Bounds().Size())
	assert.Equal(t, 10, frames)
}

func TestStudio_OnFrameErrorAborts(t *testing.T) {
	sentinel := errors.New("stop now")
	proc := newStudioProcessor()
	proc.NewWidth = 40
	frames := 0
	proc.OnFrame = func(f Frame) error {
		frames++
		if frames == 3 {
			return sentinel
		}
		return nil
	}

	out, err := proc.Run(noiseImage(60, 40, 5))
	assert.Nil(t, out)
	assert.True(t, errors.Is(err, sentinel), "got %v", err)
	assert.Equal(t, 3, frames)

	// a following run must still work normally
	next := newStudioProcessor()
	next.NewWidth = 55
	out, err = next.Run(noiseImage(60, 40, 5))
	require.NoError(t, err)
	assert.Equal(t, image.Pt(55, 40), out.Bounds().Size())
}

func TestStudio_RemovalMask(t *testing.T) {
	src := noiseImage(60, 40, 6)
	rmask := image.NewNRGBA(src.Bounds())
	for y := 10; y < 30; y++ {
		for x := 20; x < 30; x++ {
			rmask.Set(x, y, color.White)
		}
	}
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			if rmask.NRGBAAt(x, y).A == 0 {
				rmask.Set(x, y, color.Black)
			}
		}
	}

	proc := newStudioProcessor()
	proc.NewWidth = 48
	proc.SetMasks(nil, rmask)
	require.NotNil(t, proc.RMask)
	assert.Nil(t, proc.Mask)

	out, err := proc.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(48, 40), out.Bounds().Size())

	// protect mask + both dims also works
	pmask := image.NewNRGBA(src.Bounds())
	proc2 := newStudioProcessor()
	proc2.NewHeight = 34
	proc2.SetMasks(pmask, nil)
	out, err = proc2.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(60, 34), out.Bounds().Size())
}

func TestStudio_NoGoroutineLeak(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		proc := newStudioProcessor()
		proc.NewWidth = 40 // 20 seams
		proc.Preview = false
		frames := 0
		proc.OnFrame = func(f Frame) error { frames++; return nil }
		out, err := proc.Run(noiseImage(60, 40, int64(7+i)))
		require.NoError(t, err)
		assert.Equal(t, 20, frames)
		assert.Equal(t, image.Pt(40, 40), out.Bounds().Size())
	}

	var after int
	for i := 0; i < 20; i++ {
		after = runtime.NumGoroutine()
		if after <= before+2 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	assert.LessOrEqual(t, after, before+2, "goroutines before=%d after=%d", before, after)
}

// stripeMask returns a black mask of size w x h with a white vertical stripe [x0, x1).
func stripeMask(w, h, x0, x1 int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= x0 && x < x1 {
				m.Set(x, y, color.White)
			} else {
				m.Set(x, y, color.Black)
			}
		}
	}
	return m
}

func countWhite(m *image.NRGBA) int {
	n := 0
	for i := 0; i < len(m.Pix); i += 4 {
		if m.Pix[i] == 0xff && m.Pix[i+1] == 0xff && m.Pix[i+2] == 0xff && m.Pix[i+3] == 0xff {
			n++
		}
	}
	return n
}

func TestStudio_RemovalMaskSteersSeams(t *testing.T) {
	src := noiseImage(60, 40, 11)
	proc := newStudioProcessor()
	proc.NewWidth = 50
	proc.SetMasks(nil, stripeMask(60, 40, 20, 30))
	before := countWhite(proc.RMask)

	out, err := proc.Run(src)
	require.NoError(t, err)
	assert.Equal(t, image.Pt(50, 40), out.Bounds().Size())
	// The ten removed seams must go through the ten masked columns.
	left := countWhite(proc.RMask)
	assert.Less(t, left, before/10, "removal mask ignored: %d of %d masked pixels left", left, before)

	plain := newStudioProcessor()
	plain.NewWidth = 50
	plainOut, err := plain.Run(src)
	require.NoError(t, err)
	assert.NotEqual(t, plainOut.(*image.NRGBA).Pix, out.(*image.NRGBA).Pix, "mask had no effect on the output")
}

func TestStudio_ProtectMaskKeepsPixels(t *testing.T) {
	proc := newStudioProcessor()
	proc.NewWidth = 30
	proc.SetMasks(stripeMask(60, 40, 20, 30), nil)
	before := countWhite(proc.Mask)

	out, err := proc.Run(noiseImage(60, 40, 12))
	require.NoError(t, err)
	assert.Equal(t, image.Pt(30, 40), out.Bounds().Size())
	assert.GreaterOrEqual(t, countWhite(proc.Mask), before*95/100, "protected pixels were carved away")
}

func TestStudio_MasksFollowTwoAxisResize(t *testing.T) {
	for _, target := range []image.Point{{50, 30}, {50, 50}, {70, 30}} {
		proc := newStudioProcessor()
		proc.NewWidth, proc.NewHeight = target.X, target.Y
		proc.SetMasks(stripeMask(60, 40, 5, 10), stripeMask(60, 40, 20, 30))
		out, err := proc.Run(noiseImage(60, 40, 13))
		require.NoError(t, err)
		assert.Equal(t, target, out.Bounds().Size())
		assert.Equal(t, target, proc.Mask.Bounds().Size(), "protect mask out of sync for %v", target)
		assert.Equal(t, target, proc.RMask.Bounds().Size(), "removal mask out of sync for %v", target)
	}
}

func TestStudio_PercentageOnSquareSource(t *testing.T) {
	cases := []struct {
		removeW, removeH int
		want             image.Point
	}{
		{30, 0, image.Pt(70, 100)},
		{30, 50, image.Pt(70, 50)},
		{0, 40, image.Pt(100, 60)},
		{40, 40, image.Pt(60, 60)}, // equal percentages still use the uniform scale
	}
	for _, c := range cases {
		proc := newStudioProcessor()
		proc.Percentage = true
		proc.NewWidth, proc.NewHeight = c.removeW, c.removeH
		out, err := proc.Run(noiseImage(100, 100, 14))
		require.NoError(t, err)
		assert.Equal(t, c.want, out.Bounds().Size(), "remove %d%% x %d%%", c.removeW, c.removeH)
	}
}

func TestStudio_FaceAngleNotOverwritten(t *testing.T) {
	proc := newStudioProcessor()
	proc.FaceDetect = true
	proc.FaceAngle = 0.05
	proc.NewWidth, proc.NewHeight = 56, 36
	_, err := proc.Run(noiseImage(60, 40, 15))
	require.NoError(t, err)
	assert.Equal(t, 0.05, proc.FaceAngle)
}

func TestStudio_InsertedSeamsOnlyFromEnlarge(t *testing.T) {
	shrink := newStudioProcessor()
	shrink.NewWidth, shrink.NewHeight = 30, 70 // portrait: NewHeight > width during horizontal steps
	_, err := shrink.Run(noiseImage(40, 60, 16))
	require.NoError(t, err)
	assert.Empty(t, energySeams, "removed seams must not be recorded")

	grow := newStudioProcessor()
	grow.NewWidth, grow.NewHeight = 66, 44
	out, err := grow.Run(noiseImage(60, 40, 17))
	require.NoError(t, err)
	assert.Equal(t, image.Pt(66, 44), out.Bounds().Size())
	assert.Len(t, energySeams, 6)
	assert.Len(t, vEnergySeams, 4)
	for _, s := range energySeams {
		// one point per natural row; the height grows by one between horizontal steps
		assert.True(t, len(s) >= 40 && len(s) <= 44, "horizontal seam has %d points", len(s))
	}
}

func TestStudio_RecordInsertedSeamShiftsEarlierSeams(t *testing.T) {
	resetState()
	recordInsertedSeam(false, []Seam{{X: 5, Y: 0}, {X: 5, Y: 1}})
	recordInsertedSeam(false, []Seam{{X: 3, Y: 0}, {X: 7, Y: 1}})
	assert.Equal(t, []Seam{{X: 6, Y: 0}, {X: 5, Y: 1}}, energySeams[0])
	assert.Equal(t, []Seam{{X: 3, Y: 0}, {X: 7, Y: 1}}, energySeams[1])
	assert.Empty(t, vEnergySeams)
	resetState()
}
