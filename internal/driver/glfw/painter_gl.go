//go:build gl && !wgpu && !wasm && !test_web_driver

package glfw

import (
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2/internal/build"
	"fyne.io/fyne/v2/internal/painter/gl"
)

func (w *window) initPainter() {
	w.canvas.SetPainter(gl.NewPainter(w.canvas, w))
	w.canvas.Painter().Init()

	if build.IsWayland {
		glfw.SwapInterval(0)
	}
}

func (w *window) makeContextCurrent() {
	w.view().MakeContextCurrent()
}

func (w *window) swapBuffers() {
	w.view().SwapBuffers()
}

// releasePainter has nothing to do, the GL context goes with the window.
func (*window) releasePainter() {}
