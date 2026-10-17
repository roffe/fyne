//go:build wgpu && !wasm && !test_web_driver

package glfw

import (
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2/internal/painter/webgpu"
)

// With the wgpu tag windows get no GL context: GLFW only provides the native
// window and its input, and the WebGPU painter presents to a surface on it.

func initWindowHints() {
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
}

func (w *window) initPainter() {
	target := surfaceTarget(w.viewport)
	w.canvas.SetPainter(webgpu.NewPainter(w.canvas, &target))
	w.canvas.Painter().Init()
}

// makeContextCurrent has nothing to do, WebGPU is not bound to a thread.
func (*window) makeContextCurrent() {}

func (w *window) swapBuffers() {
	w.canvas.Painter().(*webgpu.Painter).Present()
}

// releasePainter drops the window's surface, which has to go before the window.
func (w *window) releasePainter() {
	if p, ok := w.canvas.Painter().(*webgpu.Painter); ok {
		p.Release()
	}
}
