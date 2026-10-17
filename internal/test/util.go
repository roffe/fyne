package test

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// PixelTolerance loosens AssertImageMatches for a painter that rasterises
// differently from the software one the masters come from: up to Share of the
// pixels may differ by more than Channel in some colour channel. Zero keeps
// the comparison exact.
var PixelTolerance struct {
	Channel int
	Share   float64
}

func pixWithinTolerance(a, b []uint8) bool {
	if PixelTolerance.Share == 0 || len(a) != len(b) || len(a) == 0 {
		return false
	}
	bad := 0
	for i := 0; i+3 < len(a); i += 4 {
		for c := range 4 {
			if d := int(a[i+c]) - int(b[i+c]); d > PixelTolerance.Channel || -d > PixelTolerance.Channel {
				bad++
				break
			}
		}
	}
	return float64(bad) <= PixelTolerance.Share*float64(len(a)/4)
}

// pixCloseEnough reports whether a and b are mostly the same.
func pixCloseEnough(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	mismatches := 0

	for i, v := range a {
		w := b[i]
		if v == w {
			continue
		}
		// Allow a small delta for rendering variation.
		delta := int(v) - int(w) // use int to avoid overflow
		if delta < 0 {
			delta = -delta
		}

		if delta > 30 {
			return false
		}

		mismatches += delta
	}

	// Allow an average of 1/2 bit difference per byte.
	// This is should be more reliable for images than a percentage of mismatched bytes.
	return float64(mismatches)/float64(len(a)) < 0.5
}

// NewCheckedImage returns a new black/white checked image with the specified size
// and the specified amount of horizontal and vertical tiles.
func NewCheckedImage(w, h, hTiles, vTiles int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	colors := []color.Color{color.White, color.Black}
	tileWidth := float64(w) / float64(hTiles)
	tileHeight := float64(h) / float64(vTiles)
	for y := 0; y < h; y++ {
		yTile := int(math.Floor(float64(y) / tileHeight))
		for x := 0; x < w; x++ {
			xTile := int(math.Floor(float64(x) / tileWidth))
			img.Set(x, y, colors[(xTile+yTile)%2])
		}
	}
	return img
}

func writeImage(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err = png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
