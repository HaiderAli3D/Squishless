# Getting started

This guide takes you from nothing to a running app. It takes about five minutes.

> **Just want to use it?** Download a ready-made program for Windows, macOS or Linux from
> the [Releases page](https://github.com/HaiderAli3D/Squishless/releases), unzip it and run
> `squishless`. You only need the steps below to build from source.

## 1. Install Go

Both apps are written in [Go](https://go.dev). You need Go **1.22 or newer** to build them.
You do not need Node, npm, a C compiler or anything else: the web UI is plain HTML, CSS and
JavaScript built into the program.

- **Windows:** download the `.msi` installer from <https://go.dev/dl/> and run it, or use
  `winget install GoLang.Go`.
- **macOS:** download the `.pkg` from <https://go.dev/dl/>, or `brew install go`.
- **Linux:** follow <https://go.dev/doc/install>, or use your package manager if it has
  Go 1.22+.

Open a **new** terminal and check:

```sh
go version
```

It should print `go version go1.22` or newer.

> **Portable Go without an installer.** Unzip a Go release anywhere and put its `bin`
> folder on `PATH` for the current terminal:
> - PowerShell: `$env:Path = "C:\path\to\go\bin;$env:Path"`
> - Git Bash / macOS / Linux: `export PATH="/path/to/go/bin:$PATH"`

## 2. Get the code

```sh
git clone https://github.com/HaiderAli3D/Squishless.git
cd Squishless
```

All dependencies are pinned in `go.mod` / `go.sum`. Go downloads them on the first build.
The caire library itself is already in the repository (`third_party/caire`).

## 3. Run an app

Run these from the repository folder.

**Squishless** (the fast everyday resizer):

```sh
go run ./cmd/squishless
```

**Squishless Studio** (the live seam-carving lab):

```sh
go run ./cmd/squishless-studio
```

The first run takes a little longer because Go compiles everything. Then the terminal
prints the address and your browser opens it:

| App | Address |
| --- | --- |
| Squishless | <http://127.0.0.1:7878> |
| Squishless Studio | <http://127.0.0.1:7777> |

Press **Ctrl+C** in the terminal to stop the app. Both apps can run at the same time.

## 4. Build a standalone program (optional)

A build gives you a single file that contains the app and its UI. You can move it anywhere
or give it to someone without Go.

```sh
# Windows
go build -o squishless.exe ./cmd/squishless
go build -o squishless-studio.exe ./cmd/squishless-studio

# macOS / Linux
go build -o squishless ./cmd/squishless
go build -o squishless-studio ./cmd/squishless-studio
```

Start it by double-clicking it (Windows) or running `./squishless`.

**Building for another computer.** Go cross-compiles with two environment variables.
For example, from any OS:

```sh
GOOS=windows GOARCH=amd64 go build -o squishless.exe ./cmd/squishless
GOOS=darwin  GOARCH=arm64 go build -o squishless-mac ./cmd/squishless   # Apple Silicon
GOOS=linux   GOARCH=amd64 go build -o squishless-linux ./cmd/squishless
```

(In PowerShell, set them first: `$env:GOOS="darwin"; $env:GOARCH="arm64"`, and clear them
afterwards with `Remove-Item Env:GOOS, Env:GOARCH`.)

## 5. Command-line options

Both programs take the same two flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `127.0.0.1:7878` (Squishless), `127.0.0.1:7777` (Studio) | Address and port to listen on. |
| `-open` | `true` | Open the browser on start. Use `-open=false` to skip it. |

Examples:

```sh
squishless.exe -addr 127.0.0.1:9000
go run ./cmd/squishless -open=false
```

The apps only listen on your own computer (`127.0.0.1`) by default, so other devices on
your network cannot reach them. If you change `-addr` to listen on all interfaces
(`0.0.0.0:7878`), anyone on your network can use the app; there is no login.

## Next steps

- [Squishless guide](squishless.md): every control explained.
- [Squishless Studio guide](squishless-studio.md): the live seam lab.
- [Troubleshooting & FAQ](troubleshooting.md) if something does not work.
