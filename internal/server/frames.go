package server

import (
	"bytes"
	"image"
	"image/jpeg"
	"time"

	"github.com/esimov/caire"
	"golang.org/x/image/draw"
)

const (
	frameMaxEdge     = 1000             // long edge of streamed preview frames
	frameInterval    = time.Second / 25 // at most 25 frames per second
	frameJPEGQuality = 80
	maxSeamPoints    = 1200 // seam points per frame after scaling/deduplication
)

// framePayload is the JSON body of the "frame" SSE event.
type framePayload struct {
	JPEG       []byte   `json:"jpeg"` // base64 encoded by encoding/json
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Seams      [][2]int `json:"seams"`
	Pass       string   `json:"pass"`
	FullWidth  int      `json:"fullWidth"`
	FullHeight int      `json:"fullHeight"`
}

// throttle decides whether a frame should be emitted now.
type throttle struct {
	interval time.Duration
	last     time.Time
	started  bool
}

func (t *throttle) ready(now time.Time) bool {
	if !t.started || now.Sub(t.last) >= t.interval {
		t.started = true
		t.last = now
		return true
	}
	return false
}

// previewSize returns the size of img scaled so that its long edge is <= maxEdge.
func previewSize(w, h, maxEdge int) (int, int) {
	if w <= maxEdge && h <= maxEdge {
		return w, h
	}
	if w >= h {
		nh := int(float64(h)*float64(maxEdge)/float64(w) + 0.5)
		return maxEdge, max(nh, 1)
	}
	nw := int(float64(w)*float64(maxEdge)/float64(h) + 0.5)
	return max(nw, 1), maxEdge
}

// downscale returns img (or a scaled copy) whose long edge is <= maxEdge.
// The returned image never needs to outlive the call that encodes it, so
// no copy is made when no scaling is necessary.
func downscale(img image.Image, maxEdge int) (image.Image, float64, float64) {
	b := img.Bounds()
	nw, nh := previewSize(b.Dx(), b.Dy(), maxEdge)
	if nw == b.Dx() && nh == b.Dy() {
		return img, 1, 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst, float64(nw) / float64(b.Dx()), float64(nh) / float64(b.Dy())
}

// scaleSeams maps seam points into preview space, drops consecutive duplicates
// and subsamples to at most maxPts points. Returns nil for an empty seam.
func scaleSeams(seams []caire.Seam, sx, sy float64, w, h, maxPts int) [][2]int {
	if len(seams) == 0 {
		return nil
	}
	pts := make([][2]int, 0, len(seams))
	for _, s := range seams {
		x := int(float64(s.X)*sx + sx/2)
		y := int(float64(s.Y)*sy + sy/2)
		x = min(max(x, 0), w-1)
		y = min(max(y, 0), h-1)
		if n := len(pts); n > 0 && pts[n-1][0] == x && pts[n-1][1] == y {
			continue
		}
		pts = append(pts, [2]int{x, y})
	}
	if maxPts > 0 && len(pts) > maxPts {
		step := float64(len(pts)) / float64(maxPts)
		out := make([][2]int, 0, maxPts+1)
		for i := 0; i < maxPts; i++ {
			out = append(out, pts[int(float64(i)*step)])
		}
		if last := pts[len(pts)-1]; out[len(out)-1] != last {
			out = append(out, last)
		}
		pts = out
	}
	return pts
}

// buildFrame turns an upright image (and optional upright seam) into a frame payload.
func buildFrame(img image.Image, seams []caire.Seam, withSeams bool, pass string) (*framePayload, error) {
	b := img.Bounds()
	small, sx, sy := downscale(img, frameMaxEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: frameJPEGQuality}); err != nil {
		return nil, err
	}
	sb := small.Bounds()
	fp := &framePayload{
		JPEG:       buf.Bytes(),
		Width:      sb.Dx(),
		Height:     sb.Dy(),
		Pass:       pass,
		FullWidth:  b.Dx(),
		FullHeight: b.Dy(),
	}
	if withSeams {
		fp.Seams = scaleSeams(seams, sx, sy, sb.Dx(), sb.Dy(), maxSeamPoints)
	}
	return fp, nil
}
