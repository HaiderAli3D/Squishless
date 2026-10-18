# Development

## Requirements

- Go 1.22+
- Optional: Node.js, only to syntax-check the UI scripts with `node --check`.

There is no frontend build step. The UIs are plain HTML, CSS and JavaScript that are
embedded into the Go binaries with `//go:embed`; edit a file, restart the app, reload the
page.

## Code map

| Path | What is in it |
| --- | --- |
| `resizer/main.go` | Caire Resize entry point: flags, listener, browser launch, graceful shutdown. |
| `resizer/server.go` | `POST /api/resize`: form validation, size limits, a semaphore that limits concurrent resizes, encoding. |
| `resizer/engine.go` | Wires the server to `internal/fastcarve` (build tag `!nofastcarve`, so tests can stub the engine). |
| `resizer/web/` | Caire Resize UI: batch list, size modes, carving sliders, before/after viewer, Mark areas editor, client-side ZIP writer. |
| `internal/fastcarve/` | The fast seam-carving engine: `fastcarve.go` (API, scale-then-carve), `carver.go` (incremental energy + DP), `image.go` (packed pixels, scaling, transpose), `faces.go` (pigo face detection with the embedded `facefinder` cascade). |
| `main.go` | Caire Studio entry point. |
| `internal/server/` | Caire Studio API: job manager (`jobs.go`), frame encoding (`frames.go`), handlers (`server.go`). |
| `web/` | Caire Studio UI and `web/samples`. |
| `third_party/caire/` | Patched esimov/caire v1.5.0, used through a `replace` directive in `go.mod`. See [How it works](how-it-works.md#the-third_partycaire-patch). |

## Tests

```sh
go vet ./...
go test ./...                       # fastcarve, Resize server, Studio server
go test -race ./...                 # slower, checks for data races
(cd third_party/caire && go test .) # the patched library (separate module)
node --check resizer/web/app.js
node --check web/app.js
```

Benchmarks for the engine:

```sh
go test -run=NONE -bench=. ./internal/fastcarve/
```

## Using fastcarve from Go

The engine has a small API you can call from other Go code in this module:

```go
out, err := fastcarve.Resize(ctx, src, fastcarve.Options{
    Width:          800, // 0 keeps the width
    Height:         0,   // 0 keeps the height
    ProtectFaces:   true,
    BlurRadius:     fastcarve.DefaultBlurRadius,     // 2; note the zero value means no blur
    SobelThreshold: fastcarve.DefaultSobelThreshold, // 0
    // Protect, Remove: optional *image.Gray masks, same size as src, > 127 = marked
}, func(fraction float64) { /* progress 0..1, may be nil */ })
```

`Resize` returns an `*image.NRGBA` and stops promptly with `ctx.Err()` when `ctx` is
cancelled. It is safe to call concurrently.

## Building release binaries

```sh
go build -trimpath -ldflags "-s -w" -o caire-resize.exe ./resizer
go build -trimpath -ldflags "-s -w" -o caire-studio.exe .
```

`-trimpath` removes local paths from the binary and `-s -w` drops debug symbols, which
makes the files smaller. Built binaries (`*.exe`) are ignored by git.
