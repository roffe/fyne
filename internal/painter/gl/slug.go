package gl

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal"
	paint "fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/theme"
)

// This file is the GL half of Slug text, see internal/painter/slug.go.

// slugVertexFloats is one vertex: clip space position, glyph units,
// straight-alpha colour and the glyph's base in the texture.
const slugVertexFloats = 2 + 2 + 4 + 1

// drawTextSlug queues a quad per inked glyph of the run, reporting false when
// it has to be drawn some other way.
func (p *painter) drawTextSlug(text *canvas.Text, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) bool {
	if p.slugProgram() == nil {
		return false
	}
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
	r, g, b, a := getFragmentColor(col)
	fw, fh := p.scaleFrameSize(frame)
	for _, q := range p.slugQuads {
		p.pushSlug(q, float32(q.Base), [4]float32{r, g, b, a}, fw, fh)
	}
	return true
}

// pushSlug queues q, given in destination pixels, on the Slug batch in a
// straight-alpha colour, with base the glyph's or SlugSolid.
func (p *painter) pushSlug(q paint.SlugQuad, base float32, c [4]float32, fw, fh float32) {
	p.flushQuads() // anything queued before this quad belongs under it
	p.flushLines()
	x1, y1 := q.X1/fw*2-1, 1-q.Y1/fh*2
	x2, y2 := q.X2/fw*2-1, 1-q.Y2/fh*2
	r, g, b, a := c[0], c[1], c[2], c[3]
	p.slugPending = append(p.slugPending,
		x1, y1, q.U1, q.V1, r, g, b, a, base,
		x2, y1, q.U2, q.V1, r, g, b, a, base,
		x1, y2, q.U1, q.V2, r, g, b, a, base,
		x1, y2, q.U1, q.V2, r, g, b, a, base,
		x2, y1, q.U2, q.V1, r, g, b, a, base,
		x2, y2, q.U2, q.V2, r, g, b, a, base,
	)
}

func (p *painter) flushSlug() {
	vertices := len(p.slugPending) / slugVertexFloats
	if vertices == 0 {
		return
	}
	p.slug.Sync(p)

	prog := *p.slugProg
	p.ctx.UseProgram(prog.ref)
	p.ctx.BindBuffer(arrayBuffer, prog.buff)
	p.ctx.BufferData(arrayBuffer, p.slugPending, staticDraw)
	p.UpdateVertexArray(prog, attrVertex, 2, slugVertexFloats, 0)
	p.UpdateVertexArray(prog, attrVertexTextureCoordinates, 2, slugVertexFloats, 2)
	p.UpdateVertexArray(prog, attrVertexColor, 4, slugVertexFloats, 4)
	p.UpdateVertexArray(prog, "glyphBase", 1, slugVertexFloats, 8)
	p.SetUniform1f(prog, "rows", float32(p.slug.Rows()))
	p.ctx.BlendFunc(srcAlpha, oneMinusSrcAlpha)
	p.ctx.ActiveTexture(texture0)
	p.ctx.BindTexture(texture2D, p.slugTex)
	p.ctx.DrawArrays(triangles, 0, vertices)
	p.logError()

	if glDebug {
		p.countBatch(vertices / 6)
	}
	p.slugPending = p.slugPending[:0]
}

// ResizeSlug swaps the Slug texture for an empty one rows tall.
func (p *painter) ResizeSlug(rows int) {
	if p.slugTexValid {
		p.ctx.DeleteTexture(p.slugTex)
	}
	p.slugTex = p.newTexture(canvas.ImageScalePixels)
	p.ctx.TexImage2D(texture2D, 0, paint.SlugTexWidth, rows, colorFormatRGBA, unsignedByte, make([]uint8, paint.SlugTexWidth*rows*4))
	p.logError()
	p.slugTexValid = true
}

// UploadSlug writes whole rows of texels into the Slug texture from row y.
func (p *painter) UploadSlug(y int, pix []byte) {
	p.ctx.ActiveTexture(texture0)
	p.ctx.BindTexture(texture2D, p.slugTex)
	p.ctx.TexSubImage2D(texture2D, 0, 0, y, paint.SlugTexWidth, len(pix)/(paint.SlugTexWidth*4), colorFormatRGBA, unsignedByte, pix)
	p.logError()
}

// slugProgram returns the program Slug text draws with, compiled on first use,
// or nil if the GL in use cannot run it.
func (p *painter) slugProgram() *programState {
	if p.slugProg == nil && !p.slugFailed {
		ref, err := p.createProgram(shaderVertSlug, shaderFragSlug)
		if err != nil {
			fyne.LogError("GL cannot draw Slug text, using the glyph atlas", err)
			p.slugFailed = true
			return nil
		}
		p.slugProg = &programState{
			ref:        ref,
			buff:       p.createBuffer(6 * slugVertexFloats),
			uniforms:   make(map[string]*uniformState),
			attributes: make(map[string]Attribute),
		}
	}
	return p.slugProg
}
