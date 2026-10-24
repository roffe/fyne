//go:build (wgpu || !gl) && !wasm && !test_web_driver

package glfw

import (
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2/internal/painter/webgpu"
)

// WebGPU is the default painter, with GL to fall back to; -tags gl keeps
// only GL and -tags wgpu only WebGPU.
// Windows get no GL context: GLFW only provides the native window and its
// input, and the WebGPU painter presents to a surface on it.
type wgpuPainter struct{}

// The driver paints and presents on the main goroutine, locked to the main
// thread by GLFW, so the GL backend may keep its context current there.
// Capture goes through RunWithContext on the caller's goroutine: off the
// main goroutine it belongs inside fyne.DoAndWait, as before.
func init() {
	webgpu.PinnedThread = true
	painters = append([]windowPainter{wgpuPainter{}}, painters...)
}

func (wgpuPainter) windowHints() {
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
}

func (wgpuPainter) initPainter(w *window) error {
	target := surfaceTarget(w.viewport)
	p := webgpu.NewPainter(w.canvas, &target)
	if err := p.TryInit(); err != nil {
		p.Release()
		return err
	}
	w.canvas.SetPainter(p)
	return nil
}

// makeContextCurrent has nothing to do, WebGPU is not bound to a thread.
func (wgpuPainter) makeContextCurrent(*window) {}

func (wgpuPainter) swapBuffers(w *window) {
	p, _ := w.canvas.Painter().(*webgpu.Painter)
	followBackingScale(w.viewport)
	p.Present()
	if p.NeedsRepaint() { // the surface lagged the window's size, catch up next frame
		w.canvas.SetDirty()
	}
}

// releasePainter drops the window's surface, which has to go before the window.
func (wgpuPainter) releasePainter(w *window) {
	if p, ok := w.canvas.Painter().(*webgpu.Painter); ok {
		p.Release()
	}
}
