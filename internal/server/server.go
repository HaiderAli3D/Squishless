// Package server implements the Caire Studio HTTP API: static UI files, sample
// listing, and seam-carving jobs with live progress streamed over SSE.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	// Input decoders.
	_ "image/gif"

	_ "golang.org/x/image/bmp"
)

const (
	maxImageBytes  = 25 << 20   // per uploaded image
	maxMaskBytes   = 25 << 20   // per uploaded mask
	maxBodyBytes   = 80 << 20   // whole multipart request
	maxMegapixels  = 40_000_000 // decoded pixel count limit
	maxTargetSide  = 10000      // upper bound for requested width/height (non-percentage)
	pingInterval   = 15 * time.Second
	multipartInMem = 32 << 20
	maxDecoding    = 2 // job requests parsed and decoded at the same time
)

// Params mirrors the "params" JSON form field of POST /api/jobs.
type Params struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Percentage bool    `json:"percentage"`
	Square     bool    `json:"square"`
	Blur       int     `json:"blur"`
	Sobel      int     `json:"sobel"`
	Debug      bool    `json:"debug"`
	Shape      string  `json:"shape"`
	SeamColor  string  `json:"seamColor"`
	Face       bool    `json:"face"`
	FaceAngle  float64 `json:"faceAngle"`
}

// defaultParams returns the CLI defaults; fields present in the request JSON override them.
func defaultParams() Params {
	return Params{Blur: 4, Sobel: 2, Shape: "circle", SeamColor: "#ff0000"}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (p *Params) validate() error {
	if p.Width < 0 || p.Height < 0 {
		return errors.New("width and height must not be negative")
	}
	if p.Width == 0 && p.Height == 0 {
		return errors.New("please provide a new width and/or height")
	}
	if p.Square && (p.Width == 0 || p.Height == 0) {
		return errors.New("the square option needs both a width and a height")
	}
	if p.Percentage {
		if p.Width > 99 || p.Height > 99 {
			return errors.New("percentage values must be between 1 and 99 (percentage only shrinks)")
		}
	} else if p.Width > maxTargetSide || p.Height > maxTargetSide {
		return fmt.Errorf("width and height must be at most %d", maxTargetSide)
	}
	if p.Blur < 0 || p.Blur > 50 {
		return errors.New("blur must be between 0 and 50")
	}
	if p.Sobel < 0 || p.Sobel > 50 {
		return errors.New("sobel must be between 0 and 50")
	}
	if p.Shape == "" {
		p.Shape = "circle"
	}
	if p.Shape != "circle" && p.Shape != "line" {
		return errors.New(`shape must be "circle" or "line"`)
	}
	if p.SeamColor == "" {
		p.SeamColor = "#ff0000"
	}
	if !hexColor.MatchString(p.SeamColor) {
		return errors.New("seamColor must look like #rrggbb")
	}
	if p.FaceAngle != p.FaceAngle || p.FaceAngle < -360 || p.FaceAngle > 360 {
		return errors.New("faceAngle must be between -360 and 360")
	}
	return nil
}

// Server is the HTTP handler for Caire Studio.
type Server struct {
	static   fs.FS
	jobs     *Manager
	mux      *http.ServeMux
	decoding chan struct{} // semaphore bounding concurrent upload decoding
}

// New returns the Caire Studio handler. static is the web root (index.html,
// app.js, styles.css, samples/...).
func New(static fs.FS) http.Handler {
	return newServer(static)
}

func newServer(static fs.FS) *Server {
	s := &Server{static: static, jobs: NewManager(), mux: http.NewServeMux(), decoding: make(chan struct{}, maxDecoding)}
	s.mux.HandleFunc("GET /api/samples", s.handleSamples)
	s.mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	s.mux.HandleFunc("GET /api/jobs/{id}/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/jobs/{id}/result", s.handleResult)
	s.mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	s.mux.Handle("GET /", http.FileServerFS(static))
	return s
}

// ServeHTTP rejects requests addressed to a host name other than localhost (DNS
// rebinding) and cross-origin writes (any web page could otherwise start jobs that
// cancel the user's run), then dispatches to the routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowedHost(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "unexpected Host header; open Caire Studio via localhost or an IP address")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// allowedHost accepts localhost and IP literals. DNS rebinding needs a host name the
// attacker controls, so IP literals stay usable when listening on a LAN address.
func allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host) != nil
}

// sameOrigin reports whether a state-changing request comes from the Studio page
// itself. Requests without an Origin header (curl, scripts) are allowed unless the
// browser marks them as cross-site.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- samples ----

type sample struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func isImageName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp":
		return true
	}
	return false
}

func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	out := []sample{}
	entries, err := fs.ReadDir(s.static, "samples")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !isImageName(e.Name()) {
				continue
			}
			f, err := s.static.Open("samples/" + e.Name())
			if err != nil {
				continue
			}
			cfg, _, err := image.DecodeConfig(f)
			f.Close()
			if err != nil {
				continue
			}
			out = append(out, sample{Name: e.Name(), URL: "/samples/" + e.Name(), Width: cfg.Width, Height: cfg.Height})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

// ---- jobs ----

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &httpError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

// decodeUpload decodes a multipart file, enforcing byte and pixel limits.
func decodeUpload(fh *multipart.FileHeader, field string, maxBytes int64) (image.Image, error) {
	if fh.Size > maxBytes {
		return nil, &httpError{http.StatusRequestEntityTooLarge, fmt.Sprintf("%s is larger than %d MB", field, maxBytes>>20)}
	}
	f, err := fh.Open()
	if err != nil {
		return nil, badRequest("cannot read %s: %v", field, err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, badRequest("%s: unsupported or corrupt image (jpg, png, bmp, gif)", field)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, badRequest("%s has an invalid size", field)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxMegapixels {
		return nil, &httpError{http.StatusRequestEntityTooLarge, fmt.Sprintf("%s is %dx%d; the limit is %d megapixels", field, cfg.Width, cfg.Height, maxMegapixels/1_000_000)}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, badRequest("cannot read %s: %v", field, err)
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, badRequest("%s: cannot decode image: %v", field, err)
	}
	return img, nil
}

func optionalFile(form *multipart.Form, field string) *multipart.FileHeader {
	if fhs := form.File[field]; len(fhs) > 0 {
		return fhs[0]
	}
	return nil
}

func (s *Server) parseJobRequest(w http.ResponseWriter, r *http.Request) (*jobInput, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseMultipartForm(multipartInMem); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, &httpError{http.StatusRequestEntityTooLarge, "request body too large"}
		}
		return nil, badRequest("expected multipart/form-data: %v", err)
	}
	form := r.MultipartForm

	params := defaultParams()
	if raw := strings.TrimSpace(r.FormValue("params")); raw != "" {
		dec := json.NewDecoder(strings.NewReader(raw))
		if err := dec.Decode(&params); err != nil {
			return nil, badRequest("invalid params JSON: %v", err)
		}
	}
	if err := params.validate(); err != nil {
		return nil, badRequest("%s", err.Error())
	}

	fh := optionalFile(form, "image")
	if fh == nil {
		return nil, badRequest("missing image file")
	}
	img, err := decodeUpload(fh, "image", maxImageBytes)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	in := &jobInput{params: params, img: img}

	for _, field := range []string{"mask", "rmask"} {
		mfh := optionalFile(form, field)
		if mfh == nil {
			continue
		}
		m, err := decodeUpload(mfh, field, maxMaskBytes)
		if err != nil {
			return nil, err
		}
		if m.Bounds().Dx() != b.Dx() || m.Bounds().Dy() != b.Dy() {
			return nil, badRequest("%s is %dx%d but the image is %dx%d; masks must match the image size",
				field, m.Bounds().Dx(), m.Bounds().Dy(), b.Dx(), b.Dy())
		}
		if field == "mask" {
			in.mask = m
		} else {
			in.rmask = m
		}
	}
	return in, nil
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	// Each request holds a fully decoded image (plus masks); bound how many exist at once.
	select {
	case s.decoding <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	in, err := s.parseJobRequest(w, r)
	<-s.decoding
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			writeError(w, he.status, he.msg)
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	job := s.jobs.Start(in)
	b := in.img.Bounds()
	writeJSON(w, http.StatusOK, map[string]any{"id": job.ID, "srcWidth": b.Dx(), "srcHeight": b.Dy()})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	job := s.jobs.Get(r.PathValue("id"))
	if job == nil {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	job.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	job := s.jobs.Get(r.PathValue("id"))
	if job == nil {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	res, errMsg, finished := job.Result()
	if !finished {
		writeError(w, http.StatusConflict, "job is not finished")
		return
	}
	if res == nil {
		writeError(w, http.StatusConflict, "job failed: "+errMsg)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "png"
	}
	if format == "jpeg" {
		format = "jpg"
	}
	if format != "png" && format != "jpg" {
		writeError(w, http.StatusBadRequest, `format must be "png" or "jpg"`)
		return
	}
	b := res.Bounds()
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="caire-%dx%d.%s"`, b.Dx(), b.Dy(), format))
	w.Header().Set("Cache-Control", "no-store")
	if format == "png" {
		w.Header().Set("Content-Type", "image/png")
		_ = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, res)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_ = jpeg.Encode(w, res, &jpeg.Options{Quality: 95})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	job := s.jobs.Get(r.PathValue("id"))
	if job == nil {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	replay, sub := job.Subscribe()
	defer job.Unsubscribe(sub)

	for _, ev := range replay {
		if err := writeEvent(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()
	if sub == nil { // already finished; replay contained the terminal event
		return
	}

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-sub.ch:
			if err := writeEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-job.done:
			// Drain anything still buffered, then send the terminal event.
			for {
				select {
				case ev := <-sub.ch:
					if err := writeEvent(w, ev); err != nil {
						return
					}
					continue
				default:
				}
				break
			}
			if t := job.terminalEvent(); t != nil {
				_ = writeEvent(w, *t)
			}
			flusher.Flush()
			return
		}
	}
}

func writeEvent(w io.Writer, ev event) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
	return err
}
