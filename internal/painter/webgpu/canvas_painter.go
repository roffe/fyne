package webgpu

import (
	"image"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/internal/driver"
	"fyne.io/fyne/v2/internal/scale"
)

// CanvasPainter paints a whole canvas offscreen and reads it back, which is
// what the test driver asks of a painter (driver.Painter). The painter tests
// use it to compare WebGPU with the software painter.
type CanvasPainter struct{}

// canvasPaint keeps one test painting at a time on the shared device.
var canvasPaint sync.Mutex

// Paint renders c with a painter made for it and returns what it drew. The
// painter goes afterwards, so like a closing window it hands back the textures
// it cached for c's objects: the next painter would read their IDs as its own.
func (CanvasPainter) Paint(c fyne.Canvas) image.Image {
	canvasPaint.Lock()
	defer canvasPaint.Unlock()
	p := NewPainter(c, nil)
	p.Init()
	defer p.Release()
	defer cache.RangeTexturesFor(c, p.Free)
	return paintCanvas(p, c)
}

// paintCanvas walks c the way the GLFW canvas does and captures the result.
func paintCanvas(p *Painter, c fyne.Canvas) image.Image {
	size := c.Size()
	w, h := scale.ToScreenCoordinate(c, size.Width), scale.ToScreenCoordinate(c, size.Height)
	p.SetFrameBufferScale(1)
	p.SetOutputSize(w, h)
	p.Clear()

	clips := &internal.ClipStack{}
	paint := func(obj fyne.CanvasObject, pos, _ fyne.Position, _ fyne.Size) bool {
		if driver.IsClip(obj) {
			p.StartClipping(clips.Push(pos, obj.Size()).Rect())
		}
		p.Paint(obj, pos, size, clips.Top())
		return false
	}
	afterPaint := func(obj fyne.CanvasObject, _ fyne.Position, _ fyne.CanvasObject) {
		if driver.IsClip(obj) {
			clips.Pop()
			if top := clips.Top(); top != nil {
				p.StartClipping(top.Rect())
			} else {
				p.StopClipping()
			}
		}
	}
	driver.WalkVisibleObjectTree(c.Content(), paint, afterPaint)
	for _, o := range c.Overlays().List() {
		driver.WalkVisibleObjectTree(o, paint, afterPaint)
	}
	p.FlushGlyphs()
	return p.Capture(c)
}
