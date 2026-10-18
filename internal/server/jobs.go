package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/esimov/caire"
)

const (
	maxFinishedJobs = 20        // successful jobs whose results stay downloadable
	maxFailedJobs   = 20        // cancelled or failed jobs kept for event replay (no pixels)
	maxResultBytes  = 512 << 20 // decoded result pixels kept across successful jobs
	subscriberBuf   = 16
)

var errCancelled = errors.New("cancelled")

// event is one SSE message.
type event struct {
	name string
	data []byte
}

func mkEvent(name string, v any) event {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"marshal failure"}`)
	}
	return event{name: name, data: b}
}

type subscriber struct {
	ch chan event
}

// jobInput is a validated job request.
type jobInput struct {
	params Params
	img    image.Image
	mask   image.Image
	rmask  image.Image
}

// Job is a single seam-carving run.
type Job struct {
	ID     string
	input  *jobInput
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed once the terminal event is recorded

	mu       sync.Mutex
	start    *event
	progress *event
	frame    *event
	terminal *event
	subs     map[*subscriber]struct{}
	result   image.Image
	errMsg   string
	finished bool
}

// Cancel requests cancellation; it is a no-op for finished jobs.
func (j *Job) Cancel() { j.cancel() }

// takeInput returns the job input, or nil once it was released.
func (j *Job) takeInput() *jobInput {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.input
}

// releaseInput drops the decoded source and masks of a superseded job. A queued job
// never needs them again, and a running one already holds its own reference.
func (j *Job) releaseInput() {
	j.mu.Lock()
	j.input = nil
	j.mu.Unlock()
}

// resultBytes approximates the memory held by the job's result image.
func (j *Job) resultBytes() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return imageBytes(j.result)
}

func imageBytes(img image.Image) int64 {
	switch im := img.(type) {
	case nil:
		return 0
	case *image.NRGBA:
		return int64(len(im.Pix))
	case *image.RGBA:
		return int64(len(im.Pix))
	default:
		b := img.Bounds()
		return int64(b.Dx()) * int64(b.Dy()) * 4
	}
}

// Result returns the final image (nil on failure), the error message, and whether the job finished.
func (j *Job) Result() (image.Image, string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result, j.errMsg, j.finished
}

func (j *Job) terminalEvent() *event {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.terminal
}

// Subscribe returns the replay of the latest state and a live subscription.
// The subscription is nil when the job already finished (the replay then ends
// with the terminal event).
func (j *Job) Subscribe() ([]event, *subscriber) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var replay []event
	for _, ev := range []*event{j.start, j.progress, j.frame, j.terminal} {
		if ev != nil {
			replay = append(replay, *ev)
		}
	}
	if j.terminal != nil {
		return replay, nil
	}
	sub := &subscriber{ch: make(chan event, subscriberBuf)}
	j.subs[sub] = struct{}{}
	return replay, sub
}

// Unsubscribe removes a subscription (nil is ignored).
func (j *Job) Unsubscribe(sub *subscriber) {
	if sub == nil {
		return
	}
	j.mu.Lock()
	delete(j.subs, sub)
	j.mu.Unlock()
}

// publish records ev as the latest of its kind and fans it out without blocking.
// Slow subscribers miss intermediate events (they still get the terminal one).
func (j *Job) publish(ev event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal != nil {
		return
	}
	e := ev
	switch ev.name {
	case "start":
		j.start = &e
	case "progress":
		j.progress = &e
	case "frame":
		j.frame = &e
	}
	for sub := range j.subs {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// finish records the terminal state exactly once and wakes subscribers.
func (j *Job) finish(res image.Image, err error, elapsed time.Duration) {
	j.mu.Lock()
	if j.terminal != nil {
		j.mu.Unlock()
		return
	}
	var ev event
	if err != nil {
		j.errMsg = err.Error()
		ev = mkEvent("error", map[string]string{"error": j.errMsg})
	} else {
		j.result = res
		b := res.Bounds()
		ev = mkEvent("done", map[string]int{"elapsedMs": int(elapsed.Milliseconds()), "width": b.Dx(), "height": b.Dy()})
	}
	j.terminal = &ev
	j.finished = true
	j.input = nil // release source image and masks
	j.mu.Unlock()
	close(j.done)
	j.cancel()
}

// Manager owns jobs; only one job runs at a time because caire uses package globals.
type Manager struct {
	runMu sync.Mutex // serialises caire runs

	mu           sync.Mutex
	jobs         map[string]*Job
	finished     []*Job // successful jobs, oldest first
	failed       []*Job // cancelled or failed jobs, oldest first
	resultBytes  int64  // memory held by the results of finished
	resultBudget int64
	current      *Job
}

// NewManager returns an empty job manager.
func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*Job), resultBudget: maxResultBytes}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Get returns a job by id, or nil.
func (m *Manager) Get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// Start cancels any running job and starts a new one.
func (m *Manager) Start(in *jobInput) *Job {
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		ID:     newID(),
		input:  in,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		subs:   make(map[*subscriber]struct{}),
	}
	m.mu.Lock()
	if m.current != nil {
		m.current.Cancel()
		m.current.releaseInput()
	}
	m.current = j
	m.jobs[j.ID] = j
	m.mu.Unlock()

	go m.run(j)
	return j
}

// markFinished files a finished job under its outcome. Successful jobs are evicted by
// count and by the memory their results hold (the newest is always kept). Cancelled and
// failed jobs have their own list, so restarts never push results out of the history.
func (m *Manager) markFinished(j *Job) {
	size := j.resultBytes()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == j {
		m.current = nil
	}
	if size == 0 {
		m.failed = append(m.failed, j)
		for len(m.failed) > maxFailedJobs {
			delete(m.jobs, m.failed[0].ID)
			m.failed = m.failed[1:]
		}
		return
	}
	m.finished = append(m.finished, j)
	m.resultBytes += size
	for len(m.finished) > maxFinishedJobs || (m.resultBytes > m.resultBudget && len(m.finished) > 1) {
		old := m.finished[0]
		delete(m.jobs, old.ID)
		m.resultBytes -= old.resultBytes()
		m.finished = m.finished[1:]
	}
}

func (m *Manager) run(j *Job) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	defer m.markFinished(j)

	t0 := time.Now()
	in := j.takeInput()
	if j.ctx.Err() != nil || in == nil {
		j.finish(nil, errCancelled, time.Since(t0))
		return
	}
	res, err := m.carve(j, in, t0)
	if err != nil && (errors.Is(err, context.Canceled) || j.ctx.Err() != nil) {
		err = errCancelled
	}
	j.finish(res, err, time.Since(t0))
}

// carve runs caire for the job, publishing start/progress/frame events.
func (m *Manager) carve(j *Job, in *jobInput, t0 time.Time) (res image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("internal error: %v", r)
			log.Printf("caire panic: %v\n%s", r, debug.Stack())
		}
	}()

	p := in.params
	newW, newH := p.Width, p.Height
	if p.Percentage {
		// The API takes the percentage of the size to KEEP. Turn it into pixels here,
		// rounded like the UI, instead of using caire's percentage mode: that takes the
		// share to REMOVE, truncates, treats the values as pixels together with Square,
		// and takes a uniform-scale shortcut for square sources.
		src := in.img.Bounds()
		newW = keepPixels(src.Dx(), p.Width)
		newH = keepPixels(src.Dy(), p.Height)
	}
	proc := &caire.Processor{
		NewWidth:       newW,
		NewHeight:      newH,
		Square:         p.Square,
		BlurRadius:     p.Blur,
		SobelThreshold: p.Sobel,
		Debug:          p.Debug,
		ShapeType:      caire.ShapeType(p.Shape),
		SeamColor:      p.SeamColor,
		FaceDetect:     p.Face,
		FaceAngle:      degreesToTurns(p.FaceAngle),
		Preview:        false,
	}
	if in.mask != nil || in.rmask != nil {
		proc.SetMasks(in.mask, in.rmask)
	}

	var (
		total, done int
		started     bool
		pass        = "horizontal"
		thr         = throttle{interval: frameInterval}
	)
	progress := func(w, h int) {
		j.publish(mkEvent("progress", map[string]any{
			"done": done, "total": total, "width": w, "height": h,
			"pass": pass, "elapsedMs": time.Since(t0).Milliseconds(),
		}))
	}

	proc.OnStart = func(w, h, tw, th int) {
		started = true
		total = abs(w-tw) + abs(h-th)
		j.publish(mkEvent("start", map[string]int{"width": w, "height": h, "targetWidth": tw, "targetHeight": th, "total": total}))
	}
	proc.OnFrame = func(f caire.Frame) error {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		done++
		if f.Vertical {
			pass = "vertical"
		} else {
			pass = "horizontal"
		}
		if !thr.ready(time.Now()) {
			return nil
		}
		img, seams := f.Upright()
		b := img.Bounds()
		progress(b.Dx(), b.Dy())
		fp, err := buildFrame(img, seams, p.Debug, pass)
		if err != nil {
			return err
		}
		j.publish(mkEvent("frame", fp))
		return j.ctx.Err()
	}

	out, err := proc.Run(in.img)
	if err != nil {
		return nil, err
	}
	if err := j.ctx.Err(); err != nil {
		return nil, err
	}
	b := out.Bounds()
	if !started {
		// caire took a shortcut (e.g. plain resize) without carving.
		src := in.img.Bounds()
		j.publish(mkEvent("start", map[string]int{"width": src.Dx(), "height": src.Dy(), "targetWidth": b.Dx(), "targetHeight": b.Dy(), "total": 0}))
	}
	// Always finish with a full progress and the final frame.
	if done < total {
		done = total
	}
	total = max(total, done)
	progress(b.Dx(), b.Dy())
	fp, err := buildFrame(out, nil, false, pass)
	if err != nil {
		return nil, err
	}
	j.publish(mkEvent("frame", fp))
	return out, nil
}

// keepPixels returns pct percent of size in pixels (at least 1), or 0 when pct is 0.
func keepPixels(size, pct int) int {
	if pct <= 0 {
		return 0
	}
	return max(1, int(math.Round(float64(size)*float64(pct)/100)))
}

// degreesToTurns converts the API's face angle in degrees to pigo's rotation, a
// fraction of a full turn in [0, 1); pigo treats values <= 0 as upright.
func degreesToTurns(deg float64) float64 {
	t := math.Mod(deg/360, 1)
	if t < 0 {
		t++
	}
	return t
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
