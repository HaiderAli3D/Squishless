package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*7 + y*13 + (x*y)%17) % 256)
			img.Set(x, y, color.NRGBA{v, uint8(x * 255 / w), uint8(y * 255 / h), 255})
		}
	}
	return img
}

func pngBytes(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	static := fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><title>Caire Studio</title>")},
		"samples/sample.png": {Data: pngBytes(t, testImage(32, 24))},
		"samples/notes.txt":  {Data: []byte("not an image")},
	}
	ts := httptest.NewServer(New(static))
	t.Cleanup(ts.Close)
	return ts
}

type files map[string][]byte

func postJob(t *testing.T, ts *httptest.Server, fs files, params any) (*http.Response, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, data := range fs {
		fw, err := mw.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	if params != nil {
		var raw []byte
		switch p := params.(type) {
		case string:
			raw = []byte(p)
		default:
			raw, _ = json.Marshal(p)
		}
		mw.WriteField("params", string(raw))
	}
	mw.Close()
	resp, err := http.Post(ts.URL+"/api/jobs", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

type sseEvent struct {
	name string
	data map[string]any
}

// readEvents reads SSE events until a terminal event, calling onEvent for each.
func readEvents(t *testing.T, ts *httptest.Server, id string, timeout time.Duration, onEvent func(sseEvent)) []sseEvent {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/jobs/" + id + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	type result struct {
		evs []sseEvent
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var evs []sseEvent
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var d map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d); err != nil {
					ch <- result{evs, err}
					return
				}
				ev := sseEvent{name, d}
				evs = append(evs, ev)
				if onEvent != nil {
					onEvent(ev)
				}
				if name == "done" || name == "error" {
					ch <- result{evs, nil}
					return
				}
			}
		}
		ch <- result{evs, fmt.Errorf("stream ended without terminal event: %v", sc.Err())}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.evs
	case <-time.After(timeout):
		resp.Body.Close()
		t.Fatal("timed out waiting for events")
	}
	return nil
}

func byName(evs []sseEvent, name string) []sseEvent {
	var out []sseEvent
	for _, e := range evs {
		if e.name == name {
			out = append(out, e)
		}
	}
	return out
}

func num(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func TestStaticAndSamples(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Caire Studio") {
		t.Fatalf("index: %d %q", resp.StatusCode, b)
	}

	resp, err = http.Get(ts.URL + "/api/samples")
	if err != nil {
		t.Fatal(err)
	}
	var samples []sample
	json.NewDecoder(resp.Body).Decode(&samples)
	resp.Body.Close()
	if len(samples) != 1 || samples[0].Name != "sample.png" || samples[0].URL != "/samples/sample.png" ||
		samples[0].Width != 32 || samples[0].Height != 24 {
		t.Fatalf("samples: %+v", samples)
	}
	resp, _ = http.Get(ts.URL + samples[0].URL)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sample file status %d", resp.StatusCode)
	}
}

func TestJobLifecycle(t *testing.T) {
	ts := newTestServer(t)
	resp, out := postJob(t, ts, files{"image": pngBytes(t, testImage(80, 60))},
		map[string]any{"width": 70, "debug": true})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	id, _ := out["id"].(string)
	if id == "" || num(out, "srcWidth") != 80 || num(out, "srcHeight") != 60 {
		t.Fatalf("bad create response %v", out)
	}

	evs := readEvents(t, ts, id, 60*time.Second, nil)
	starts := byName(evs, "start")
	if len(starts) == 0 {
		t.Fatal("no start event")
	}
	s := starts[0].data
	if num(s, "width") != 80 || num(s, "height") != 60 || num(s, "targetWidth") != 70 || num(s, "targetHeight") != 60 || num(s, "total") != 10 {
		t.Fatalf("start %v", s)
	}
	progs := byName(evs, "progress")
	if len(progs) == 0 {
		t.Fatal("no progress events")
	}
	last := progs[len(progs)-1].data
	if num(last, "done") != 10 || num(last, "total") != 10 || num(last, "width") != 70 || last["pass"] != "horizontal" {
		t.Fatalf("last progress %v", last)
	}
	frames := byName(evs, "frame")
	if len(frames) < 2 {
		t.Fatalf("want first and final frame, got %d", len(frames))
	}
	first := frames[0].data
	if seams, ok := first["seams"].([]any); !ok || len(seams) == 0 {
		t.Fatalf("first frame should carry seams in debug mode: %v", first["seams"])
	}
	fl := frames[len(frames)-1].data
	if num(fl, "width") != 70 || num(fl, "height") != 60 {
		t.Fatalf("final frame dims %v x %v", fl["width"], fl["height"])
	}
	jb, _ := fl["jpeg"].(string)
	raw, err := decodeB64(jb)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 70 {
		t.Fatalf("frame jpeg: %v %+v", err, cfg)
	}
	done := evs[len(evs)-1]
	if done.name != "done" || num(done.data, "width") != 70 || num(done.data, "height") != 60 {
		t.Fatalf("terminal %v", done)
	}

	// Late subscriber gets a replay ending with done.
	replay := readEvents(t, ts, id, 5*time.Second, nil)
	if len(byName(replay, "start")) != 1 || len(byName(replay, "progress")) != 1 ||
		len(byName(replay, "frame")) != 1 || replay[len(replay)-1].name != "done" {
		t.Fatalf("replay: %v", names(replay))
	}

	for _, format := range []string{"png", "jpg"} {
		resp, err := http.Get(ts.URL + "/api/jobs/" + id + "/result?format=" + format)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if b := img.Bounds(); b.Dx() != 70 || b.Dy() != 60 {
			t.Fatalf("%s result %v", format, b)
		}
		want := fmt.Sprintf(`attachment; filename="caire-70x60.%s"`, format)
		if cd := resp.Header.Get("Content-Disposition"); cd != want {
			t.Fatalf("content-disposition %q", cd)
		}
	}

	resp, _ = http.Get(ts.URL + "/api/jobs/nope/result")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown result status %d", resp.StatusCode)
	}
}

func TestPercentageKeepsGivenShare(t *testing.T) {
	ts := newTestServer(t)
	_, out := postJob(t, ts, files{"image": pngBytes(t, testImage(80, 60))},
		map[string]any{"width": 70, "percentage": true})
	evs := readEvents(t, ts, out["id"].(string), 60*time.Second, nil)
	done := evs[len(evs)-1]
	if done.name != "done" || num(done.data, "width") != 56 || num(done.data, "height") != 60 {
		t.Fatalf("terminal %v", done)
	}
}

func TestBothDimensions(t *testing.T) {
	ts := newTestServer(t)
	_, out := postJob(t, ts, files{"image": pngBytes(t, testImage(80, 60))},
		map[string]any{"width": 60, "height": 50})
	evs := readEvents(t, ts, out["id"].(string), 60*time.Second, nil)
	done := evs[len(evs)-1]
	if done.name != "done" || num(done.data, "width") != 60 || num(done.data, "height") != 50 {
		t.Fatalf("terminal %v", done)
	}
}

func names(evs []sseEvent) []string {
	var n []string
	for _, e := range evs {
		n = append(n, e.name)
	}
	return n
}

func TestValidation(t *testing.T) {
	ts := newTestServer(t)
	img := pngBytes(t, testImage(40, 30))
	cases := []struct {
		name   string
		files  files
		params any
	}{
		{"no image", files{}, map[string]any{"width": 20}},
		{"no size", files{"image": img}, map[string]any{}},
		{"no params", files{"image": img}, nil},
		{"bad json", files{"image": img}, "{nope"},
		{"square one dim", files{"image": img}, map[string]any{"width": 20, "square": true}},
		{"blur range", files{"image": img}, map[string]any{"width": 20, "blur": 51}},
		{"sobel range", files{"image": img}, map[string]any{"width": 20, "sobel": -1}},
		{"bad shape", files{"image": img}, map[string]any{"width": 20, "shape": "star"}},
		{"bad color", files{"image": img}, map[string]any{"width": 20, "seamColor": "red"}},
		{"not an image", files{"image": []byte("hello")}, map[string]any{"width": 20}},
		{"mask size", files{"image": img, "mask": pngBytes(t, testImage(10, 10))}, map[string]any{"width": 20}},
		{"rmask size", files{"image": img, "rmask": pngBytes(t, testImage(41, 30))}, map[string]any{"width": 20}},
	}
	for _, c := range cases {
		resp, out := postJob(t, ts, c.files, c.params)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d", c.name, resp.StatusCode)
		}
		if msg, _ := out["error"].(string); msg == "" {
			t.Errorf("%s: missing error message: %v", c.name, out)
		}
	}

	// Matching masks are accepted.
	mask := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for x := 10; x < 20; x++ {
		for y := 0; y < 30; y++ {
			mask.Set(x, y, color.White)
		}
	}
	resp, out := postJob(t, ts, files{"image": img, "mask": pngBytes(t, mask)}, map[string]any{"width": 35})
	if resp.StatusCode != 200 {
		t.Fatalf("mask job: %d %v", resp.StatusCode, out)
	}
	evs := readEvents(t, ts, out["id"].(string), 30*time.Second, nil)
	if last := evs[len(evs)-1]; last.name != "done" || num(last.data, "width") != 35 {
		t.Fatalf("mask job terminal %v", last)
	}
}

func TestCancel(t *testing.T) {
	ts := newTestServer(t)
	_, out := postJob(t, ts, files{"image": pngBytes(t, testImage(500, 300))}, map[string]any{"width": 100})
	id := out["id"].(string)

	cancelled := false
	evs := readEvents(t, ts, id, 60*time.Second, func(ev sseEvent) {
		if ev.name == "frame" && !cancelled {
			cancelled = true
			resp, err := http.Post(ts.URL+"/api/jobs/"+id+"/cancel", "", nil)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("cancel status %d", resp.StatusCode)
			}
		}
	})
	last := evs[len(evs)-1]
	if last.name != "error" || last.data["error"] != "cancelled" {
		t.Fatalf("want cancelled error, got %v", last)
	}
	resp, _ := http.Get(ts.URL + "/api/jobs/" + id + "/result")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("result of cancelled job: %d", resp.StatusCode)
	}
}

func TestSecondJobCancelsFirst(t *testing.T) {
	ts := newTestServer(t)
	_, a := postJob(t, ts, files{"image": pngBytes(t, testImage(500, 300))}, map[string]any{"width": 100})
	idA := a["id"].(string)

	var idB string
	evsA := readEvents(t, ts, idA, 60*time.Second, func(ev sseEvent) {
		if ev.name == "start" && idB == "" {
			_, b := postJob(t, ts, files{"image": pngBytes(t, testImage(80, 60))}, map[string]any{"width": 75})
			idB, _ = b["id"].(string)
		}
	})
	if last := evsA[len(evsA)-1]; last.name != "error" || last.data["error"] != "cancelled" {
		t.Fatalf("job A: %v", last)
	}
	if idB == "" {
		t.Fatal("job B was not created")
	}
	evsB := readEvents(t, ts, idB, 60*time.Second, nil)
	if last := evsB[len(evsB)-1]; last.name != "done" || num(last.data, "width") != 75 {
		t.Fatalf("job B: %v", last)
	}
}

func TestEviction(t *testing.T) {
	m := NewManager()
	img := testImage(20, 10)
	var ids []string
	for i := 0; i < maxFinishedJobs+3; i++ {
		j := m.Start(&jobInput{params: Params{Width: 19, Blur: 1, Sobel: 2, Shape: "circle", SeamColor: "#ff0000"}, img: img})
		<-j.done
		ids = append(ids, j.ID)
	}
	time.Sleep(50 * time.Millisecond) // markFinished runs right after done is closed
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) != maxFinishedJobs {
		t.Fatalf("kept %d jobs", len(m.jobs))
	}
	if _, ok := m.jobs[ids[0]]; ok {
		t.Fatal("oldest job not evicted")
	}
	if _, ok := m.jobs[ids[len(ids)-1]]; !ok {
		t.Fatal("newest job evicted")
	}
}

func TestScaleSeams(t *testing.T) {
	if scaleSeams(nil, 1, 1, 10, 10, 0) != nil {
		t.Fatal("nil seams should stay nil")
	}
	w, h := previewSize(4000, 2000, 1000)
	if w != 1000 || h != 500 {
		t.Fatalf("previewSize %d %d", w, h)
	}
}

func decodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func runJob(t *testing.T, ts *httptest.Server, img image.Image, params map[string]any) sseEvent {
	t.Helper()
	resp, out := postJob(t, ts, files{"image": pngBytes(t, img)}, params)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	evs := readEvents(t, ts, out["id"].(string), 60*time.Second, nil)
	return evs[len(evs)-1]
}

func TestPercentageMatchesUIEstimate(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name   string
		w, h   int
		params map[string]any
		wantW  int
		wantH  int
	}{
		// Square sources used to hit caire's uniform-scale shortcut.
		{"square width only", 60, 60, map[string]any{"width": 50, "percentage": true}, 30, 60},
		{"square both", 60, 60, map[string]any{"width": 50, "height": 80, "percentage": true}, 30, 48},
		// Square + percent used to read the percentages as pixels.
		{"square option", 90, 60, map[string]any{"width": 50, "height": 50, "percentage": true, "square": true}, 30, 30},
		// caire truncated: 1% of 60 failed as "enlargement", 70% of 101 gave 70.
		{"one percent", 60, 40, map[string]any{"width": 1, "percentage": true}, 1, 40},
		{"rounding", 101, 40, map[string]any{"width": 70, "percentage": true}, 71, 40},
	}
	for _, c := range cases {
		done := runJob(t, ts, testImage(c.w, c.h), c.params)
		if done.name != "done" || num(done.data, "width") != c.wantW || num(done.data, "height") != c.wantH {
			t.Errorf("%s: want %dx%d, got %v", c.name, c.wantW, c.wantH, done)
		}
	}
}

func TestKeepPixelsAndAngle(t *testing.T) {
	for _, c := range []struct{ size, pct, want int }{{150, 1, 2}, {60, 1, 1}, {101, 70, 71}, {80, 70, 56}, {80, 0, 0}} {
		if got := keepPixels(c.size, c.pct); got != c.want {
			t.Errorf("keepPixels(%d, %d) = %d, want %d", c.size, c.pct, got, c.want)
		}
	}
	for _, c := range []struct{ deg, want float64 }{{0, 0}, {90, 0.25}, {-90, 0.75}, {180, 0.5}, {-180, 0.5}, {360, 0}} {
		if got := degreesToTurns(c.deg); got != c.want {
			t.Errorf("degreesToTurns(%v) = %v, want %v", c.deg, got, c.want)
		}
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	ts := newTestServer(t)
	do := func(method, path, host, origin string) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("GET", "/api/samples", "evil.example:7777", ""); got != http.StatusMisdirectedRequest {
		t.Errorf("foreign Host: %d", got)
	}
	for _, host := range []string{"localhost:7777", "127.0.0.1:7777", "[::1]:7777", "192.168.1.5:7777"} {
		if got := do("GET", "/api/samples", host, ""); got != 200 {
			t.Errorf("Host %s: %d", host, got)
		}
	}
	if got := do("POST", "/api/jobs", "", "http://evil.example"); got != http.StatusForbidden {
		t.Errorf("cross-origin POST: %d", got)
	}
	if got := do("POST", "/api/jobs/x/cancel", "", "null"); got != http.StatusForbidden {
		t.Errorf("opaque-origin POST: %d", got)
	}
	// Same-origin POST reaches the handler (400: no multipart body).
	if got := do("POST", "/api/jobs", "", ts.URL); got != http.StatusBadRequest {
		t.Errorf("same-origin POST: %d", got)
	}
}

func testParams(width int) Params {
	return Params{Width: width, Blur: 1, Sobel: 2, Shape: "circle", SeamColor: "#ff0000"}
}

func TestFailedJobsKeepResults(t *testing.T) {
	m := NewManager()
	img := testImage(20, 10)
	good := m.Start(&jobInput{params: testParams(19), img: img})
	<-good.done
	for i := 0; i < maxFailedJobs+5; i++ {
		p := testParams(10)
		p.Square = true // caire rejects square without a height
		j := m.Start(&jobInput{params: p, img: img})
		<-j.done
		if _, msg, _ := j.Result(); msg == "" {
			t.Fatal("expected the job to fail")
		}
	}
	time.Sleep(50 * time.Millisecond) // markFinished runs right after done is closed
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[good.ID] == nil {
		t.Fatal("failed jobs evicted the successful one")
	}
	if len(m.failed) != maxFailedJobs || len(m.jobs) != maxFailedJobs+1 {
		t.Fatalf("failed=%d jobs=%d", len(m.failed), len(m.jobs))
	}
}

func TestResultByteBudget(t *testing.T) {
	m := NewManager()
	m.resultBudget = 2000 // a 19x10 NRGBA result is 760 bytes, so two fit
	img := testImage(20, 10)
	var last *Job
	for i := 0; i < 5; i++ {
		last = m.Start(&jobInput{params: testParams(19), img: img})
		<-last.done
	}
	time.Sleep(50 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.finished) != 2 || m.resultBytes != 1520 || m.jobs[last.ID] == nil {
		t.Fatalf("finished=%d bytes=%d", len(m.finished), m.resultBytes)
	}
}

func TestStartReleasesSupersededInput(t *testing.T) {
	m := NewManager()
	img := testImage(20, 10)
	m.runMu.Lock() // pretend a long job is running, so the next ones queue
	a := m.Start(&jobInput{params: testParams(19), img: img})
	b := m.Start(&jobInput{params: testParams(18), img: img})
	if a.takeInput() != nil {
		t.Fatal("superseded queued job still holds its input")
	}
	if b.takeInput() == nil {
		t.Fatal("current job lost its input")
	}
	m.runMu.Unlock()
	<-a.done
	<-b.done
	if _, msg, _ := a.Result(); msg != "cancelled" {
		t.Fatalf("job A: %q", msg)
	}
	if res, _, _ := b.Result(); res == nil || res.Bounds().Dx() != 18 {
		t.Fatal("job B did not finish")
	}
}
