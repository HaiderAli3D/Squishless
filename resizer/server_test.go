package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func stubResize(t *testing.T, fn func(ctx context.Context, src image.Image, o resizeOptions) (*image.NRGBA, error)) {
	t.Helper()
	old := resizeFn
	resizeFn = fn
	t.Cleanup(func() { resizeFn = old })
}

// simpleStub returns a blank image of the requested size.
func simpleStub(got *resizeOptions) func(context.Context, image.Image, resizeOptions) (*image.NRGBA, error) {
	return func(_ context.Context, src image.Image, o resizeOptions) (*image.NRGBA, error) {
		if got != nil {
			*got = o
		}
		w, h := o.Width, o.Height
		if w == 0 {
			w = src.Bounds().Dx()
		}
		if h == 0 {
			h = src.Bounds().Dy()
		}
		return image.NewNRGBA(image.Rect(0, 0, w, h)), nil
	}
}

func testImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func encPNG(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func encJPG(img image.Image) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

type part struct {
	field, filename string
	data            []byte
}

func multipartReq(t *testing.T, fields map[string]string, files ...part) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.field, f.filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(f.data)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/resize", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func do(s *server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func errMsg(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("error body is not JSON: %q", rec.Body.String())
	}
	return m["error"]
}

func TestResizePNGSameFormat(t *testing.T) {
	var got resizeOptions
	stubResize(t, simpleStub(&got))
	s := newServer()
	req := multipartReq(t, map[string]string{"width": "60", "height": "0", "faces": "1", "format": "same"},
		part{"image", "My Photo.png", encPNG(testImage(100, 50))})
	rec := do(s, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="My Photo-60x50.png"`) {
		t.Errorf("content-disposition %q", cd)
	}
	if rec.Header().Get("X-Width") != "60" || rec.Header().Get("X-Height") != "50" {
		t.Errorf("dims %s x %s", rec.Header().Get("X-Width"), rec.Header().Get("X-Height"))
	}
	if rec.Header().Get("X-Elapsed-Ms") == "" {
		t.Error("missing X-Elapsed-Ms")
	}
	if !got.ProtectFaces || got.Width != 60 || got.Height != 0 {
		t.Errorf("options passed %+v", got)
	}
	out, err := png.Decode(rec.Body)
	if err != nil || out.Bounds().Dx() != 60 {
		t.Fatalf("bad png output: %v", err)
	}
}

func TestResizeJPEGSameAndExplicitFormats(t *testing.T) {
	stubResize(t, simpleStub(nil))
	s := newServer()
	jpg := encJPG(testImage(40, 40))
	cases := []struct{ format, ctype, ext string }{
		{"same", "image/jpeg", "jpg"},
		{"png", "image/png", "png"},
		{"jpg", "image/jpeg", "jpg"},
	}
	for _, c := range cases {
		rec := do(s, multipartReq(t, map[string]string{"height": "30", "format": c.format, "quality": "80"},
			part{"image", "a.jpeg", jpg}))
		if rec.Code != 200 {
			t.Fatalf("%s: status %d %s", c.format, rec.Code, rec.Body)
		}
		if rec.Header().Get("Content-Type") != c.ctype {
			t.Errorf("%s: ctype %s", c.format, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Content-Disposition"), "a-40x30."+c.ext) {
			t.Errorf("%s: disposition %s", c.format, rec.Header().Get("Content-Disposition"))
		}
	}
}

func TestResizeValidation(t *testing.T) {
	stubResize(t, simpleStub(nil))
	s := newServer()
	img := encPNG(testImage(20, 20))
	cases := []struct {
		name   string
		fields map[string]string
		files  []part
		status int
		want   string
	}{
		{"no image", map[string]string{"width": "10"}, nil, 400, "missing image"},
		{"both zero", map[string]string{"width": "0", "height": "0"}, []part{{"image", "a.png", img}}, 400, "width or height"},
		{"too big", map[string]string{"width": "81"}, []part{{"image", "a.png", img}}, 400, "4x"},
		{"negative", map[string]string{"width": "-1"}, []part{{"image", "a.png", img}}, 400, "positive"},
		{"not a number", map[string]string{"width": "abc"}, []part{{"image", "a.png", img}}, 400, "whole number"},
		{"bad quality", map[string]string{"width": "10", "quality": "0"}, []part{{"image", "a.png", img}}, 400, "quality"},
		{"bad format", map[string]string{"width": "10", "format": "tiff"}, []part{{"image", "a.png", img}}, 400, "format"},
		{"not an image", map[string]string{"width": "10"}, []part{{"image", "a.png", []byte("hello")}}, 415, "unsupported"},
		{"mask size", map[string]string{"width": "10"}, []part{{"image", "a.png", img}, {"protect", "m.png", encPNG(image.NewGray(image.Rect(0, 0, 5, 5)))}}, 400, "protect mask is 5x5"},
		{"mask not png", map[string]string{"width": "10"}, []part{{"image", "a.png", img}, {"remove", "m.png", encJPG(testImage(20, 20))}}, 400, "remove mask must be a PNG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(s, multipartReq(t, c.fields, c.files...))
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
			if msg := errMsg(t, rec); !strings.Contains(msg, c.want) {
				t.Errorf("error %q does not contain %q", msg, c.want)
			}
		})
	}
}

func TestResizeTooManyPixels(t *testing.T) {
	stubResize(t, simpleStub(nil))
	// A PNG header claiming 10000x6000 is rejected before decoding pixels.
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 10000, 6000)))
	rec := do(newServer(), multipartReq(t, map[string]string{"width": "100"}, part{"image", "big.png", b.Bytes()}))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(errMsg(t, rec), "50 megapixels") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestMasksPassedThrough(t *testing.T) {
	var got resizeOptions
	stubResize(t, simpleStub(&got))
	mask := image.NewGray(image.Rect(0, 0, 20, 10))
	mask.SetGray(3, 4, color.Gray{255})
	rgbaMask := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	rgbaMask.Set(7, 2, color.White)
	rec := do(newServer(), multipartReq(t, map[string]string{"width": "15", "faces": "0"},
		part{"image", "a.png", encPNG(testImage(20, 10))},
		part{"protect", "p.png", encPNG(mask)},
		part{"remove", "r.png", encPNG(rgbaMask)}))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.ProtectFaces {
		t.Error("faces should be off")
	}
	if got.Protect == nil || got.Protect.GrayAt(3, 4).Y != 255 || got.Protect.GrayAt(0, 0).Y != 0 {
		t.Error("protect mask not passed correctly")
	}
	if got.Remove == nil || got.Remove.GrayAt(7, 2).Y != 255 {
		t.Error("remove mask not converted to gray")
	}
}

func TestEngineErrorIsJSON(t *testing.T) {
	stubResize(t, func(context.Context, image.Image, resizeOptions) (*image.NRGBA, error) {
		return nil, io.ErrUnexpectedEOF
	})
	rec := do(newServer(), multipartReq(t, map[string]string{"width": "10"}, part{"image", "a.png", encPNG(testImage(20, 20))}))
	if rec.Code != http.StatusUnprocessableEntity || errMsg(t, rec) == "" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestClientCancelStopsResize(t *testing.T) {
	started := make(chan struct{})
	var sawCancel atomic.Bool
	stubResize(t, func(ctx context.Context, _ image.Image, _ resizeOptions) (*image.NRGBA, error) {
		close(started)
		select {
		case <-ctx.Done():
			sawCancel.Store(true)
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := multipartReq(t, map[string]string{"width": "10"}, part{"image", "a.png", encPNG(testImage(20, 20))}).WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- do(newServer(), req) }()
	<-started
	cancel()
	select {
	case rec := <-done:
		if !sawCancel.Load() {
			t.Fatal("resize did not see cancellation")
		}
		if rec.Body.Len() != 0 {
			t.Errorf("expected no body after cancel, got %q", rec.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return promptly after cancel")
	}
}

func TestConcurrencyLimited(t *testing.T) {
	s := newServer()
	limit := cap(s.sem)
	var cur, peak atomic.Int32
	stubResize(t, func(_ context.Context, src image.Image, o resizeOptions) (*image.NRGBA, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		cur.Add(-1)
		return image.NewNRGBA(image.Rect(0, 0, o.Width, src.Bounds().Dy())), nil
	})
	img := encPNG(testImage(20, 20))
	const n = 8
	errs := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			errs <- do(s, multipartReq(t, map[string]string{"width": "10"}, part{"image", "a.png", img})).Code
		}()
	}
	for i := 0; i < n; i++ {
		if code := <-errs; code != 200 {
			t.Errorf("status %d", code)
		}
	}
	if int(peak.Load()) > limit {
		t.Fatalf("peak concurrency %d exceeds limit %d", peak.Load(), limit)
	}
}

func TestStaticAndMethod(t *testing.T) {
	s := newServer()
	rec := do(s, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("index: %d", rec.Code)
	}
	for _, p := range []string{"/app.js", "/styles.css"} {
		if rec := do(s, httptest.NewRequest(http.MethodGet, p, nil)); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := do(s, httptest.NewRequest(http.MethodGet, "/api/resize", nil)); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/resize: %d", rec.Code)
	}
}

func TestSafeBase(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":            "photo",
		`C:\x\y\holiday 1.png`: "holiday 1",
		"../../etc/passwd":     "passwd",
		`we"ird;name.webp`:     "we_ird_name",
		".png":                 "image",
		"日本.jpg":               "日本",
	}
	for in, want := range cases {
		if got := safeBase(in); got != want {
			t.Errorf("safeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResizeOutputTooManyPixels(t *testing.T) {
	called := false
	stubResize(t, func(ctx context.Context, src image.Image, o resizeOptions) (*image.NRGBA, error) {
		called = true
		return simpleStub(nil)(ctx, src, o)
	})
	// 4000x4000 -> 16000x16000 is within 4x per side but 256 MP.
	src := image.NewGray(image.Rect(0, 0, 4000, 4000))
	rec := do(newServer(), multipartReq(t, map[string]string{"width": "16000", "height": "16000"}, part{"image", "a.png", encPNG(src)}))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(errMsg(t, rec), "50 megapixels") || called {
		t.Fatalf("status %d called=%v: %s", rec.Code, called, rec.Body)
	}
	// Width only, height kept: 16000x4000 = 64 MP is also too large.
	rec = do(newServer(), multipartReq(t, map[string]string{"width": "16000"}, part{"image", "a.png", encPNG(src)}))
	if rec.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("width only: status %d called=%v", rec.Code, called)
	}
}

func TestSafeBaseTruncatesOnRuneBoundary(t *testing.T) {
	got := safeBase(strings.Repeat("a", 119) + "日本.jpg")
	if !utf8.ValidString(got) || got != strings.Repeat("a", 119) {
		t.Fatalf("safeBase = %q", got)
	}
	if got := safeBase(strings.Repeat("a", 119) + " b.jpg"); got != strings.Repeat("a", 119) {
		t.Fatalf("trailing space kept: %q", got)
	}
}
