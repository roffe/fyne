//go:build !wgputest

package test

import (
	fynedriver "fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/internal/painter/software"
)

// newPainter is what test canvases render with: the software painter the
// master images come from.
func newPainter() fynedriver.Painter {
	return software.NewPainter()
}
