package webgpu

import (
	"github.com/roffe/gowgpu/gputypes"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal"
	paint "fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/theme"
)

// This file is the WebGPU half of Slug text, see internal/painter/slug.go.

// slugVertexFloats is one vertex: clip space position, glyph units,
// straight-alpha colour and the glyph's base in the texture.
const slugVertexFloats = 2 + 2 + 4 + 1

var slugVertexLayout = []gputypes.VertexBufferLayout{{
	ArrayStride: slugVertexFloats * 4,
	StepMode:    gputypes.VertexStepModeVertex,
	Attributes: []gputypes.VertexAttribute{
		{Format: gputypes.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
		{Format: gputypes.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
		{Format: gputypes.VertexFormatFloat32x4, Offset: 16, ShaderLocation: 2},
		{Format: gputypes.VertexFormatFloat32, Offset: 32, ShaderLocation: 3},
	},
}}

// drawTextSlug queues a quad per inked glyph of the run, reporting false when
// it has to be drawn some other way.
func (p *Painter) drawTextSlug(text *canvas.Text, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) bool {
	glyphs, ok := p.atlas.Shape(text, p.pixScale)
	if !ok {
		return false
	}
	clipPos, clipSize := fyne.Position{}, frame
	if clip != nil {
		clipPos, clipSize = clip.Rect()
	}
	minX := max(clipPos.X, 0) * p.pixScale
	maxX := min(clipPos.X+clipSize.Width, frame.Width) * p.pixScale
	p.slugQuads, ok = p.slug.TextQuads(p.slugQuads[:0], glyphs, text, pos, p.pixScale, minX, maxX)
	if !ok {
		return false
	}

	col := text.Color
	if col == nil {
		col = theme.Color(theme.ColorNameForeground)
	}
	c := fragmentColor(col)
	fw, fh := p.scaleFrameSize(frame)
	for _, q := range p.slugQuads {
		p.pushSlug(q, float32(q.Base), c, fw, fh)
	}
	return true
}

// pushSlug queues q, given in destination pixels, on the Slug batch in a
// straight-alpha colour, with base the glyph's or SlugSolid.
func (p *Painter) pushSlug(q paint.SlugQuad, base float32, c [4]float32, fw, fh float32) {
	p.flushInstances() // anything queued before this quad belongs under it
	p.flushQuads()
	p.flushLines()
	x1, y1 := q.X1/fw*2-1, 1-q.Y1/fh*2
	x2, y2 := q.X2/fw*2-1, 1-q.Y2/fh*2
	p.slugPending = append(p.slugPending,
		x1, y1, q.U1, q.V1, c[0], c[1], c[2], c[3], base,
		x2, y1, q.U2, q.V1, c[0], c[1], c[2], c[3], base,
		x1, y2, q.U1, q.V2, c[0], c[1], c[2], c[3], base,
		x1, y2, q.U1, q.V2, c[0], c[1], c[2], c[3], base,
		x2, y1, q.U2, q.V1, c[0], c[1], c[2], c[3], base,
		x2, y2, q.U2, q.V2, c[0], c[1], c[2], c[3], base,
	)
}

func (p *Painter) flushSlug() {
	if len(p.slugPending) == 0 {
		return
	}
	p.slug.Sync(p)
	if p.slugTex != nil {
		p.drawBatch(p.gpu.slug, p.slugPending, slugVertexFloats, p.slugTex.group)
	}
	p.slugPending = p.slugPending[:0]
}

// ResizeSlug swaps the Slug texture for an empty one rows tall. Queued draws
// may still read the old one, so it is retired rather than released.
func (p *Painter) ResizeSlug(rows int) {
	if p.slugTex != nil {
		p.retire(p.slugTex.release)
		p.slugTex = nil
	}
	t, err := p.gpu.newTexture(paint.SlugTexWidth, rows, gputypes.TextureUsageCopyDst, p.gpu.nearest)
	if err != nil {
		logError("slug texture", err)
		return
	}
	p.slugTex = t
}

// UploadSlug writes whole rows of texels into the Slug texture from row y.
func (p *Painter) UploadSlug(y int, pix []byte) {
	if p.slugTex != nil {
		rowBytes := paint.SlugTexWidth * 4
		p.gpu.write(p.slugTex, pix, rowBytes, 0, y, paint.SlugTexWidth, len(pix)/rowBytes)
	}
}
