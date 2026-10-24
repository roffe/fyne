//go:build !wgpu && !wasm && !test_web_driver

package glfw

import (
	"github.com/go-gl/glfw/v3.4/glfw"

	"fyne.io/fyne/v2/internal/build"
	"fyne.io/fyne/v2/internal/painter/gl"
)

type glPainter struct{}

func init() {
	painters = append(painters, glPainter{})
}

func (glPainter) windowHints() { glWindowHints() }

func (glPainter) initPainter(w *window) error {
	w.canvas.SetPainter(gl.NewPainter(w.canvas, w))
	w.canvas.Painter().Init()

	if build.IsWayland {
		glfw.SwapInterval(0)
	}
	return nil
}

func (glPainter) makeContextCurrent(w *window) {
	w.view().MakeContextCurrent()
}

func (glPainter) swapBuffers(w *window) {
	w.view().SwapBuffers()
}

// releasePainter has nothing to do, the GL context goes with the window.
func (glPainter) releasePainter(*window) {}
