//go:build !wasm && !test_web_driver

package glfw

// windowPainter is a painter the driver can create windows for.
type windowPainter interface {
	windowHints()
	initPainter(*window) error
	makeContextCurrent(*window)
	swapBuffers(*window)
	releasePainter(*window)
}

// painters are what the first window tries in turn, WebGPU ahead of GL. The
// first one that can draw it paints every window from then on.
var painters []windowPainter

func (w *window) makeContextCurrent() { painters[0].makeContextCurrent(w) }

func (w *window) swapBuffers() { painters[0].swapBuffers(w) }

func (w *window) releasePainter() { painters[0].releasePainter(w) }
