# Troubleshooting & FAQ

## Starting the apps

**`go: command not found` / `'go' is not recognized`**
Go is not installed or not on `PATH`. Install it from <https://go.dev/dl/> and open a
**new** terminal. See [Getting started](getting-started.md#1-install-go).

**`listen on 127.0.0.1:7878: bind: address already in use`** (or *Only one usage of each
socket address...*)
The app is already running (check other terminals and the system tray), or another
program uses the port. Close it, or pick another port: `squishless.exe -addr 127.0.0.1:7900`.

**The browser does not open.**
Open the address printed in the terminal yourself (<http://127.0.0.1:7878> for Squishless,
<http://127.0.0.1:7777> for Studio). Some locked-down systems block programs from
launching the browser.

**Windows SmartScreen says the program is unrecognized.**
The `.exe` files (release downloads and your own builds) are not code-signed. Choose *More info → Run anyway*, or run
it with `go run ./cmd/squishless`.

**macOS says the app "cannot be opened because the developer cannot be verified".**
The release builds are not notarized. Right-click (or Control-click) the program, choose
*Open*, then *Open* again. On Linux and macOS you may also need `chmod +x squishless`.

**Windows Firewall asks for access.**
The apps only listen on `127.0.0.1`, so they work even if you click *Cancel*.

**The first `go run` is slow.**
Go downloads the dependencies and compiles everything once. Later runs start in about a
second. Build an `.exe` to skip compiling entirely.

## Using Squishless

**"More than 4x the original" / "Result over 50 megapixels"**
These are safety limits. Use a smaller target, or resize in a normal image editor first.

**"image is larger than 30 MB"**
Save the image as a JPEG first, or reduce its size in another tool.

**A face got squeezed.**
The face detector finds frontal faces; it can miss profiles, small faces, and faces behind
large sunglasses. Use **Mark areas** and paint the face green.

**Straight lines (horizon, building edges, roads) are bent.**
Paint a thin green stroke along the line, or make a smaller change.

**The result looks stretched or repetitive after enlarging.**
Seam insertion can only add so much before textures repeat. Keep enlargements of one side
under about 1.5×, or change both sides so the image is first scaled up uniformly.

**The object I painted red is still there.**
Removal only happens while the image *shrinks* in that direction. Use **Fit width** (for
tall objects) or **Fit height** (for wide objects) and remove at least as many pixels as
the red area is wide. Red marks are ignored when enlarging. A faint line can sometimes
remain in very smooth areas like clear sky; painting a slightly wider area helps.

**The photo comes out rotated.**
Photos straight from a phone camera may store their rotation in EXIF metadata, which the
apps do not read yet. Open and re-save the photo in any editor (or rotate it there) first.

**Large photos are slow.**
Time grows with pixels × seams. A 12 MP photo with a big width change takes 4-6 seconds.
If you only need a smaller image, scale it down first (or change both sides, which scales
first automatically).

**My settings were reset.**
Settings are saved in the browser's local storage for `127.0.0.1:7878`. Private windows,
clearing site data, or changing the port start fresh.

## Getting good results

- Seam carving needs low-detail areas (sky, water, walls, grass, blurred backgrounds) to
  absorb the change. Busy, detailed photos will show artifacts sooner.
- Removing up to about 30-40% of one side usually looks natural.
- Raise **Blur radius** when seams avoid textures like grass and cut the subject instead;
  lower it for graphics and text.
- Raise **Edge threshold** when faint background detail is kept at the subject's expense.
- Always check with the before/after slider before downloading.

## FAQ

**Are my images uploaded anywhere?**
No. The app runs on your computer; the browser sends images only to the local program at
`127.0.0.1`, and nothing is stored on disk.

**Can other devices on my network use it?**
Only if you start it with an `-addr` that listens on your network (for example
`-addr 0.0.0.0:7878`). There is no login, so only do that on a trusted network.

**Which app should I use?**
Squishless for real work (fast, batch, downloads). Squishless Studio to watch and learn how
seam carving behaves with each setting.

**Does it work on macOS and Linux?**
Yes. Build it from source as described in [Getting started](getting-started.md).
