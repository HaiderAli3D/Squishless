# Caire Studio

Caire Studio is a local web GUI for [esimov/caire](https://github.com/esimov/caire), a
content-aware image resizer based on *seam carving*. A seam is a connected path of pixels,
one per row (or column), that runs through the least noticeable part of an image. Caire
removes or duplicates seams one at a time, so empty sky and plain walls shrink or stretch
while people and strong edges keep their shape.

The Studio is a single Go binary with the UI embedded. It runs on your machine, streams
every carving step to the browser as it happens, and lets you paint areas to protect or
remove before you run.

## Prerequisites

- Go 1.22 or newer.
  On this machine a portable Go is installed at `C:\Users\mrhai\.local\gosdk\go\bin`. It is
  not on `PATH`, so add it first:
  - Git Bash: `export PATH="/c/Users/mrhai/.local/gosdk/go/bin:$PATH"`
  - PowerShell: `$env:Path = "C:\Users\mrhai\.local\gosdk\go\bin;$env:Path"`
- A current browser (Chrome, Edge, Firefox or Safari).

No other dependencies. caire is vendored in `third_party/caire` and wired in with a
`replace` directive in `go.mod`.

## Run

```sh
go run .
```

or build a binary and run it:

```sh
go build -o caire-studio.exe .
./caire-studio.exe
```

The server listens on <http://127.0.0.1:7777> and opens it in your default browser.

| Flag     | Default          | Meaning                                         |
| -------- | ---------------- | ----------------------------------------------- |
| `-addr`  | `127.0.0.1:7777` | Address to listen on.                           |
| `-open`  | `true`           | Open the UI in the default browser on start. Use `-open=false` to skip. |

Example: `./caire-studio.exe -addr 127.0.0.1:7790 -open=false`.

## Feature tour

1. **Source.** Drop a photo anywhere on the page, paste one with Ctrl+V, choose a file, or
   click a sample. The empty stage offers *Load the sample photo* (`sample.jpg`). JPEG, PNG,
   BMP and GIF are accepted, up to 25 MB and 40 megapixels.
2. **Target size.** Enter a new width and/or height (leave a side empty to keep it). Quick
   presets cover the common cases (width -25%, -50%, height -30%, square, width +20%). The
   plan line under the inputs shows the resulting size and how many columns or rows will
   be removed or added. The stage rulers mark the target with an amber guide.
3. **Masks.** On the *Source & masks* tab, paint with the **Protect** brush (green) to keep
   areas intact, or the **Remove** brush (red) to make seams go through them first. Use the
   **Eraser**, `[` and `]` for brush size, *Show* to toggle the overlay, and *Clear masks* to
   start over.
4. **Carve.** Press *Carve image* (or Ctrl+Enter). The *Live* tab shows the image shrinking
   or growing step by step, with rulers for the original, target and current size, plus
   progress, the pass (columns or rows), seams per second and elapsed time. Esc or *Cancel*
   stops the run; pressing *Restart run* starts over with the current settings.
5. **Result.** The *Result* tab shows a draggable before/after comparison (arrow keys move
   the divider too) against either the original scaled to the same height or a naive
   resize to the same size. *Download* saves PNG or JPEG (quality 95).
6. **History.** Every finished run lands in the strip at the bottom with a thumbnail and a
   summary of its settings, including any masks used. Click one to bring back its settings
   and result.

Light and dark themes follow the system setting; the toggle in the header overrides it.

## The settings

| Setting | What it does |
| ------- | ------------ |
| **Width / Height** | Target size in pixels. A side left empty is unchanged. Each side can be at most 10,000 px. |
| **% (percentage)** | The number is the share of the original to **keep**: width 70% gives 70% of the original width. 1-99 only, so percent never enlarges. The server turns it into pixels (rounded, at least 1 px) before carving. |
| **Make square** | Needs a width and a height. The result is a square on the shorter of the two. Caire first scales the image so the short side fits, then carves the rest. Works with px and %. |
| **Enlarge** | A width or height larger than the source makes Caire *insert* seams instead of removing them. Each inserted seam is the average of its neighbours, and recently inserted seams are avoided so the stretch spreads out. |
| **Blur radius** (0-20) | Stack blur applied to the edge map before seams are chosen. Higher values smooth over fine texture (grass, noise) so seams can pass through it. |
| **Sobel threshold** (0-20) | Edges weaker than this count as empty space. Raise it to let seams cut through faint detail. |
| **Detect and protect faces** | Runs the [pigo](https://github.com/esimov/pigo) face detector and fills found faces with maximum energy so seams avoid them. A job fails with a clear message if the target is smaller than a detected face. |
| **Detector angle** (-180° to 180°) | Rotation of the face detector, for photos with tilted faces. The API takes degrees and converts them to pigo's fraction of a turn. Vertical passes use a fixed sideways angle because the image is rotated for them. |
| **Protect mask** | White in the mask is treated as maximum energy, so seams go around it. |
| **Remove mask** | White in the mask is treated as zero energy, so seams go through it first. Shrink by about the width of the painted area to remove an object. |
| **Show each seam** | Draws the seam that was just processed on the live stage, as dots or a line, in the chosen color. |

When both sides shrink, caire scales the whole image down to the target aspect ratio first
and carves only the remainder, so fewer seams are carved than the plan's column and row
counts suggest. The plan line says so.

## How the live preview works

`POST /api/jobs` decodes the upload and starts a job. Only one job runs at a time, because
caire keeps state in package variables. Starting a new job cancels the previous one.

The job calls caire's `Processor.Run` with two hooks added by the patch (below):

- `OnStart` reports the effective source size (after any pre-scaling) and the target size,
  which gives the total number of seams.
- `OnFrame` is called after every removed or inserted seam with the current image and the
  seam. Vertical passes work on a rotated image, so `Frame.Upright` turns it back.

The server throttles frames to at most 25 per second, scales them so the long edge is at
most 1000 px, encodes them as JPEG and sends them over Server-Sent Events
(`GET /api/jobs/{id}/events`) together with `progress` events. A subscriber that connects
late gets the latest `start`, `progress` and `frame` events replayed. When the run ends the
stream sends `done` or `error`, and the browser fetches the full-resolution result.
Returning an error from `OnFrame` is how cancellation stops caire between two seams.

If the stream drops (for example because the server was stopped), the UI shows
*Reconnecting to the server…* and reports a lost connection after 10 seconds.

## The `third_party/caire` patch

`third_party/caire` is caire v1.5.0 with a small patch. Every change is marked with a
`caire-studio:` comment, and the additions live mostly in `studio.go` (tests in
`studio_test.go`). Why it was needed:

- **No progress hook.** Upstream only reports progress to its own Gio preview window.
  The patch adds `Processor.OnStart` / `Processor.OnFrame`, the `Frame` type with
  `Upright()`, and `Processor.Run(image.Image)`, an in-memory counterpart to `Process`
  that returns the image instead of encoding it.
- **Goroutine leak.** Upstream sends every frame to the Gio preview channel from a new
  goroutine even when no preview is running, so each seam leaked a blocked goroutine. Those
  sends now happen only when `Preview` is set.
- **Global state reset.** caire keeps the resize mode, the edge map, the inserted-seam table
  and the face detection state in package variables, so a second run in the same process
  inherited the first run's state. `resetState()` clears them at the start of every run.
- **In-memory masks.** `SetMasks(mask, rmask)` thresholds mask images the same way the CLI
  does. Upstream only applied masks to the energy map when a mask *file path* was set, so
  in-memory masks were carved along with the image but never steered the seams. The energy
  step now checks the mask itself.
- **Fixes for two-axis resizes** (carried over from upstream v1.5.0):
  masks are rotated together with the image on every vertical step, so they stay aligned;
  the table of inserted seams records only real insertions and keeps one list per
  orientation, shifted as columns are inserted; and the user's face angle is no longer
  overwritten by the first vertical step.
- **Percentage on square images.** caire's percentage mode resized square sources
  uniformly and ignored per-axis values. That shortcut now only applies when both
  percentages are equal. (The Studio itself sends pixels, see the % setting.)

Run the library tests with `go test .` inside `third_party/caire`.

## HTTP API

All endpoints are on the same origin as the UI. The server only answers requests whose
`Host` is `localhost` or an IP address (to block DNS rebinding), and rejects cross-origin
`POST` requests.

| Method & path | Description |
| ------------- | ----------- |
| `GET /api/samples` | JSON list of bundled samples: `name`, `url`, `width`, `height`. |
| `POST /api/jobs` | `multipart/form-data` with `image` (required), optional `mask` and `rmask` (same size as the image; white = painted), and `params` (JSON, below). Returns `{"id", "srcWidth", "srcHeight"}`. Cancels the job that was running. |
| `GET /api/jobs/{id}/events` | SSE stream: `start` (`width`, `height`, `targetWidth`, `targetHeight`, `total`), `progress` (`done`, `total`, `width`, `height`, `pass`, `elapsedMs`), `frame` (base64 `jpeg`, `width`, `height`, `fullWidth`, `fullHeight`, `seams`, `pass`), then `done` (`width`, `height`, `elapsedMs`) or `error` (`error`; `"cancelled"` after a cancel). |
| `GET /api/jobs/{id}/result?format=png\|jpg` | The full-resolution result as an attachment. 409 while running or after a failure. |
| `POST /api/jobs/{id}/cancel` | Cancel a job. Returns 204. |

`params` fields (defaults in brackets): `width`, `height` [0 = unchanged], `percentage`
[false], `square` [false], `blur` [4, 0-50], `sobel` [2, 0-50], `face` [false],
`faceAngle` [0, degrees], `debug` [false, seams in frames], `shape` [`"circle"` or
`"line"`], `seamColor` [`"#ff0000"`].

Example:

```sh
curl -F image=@photo.jpg -F 'params={"width":70,"percentage":true}' http://127.0.0.1:7777/api/jobs
```

The server keeps the results of the last 20 successful jobs, within about 512 MB of decoded
pixels (the newest result is always kept). Cancelled and failed jobs are tracked
separately, so restarting runs never pushes results out. If a result is gone, the UI
downloads its own copy instead, or removes the history entry when it no longer has one.

## Development

```sh
go vet ./...
go test -race ./...
(cd third_party/caire && go test .)
node --check web/app.js
```

Layout: `main.go` (flags, listener, browser launch, embedded `web/`), `internal/server`
(HTTP API, job manager, frame encoding), `web/` (vanilla JS UI, no build step),
`third_party/caire` (patched library).

## Sample images

The images in `web/samples` come from the caire repository (MIT) and from Unsplash via
Lorem Picsum. Credits and licenses are in [`web/samples/CREDITS.txt`](web/samples/CREDITS.txt).
