//go:build (wgpu || !gl) && !wasm && !test_web_driver

package glfw

import (
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2/internal/painter/webgpu"
)

// WebGPU is the default painter; -tags gl keeps GL.
// Windows get no GL context: GLFW only provides the native window and its
// input, and the WebGPU painter presents to a surface on it.

func initWindowHints() {
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
}

// The driver paints and presents on the main goroutine, locked to the main
// thread by GLFW, so the GL backend may keep its context current there.
// Capture goes through RunWithContext on the caller's goroutine: off the
// main goroutine it belongs inside fyne.DoAndWait, as before.
func init() {
	webgpu.PinnedThread = true
}

func (w *window) initPainter() {
	target := surfaceTarget(w.viewport)
	w.canvas.SetPainter(webgpu.NewPainter(w.canvas, &target))
	w.canvas.Painter().Init()
}

// makeContextCurrent has nothing to do, WebGPU is not bound to a thread.
func (*window) makeContextCurrent() {}

func (w *window) swapBuffers() {
	p, _ := w.canvas.Painter().(*webgpu.Painter)
	followBackingScale(w.viewport)
	p.Present()
	if p.NeedsRepaint() { // the surface lagged the window's size, catch up next frame
		w.canvas.SetDirty()
	}
}

// releasePainter drops the window's surface, which has to go before the window.
func (w *window) releasePainter() {
	if p, ok := w.canvas.Painter().(*webgpu.Painter); ok {
		p.Release()
	}
}
