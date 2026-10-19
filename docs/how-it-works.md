# How it works

## Seam carving in one page

A normal resize treats every pixel the same, so shrinking a photo's width squeezes the
people in it. Seam carving (Avidan & Shamir, 2007) removes the *least important* pixels
instead:

1. **Energy map.** Convert the image to grayscale, blur it a little, and measure how much
   each pixel differs from its neighbours (the Sobel operator). Edges and detail get high
   energy; sky and flat walls get low energy.
2. **Find a seam.** A vertical seam is a connected path of pixels from top to bottom, one
   per row, where each step moves at most one column left or right. Dynamic programming
   finds the seam with the lowest total energy: for every pixel, add the cheapest of the
   three pixels above it, then walk back up from the cheapest pixel in the bottom row.
3. **Remove it.** Delete that pixel from every row. The image is now one column narrower.
4. **Repeat** until the width is right. For height, do the same on the transposed image.

To **enlarge**, find the *k* cheapest seams first and then duplicate each one (averaging
its neighbours), so the stretch spreads over the flattest regions.

**Masks and faces** simply change the energy map: protected pixels get a huge energy so no
seam goes near them, and pixels marked for removal get a huge negative energy so seams go
through them first.

The *Blur radius* and *Edge threshold* settings in both apps change step 1: more blur
hides fine texture, and a threshold sets weak edges to zero.

## Why Squishless is about 50× faster

Squishless Studio runs the original caire library, which recomputes the blur, the Sobel
energy and the full dynamic-programming table on a fresh image copy for every single seam.
That is easy to follow (and great for watching it happen), but slow:

| Job | caire (Squishless Studio) | fastcarve (Squishless) |
| --- | --- | --- |
| `sample.jpg` 670 × 447, width -30% | ~5.5 s | ~0.1 s |
| `portrait-field.jpg` 1000 × 650 to 650 wide | ~21 s | ~0.1-0.15 s |

Squishless uses its own engine, `internal/fastcarve`, which gives the same kind of result
with much less work:

- **The energy map is computed once** and then patched incrementally. Removing a seam only
  changes the energy of pixels next to it, so only a narrow band around the seam (the blur
  radius plus the Sobel neighbourhood) is recomputed.
- **The DP table is updated, not rebuilt.** Only the cone of cells below the changed
  pixels needs new values.
- **Flat pixel buffers.** Pixels, gray values and energy live in plain slices; removing a
  seam is one in-place `copy` per row instead of building a new image.
- **Height changes run on a transposed copy**, so the same fast column code handles both
  directions.
- **Enlarging** finds all seams on a working copy while remembering each pixel's original
  column, then inserts them into the original in one pass.
- **Faces are detected once** on the source image (pigo, with the cascade built in) and
  turned into a protect mask, instead of on every step.
- **Two-axis changes scale first.** When both sides shrink or both grow, the image is
  scaled uniformly until one side matches and only the remaining side is carved. This is
  faster and avoids the stretched look of large seam insertion.

A 12-megapixel photo with a large width change still takes a few seconds, because the
work grows with the number of pixels times the number of seams.

## The `third_party/caire` patch

`third_party/caire` is caire v1.5.0 (MIT License) with a small patch used by Squishless Studio.
Every change is marked with a `squishless:` comment; the additions are mostly in
`studio.go`, with tests in `studio_test.go`. Why it was needed:

- **No progress hook.** Upstream only reports progress to its own desktop preview window.
  The patch adds `Processor.OnStart` / `Processor.OnFrame`, the `Frame` type with
  `Upright()`, and `Processor.Run(image.Image)`, an in-memory counterpart to `Process`
  that returns the image instead of encoding it.
- **Goroutine leak.** Upstream sent every frame to the preview channel from a new
  goroutine even when no preview was running, so each seam leaked a blocked goroutine.
  Those sends now happen only when `Preview` is set.
- **Global state reset.** caire keeps the resize mode, the edge map, the inserted-seam
  table and the face detection state in package variables, so a second run in the same
  process inherited the first run's state. `resetState()` clears them at the start of
  every run.
- **In-memory masks.** `SetMasks(mask, rmask)` thresholds mask images the same way the
  CLI does. Upstream only applied masks when a mask *file path* was set; the energy step
  now checks the mask itself.
- **Two-axis resize fixes.** Masks are rotated together with the image on vertical
  steps so they stay aligned; the inserted-seam table records only real insertions, one
  list per orientation; and the user's face angle is no longer overwritten by the first
  vertical step.
- **Desktop preview is opt-in.** caire's own preview window uses the Gio GUI toolkit,
  which needs cgo and GUI system libraries on Linux and macOS. Squishless Studio never
  opens that window, so `gui.go`, `draw.go` and `preview.go` now build only with
  `-tags caire_gui`. Without the tag, `Processor.Preview` is switched off with a warning.
  This is what lets both apps build anywhere with plain `go build` and cross-compile
  with `CGO_ENABLED=0`.
- **Percentage on square images.** caire's percentage mode resized square sources
  uniformly and ignored per-axis values. That shortcut now only applies when both
  percentages are equal.
