# Squishless

[![CI](https://github.com/HaiderAli3D/Squishless/actions/workflows/ci.yml/badge.svg)](https://github.com/HaiderAli3D/Squishless/actions/workflows/ci.yml)

**Resize images without squishing what matters.** Squishless changes a photo's width,
height or aspect ratio with *seam carving*: people, buildings and faces keep their shape,
while empty sky, water and plain backgrounds absorb the change.

![Squishless with the before/after slider](docs/images/squishless.png)

It runs on your own computer as a small local web app. You start one program, it opens in
your browser, and your images never leave your machine.

- **Fast.** About 0.1-0.3 s for a typical 1000 × 650 photo.
- **Batch.** Drop in many images, resize them all, download one by one or as a ZIP.
- **Any size or shape.** Exact pixels, percent, fit width or fit height, plus presets
  for common social and web formats. Shrink or enlarge.
- **Before/after slider** to check every result, plus side-by-side and fullscreen views.
- **Keep or remove things.** Paint areas to protect (green) or to remove first (red).
- **Face protection** and tunable carving (blur radius, edge threshold).

## Download

Prebuilt programs for Windows, macOS and Linux are on the
[Releases page](https://github.com/HaiderAli3D/Squishless/releases). Unzip, run
`squishless` (`squishless.exe` on Windows), and the app opens at <http://127.0.0.1:7878>.

The builds are not code-signed, so Windows SmartScreen or macOS Gatekeeper may warn on
first launch. See [Troubleshooting](docs/troubleshooting.md#starting-the-apps).

## Build from source

You need [Go](https://go.dev/dl/) (the latest release is best; 1.22 is the minimum).
Nothing else: no Node, no C compiler.

```sh
git clone https://github.com/HaiderAli3D/Squishless.git
cd Squishless
go run ./cmd/squishless                   # opens http://127.0.0.1:7878
```

Or build a standalone program:

```sh
go build -o squishless.exe ./cmd/squishless   # drop .exe on macOS / Linux
```

[Getting started](docs/getting-started.md) has the full setup, cross-compiling and the
command-line options.

## Using it in 30 seconds

1. Drop one or more images on the page (or click *choose files*, or paste with Ctrl+V).
2. Pick a size: **Exact** (for example 1080 × 1080), **Percent**, **Fit width** or
   **Fit height**, or click a preset.
3. Press **Resize all** (Ctrl+Enter).
4. Drag the divider in **Before and after** to compare, then **Download** one image or
   **Download all (.zip)**.

To remove an object or keep something safe, use **Mark areas** on an image and paint it
red (remove first) or green (keep). The [Squishless guide](docs/squishless.md) explains
every control and has tips for good results.

## Squishless Studio

The repository also contains **Squishless Studio**, a lab for learning how seam carving
works. It runs the original [esimov/caire](https://github.com/esimov/caire) library step by
step and streams every seam to the browser as it is removed, so you can watch the image
shrink and see how each setting changes the result. It is much slower than Squishless and
meant for exploring, not batch work.

```sh
go run ./cmd/squishless-studio            # opens http://127.0.0.1:7777
```

![Squishless Studio carving live](docs/images/squishless-studio.png)

## Documentation

| Guide | Read it when you want to... |
| --- | --- |
| [Getting started](docs/getting-started.md) | Install Go, build and run the apps on Windows, macOS or Linux. |
| [Squishless guide](docs/squishless.md) | Learn every control, with tips and the HTTP API for scripting. |
| [Squishless Studio guide](docs/squishless-studio.md) | Use the live seam-carving lab and its HTTP API. |
| [How it works](docs/how-it-works.md) | Understand seam carving, why Squishless is ~50× faster than the reference library, and the library patch. |
| [Troubleshooting & FAQ](docs/troubleshooting.md) | Fix common problems and get better-looking results. |
| [Development](docs/development.md) | Find your way around the code, run the tests, make releases. |

## Project layout

```
.
├── cmd/squishless/          Squishless: server, flags, embedded web UI (web/)
├── cmd/squishless-studio/   Squishless Studio: entry point, embedded web UI and samples
├── internal/fastcarve/      The fast seam-carving engine used by Squishless
├── internal/server/         Squishless Studio HTTP API, job runner, live frame streaming
├── third_party/caire/       esimov/caire v1.5.0, vendored with a small patch
└── docs/                    Guides and screenshots
```

## Contributing

Issues and pull requests are welcome. Please run `go vet ./...` and `go test ./...`
before opening a pull request; [Development](docs/development.md) has the details.

## Credits

- Seam carving: Shai Avidan and Ariel Shamir, *Seam Carving for Content-Aware Image
  Resizing* (SIGGRAPH 2007).
- [esimov/caire](https://github.com/esimov/caire) by Endre Simo (MIT License), vendored in
  `third_party/caire` with its license. Face detection uses
  [esimov/pigo](https://github.com/esimov/pigo) (MIT License).
- Sample photos: see
  [`cmd/squishless-studio/web/samples/CREDITS.txt`](cmd/squishless-studio/web/samples/CREDITS.txt).
