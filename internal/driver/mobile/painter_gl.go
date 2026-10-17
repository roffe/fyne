//go:build !((ios || (android && (arm64 || amd64))) && (wgpu || !gl))

package mobile

import (
	"fyne.io/fyne/v2/internal/driver/mobile/gl"
	pgl "fyne.io/fyne/v2/internal/painter/gl"
	"fyne.io/fyne/v2/theme"
)

// painterState is what the driver keeps for its painter besides glctx.
type painterState struct{}

func newPainter(c *canvas, w *window) pgl.Painter {
	return pgl.NewPainter(c, w)
}

// setDrawContext takes what the app hands out once it is visible.
func (d *driver) setDrawContext(ctx any) {
	d.glctx, _ = ctx.(gl.Context)
}

// dropDrawContext lets go of it when the app stops being visible.
func (d *driver) dropDrawContext() {
	d.glctx = nil
}

func (d *driver) canPaint() bool {
	return d.glctx != nil
}

// prepareSurface readies w's painter to present; GL presents through the app.
func (*driver) prepareSurface(*window) {}

func (d *driver) clearFrame(*canvas) {
	r, g, b, a := theme.Color(theme.ColorNameBackground).RGBA()
	max16bit := float32(255 * 255)
	d.glctx.ClearColor(float32(r)/max16bit, float32(g)/max16bit, float32(b)/max16bit, float32(a)/max16bit)
	d.glctx.Clear(gl.ColorBufferBit)
}

func (d *driver) present(*canvas) {
	d.app.Publish()
}
