package webgpu

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/internal"
	"fyne.io/fyne/v2/internal/driver"
	softpaint "fyne.io/fyne/v2/internal/painter/software"
	internalTest "fyne.io/fyne/v2/internal/test"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var wgslField = regexp.MustCompile(`(?m)^\s*(\w+):\s*(vec4<f32>|array<vec4<f32>,\s*(\d+)>|Shape),`)

// The uniform structs are the one place a slip corrupts every draw silently:
// WGSL reads whatever bytes sit at each offset. Pin the Go mirrors to them.
func TestUniformsMatchShaders(t *testing.T) {
	for _, c := range []struct {
		src, name string
		goType    any
	}{
		{shaderCommon, "Shape", shapeParams{}},
		{shaderArbitraryPolygon, "Record", polyParams{}},
		{shaderTexture, "Tex", texParams{}},
		{shaderBlur, "Blur", blurParams{}},
		{shaderUserPrelude, "Fyne", userParams{}},
	} {
		start := strings.Index(c.src, "struct "+c.name+" {")
		end := strings.Index(c.src[start:], "\n}")
		if start < 0 || end < 0 {
			t.Fatalf("struct %s not found", c.name)
		}
		fields := wgslField.FindAllStringSubmatch(c.src[start:start+end], -1)
		typ := reflect.TypeOf(c.goType)
		if len(fields) != typ.NumField() {
			t.Fatalf("%s: shader has %d members, Go has %d", c.name, len(fields), typ.NumField())
		}
		vec4s := 0
		for i, f := range fields {
			goField := typ.Field(i)
			if !strings.EqualFold(strings.ReplaceAll(f[1], "_", ""), goField.Name) {
				t.Errorf("%s member %d: shader has %s, Go has %s", c.name, i, f[1], goField.Name)
			}
			want := reflect.TypeOf([4]float32{})
			n := 1
			switch {
			case f[2] == "Shape":
				want = reflect.TypeOf(shapeParams{})
				n = int(want.Size() / 16)
			case f[3] != "":
				fmt.Sscan(f[3], &n)
				want = reflect.ArrayOf(n, want)
			}
			if goField.Type != want {
				t.Errorf("%s.%s is %s, shader wants %s", c.name, goField.Name, goField.Type, want)
			}
			vec4s += n
		}
		if size := reflect.TypeOf(c.goType).Size(); size != uintptr(vec4s*16) || size > bindWindow {
			t.Errorf("%s is %d bytes, shader has %d, a bind covers %d", c.name, size, vec4s*16, bindWindow)
		}
	}
}

// TestPainterMatchesSoftware draws the software painter's test scenes on the
// GPU and compares. Antialiasing differs between a scanline rasteriser and
// shaders, so a pixel only counts as wrong when a channel is far off, and only
// a small share of them may be. WGPU_DUMP=dir writes both images of a failure.
func TestPainterMatchesSoftware(t *testing.T) {
	test.NewTempApp(t)
	test.ApplyTheme(t, test.Theme())
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}

	padded := func(obj fyne.CanvasObject, size float32) func() software.WindowlessCanvas {
		return func() software.WindowlessCanvas {
			c := test.NewCanvas()
			c.SetPadded(true)
			c.SetContent(obj)
			c.Resize(fyne.NewSize(size+2*theme.Padding(), size+2*theme.Padding()))
			return c
		}
	}
	unpadded := func(obj fyne.CanvasObject, w, h float32) func() software.WindowlessCanvas {
		return func() software.WindowlessCanvas {
			c := test.NewCanvas()
			c.SetPadded(false)
			c.SetContent(obj)
			c.Resize(fyne.NewSize(w, h))
			return c
		}
	}
	// strokeInside insets obj by half its stroke, which moves the software
	// painter's stroke, centred on the outline, inside the original bounds,
	// where the GL shaders this painter ports put it.
	strokeInside := func(obj fyne.CanvasObject, width float32) fyne.CanvasObject {
		return container.New(layout.NewCustomPaddedLayout(width/2, width/2, width/2, width/2), obj)
	}
	scrolled := func(content fyne.CanvasObject) fyne.CanvasObject {
		scroll := container.NewScroll(content)
		scroll.Move(fyne.NewPos(10, 10))
		scroll.Resize(fyne.NewSize(50, 50))
		scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(-10, -10)})
		return container.NewWithoutLayout(scroll)
	}

	circleStroke := canvas.NewCircle(color.White)
	circleStroke.StrokeColor, circleStroke.StrokeWidth = color.Black, 4
	circleShadow := canvas.NewCircle(color.NRGBA{A: 150})
	circleShadow.Shadow = canvas.Shadow{Color: color.White, Offset: fyne.NewPos(-10, -5), BlurRadius: 3, Variant: canvas.DropShadow}
	circleShadow.Resize(fyne.NewSize(150, 150))
	circleShadow.Move(fyne.NewPos(20, 20))

	rectStroke := canvas.NewRectangle(color.Black)
	rectStroke.StrokeWidth, rectStroke.StrokeColor = 5, &color.RGBA{R: 0xff, G: 0x33, B: 0x33, A: 0xff}
	rectRound := canvas.NewRectangle(color.Black)
	rectRound.StrokeWidth, rectRound.StrokeColor = 5, &color.RGBA{R: 0xff, G: 0x33, B: 0x33, A: 0xff}
	rectRound.CornerRadius, rectRound.TopRightCornerRadius, rectRound.TopLeftCornerRadius = 35, 2, 8
	rectRound.BottomLeftCornerRadius, rectRound.BottomRightCornerRadius = 14, 20
	rectShadow := canvas.NewRectangle(color.NRGBA{A: 150})
	rectShadow.Shadow = canvas.Shadow{Color: &color.RGBA{R: 0xff, G: 0x33, B: 0x33, A: 0xff}, Offset: fyne.NewPos(3, -4), BlurRadius: 8, Variant: canvas.BoxShadow}
	rectShadow.Resize(fyne.NewSize(150, 150))
	rectShadow.Move(fyne.NewPos(12, 18))

	polygon := canvas.NewRegularPolygon(6, color.Black)
	polygon.StrokeColor, polygon.StrokeWidth, polygon.CornerRadius = color.NRGBA{R: 200, A: 255}, 3, 6
	arc := canvas.NewArc(30, 300, 0.5, color.Black)
	arc.StrokeColor, arc.StrokeWidth, arc.CornerRadius = color.NRGBA{B: 200, A: 255}, 2, 4
	heart := canvas.NewArbitraryPolygon([]fyne.Position{
		fyne.NewPos(50, 25), fyne.NewPos(80, 5), fyne.NewPos(100, 40),
		fyne.NewPos(50, 95), fyne.NewPos(0, 40), fyne.NewPos(20, 5),
	}, color.NRGBA{R: 255, A: 255})
	heart.CornerRadii = []float32{0, 20, 20, 0, 20, 20}
	heart.Resize(fyne.NewSize(100, 100))
	ellipse := canvas.NewEllipse(color.Black)
	ellipse.StrokeColor, ellipse.StrokeWidth = color.NRGBA{G: 200, A: 255}, 3

	line := canvas.NewLine(color.Black)
	line.StrokeWidth = 6
	var thin []fyne.CanvasObject
	for i, sw := range []float32{4, 2, 1, 0.5, 0.3} {
		l := canvas.NewLine(color.RGBA{R: 255, A: 255})
		l.StrokeWidth = sw
		l.Position1, l.Position2 = fyne.NewPos(float32(i*20), 10), fyne.NewPos(float32(i*20)+15, 10)
		thin = append(thin, l)
	}
	quadratic := canvas.NewQuadraticBezierCurve(fyne.NewPos(0, 0), fyne.NewPos(10, 50), fyne.NewPos(70, 30), color.Black)
	quadratic.StrokeWidth = 6
	cubic := canvas.NewCubicBezierCurve(fyne.NewPos(10, 10), fyne.NewPos(35, 60), fyne.NewPos(70, 40), fyne.NewPos(30, 10), color.Black)
	cubic.StrokeWidth = 6

	image3 := internalTest.NewCheckedImage(3, 3, 3, 3)
	smooth := canvas.NewImageFromImage(image3)
	pixels := canvas.NewImageFromImage(image3)
	pixels.ScaleMode = canvas.ImageScalePixels
	cover := canvas.NewImageFromImage(internalTest.NewCheckedImage(10, 4, 10, 4))
	cover.FillMode = canvas.ImageFillCover
	blurBase := canvas.NewImageFromImage(image3)
	blurBase.ScaleMode = canvas.ImageScalePixels
	raster := canvas.NewRasterWithPixels(func(x, y, w, h int) color.Color {
		return color.NRGBA{R: uint8(x * 255 / max(w, 1)), G: uint8(y * 255 / max(h, 1)), B: 128, A: 255}
	})

	var clipped []fyne.CanvasObject
	for _, c := range []color.NRGBA{{R: 200, A: 255}, {G: 200, A: 255}, {B: 200, A: 255}} {
		row := container.NewHBox()
		for _, shade := range []uint8{200, 150, 100} {
			col := c
			col.R, col.G, col.B = c.R/200*shade, c.G/200*shade, c.B/200*shade
			r := canvas.NewRectangle(col)
			r.SetMinSize(fyne.NewSize(20, 20))
			row.Add(r)
		}
		clipped = append(clipped, row)
	}
	gradient := canvas.NewRadialGradient(color.NRGBA{R: 200, A: 255}, color.NRGBA{B: 200, A: 255})
	gradient.SetMinSize(fyne.NewSize(100, 100))
	// Enough shapes in a row to fill several instanced batches, of two kinds
	// that share a pipeline, with lines cutting runs short every so often.
	var many []fyne.CanvasObject
	for i := range 250 {
		pos := fyne.NewPos(float32(i%25)*8+2, float32(i/25)*8+2)
		col := palette(i)
		var obj fyne.CanvasObject = canvas.NewCircle(col)
		if i%2 == 0 {
			r := canvas.NewRectangle(col)
			r.CornerRadius = 2
			obj = r
		}
		if i%37 == 0 {
			l := canvas.NewLine(color.Black)
			l.Position1, l.Position2 = pos, pos.AddXY(6, 0)
			many = append(many, l)
		}
		obj.Resize(fyne.NewSize(6, 6))
		obj.Move(pos)
		many = append(many, obj)
	}
	boldItalic := canvas.NewText("Dd", theme.Color(theme.ColorNameForeground))
	boldItalic.TextStyle, boldItalic.TextSize = fyne.TextStyle{Bold: true, Italic: true}, 42

	scenes := []struct {
		name  string
		scene func() software.WindowlessCanvas
		// ref is the software painter's scene where it differs from scene.
		ref func() software.WindowlessCanvas
		// budget overrides pixelTolerance.
		budget float64
	}{
		{name: "circle", scene: padded(canvas.NewCircle(color.Black), 70)},
		{name: "circle_stroke", scene: padded(circleStroke, 70), ref: padded(strokeInside(circleStroke, 4), 70)},
		// The inset trick is exact for a circle. The software painter's offset
		// stroke also rounds square corners, grows radii by half the stroke and
		// moves an arc's inner edge with the box, which GL's does not.
		{name: "circle_shadow", scene: padded(container.NewWithoutLayout(circleShadow), 170)},
		{name: "rect_stroke", scene: padded(rectStroke, 70), ref: padded(strokeInside(rectStroke, 5), 70), budget: 0.10},
		{name: "rect_per_corner_radius", scene: padded(rectRound, 70), ref: padded(strokeInside(rectRound, 5), 70), budget: 0.10},
		{name: "rect_shadow", scene: padded(container.NewWithoutLayout(rectShadow), 170)},
		{name: "regular_polygon", scene: padded(polygon, 70), ref: padded(strokeInside(polygon, 3), 70)},
		{name: "arc", scene: padded(arc, 70), ref: padded(strokeInside(arc, 2), 70), budget: 0.10},
		{name: "arbitrary_polygon", scene: padded(heart, 100)},
		{name: "ellipse", scene: padded(ellipse, 70), ref: padded(strokeInside(ellipse, 3), 70)},
		{name: "line", scene: padded(line, 70)},
		{name: "line_thin", scene: unpadded(container.NewWithoutLayout(thin...), 109, 28)},
		{name: "bezier_quadratic", scene: padded(quadratic, 70)},
		{name: "bezier_cubic", scene: padded(cubic, 70)},
		{name: "image_smooth", scene: padded(smooth, 70)},
		{name: "image_pixels", scene: padded(pixels, 70)},
		// GL magnifies a cover image's texels bilinearly on the GPU, the
		// software painter resamples on the CPU; a wrong crop is far more.
		{name: "image_cover", scene: padded(cover, 70), budget: 0.10},
		{name: "raster", scene: padded(raster, 70)},
		{name: "rect_clipped", scene: unpadded(scrolled(container.NewVBox(clipped...)), 70, 70)},
		{name: "gradient_clipped", scene: unpadded(scrolled(gradient), 70, 70)},
		{name: "blur", scene: padded(container.NewStack(blurBase, container.NewPadded(canvas.NewBlur(5))), 70)},
		{name: "text_clipped", scene: unpadded(scrolled(widget.NewLabel("some text\nis here\nand here")), 70, 70)},
		{name: "text_bold_italic", scene: unpadded(boldItalic, 70, boldItalic.MinSize().Height)},
		{name: "instanced", scene: unpadded(container.NewWithoutLayout(many...), 202, 82)},
	}

	const (
		channelTolerance = 64   // per channel, out of 255
		pixelTolerance   = 0.04 // share of pixels allowed past it
	)
	dump := os.Getenv("WGPU_DUMP")
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			c := s.scene()
			p := NewPainter(c, nil)
			p.Init()
			defer p.Release()
			got := paintCanvas(p, c)
			if s.ref != nil {
				c = s.ref() // after painting, as it lays the same objects out anew
			}
			ref := captureSoftware(c)
			budget := pixelTolerance
			if s.budget != 0 {
				budget = s.budget
			}

			if got.Bounds() != ref.Bounds() {
				t.Fatalf("size %v, software painter %v", got.Bounds(), ref.Bounds())
			}
			bad := 0
			for y := ref.Bounds().Min.Y; y < ref.Bounds().Max.Y; y++ {
				for x := ref.Bounds().Min.X; x < ref.Bounds().Max.X; x++ {
					if channelDiff(got.At(x, y), ref.At(x, y)) > channelTolerance {
						bad++
					}
				}
			}
			share := float64(bad) / float64(ref.Bounds().Dx()*ref.Bounds().Dy())
			t.Logf("%5.2f%% of pixels off", share*100)
			if share > budget {
				t.Errorf("%.2f%% of pixels differ from the software painter, allowed %.0f%%", share*100, budget*100)
				if dump != "" {
					writePNG(t, filepath.Join(dump, s.name+"_wgpu.png"), got)
					writePNG(t, filepath.Join(dump, s.name+"_software.png"), ref)
				}
			}
		})
	}
}

// captureSoftware renders c as the test driver does: the software painter
// over the theme background.
func captureSoftware(c fyne.Canvas) image.Image {
	size := c.Size()
	bounds := image.Rect(0, 0, int(size.Width*c.Scale()), int(size.Height*c.Scale()))
	img := image.NewNRGBA(bounds)
	draw.Draw(img, bounds, image.NewUniform(theme.Color(theme.ColorNameBackground)), image.Point{}, draw.Src)
	draw.Draw(img, bounds, softpaint.NewPainter().Paint(c), image.Point{}, draw.Over)
	return img
}

// paintCanvas walks c the way the GLFW canvas does and captures the result.
func paintCanvas(p *Painter, c fyne.Canvas) image.Image {
	size := c.Size()
	w, h := c.PixelCoordinateForPosition(fyne.NewPos(size.Width, size.Height))
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

func palette(i int) color.Color {
	return color.NRGBA{R: uint8(i * 47), G: uint8(255 - i*13), B: uint8(i * 101), A: 0xff}
}

func channelDiff(a, b color.Color) int {
	r1, g1, b1, _ := a.RGBA()
	r2, g2, b2, _ := b.RGBA()
	d := 0
	for _, v := range [][2]uint32{{r1, r2}, {g1, g2}, {b1, b2}} {
		d = max(d, abs(int(v[0]>>8)-int(v[1]>>8)))
	}
	return d
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func writePNG(t *testing.T, path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Log(err)
	}
}
