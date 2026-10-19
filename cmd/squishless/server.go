package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "image/gif"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

//go:embed web
var webFS embed.FS

const (
	maxUploadBytes = 30 << 20   // per image file
	maxPixels      = 50_000_000 // 50 MP
	maxScale       = 4          // target may be at most 4x the source per dimension

	// Energy tuning; defaults mirror fastcarve.DefaultBlurRadius / DefaultSobelThreshold.
	defaultBlurRadius     = 2
	maxBlurRadius         = 7
	defaultSobelThreshold = 0
	maxSobelThreshold     = 64
)

// resizeOptions mirrors fastcarve.Options so the HTTP layer can be tested
// without the engine (see engine.go).
type resizeOptions struct {
	Width, Height int
	ProtectFaces  bool
	Protect       *image.Gray
	Remove        *image.Gray

	BlurRadius     int     // 0..7, box blur before Sobel
	SobelThreshold float32 // 0..64, Sobel magnitudes at or below become 0
}

// resizeFn performs the content-aware resize. engine.go wires it to
// fastcarve.Resize; tests replace it with a stub.
var resizeFn func(ctx context.Context, src image.Image, opt resizeOptions) (*image.NRGBA, error)

type server struct {
	sem chan struct{}
	mux *http.ServeMux
}

func newServer() *server {
	n := runtime.NumCPU() / 2
	if n < 1 {
		n = 1
	}
	s := &server{sem: make(chan struct{}, n), mux: http.NewServeMux()}
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("/api/resize", s.handleResize)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &httpError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *server) handleResize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if resizeFn == nil {
		writeJSONError(w, http.StatusInternalServerError, "resize engine not available")
		return
	}
	// Image + two masks + small fields.
	r.Body = http.MaxBytesReader(w, r.Body, 3*maxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "upload is too large (max 30 MB per image)")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "expected a multipart form with an image")
		return
	}
	defer r.MultipartForm.RemoveAll()

	req, err := parseRequest(r)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			writeJSONError(w, he.status, he.msg)
		} else {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	ctx := r.Context()
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return // client gave up while queued
	}
	start := time.Now()
	out, err := func() (*image.NRGBA, error) {
		defer func() { <-s.sem }()
		return resizeFn(ctx, req.src, req.opt)
	}()
	elapsed := time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return // client disconnected; nobody to answer
		}
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var buf bytes.Buffer
	var ctype string
	if req.ext == "jpg" {
		ctype = "image/jpeg"
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: req.quality})
	} else {
		ctype = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, out)
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "encoding failed: "+err.Error())
		return
	}

	b := out.Bounds()
	name := fmt.Sprintf("%s-%dx%d.%s", req.base, b.Dx(), b.Dy(), req.ext)
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	h.Set("Content-Disposition", contentDisposition(name))
	h.Set("X-Width", strconv.Itoa(b.Dx()))
	h.Set("X-Height", strconv.Itoa(b.Dy()))
	h.Set("X-Elapsed-Ms", strconv.FormatInt(elapsed.Milliseconds(), 10))
	h.Set("Cache-Control", "no-store")
	if _, err := w.Write(buf.Bytes()); err != nil && ctx.Err() == nil {
		log.Printf("write response: %v", err)
	}
}

type resizeRequest struct {
	src     image.Image
	opt     resizeOptions
	ext     string // "jpg" or "png"
	quality int
	base    string
}

func parseRequest(r *http.Request) (*resizeRequest, error) {
	f, fh, err := r.FormFile("image")
	if err != nil {
		return nil, badRequest("missing image file")
	}
	defer f.Close()
	if fh.Size > maxUploadBytes {
		return nil, &httpError{http.StatusRequestEntityTooLarge, "image is larger than 30 MB"}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, badRequest("could not read image")
	}
	if len(data) > maxUploadBytes {
		return nil, &httpError{http.StatusRequestEntityTooLarge, "image is larger than 30 MB"}
	}
	src, format, err := decodeLimited(data)
	if err != nil {
		return nil, err
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()

	width, err := intField(r, "width", 0)
	if err != nil {
		return nil, err
	}
	height, err := intField(r, "height", 0)
	if err != nil {
		return nil, err
	}
	if width < 0 || height < 0 {
		return nil, badRequest("width and height must be positive (0 keeps the original)")
	}
	if width == 0 && height == 0 {
		return nil, badRequest("set a target width or height")
	}
	if width > maxScale*sw || height > maxScale*sh {
		return nil, badRequest("target is more than 4x the original size (%dx%d)", sw, sh)
	}
	tw, th := width, height
	if tw == 0 {
		tw = sw
	}
	if th == 0 {
		th = sh
	}
	if int64(tw)*int64(th) > maxPixels {
		return nil, &httpError{http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the result would be %dx%d; the limit is 50 megapixels", tw, th)}
	}

	quality, err := intField(r, "quality", 92)
	if err != nil {
		return nil, err
	}
	if quality < 1 || quality > 100 {
		return nil, badRequest("quality must be between 1 and 100")
	}

	var ext string
	switch strings.ToLower(r.FormValue("format")) {
	case "jpg", "jpeg":
		ext = "jpg"
	case "png":
		ext = "png"
	case "", "same":
		if format == "jpeg" {
			ext = "jpg"
		} else {
			ext = "png"
		}
	default:
		return nil, badRequest("format must be jpg, png or same")
	}

	faces := false
	switch r.FormValue("faces") {
	case "1", "true", "on":
		faces = true
	case "", "0", "false", "off":
	default:
		return nil, badRequest("faces must be 1 or 0")
	}

	blur, err := intField(r, "blur", defaultBlurRadius)
	if err != nil {
		return nil, err
	}
	if blur < 0 || blur > maxBlurRadius {
		return nil, badRequest("blur must be between 0 and %d", maxBlurRadius)
	}
	sobel, err := floatField(r, "sobel", defaultSobelThreshold)
	if err != nil {
		return nil, err
	}
	if sobel < 0 || sobel > maxSobelThreshold {
		return nil, badRequest("sobel must be between 0 and %d", maxSobelThreshold)
	}

	protect, err := maskField(r, "protect", sw, sh)
	if err != nil {
		return nil, err
	}
	remove, err := maskField(r, "remove", sw, sh)
	if err != nil {
		return nil, err
	}

	return &resizeRequest{
		src: src,
		opt: resizeOptions{
			Width: width, Height: height, ProtectFaces: faces,
			Protect: protect, Remove: remove,
			BlurRadius: blur, SobelThreshold: float32(sobel),
		},
		ext:     ext,
		quality: quality,
		base:    safeBase(fh.Filename),
	}, nil
}

func decodeLimited(data []byte) (image.Image, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", &httpError{http.StatusUnsupportedMediaType, "unsupported or damaged image (use JPG, PNG, WebP, BMP or GIF)"}
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", badRequest("image has no pixels")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, "", &httpError{http.StatusRequestEntityTooLarge,
			fmt.Sprintf("image is %dx%d; the limit is 50 megapixels", cfg.Width, cfg.Height)}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", &httpError{http.StatusUnsupportedMediaType, "unsupported or damaged image"}
	}
	return img, format, nil
}

func intField(r *http.Request, name string, def int) (int, error) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, badRequest("%s must be a whole number", name)
	}
	return n, nil
}

// floatField parses an optional finite number; NaN and infinities are rejected.
func floatField(r *http.Request, name string, def float64) (float64, error) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, badRequest("%s must be a number", name)
	}
	return f, nil
}

func maskField(r *http.Request, name string, w, h int) (*image.Gray, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File[name]) == 0 {
		return nil, nil
	}
	f, err := r.MultipartForm.File[name][0].Open()
	if err != nil {
		return nil, badRequest("could not read %s mask", name)
	}
	defer f.Close()
	return decodeMask(f, name, w, h)
}

func decodeMask(f multipart.File, name string, w, h int) (*image.Gray, error) {
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return nil, badRequest("%s mask must be a PNG", name)
	}
	if cfg.Width != w || cfg.Height != h {
		return nil, badRequest("%s mask is %dx%d but the image is %dx%d", name, cfg.Width, cfg.Height, w, h)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, badRequest("could not read %s mask", name)
	}
	m, err := png.Decode(f)
	if err != nil {
		return nil, badRequest("%s mask is not a valid PNG", name)
	}
	if g, ok := m.(*image.Gray); ok && g.Rect.Min == (image.Point{}) {
		return g, nil
	}
	g := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(g, g.Rect, m, m.Bounds().Min, draw.Src)
	return g, nil
}

// safeBase returns the upload's base name without extension, restricted to
// characters that are safe in a file name.
func safeBase(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), " .")
	if out == "" {
		out = "image"
	}
	if len(out) > 120 {
		out = out[:120]
		for !utf8.ValidString(out) { // do not cut a multi-byte character in half
			out = out[:len(out)-1]
		}
		out = strings.TrimRight(out, " .")
	}
	return out
}

func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}
