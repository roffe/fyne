package webgpu

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"unsafe"

	wgpu "github.com/roffe/gowgpu"
	"github.com/roffe/gowgpu/gputypes"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal"
	"fyne.io/fyne/v2/internal/cache"
	paint "fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/internal/painter/geom"
	"fyne.io/fyne/v2/theme"
)

// This file follows internal/painter/gl/draw.go, atlas.go and texture.go
// function for function, so that both painters put the same pixels in the
// same places. Where they differ it is WebGPU's doing: uniforms are a struct
// per draw, quads come from the uniforms rather than a vertex buffer, and
// texture and window coordinates both start at the top left.

const (
	edgeSoftness = 0.5
	// lineVertices is the two triangles a line is drawn as.
	lineVertices = 6
	// polygonPointRows is how many vec4 rows of the polygon record hold its
	// points, two to a row, ahead of the corner radii.
	polygonPointRows = 8
	epsilon          = 1e-9

	// glyphVertexFloats is one batched quad vertex: position in clip space,
	// atlas texture coordinate and a straight-alpha colour.
	glyphVertexFloats = 2 + 2 + 4
	// lineVertexFloats is one batched line vertex: position and normal in clip
	// space, a straight-alpha colour, then half width and feather.
	lineVertexFloats = 2 + 2 + 4 + 2
)

// shapeParams mirrors Shape in shaders/common.wgsl, one [4]float32 per vec4.
type shapeParams struct {
	Quad, Frame, Bounds, FillColor, StrokeColor, Radius, Params, Params2, ShadowColor, Shadow, ShadowStyle [4]float32
}

// polyParams mirrors Record in shaders/arbitrary_polygon.wgsl: the only shape
// whose record carries more than a shapeParams.
type polyParams struct {
	Shape shapeParams
	Data  [12][4]float32
}

// texParams mirrors Tex in shaders/texture.wgsl.
type texParams struct {
	Quad, Inset, Params [4]float32
}

// blurParams mirrors Blur in shaders/blur.wgsl.
type blurParams struct {
	Quad, Params, Shape [4]float32
	Kernel              [26][4]float32
}

func (p *Painter) drawObject(o fyne.CanvasObject, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) {
	switch obj := o.(type) {
	case *canvas.Blur:
		p.drawBlur(obj, pos, frame)
	case *canvas.Circle:
		p.drawCircle(obj, pos, frame)
	case *canvas.Line:
		p.drawLine(obj, pos, frame)
	case *canvas.Image:
		p.drawImage(obj, pos, frame)
	case *canvas.Raster:
		p.drawTextureWithDetails(obj, p.newRasterTexture, pos, obj.Size(), frame, canvas.ImageFillStretch, float32(obj.Alpha()), 0)
	case *canvas.Rectangle:
		p.drawRectangle(obj, pos, frame)
	case *canvas.Text:
		p.drawText(obj, pos, frame, clip)
	case *canvas.LinearGradient:
		p.drawTextureWithDetails(obj, p.newLinearGradientTexture, pos, obj.Size(), frame, canvas.ImageFillStretch, 1, 0)
	case *canvas.RadialGradient:
		p.drawTextureWithDetails(obj, p.newRadialGradientTexture, pos, obj.Size(), frame, canvas.ImageFillStretch, 1, 0)
	case *canvas.RegularPolygon:
		p.drawPolygon(obj, pos, frame)
	case *canvas.ArbitraryPolygon:
		p.drawArbitraryPolygon(obj, pos, frame)
	case *canvas.Arc:
		p.drawArc(obj, pos, frame)
	case *canvas.BezierCurve:
		p.drawBezierCurve(obj, pos, frame)
	case *canvas.Shader:
		p.drawShader(obj, pos, frame)
	case *canvas.VideoFrame:
		p.drawVideo(obj, pos, frame)
	case *canvas.Ellipse:
		p.drawEllipse(obj, pos, frame)
	}
}

func (p *Painter) drawBlur(b *canvas.Blur, pos fyne.Position, frame fyne.Size) {
	if b.Radius == 0 || p.frame == nil {
		return
	}
	radius := b.Radius * p.pixScale

	x := int(roundToPixel(pos.X*p.pixScale, 1.0))
	y := int(roundToPixel(pos.Y*p.pixScale, 1.0))
	bw := int(roundToPixel(b.Size().Width*p.pixScale, 1.0))
	bh := int(roundToPixel(b.Size().Height*p.pixScale, 1.0))
	if bw <= 0 || bh <= 0 {
		return
	}
	p.flushBatch() // the snapshot reads the frame, so queued quads have to be in it

	if p.blurSnap == nil || p.blurSnap.w != bw || p.blurSnap.h != bh {
		if p.blurSnap != nil {
			p.retire(p.blurSnap.release)
			p.blurSnap = nil
		}
		// Smooth sampling keeps a stretched kernel (sampleScale > 1) smooth.
		snap, err := p.gpu.newTexture(bw, bh, gputypes.TextureUsageCopyDst, p.gpu.linear)
		if err != nil {
			logError("blur", err)
			return
		}
		p.blurSnap = snap
	}

	// Cap the kernel at 101 samples (maxKernelRadius = 50.0) per pass. Past
	// that the samples are spread out, and smooth sampling fills between them.
	kernelRadius := radius
	var sampleScale float32 = 1.0
	const maxKernelRadius = 50.0
	if kernelRadius > maxKernelRadius {
		sampleScale = kernelRadius / maxKernelRadius
		kernelRadius = maxKernelRadius
	}
	values, ok := cache.GetBlurKernel(kernelRadius)
	if !ok {
		values = createKernel(kernelRadius)
		cache.SetBlurKernel(kernelRadius, values)
	}

	quad, _, _ := p.rectCoords(b.Size(), pos, frame, canvas.ImageFillStretch, 1.0, 0)
	cornerRadius := min(paint.GetMaximumRadius(b.Size()), b.CornerRadius)
	u := blurParams{
		Quad:   quad,
		Params: [4]float32{kernelRadius, sampleScale, 1 / float32(bw), 0},
		Shape:  [4]float32{roundToPixel(cornerRadius*p.pixScale, 1.0), float32(bw), float32(bh)},
	}
	for i, v := range values {
		u.Kernel[i/4][i%4] = v
	}

	// Horizontally from a snapshot of what is under the blur, then vertically
	// from a snapshot of that, each replacing the pixels it read.
	p.snapshot(x, y, bw, bh)
	drawOne(p, p.gpu.blur, &u, p.blurSnap.group)
	p.snapshot(x, y, bw, bh)
	u.Params[2], u.Params[3] = 0, 1/float32(bh)
	drawOne(p, p.gpu.blur, &u, p.blurSnap.group)
}

// snapshot copies the w by h pixels of the frame at x, y into the blur source.
// The copy goes between the passes in the same encoder: gowgpu scopes usage per
// pass and per copy, and puts the barriers between them.
func (p *Painter) snapshot(x, y, w, h int) {
	if p.clear && p.frame != nil {
		p.renderPass() // the copy reads the frame, so a clear still to come goes first
	}
	p.endPass()
	enc := p.encoder()
	r := image.Rect(x, y, x+w, y+h).Intersect(image.Rect(0, 0, p.width, p.height))
	if enc == nil || r.Empty() {
		return
	}
	enc.CopyTextureToTexture(p.frame.tex, p.blurSnap.tex, []wgpu.TextureCopy{{
		Source:      wgpu.ImageCopyTexture{Texture: p.frame.tex, Origin: wgpu.Origin3D{X: u32(r.Min.X), Y: u32(r.Min.Y)}},
		Destination: wgpu.ImageCopyTexture{Texture: p.blurSnap.tex, Origin: wgpu.Origin3D{X: u32(r.Min.X - x), Y: u32(r.Min.Y - y)}},
		Size:        wgpu.Extent3D{Width: u32(r.Dx()), Height: u32(r.Dy()), DepthOrArrayLayers: 1},
	}})
}

func (p *Painter) drawCircle(circle *canvas.Circle, pos fyne.Position, frame fyne.Size) {
	quad, bounds := p.vecRectCoords(pos, circle, frame, 1, circle.Shadow)
	u := p.shape(quad, bounds, frame)

	strokeWidthScaled := roundToPixel(circle.StrokeWidth*p.pixScale, 1.0)
	u.Frame[3] = strokeWidthScaled * 0.5
	u.Params[0] = (u.Bounds[2] - u.Bounds[0] - strokeWidthScaled) * 0.5
	u.Params[1] = (u.Bounds[3] - u.Bounds[1] - strokeWidthScaled) * 0.5

	radiusScaled := roundToPixel(paint.GetMaximumRadius(circle.Size())*p.pixScale, 1.0)
	u.Radius = [4]float32{radiusScaled, radiusScaled, radiusScaled, radiusScaled}
	u.FillColor = fragmentColor(circle.FillColor)
	u.StrokeColor = fragmentColor(circle.StrokeColor)
	p.setShadow(&u, circle.Shadow)
	drawQuad(p, p.gpu.shapes[pipeRoundRectangle], &u, nil)
}

func (p *Painter) drawImage(img *canvas.Image, pos fyne.Position, frame fyne.Size) {
	if dirty := cache.TakeTextureDirty(img); !dirty.Empty() && !p.updateImageTexture(img, dirty) {
		p.Free(img) // so that all of its pixels are uploaded again
	}
	p.drawTextureWithDetails(img, p.newImageTexture, pos, img.Size(), frame, img.FillMode, float32(img.Alpha()), 0)
}

// updateImageTexture uploads the pixels in the region dirty of img into the
// texture it already has. It returns false if that texture cannot be updated
// in part, as it was not uploaded from pixels laid out like those of img.Image.
func (p *Painter) updateImageTexture(img *canvas.Image, dirty image.Rectangle) bool {
	id, ok := cache.GetTexture(img)
	t, src := p.textures[id], img.Image
	if !ok || t == nil || src == nil {
		return false
	}
	bounds := src.Bounds()
	if _, uniform := src.(*image.Uniform); uniform || bounds.Dx() != t.w || bounds.Dy() != t.h {
		return false
	}
	dirty = dirty.Intersect(bounds)
	if dirty.Empty() {
		return true
	}

	// The rows go up as they are when the image holds them whole; otherwise
	// the region is drawn into a tight copy first.
	rgba, ok := src.(*image.RGBA)
	off := 0
	if ok {
		off = rgba.PixOffset(dirty.Min.X, dirty.Min.Y)
	}
	if !ok || off+dirty.Dy()*rgba.Stride > len(rgba.Pix) {
		part := &image.RGBA{Rect: dirty, Stride: 4 * dirty.Dx()}
		if size := part.Stride * dirty.Dy(); cap(p.pixels) < size {
			p.pixels = make([]uint8, size)
		}
		part.Pix = p.pixels[:part.Stride*dirty.Dy()]
		draw.Draw(part, dirty, src, dirty.Min, draw.Src)
		rgba, off = part, 0
	}
	p.gpu.write(t, rgba.Pix[off:off+dirty.Dy()*rgba.Stride], rgba.Stride, dirty.Min.X-bounds.Min.X, dirty.Min.Y-bounds.Min.Y, dirty.Dx(), dirty.Dy())
	return true
}

func (p *Painter) drawLine(line *canvas.Line, pos fyne.Position, frame fyne.Size) {
	if line.StrokeColor == color.Transparent || line.StrokeColor == nil || line.StrokeWidth == 0 {
		return
	}
	points, halfWidth, feather := p.lineCoords(pos, line.Position1, line.Position2, line.StrokeWidth, 0.5, frame)
	col := fragmentColor(line.StrokeColor)
	p.flushInstances() // anything queued before this line belongs under it
	p.flushQuads()
	p.flushSlug()
	for v := range lineVertices {
		pt := points[v*4:]
		p.linePending = append(p.linePending, pt[0], pt[1], pt[2], pt[3], col[0], col[1], col[2], col[3], halfWidth, feather)
	}
}

func (p *Painter) drawBezierCurve(bezierCurve *canvas.BezierCurve, pos fyne.Position, frame fyne.Size) {
	if bezierCurve.StrokeColor == color.Transparent || bezierCurve.StrokeColor == nil || bezierCurve.StrokeWidth == 0 {
		return
	}
	quad, bounds := p.vecRectCoords(pos, bezierCurve, frame, 0.0, canvas.Shadow{})
	u := p.shape(quad, bounds, frame)

	// ensure stroke width is not larger than the size of the object
	strokeWidth := min(bezierCurve.StrokeWidth, min(bezierCurve.Size().Width, bezierCurve.Size().Height))
	if strokeWidth < 1 {
		strokeWidth = 1
	}
	p1, p2, cp := paint.NormalizeBezierCurvePoints(bezierCurve.StartPoint, bezierCurve.EndPoint, bezierCurve.ControlPoints, bezierCurve.Size(), strokeWidth/2)
	u.Params = [4]float32{p.scaled(p1.X), p.scaled(p1.Y), p.scaled(p2.X), p.scaled(p2.Y)}
	if len(cp) > 0 {
		u.Params2[0], u.Params2[1] = p.scaled(cp[0].X), p.scaled(cp[0].Y)
	}
	if len(cp) > 1 {
		u.Params2[2], u.Params2[3] = p.scaled(cp[1].X), p.scaled(cp[1].Y)
	}
	u.Radius[0] = min(float32(len(cp)), 2)
	u.Frame[3] = roundToPixel(strokeWidth*p.pixScale, 1.0) * 0.5
	u.StrokeColor = fragmentColor(bezierCurve.StrokeColor)
	drawQuad(p, p.gpu.shapes[pipeBezierCurve], &u, nil)
}

func (p *Painter) drawArbitraryPolygon(polygon *canvas.ArbitraryPolygon, pos fyne.Position, frame fyne.Size) {
	if len(polygon.Points) < 3 || (isTransparent(polygon.FillColor) && (isTransparent(polygon.StrokeColor) || polygon.StrokeWidth == 0)) {
		return
	}
	quad, bounds := p.vecRectCoords(pos, polygon, frame, 0.0, canvas.Shadow{})
	u := polyParams{Shape: p.shape(quad, bounds, frame)}

	numPoints := int(min(paint.ArbitraryPolygonVerticesMaximum, float32(len(polygon.Points))))
	u.Shape.Params[0] = float32(numPoints)

	size := polygon.Size()
	// On the stack: a polygon has at most ArbitraryPolygonVerticesMaximum points.
	var pointsBuf [paint.ArbitraryPolygonVerticesMaximum]fyne.Position
	var radiiBuf [paint.ArbitraryPolygonVerticesMaximum]float32
	fixedPoints := pointsBuf[:numPoints]
	cornerRadii := radiiBuf[:numPoints]
	for i := range numPoints {
		px, py := polygon.Points[i].X, polygon.Points[i].Y
		if polygon.NormalizedPoints {
			px, py = px*size.Width, py*size.Height
		}
		fixedPoints[i] = fyne.NewPos(min(max(px, 0), max(size.Width, 0)), min(max(py, 0), max(size.Height, 0)))
		if i < len(polygon.CornerRadii) {
			cornerRadii[i] = polygon.CornerRadii[i]
		}
	}
	cornerRadii = paint.GetMaximumCornerRadii(fixedPoints, cornerRadii)

	// Points two to a vec4 in the first 8 entries, radii four to a vec4 after.
	for i := range numPoints {
		u.Data[i/2][i%2*2] = p.scaled(fixedPoints[i].X)
		u.Data[i/2][i%2*2+1] = p.scaled(fixedPoints[i].Y)
		u.Data[polygonPointRows+i/4][i%4] = p.scaled(cornerRadii[i])
	}

	u.Shape.FillColor = fragmentColor(polygon.FillColor)
	u.Shape.StrokeColor = fragmentColor(polygon.StrokeColor)
	u.Shape.Frame[3] = roundToPixel(polygon.StrokeWidth*p.pixScale, 1.0)
	drawQuad(p, p.gpu.shapes[pipeArbitraryPolygon], &u, nil)
}

func (p *Painter) drawRectangle(rect *canvas.Rectangle, pos fyne.Position, frame fyne.Size) {
	topRightRadius := paint.GetCornerRadius(rect.TopRightCornerRadius, rect.CornerRadius)
	topLeftRadius := paint.GetCornerRadius(rect.TopLeftCornerRadius, rect.CornerRadius)
	bottomRightRadius := paint.GetCornerRadius(rect.BottomRightCornerRadius, rect.CornerRadius)
	bottomLeftRadius := paint.GetCornerRadius(rect.BottomLeftCornerRadius, rect.CornerRadius)
	if topRightRadius == 0 && topLeftRadius == 0 && bottomRightRadius == 0 && bottomLeftRadius == 0 &&
		p.pushSolidRect(rect, pos, frame) {
		return
	}
	p.drawOblong(rect, rect.FillColor, rect.StrokeColor, rect.StrokeWidth, topRightRadius, topLeftRadius, bottomRightRadius, bottomLeftRadius, rect.Aspect, rect.Shadow, pos, frame)
}

func (p *Painter) drawOblong(obj fyne.CanvasObject, fill, stroke color.Color, strokeWidth, topRightRadius, topLeftRadius, bottomRightRadius, bottomLeftRadius, aspect float32, shadow canvas.Shadow, pos fyne.Position, frame fyne.Size) {
	if !paint.IsShadowVisible(shadow) && isTransparent(fill) && (isTransparent(stroke) || strokeWidth == 0) {
		return
	}

	quad, bounds := p.vecRectCoords(pos, obj, frame, aspect, shadow)
	u := p.shape(quad, bounds, frame)
	strokeWidthScaled := roundToPixel(strokeWidth*p.pixScale, 1.0)
	shape := pipeRectangle
	if topRightRadius != 0 || topLeftRadius != 0 || bottomRightRadius != 0 || bottomLeftRadius != 0 {
		shape = pipeRoundRectangle
		u.Frame[3] = strokeWidthScaled * 0.5
		u.Params[0] = (u.Bounds[2] - u.Bounds[0] - strokeWidthScaled) * 0.5
		u.Params[1] = (u.Bounds[3] - u.Bounds[1] - strokeWidthScaled) * 0.5

		// the maximum possible corner radii for a circular shape, calculated taking into account the rect coords with aspect ratio
		size := fyne.NewSize(bounds[2]-bounds[0], bounds[3]-bounds[1])
		u.Radius = [4]float32{
			p.scaled(paint.GetMaximumCornerRadius(topRightRadius, topLeftRadius, bottomRightRadius, size)),
			p.scaled(paint.GetMaximumCornerRadius(bottomRightRadius, bottomLeftRadius, topRightRadius, size)),
			p.scaled(paint.GetMaximumCornerRadius(topLeftRadius, topRightRadius, bottomLeftRadius, size)),
			p.scaled(paint.GetMaximumCornerRadius(bottomLeftRadius, bottomRightRadius, topLeftRadius, size)),
		}
	} else {
		u.Frame[3] = strokeWidthScaled
	}

	u.FillColor = fragmentColor(fill)
	u.StrokeColor = fragmentColor(stroke)
	p.setShadow(&u, shadow)
	drawQuad(p, p.gpu.shapes[shape], &u, nil)
}

func (p *Painter) drawPolygon(polygon *canvas.RegularPolygon, pos fyne.Position, frame fyne.Size) {
	if (isTransparent(polygon.FillColor) && (isTransparent(polygon.StrokeColor) || polygon.StrokeWidth == 0)) || polygon.Sides < 3 {
		return
	}
	size := polygon.Size()
	quad, bounds := p.vecRectCoords(pos, polygon, frame, 0.0, canvas.Shadow{})
	u := p.shape(quad, bounds, frame)

	cornerRadius := min(paint.GetMaximumRadius(size), polygon.CornerRadius)
	u.Params = [4]float32{p.scaled(min(size.Width, size.Height) / 2), polygon.Angle, float32(polygon.Sides), p.scaled(cornerRadius)}
	u.Frame[3] = roundToPixel(polygon.StrokeWidth*p.pixScale, 1.0)
	u.FillColor = fragmentColor(polygon.FillColor)
	u.StrokeColor = fragmentColor(polygon.StrokeColor)
	drawQuad(p, p.gpu.shapes[pipeRegularPolygon], &u, nil)
}

func (p *Painter) drawArc(arc *canvas.Arc, pos fyne.Position, frame fyne.Size) {
	if (isTransparent(arc.FillColor) && (isTransparent(arc.StrokeColor) || arc.StrokeWidth == 0)) || arc.StartAngle == arc.EndAngle {
		return
	}
	quad, bounds := p.vecRectCoords(pos, arc, frame, 0.0, canvas.Shadow{})
	u := p.shape(quad, bounds, frame)

	outerRadius := min(arc.Size().Width, arc.Size().Height) / 2
	innerRadius := outerRadius * float32(math.Min(1.0, math.Max(0.0, float64(arc.CutoutRatio))))
	startAngle, endAngle := paint.NormalizeArcAngles(arc.StartAngle, arc.EndAngle)
	u.Params = [4]float32{p.scaled(innerRadius), p.scaled(outerRadius), startAngle, endAngle}
	u.Radius[0] = p.scaled(min(paint.GetMaximumRadiusArc(outerRadius, innerRadius, arc.EndAngle-arc.StartAngle), arc.CornerRadius))
	u.Frame[3] = roundToPixel(arc.StrokeWidth*p.pixScale, 1.0)
	u.FillColor = fragmentColor(arc.FillColor)
	u.StrokeColor = fragmentColor(arc.StrokeColor)
	drawQuad(p, p.gpu.shapes[pipeArc], &u, nil)
}

func (p *Painter) drawEllipse(ellipse *canvas.Ellipse, pos fyne.Position, frame fyne.Size) {
	size := ellipse.Size()

	// when rotated, the ellipse needs more space
	// add half the difference between width and height as padding
	// with padding the final size is a square
	width, height := size.Components()
	rotPad := float32(math.Abs(float64(width)-float64(height)) / 2)
	xPad, yPad := float32(0), float32(0)
	if width > height {
		yPad = rotPad
	} else {
		xPad = rotPad
	}

	quad, bounds := p.vecRectCoordsWithPad(pos, ellipse, frame, -xPad, -yPad, ellipse.Shadow)
	u := p.shape(quad, bounds, frame)
	u.Frame[3] = roundToPixel(ellipse.StrokeWidth*p.pixScale, 1.0)
	u.Radius = [4]float32{p.scaled(size.Width / 2), p.scaled(size.Height / 2)}
	u.Params[0] = 0 // rotation in degrees, reserved for a future API like GL's
	u.FillColor = fragmentColor(ellipse.FillColor)
	u.StrokeColor = fragmentColor(ellipse.StrokeColor)
	p.setShadow(&u, ellipse.Shadow)
	drawQuad(p, p.gpu.shapes[pipeEllipse], &u, nil)
}

func (p *Painter) drawText(text *canvas.Text, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) {
	if text.Text == "" {
		return
	}
	decorated := text.TextStyle.Underline || text.TextStyle.Strikethrough
	if text.Text == " " && !decorated {
		return
	}

	size := text.MinSize()
	containerSize := text.Size()
	switch text.Alignment {
	case fyne.TextAlignTrailing:
		pos = fyne.NewPos(pos.X+containerSize.Width-size.Width, pos.Y)
	case fyne.TextAlignCenter:
		pos = fyne.NewPos(pos.X+(containerSize.Width-size.Width)/2, pos.Y)
	}

	if containerSize.Height > size.Height {
		pos = fyne.NewPos(pos.X, pos.Y+(containerSize.Height-size.Height)/2)
	}

	// text size is sensitive to position on screen
	size.Width = roundToPixel(size.Width, p.pixScale)
	size.Height = roundToPixel(size.Height, p.pixScale)
	size.Width += roundToPixel(paint.VectorPad(text), p.pixScale) // italic overspill to the right
	size.Height += roundToPixel(paint.TextVectorPad, p.pixScale)  // space below for descenders / underline
	// The atlas draws a quad per glyph out of one shared texture; the whole-run
	// texture is only for strings it cannot represent.
	if paint.SlugText && p.drawTextSlug(text, pos, frame, clip) || p.drawTextFromAtlas(text, pos, frame, clip) {
		p.freeTextWindow(text)
	} else if fullWidth := int(math.Ceil(float64(size.Width * p.pixScale))); fullWidth <= p.maxTextureSize {
		p.freeTextWindow(text)
		p.drawTextureWithDetails(text, p.newTextTexture, pos, size, frame, canvas.ImageFillStretch, 1.0, 0)
	} else {
		p.drawTextWindow(text, pos, size, frame, clip, fullWidth)
	}

	if decorated {
		_, baseline := cache.GetFontMetrics(text.Text, text.TextSize, text.TextStyle, text.FontSource)
		line := canvas.NewLine(text.Color)
		line.Resize(fyne.NewSize(size.Width, 0))
		if text.TextStyle.Underline {
			p.drawLine(line, fyne.NewPos(pos.X, pos.Y+baseline+paint.UnderlineOffsetFromBaseline), frame)
		}
		if text.TextStyle.Strikethrough {
			p.drawLine(line, fyne.NewPos(pos.X, pos.Y+baseline*paint.StrikethroughToBaselineFactor), frame)
		}
	}
}

func (p *Painter) drawTextureWithDetails(o fyne.CanvasObject, creator func(fyne.CanvasObject) cache.TextureType,
	pos fyne.Position, size, frame fyne.Size, fill canvas.ImageFill, alpha float32, pad float32,
) {
	id, ok := p.getTexture(o, creator)
	if !ok {
		return
	}

	cornerRadius := float32(0)
	aspect := float32(0)
	if img, ok := o.(*canvas.Image); ok {
		aspect = img.Aspect()
		if aspect == 0 {
			aspect = 1 // fallback, should not occur - normally an image load error
		}
		if img.CornerRadius > 0 {
			cornerRadius = img.CornerRadius
		}
	}
	t := p.textures[id]
	if t == nil {
		return
	}

	quad, insets, inner := p.rectCoords(size, pos, frame, fill, aspect, pad)
	if cornerRadius != 0 {
		cornerRadius = min(paint.GetMaximumRadius(size), cornerRadius) * p.pixScale
	}
	u := texParams{
		Quad:   quad,
		Inset:  insets,
		Params: [4]float32{alpha, cornerRadius, inner.Width * p.pixScale, inner.Height * p.pixScale},
	}
	drawQuad(p, p.gpu.texture, &u, t.group)
}

// drawTextFromAtlas queues one quad per visible glyph of the run, reporting
// false when the string has to go down the whole-run texture path instead.
func (p *Painter) drawTextFromAtlas(text *canvas.Text, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) bool {
	if !p.ensureAtlas() {
		return false
	}
	clipPos, clipSize := fyne.Position{}, frame
	if clip != nil {
		clipPos, clipSize = clip.Rect()
	}
	minX := max(clipPos.X, 0) * p.pixScale
	maxX := min(clipPos.X+clipSize.Width, frame.Width) * p.pixScale
	var ok bool
	p.quadScratch, ok = p.atlas.TextQuads(p.quadScratch[:0], text, pos, p.pixScale, minX, maxX, p)
	if !ok {
		return false
	}

	col := text.Color
	if col == nil {
		col = theme.Color(theme.ColorNameForeground)
	}
	c := fragmentColor(col)
	fw, fh := p.scaleFrameSize(frame)
	for _, q := range p.quadScratch {
		p.pushQuad(q, c, fw, fh)
	}
	return true
}

// pushSolidRect queues a plain filled rectangle on the batch instead of drawing
// it on its own. Only the square-cornered, unstroked, unshadowed case
// qualifies: for it the rectangle shader fills exactly the snapped pixels with
// no antialiasing, which a quad on the same pixels reproduces. Reports false
// when the shader is needed.
func (p *Painter) pushSolidRect(r *canvas.Rectangle, pos fyne.Position, frame fyne.Size) bool {
	if r.Aspect != 0 || r.StrokeWidth != 0 || paint.IsShadowVisible(r.Shadow) {
		return false
	}
	if isTransparent(r.FillColor) {
		return true // nothing to draw, and no stroke or shadow either
	}

	// The snapping vecRectCoords applies, without the antialiasing skirt it adds
	// for the shader's quad.
	size := r.Size()
	x1, x2, y1, y2 := p.scaleRectCoords(
		roundToPixel(pos.X, p.pixScale), roundToPixel(pos.X+size.Width, p.pixScale),
		roundToPixel(pos.Y, p.pixScale), roundToPixel(pos.Y+size.Height, p.pixScale),
	)
	fw, fh := p.scaleFrameSize(frame)
	if paint.SlugText { // the batch the text around it is in
		p.pushSlug(paint.SlugQuad{X1: x1, Y1: y1, X2: x2, Y2: y2}, paint.SlugSolid, fragmentColor(r.FillColor), fw, fh)
		return true
	}
	if !p.ensureAtlas() {
		return false
	}
	u, v, ok := p.atlas.White(p)
	if !ok {
		return false
	}
	p.pushQuad(paint.GlyphQuad{X1: x1, Y1: y1, X2: x2, Y2: y2, U1: u, V1: v, U2: u, V2: v}, fragmentColor(r.FillColor), fw, fh)
	return true
}

// pushQuad queues q, given in destination pixels, in a straight-alpha colour.
func (p *Painter) pushQuad(q paint.GlyphQuad, col [4]float32, fw, fh float32) {
	p.flushInstances() // anything queued before this quad belongs under it
	p.flushLines()
	p.flushSlug()
	// Pixels to clip space: x doubles and shifts, y additionally flips.
	x1, y1 := q.X1/fw*2-1, 1-q.Y1/fh*2
	x2, y2 := q.X2/fw*2-1, 1-q.Y2/fh*2
	r, g, b, a := col[0], col[1], col[2], col[3]
	p.glyphPending = append(
		p.glyphPending,
		x1, y1, q.U1, q.V1, r, g, b, a,
		x2, y1, q.U2, q.V1, r, g, b, a,
		x1, y2, q.U1, q.V2, r, g, b, a,
		x1, y2, q.U1, q.V2, r, g, b, a,
		x2, y1, q.U2, q.V1, r, g, b, a,
		x2, y2, q.U2, q.V2, r, g, b, a,
	)
}

// flushBatch draws whatever is queued: instanced shapes and textures, glyph
// quads, Slug text or lines. It has to run before any other draw, before the
// clip changes and before a submit; queuing one kind flushes the others first,
// so at most one of them holds anything.
func (p *Painter) flushBatch() {
	p.flushInstances()
	p.flushQuads()
	p.flushLines()
	p.flushSlug()
}

func (p *Painter) flushQuads() {
	if len(p.glyphPending) > 0 {
		p.drawBatch(p.gpu.glyph, p.glyphPending, glyphVertexFloats, p.atlasTex.group)
		p.glyphPending = p.glyphPending[:0]
	}
}

func (p *Painter) flushLines() {
	if len(p.linePending) > 0 {
		p.drawBatch(p.gpu.line, p.linePending, lineVertexFloats, nil)
		p.linePending = p.linePending[:0]
	}
}

func (p *Painter) drawBatch(pipe *wgpu.RenderPipeline, floats []float32, vertexFloats int, tex *wgpu.BindGroup) {
	data := unsafe.Slice((*byte)(unsafe.Pointer(&floats[0])), len(floats)*4) //nolint:gosec // G103: the vertices go to the GPU as bytes
	stride := vertexFloats * 4
	offset := p.vertexSpace(len(data), stride)
	if offset < 0 {
		return
	}
	copy(p.vertices[offset:], data)
	pass := p.renderPass()
	if pass == nil {
		return
	}
	p.bind(pass, pipe, anyUniforms, tex)
	if p.vertexPipe != pipe {
		pass.SetVertexBuffer(0, p.vertexBuf, 0)
		p.vertexPipe = pipe
	}
	pass.Draw(gputypes.DrawArgs{VertexCount: u32(len(floats) / vertexFloats), InstanceCount: 1, FirstVertex: u32(offset / stride)})
}

// UploadGlyph copies a bitmap from the shared atlas into the texture.
func (p *Painter) UploadGlyph(img *image.RGBA, x, y int) {
	p.gpu.write(p.atlasTex, img.Pix, img.Stride, x, y, img.Rect.Dx(), img.Rect.Dy())
}

// ResizeAtlas swaps the atlas texture for an empty one of the new size.
func (p *Painter) ResizeAtlas(int) {
	if p.atlasTex != nil {
		p.retire(p.atlasTex.release)
		p.atlasTex = nil
	}
	p.ensureAtlas()
}

// ensureAtlas allocates the atlas texture the first time anything is batched.
// WebGPU hands out textures cleared, so unlike GL there is nothing to upload.
func (p *Painter) ensureAtlas() bool {
	if p.atlasTex == nil {
		size := p.atlas.Size()
		tex, err := p.gpu.newTexture(size, size, gputypes.TextureUsageCopyDst, p.gpu.linear)
		if err != nil {
			logError("atlas allocation", err)
			return false
		}
		p.atlasTex = tex
	}
	return true
}

func (p *Painter) freeTexture(id cache.TextureType) {
	if t := p.textures[id]; t != nil {
		delete(p.textures, id)
		p.retire(t.release)
	}
	if v := p.videos[id]; v != nil {
		delete(p.videos, id)
		p.retire(v.release)
	}
}

func (p *Painter) getTexture(object fyne.CanvasObject, creator func(fyne.CanvasObject) cache.TextureType) (cache.TextureType, bool) {
	if t, ok := object.(*canvas.Text); ok {
		custom := ""
		if t.FontSource != nil {
			custom = t.FontSource.Name()
		}
		ent := cache.FontCacheEntry{Color: t.Color, Canvas: p.canvas}
		ent.Text = t.Text
		ent.Size = t.TextSize
		ent.Style = t.TextStyle
		ent.Source = custom

		id, ok := cache.GetTextTexture(ent)
		if !ok {
			id = creator(object)
			cache.SetTextTexture(ent, id, p.canvas, func() { p.freeTexture(id) })
		}
		return id, cache.IsValid(id)
	}

	id, ok := cache.GetTexture(object)
	if !ok {
		id = creator(object)
		cache.SetTexture(object, id, p.canvas)
	}
	return id, cache.IsValid(id)
}

// imgToTexture uploads img and names it for the texture cache.
func (p *Painter) imgToTexture(img image.Image, filter canvas.ImageScale) cache.TextureType {
	smp := p.gpu.linear
	if filter == canvas.ImageScalePixels {
		smp = p.gpu.nearest
	}
	t := p.imageTexture(img, smp)
	if t == nil {
		return cache.NoTexture
	}
	nextTexture++
	id := textureID(nextTexture)
	p.textures[id] = t
	return id
}

// imageTexture uploads img to a texture sampled with smp, or returns nil for
// an empty image or a failed upload.
func (p *Painter) imageTexture(img image.Image, smp *wgpu.Sampler) *gpuTexture {
	var pix []uint8
	var stride, w, h int
	switch i := img.(type) {
	case *image.Uniform:
		c, _ := color.RGBAModel.Convert(i.C).(color.RGBA)
		pix, stride, w, h = []uint8{c.R, c.G, c.B, c.A}, 4, 1, 1
	case *image.RGBA:
		if len(i.Pix) == 0 { // image is empty
			return nil
		}
		pix, stride, w, h = i.Pix[i.PixOffset(i.Rect.Min.X, i.Rect.Min.Y):], i.Stride, i.Rect.Dx(), i.Rect.Dy()
	default:
		rgba := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		draw.Draw(rgba, rgba.Rect, img, img.Bounds().Min, draw.Over)
		return p.imageTexture(rgba, smp)
	}
	t, err := p.gpu.newTexture(w, h, gputypes.TextureUsageCopyDst, smp)
	if err != nil {
		logError("texture allocation", err)
		return nil
	}
	p.gpu.write(t, pix, stride, 0, 0, w, h)
	return t
}

func (p *Painter) newImageTexture(obj fyne.CanvasObject) cache.TextureType {
	img, _ := obj.(*canvas.Image)

	width := p.textureScale(img.Size().Width)
	height := p.textureScale(img.Size().Height)

	tex := paint.PaintImage(img, p.canvas, int(width), int(height))
	if tex == nil {
		return cache.NoTexture
	}
	return p.imgToTexture(tex, img.ScaleMode)
}

func (p *Painter) newLinearGradientTexture(obj fyne.CanvasObject) cache.TextureType {
	gradient, _ := obj.(*canvas.LinearGradient)

	w := gradient.Size().Width
	h := gradient.Size().Height
	switch a := gradient.Angle; {
	case almostEqual(a, geom.AngleQuarter), almostEqual(a, geom.AngleThreeQuarter):
		h = 1
	case almostEqual(a, geom.AngleNone), almostEqual(a, geom.AngleHalf):
		w = 1
	}
	return p.imgToTexture(gradient.Generate(int(p.textureScale(w)), int(p.textureScale(h))), canvas.ImageScaleSmooth)
}

func (p *Painter) newRadialGradientTexture(obj fyne.CanvasObject) cache.TextureType {
	gradient, _ := obj.(*canvas.RadialGradient)
	return p.imgToTexture(gradient.Generate(int(p.textureScale(gradient.Size().Width)), int(p.textureScale(gradient.Size().Height))), canvas.ImageScaleSmooth)
}

func (p *Painter) newRasterTexture(obj fyne.CanvasObject) cache.TextureType {
	rast, _ := obj.(*canvas.Raster)
	return p.imgToTexture(rast.Generator(int(p.textureScale(rast.Size().Width)), int(p.textureScale(rast.Size().Height))), rast.ScaleMode)
}

func (p *Painter) newTextTexture(obj fyne.CanvasObject) cache.TextureType {
	text, _ := obj.(*canvas.Text)
	col := text.Color
	if col == nil {
		col = theme.Color(theme.ColorNameForeground)
	}

	bounds := text.MinSize()
	width := int(math.Ceil(float64(p.textureScale(bounds.Width) + paint.VectorPad(text)))) // potentially italic overspill
	height := int(math.Ceil(float64(p.textureScale(bounds.Height) + paint.TextVectorPad))) // space below for descenders / underline
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	face := paint.CachedFontFace(text.TextStyle, text.FontSource, text)
	paint.DrawString(img, text.Text, col, face.Fonts, text.TextSize, p.pixScale, text.TextStyle)
	return p.imgToTexture(img, canvas.ImageScaleSmooth)
}

// textWindow is the part of a run too wide for one texture that was last
// drawn, offset device pixels from its start.
type textWindow struct {
	id            cache.TextureType
	offset        int
	width, height int
	scale         float32
}

func (w textWindow) covers(offset, width, height int, scale float32) bool {
	return w.height == height && w.scale == scale &&
		w.offset <= offset && w.offset+w.width >= offset+width
}

// drawTextWindow draws the visible part of a run wider than the largest
// texture from a texture as wide as the device allows, centred on it.
func (p *Painter) drawTextWindow(text *canvas.Text, pos fyne.Position, size, frame fyne.Size, clip *internal.ClipItem, fullWidth int) {
	visibleOffset, visibleWidth := visibleTextPixels(pos, size, frame, clip, p.pixScale)
	if visibleWidth <= 0 {
		return
	}
	height := int(math.Ceil(float64(size.Height * p.pixScale)))
	w, ok := p.textWindows[text]
	if id, live := cache.GetTexture(text); !ok || !live || id != w.id || !w.covers(visibleOffset, visibleWidth, height, p.pixScale) {
		p.Free(text)
		col := text.Color
		if col == nil {
			col = theme.Color(theme.ColorNameForeground)
		}
		offset, width := textTextureWindow(visibleOffset, visibleWidth, fullWidth, p.maxTextureSize)
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		face := paint.CachedFontFace(text.TextStyle, text.FontSource, text)
		paint.DrawStringOffset(img, text.Text, col, face.Fonts, text.TextSize, p.pixScale, text.TextStyle, offset)
		w = textWindow{id: p.imgToTexture(img, canvas.ImageScaleSmooth), offset: offset, width: width, height: height, scale: p.pixScale}
		cache.SetTexture(text, w.id, p.canvas)
		p.textWindows[text] = w
	}
	t := p.textures[w.id]
	if t == nil {
		return
	}
	winPos := fyne.NewPos(pos.X+float32(w.offset)/p.pixScale, pos.Y)
	winSize := fyne.NewSize(float32(w.width)/p.pixScale, size.Height)
	quad, insets, inner := p.rectCoords(winSize, winPos, frame, canvas.ImageFillStretch, 0, 0)
	u := texParams{
		Quad:   quad,
		Inset:  insets,
		Params: [4]float32{1, 0, inner.Width * p.pixScale, inner.Height * p.pixScale},
	}
	drawQuad(p, p.gpu.texture, &u, t.group)
}

func (p *Painter) freeTextWindow(text *canvas.Text) {
	if len(p.textWindows) == 0 {
		return
	}
	if _, ok := p.textWindows[text]; ok {
		p.Free(text)
	}
}

func textTextureWindow(visibleOffset, visibleWidth, fullWidth, maxWidth int) (offset, width int) {
	width = min(maxWidth, fullWidth)
	visibleWidth = min(visibleWidth, width)
	offset = max(visibleOffset-(width-visibleWidth)/2, 0)
	return min(offset, fullWidth-width), width
}

func visibleTextPixels(pos fyne.Position, size, frame fyne.Size, clip *internal.ClipItem, scale float32) (offset, width int) {
	clipPos, clipSize := fyne.Position{}, frame
	if clip != nil {
		clipPos, clipSize = clip.Rect()
	}
	left := max(pos.X, clipPos.X)
	right := min(pos.X+size.Width, clipPos.X+clipSize.Width)
	if right <= left {
		return 0, 0
	}
	offset = int(math.Floor(float64((left - pos.X) * scale)))
	width = int(math.Ceil(float64((right-pos.X)*scale))) - offset
	return offset, width
}

// shape starts the uniforms of a vector shape drawn over quad, with bounds in
// unscaled frame coordinates.
func (p *Painter) shape(quad, bounds [4]float32, frame fyne.Size) shapeParams {
	fw, fh := p.scaleFrameSize(frame)
	x1, x2, y1, y2 := p.scaleRectCoords(bounds[0], bounds[2], bounds[1], bounds[3])
	return shapeParams{
		Quad:   quad,
		Frame:  [4]float32{fw, fh, roundToPixel(edgeSoftness*p.pixScale, 1.0)},
		Bounds: [4]float32{x1, y1, x2, y2},
	}
}

func (p *Painter) setShadow(u *shapeParams, shadow canvas.Shadow) {
	if !paint.IsShadowVisible(shadow) {
		return
	}
	u.ShadowColor = fragmentColor(shadow.Color)
	u.Shadow = [4]float32{p.scaled(shadow.Offset.X), p.scaled(shadow.Offset.Y), p.scaled(shadow.BlurRadius), p.scaled(shadow.Spread)}
	u.ShadowStyle = [4]float32{1, float32(shadow.Variant)}
}

// scaled converts v to whole device pixels.
func (p *Painter) scaled(v float32) float32 {
	return roundToPixel(v*p.pixScale, 1.0)
}

func (p *Painter) lineCoords(pos, pos1, pos2 fyne.Position, lineWidth, feather float32, frame fyne.Size) (points [lineVertices * 4]float32, halfWidth, featherWidth float32) {
	// Shift line coordinates so that they match the target position.
	xPosDiff := pos.X - min(pos1.X, pos2.X)
	yPosDiff := pos.Y - min(pos1.Y, pos2.Y)
	pos1.X = roundToPixel(pos1.X+xPosDiff, p.pixScale)
	pos1.Y = roundToPixel(pos1.Y+yPosDiff, p.pixScale)
	pos2.X = roundToPixel(pos2.X+xPosDiff, p.pixScale)
	pos2.Y = roundToPixel(pos2.Y+yPosDiff, p.pixScale)

	if lineWidth <= 1 {
		offset := float32(0.5)                  // adjust location for lines < 1pt on regular display
		if lineWidth <= 0.5 && p.pixScale > 1 { // and for 1px drawing on HiDPI (width 0.5)
			offset = 0.25
		}
		if pos1.X == pos2.X {
			pos1.X -= offset
			pos2.X -= offset
		}
		if pos1.Y == pos2.Y {
			pos1.Y -= offset
			pos2.Y -= offset
		}
	}

	x1 := -1 + pos1.X/frame.Width*2
	y1 := 1 - pos1.Y/frame.Height*2
	x2 := -1 + pos2.X/frame.Width*2
	y2 := 1 - pos2.Y/frame.Height*2

	normalX := (pos2.Y - pos1.Y) / frame.Width
	normalY := (pos2.X - pos1.X) / frame.Height
	dirLength := float32(math.Sqrt(float64(normalX*normalX + normalY*normalY)))
	normalX /= dirLength
	normalY /= dirLength

	normalObjX := normalX * 0.5 * frame.Width
	normalObjY := normalY * 0.5 * frame.Height
	widthMultiplier := float32(math.Sqrt(float64(normalObjX*normalObjX + normalObjY*normalObjY)))
	halfWidth = (roundToPixel(lineWidth+feather, p.pixScale) * 0.5) / widthMultiplier
	featherWidth = feather / widthMultiplier

	return [lineVertices * 4]float32{
		// coord x, y normal x, y
		x1, y1, normalX, normalY,
		x2, y2, normalX, normalY,
		x2, y2, -normalX, -normalY,
		x2, y2, -normalX, -normalY,
		x1, y1, normalX, normalY,
		x1, y1, -normalX, -normalY,
	}, halfWidth, featherWidth
}

// rectCoords returns the clip-space quad x1, y1 (top), x2, y2 (bottom) for a
// texture, the texture coordinates at its corners, and the size it fills.
func (p *Painter) rectCoords(size fyne.Size, pos fyne.Position, frame fyne.Size, fill canvas.ImageFill, aspect, pad float32) (quad, inset [4]float32, filled fyne.Size) {
	innerSize, innerPos := rectInnerCoords(size, pos, fill, aspect)
	pixelSize, pixelPos := roundToPixelCoords(innerSize, innerPos, p.pixScale)

	x1 := -1 + (pixelPos.X-pad)/frame.Width*2
	x2 := -1 + (pixelPos.X+pixelSize.Width+pad)/frame.Width*2
	y1 := 1 - (pixelPos.Y-pad)/frame.Height*2
	y2 := 1 - (pixelPos.Y+pixelSize.Height+pad)/frame.Height*2

	xInset := float32(0.0)
	yInset := float32(0.0)
	if fill == canvas.ImageFillCover {
		viewAspect := pixelSize.Width / pixelSize.Height
		if viewAspect > aspect {
			newHeight := pixelSize.Width / aspect
			heightPad := (newHeight - pixelSize.Height) / 2
			yInset = heightPad / newHeight
		} else if viewAspect < aspect {
			newWidth := pixelSize.Height * aspect
			widthPad := (newWidth - pixelSize.Width) / 2
			xInset = widthPad / newWidth
		}
	}

	return [4]float32{x1, y1, x2, y2}, [4]float32{xInset, yInset, 1.0 - xInset, 1.0 - yInset}, innerSize
}

func rectInnerCoords(size fyne.Size, pos fyne.Position, fill canvas.ImageFill, aspect float32) (fyne.Size, fyne.Position) {
	if fill != canvas.ImageFillContain && fill != canvas.ImageFillOriginal {
		return size, pos
	}

	viewAspect := size.Width / size.Height
	if viewAspect == aspect {
		return size, pos
	}

	// change pos and size accordingly
	newWidth, newHeight := size.Width, size.Height
	newX, newY := pos.X, pos.Y
	if viewAspect > aspect {
		newWidth = size.Height * aspect
		newX += (size.Width - newWidth) / 2
	} else if viewAspect < aspect {
		newHeight = size.Width / aspect
		newY += (size.Height - newHeight) / 2
	}
	return fyne.NewSize(newWidth, newHeight), fyne.NewPos(newX, newY)
}

func (p *Painter) vecRectCoords(pos fyne.Position, rect fyne.CanvasObject, frame fyne.Size, aspect float32, shadow canvas.Shadow) (quad, bounds [4]float32) {
	xPad, yPad := float32(0), float32(0)

	if aspect != 0 {
		inner := rect.Size()
		frameAspect := inner.Width / inner.Height

		if frameAspect > aspect {
			newWidth := inner.Height * aspect
			xPad = (inner.Width - newWidth) / 2
		} else if frameAspect < aspect {
			newHeight := inner.Width / aspect
			yPad = (inner.Height - newHeight) / 2
		}
	}

	return p.vecRectCoordsWithPad(pos, rect, frame, xPad, yPad, shadow)
}

// vecRectCoordsWithPad returns the clip-space quad x1, y1 (top), x2, y2
// (bottom) a shape is rasterised over, and its pixel-aligned bounds.
func (p *Painter) vecRectCoordsWithPad(pos fyne.Position, rect fyne.CanvasObject, frame fyne.Size, xPad, yPad float32, shadow canvas.Shadow) (quad, bounds [4]float32) {
	// align each edge to a pixel so that objects sharing an edge do not overlap or leave a gap
	size := rect.Size()
	left, top := pos.X+xPad, pos.Y+yPad
	x1Pos := roundToPixel(left, p.pixScale)
	y1Pos := roundToPixel(top, p.pixScale)
	x2Pos := roundToPixel(left+size.Width-2*xPad, p.pixScale)
	y2Pos := roundToPixel(top+size.Height-2*yPad, p.pixScale)

	shadowPads := paint.GetShadowPaddings(shadow)
	shadowPadLeft := roundToPixel(shadowPads[0], p.pixScale)
	shadowPadRight := roundToPixel(shadowPads[2], p.pixScale)
	shadowPadTop := roundToPixel(shadowPads[1], p.pixScale)
	shadowPadBottom := roundToPixel(shadowPads[3], p.pixScale)

	// without edge softness adjustment the rectangle has cropped edges
	edgeSoftnessScaled := roundToPixel(edgeSoftness*p.pixScale, 1.0)
	x1Norm := -1 + (x1Pos-edgeSoftnessScaled-shadowPadLeft)*2/frame.Width
	x2Norm := -1 + (x2Pos+edgeSoftnessScaled+shadowPadRight)*2/frame.Width
	y1Norm := 1 - (y1Pos-edgeSoftnessScaled-shadowPadTop)*2/frame.Height
	y2Norm := 1 - (y2Pos+edgeSoftnessScaled+shadowPadBottom)*2/frame.Height

	return [4]float32{x1Norm, y1Norm, x2Norm, y2Norm}, [4]float32{x1Pos, y1Pos, x2Pos, y2Pos}
}

func roundToPixel(v float32, pixScale float32) float32 {
	if pixScale == 1.0 {
		return float32(math.Round(float64(v)))
	}

	return float32(math.Round(float64(v*pixScale))) / pixScale
}

func roundToPixelCoords(size fyne.Size, pos fyne.Position, pixScale float32) (fyne.Size, fyne.Position) {
	end := pos.Add(size)
	end.X = roundToPixel(end.X, pixScale)
	end.Y = roundToPixel(end.Y, pixScale)
	pos.X = roundToPixel(pos.X, pixScale)
	pos.Y = roundToPixel(pos.Y, pixScale)
	size.Width = end.X - pos.X
	size.Height = end.Y - pos.Y

	return size, pos
}

// fragmentColor returns col as straight-alpha floats, as the shaders want it.
func fragmentColor(col color.Color) [4]float32 {
	if col == nil {
		return [4]float32{}
	}
	r, g, b, a := col.RGBA()
	if a == 0 {
		return [4]float32{}
	}
	alpha := float32(a)
	return [4]float32{float32(r) / alpha, float32(g) / alpha, float32(b) / alpha, alpha / 0xffff}
}

func isTransparent(col color.Color) bool {
	return col == nil || col == color.Transparent
}

func (p *Painter) scaleFrameSize(frame fyne.Size) (width, height float32) {
	return roundToPixel(frame.Width*p.pixScale, 1.0), roundToPixel(frame.Height*p.pixScale, 1.0)
}

// Returns scaled RectCoords(x1,x2,y1,y2) in same order
func (p *Painter) scaleRectCoords(x1, x2, y1, y2 float32) (x1Scaled, x2Scaled, y1Scaled, y2Scaled float32) {
	return p.scaled(x1), p.scaled(x2), p.scaled(y1), p.scaled(y2)
}

func (p *Painter) textureScale(v float32) float32 {
	if p.pixScale == 1.0 {
		return float32(math.Round(float64(v)))
	}

	return float32(math.Round(float64(v * p.pixScale)))
}

func createKernel(radius float32) []float32 {
	sum := float32(0.0)
	length := int(radius)*2 + 1
	values := make([]float32, length)
	for i, x := 0, float64(-radius); i < length; i, x = i+1, x+1 {
		value := float32(math.Exp(-(x * x / 4 / float64(radius))))
		values[i] = value
		sum += value
	}
	for i := 0; i < length; i++ {
		values[i] /= sum
	}

	return values
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}
