//go:build !nofastcarve

package main

import (
	"context"
	"image"

	"caire-studio/internal/fastcarve"
)

func init() {
	resizeFn = func(ctx context.Context, src image.Image, o resizeOptions) (*image.NRGBA, error) {
		return fastcarve.Resize(ctx, src, fastcarve.Options{
			Width:        o.Width,
			Height:       o.Height,
			ProtectFaces: o.ProtectFaces,
			Protect:      o.Protect,
			Remove:       o.Remove,
		}, nil)
	}
}
