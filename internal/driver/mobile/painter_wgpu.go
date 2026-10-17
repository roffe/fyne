//go:build android && wgpu && (arm64 || amd64)

package mobile

import (
	"github.com/roffe/wgpu"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/driver/mobile/app"
	pgl "fyne.io/fyne/v2/internal/painter/gl"
	"fyne.io/fyne/v2/internal/painter/webgpu"
)

// With wgpu the WebGPU painter presents to the Android window itself, through
// a Vulkan surface, and the app makes no EGL surface on it.

// painterState is what the driver keeps for its painter besides glctx: the
// window Android gave the app, and the window whose painter has a surface on it.
type painterState struct {
	nativeWindow app.NativeWindow
	surfaceOwner *window
}

func newPainter(c *canvas, _ *window) pgl.Painter {
	return webgpu.NewPainter(c, nil)
}

// setDrawContext takes what the app hands out once it is visible.
func (d *driver) setDrawContext(ctx any) {
	d.nativeWindow, _ = ctx.(app.NativeWindow)
}

// dropDrawContext lets go of the window Android is destroying, surface first,
// and tells the app it may let the window go.
func (d *driver) dropDrawContext() {
	if d.surfaceOwner != nil {
		d.surfaceOwner.canvas.Painter().(*webgpu.Painter).DetachSurface()
		d.surfaceOwner = nil
	}
	d.nativeWindow = 0
	app.WindowReleased()
}

func (d *driver) canPaint() bool {
	return d.nativeWindow != 0
}

// prepareSurface moves the window's surface to w's painter. Only one fyne
// window shows at a time on mobile, and each has its own painter.
func (d *driver) prepareSurface(w *window) {
	if d.surfaceOwner == w {
		return
	}
	if d.surfaceOwner != nil {
		d.surfaceOwner.canvas.Painter().(*webgpu.Painter).DetachSurface()
		d.surfaceOwner = nil
	}
	target := wgpu.SurfaceTargetFromAndroidNativeWindow(uintptr(d.nativeWindow))
	if err := w.canvas.Painter().(*webgpu.Painter).AttachSurface(target); err != nil {
		fyne.LogError("WebGPU surface creation failed", err)
		return
	}
	d.surfaceOwner = w
}

func (*driver) clearFrame(c *canvas) {
	c.Painter().Clear()
}

func (d *driver) present(c *canvas) {
	p := c.Painter().(*webgpu.Painter)
	p.Present()
	d.app.Publish()
	if p.NeedsRepaint() { // the window is mid-rotation, catch up next frame
		c.SetDirty()
	}
}
