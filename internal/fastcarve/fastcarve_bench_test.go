package fastcarve

import (
	"context"
	"image"
	"image/jpeg"
	"os"
	"testing"

	"github.com/esimov/caire"
)

const benchShrink = 350

func loadSample(b *testing.B) image.Image {
	f, err := os.Open("../../web/samples/portrait-field.jpg")
	if err != nil {
		b.Skip("web/samples/portrait-field.jpg not present")
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		b.Fatal(err)
	}
	return img
}

func benchFast(b *testing.B, img image.Image, opt Options) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Resize(context.Background(), img, opt, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func benchCaire(b *testing.B, img image.Image, newW, newH int) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := &caire.Processor{NewWidth: newW, NewHeight: newH, BlurRadius: 4, SobelThreshold: 2}
		if _, err := p.Run(img); err != nil {
			b.Fatal(err)
		}
	}
}

// Noise image, 1000x650, shrink width by 350.
func BenchmarkNoiseShrinkW_Fast(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Width: 1000 - benchShrink, BlurRadius: DefaultBlurRadius})
}

func BenchmarkNoiseShrinkW_Caire(b *testing.B) {
	benchCaire(b, noiseImage(1000, 650, 42), 1000-benchShrink, 0)
}

// Photo sample, 1000x650, shrink width by 350.
func BenchmarkPortraitShrinkW_Fast(b *testing.B) {
	img := loadSample(b)
	benchFast(b, img, Options{Width: img.Bounds().Dx() - benchShrink, BlurRadius: DefaultBlurRadius})
}

func BenchmarkPortraitShrinkW_FastFaces(b *testing.B) {
	img := loadSample(b)
	benchFast(b, img, Options{Width: img.Bounds().Dx() - benchShrink, ProtectFaces: true, BlurRadius: DefaultBlurRadius})
}

func BenchmarkPortraitShrinkW_Caire(b *testing.B) {
	img := loadSample(b)
	benchCaire(b, img, img.Bounds().Dx()-benchShrink, 0)
}

// Other shapes (fast engine only).
func BenchmarkNoiseShrinkH_Fast(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Height: 400, BlurRadius: DefaultBlurRadius})
}

func BenchmarkNoiseEnlargeW_Fast(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Width: 1350, BlurRadius: DefaultBlurRadius})
}

func BenchmarkNoiseShrinkBoth_Fast(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Width: 700, Height: 500, BlurRadius: DefaultBlurRadius})
}

// Blur radius / Sobel threshold extremes (fast engine only).
func BenchmarkNoiseShrinkW_FastBlur0(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Width: 1000 - benchShrink})
}

func BenchmarkNoiseShrinkW_FastBlur7(b *testing.B) {
	benchFast(b, noiseImage(1000, 650, 42), Options{Width: 1000 - benchShrink, BlurRadius: 7})
}

func BenchmarkPortraitShrinkW_FastBlur7(b *testing.B) {
	img := loadSample(b)
	benchFast(b, img, Options{Width: img.Bounds().Dx() - benchShrink, BlurRadius: 7})
}

func BenchmarkPortraitShrinkW_FastBlur7Sobel16(b *testing.B) {
	img := loadSample(b)
	benchFast(b, img, Options{Width: img.Bounds().Dx() - benchShrink, BlurRadius: 7, SobelThreshold: 16})
}
