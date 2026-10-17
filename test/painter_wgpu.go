//go:build wgputest

package test

import (
	fynedriver "fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/internal/painter/webgpu"
	internaltest "fyne.io/fyne/v2/internal/test"
)

// With -tags wgputest test canvases render through the WebGPU painter, so
// every image assertion compares it against the software painter's masters.
func newPainter() fynedriver.Painter {
	return webgpu.CanvasPainter{}
}

// A GPU rasterises glyph and anti-aliased edges differently from a scanline
// painter, so let some pixels differ: the share bounds area, so a wrong
// colour, position or size still fails.
func init() {
	internaltest.PixelTolerance.Channel = 96
	internaltest.PixelTolerance.Share = 0.08
}
