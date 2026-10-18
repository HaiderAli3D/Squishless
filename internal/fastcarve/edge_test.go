package fastcarve

import (
	"context"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// full reference: recompute everything for the current image and compare.
func checkCarverExact(t *testing.T, c *carver, step int) {
	t.Helper()
	p := make([]uint32, c.w*c.h)
	var b []float32
	if c.bias != nil {
		b = make([]float32, c.w*c.h)
	}
	for y := 0; y < c.h; y++ {
		copy(p[y*c.w:], c.gray2pix(y))
		if b != nil {
			copy(b[y*c.w:y*c.w+c.w], c.bias[y*c.stride:y*c.stride+c.w])
		}
	}
	ref := newCarver(c.w, c.h, p, b, false, false)
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			i, j := y*c.stride+x, y*ref.stride+x
			if c.blur[i] != ref.blur[j] {
				t.Fatalf("step %d w=%d: blur mismatch at %d,%d", step, c.w, x, y)
			}
			if c.energy[i] != ref.energy[j] {
				t.Fatalf("step %d w=%d: energy mismatch at %d,%d: %v vs %v", step, c.w, x, y, c.energy[i], ref.energy[j])
			}
		}
	}
	if c.dpValid {
		c.updateDP()
		inc := append([]float64(nil), c.dp...)
		c.fullDP()
		for y := 0; y < c.h; y++ {
			for x := 0; x < c.w; x++ {
				if i := y*c.stride + x; inc[i] != c.dp[i] {
					t.Fatalf("step %d w=%d: dp mismatch at %d,%d", step, c.w, x, y)
				}
			}
		}
		c.backtrack()
		return
	}
	c.findSeam()
}

// gray2pix rebuilds a gray-only packed row (gray -> r=g=b) so that newCarver reproduces gray exactly.
func (c *carver) gray2pix(y int) []uint32 {
	out := make([]uint32, c.w)
	for x := 0; x < c.w; x++ {
		v := c.gray[y*c.stride+x]
		out[x] = pack(v, v, v, 255)
	}
	return out
}

func TestEdgeExactEveryStep(t *testing.T) {
	for _, sz := range [][2]int{{6, 5}, {3, 1}, {1, 7}, {2, 2}, {40, 3}, {700, 200}} {
		w, h := sz[0], sz[1]
		src := noiseImage(w, h, int64(w*h))
		pix := toPacked(src)
		rng := rand.New(rand.NewSource(1))
		bias := make([]float32, w*h)
		for i := range bias {
			switch rng.Intn(10) {
			case 0:
				bias[i] = protectBias
			case 1:
				bias[i] = removeBias
			}
		}
		for _, bb := range [][]float32{nil, bias} {
			c := newCarver(w, h, pix, bb, true, false)
			steps := w - 1
			if w > 100 {
				steps = 150
			}
			for s := 0; s < steps; s++ {
				checkCarverExact(t, c, s)
				c.removeSeam()
			}
		}
	}
}

func TestEdgeTiny(t *testing.T) {
	cases := []struct{ sw, sh, w, h int }{
		{2, 2, 1, 1}, {2, 2, 8, 8}, {1, 1, 4, 4}, {1, 1, 4, 1}, {3, 1, 12, 1}, {1, 5, 4, 0},
		{2, 2, 1, 8}, {2, 2, 8, 1}, {1, 1, 1, 1}, {5, 1, 1, 1}, {1, 9, 0, 1}, {2, 3, 7, 2},
	}
	for _, c := range cases {
		src := noiseImage(c.sw, c.sh, 3)
		out, err := Resize(context.Background(), src, Options{Width: c.w, Height: c.h, ProtectFaces: true}, nil)
		ew, eh := c.w, c.h
		if ew == 0 {
			ew = c.sw
		}
		if eh == 0 {
			eh = c.sh
		}
		if err != nil || out.Rect.Dx() != ew || out.Rect.Dy() != eh {
			t.Errorf("%+v: %v %v", c, err, out)
		}
	}
}

// Enlarge must duplicate seams: every original pixel must still be present in order.
func TestEdgeEnlargeKeepsOriginals(t *testing.T) {
	src := noiseImage(30, 10, 11)
	out, err := Resize(context.Background(), src, Options{Width: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 10; y++ {
		ox := 0
		for x := 0; x < 30; x++ {
			want := src.NRGBAAt(x, y)
			for ox < 100 && out.NRGBAAt(ox, y) != want {
				ox++
			}
			if ox == 100 {
				t.Fatalf("row %d: original pixel %d not found in order", y, x)
			}
			ox++
		}
	}
}

// Mask after pre-scale: removing a marked block with both dimensions shrinking.
func TestEdgeMaskPrescale(t *testing.T) {
	w, h := 200, 100
	src := noiseImage(w, h, 4)
	mask := image.NewGray(src.Bounds())
	for y := 0; y < h; y++ {
		for x := 150; x < 170; x++ {
			src.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
			mask.SetGray(x, y, color.Gray{255})
		}
	}
	out, err := Resize(context.Background(), src, Options{Width: 120, Height: 90, Remove: mask}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := 0; i < len(out.Pix); i += 4 {
		if out.Pix[i] > 240 && out.Pix[i+1] > 240 && out.Pix[i+2] > 240 {
			n++
		}
	}
	if n > 0 {
		t.Fatalf("white pixels left: %d", n)
	}
}
