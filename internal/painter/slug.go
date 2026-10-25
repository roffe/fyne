package painter

import (
	"encoding/binary"
	"math"
	"os"
	"slices"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// This file is the renderer-neutral half of Slug text: Eric Lengyel's
// algorithm (github.com/EricLengyel/Slug, MIT, patent dedicated to the public
// domain), in which every fragment works out its glyph coverage from the
// outline's quadratic Béziers. Glyphs are sharp at any size and scale, and an
// outline is stored once whatever size it is drawn at.
//
// Everything lives in one RGBA8 texture, so GL 2.1 and ES 2 can read it: a
// texel is two 16-bit numbers, lo in red and green and hi in blue and alpha,
// and texture is SlugTexWidth texels to a row, read as one flat array. A glyph
// is a block from its base: SlugBands horizontal then SlugBands vertical band
// headers (lo curve count, hi offset of its list), the lists (lo offset of a
// curve), sorted by descending maximum x for horizontal bands and y for
// vertical ones, then the curves' points (lo x, hi y). Curve i is the points at
// i, i+1 and i+2, so a contour shares each end with the next curve's start.
// Offsets are from the base and points are in glyph units, which map the
// outline's bounds onto 0 to SlugUnits.

// SlugText draws canvas.Text with Slug instead of the glyph atlas. ponytail:
// an experiment behind FYNE_TEXT=slug until it is measured against the atlas.
var SlugText = os.Getenv("FYNE_TEXT") == "slug"

const (
	// SlugTexWidth is the texels to a row of the Slug texture.
	SlugTexWidth = 1024
	// SlugBands is how many bands a glyph is cut into along each axis.
	// ponytail: fixed, choose per glyph to minimise the fullest band if large
	// CJK glyphs turn out slow.
	SlugBands = 8
	// SlugMaxBandCurves is the most curves a band may hold, the bound of the
	// GLSL ES loop over them. A glyph past it is drawn by the atlas.
	SlugMaxBandCurves = 256
	// SlugUnits is the glyph unit at the far edge of a glyph's bounds.
	SlugUnits = 65535

	slugTexelBytes = 4
	slugRowBytes   = SlugTexWidth * slugTexelBytes
	// slugMaxRows caps the texture at 8MB, which holds thousands of glyphs.
	// ponytail: a full texture sends new glyphs to the atlas; evict like the
	// atlas does if an app ever fills it.
	slugMaxRows = 2048
	slugMinRows = 64
	// slugHalf shifts a 16-bit value into the high half of a texel.
	slugHalf = 16
	// slugBandOverlap is the em that bands overlap by, so a curve on a
	// boundary lands in both.
	slugBandOverlap = 1.0 / 1024
)

// SlugSolid is the base a painter gives a quad that is filled whole, which
// lets plain rectangles ride the Slug batch between labels instead of
// splitting it.
const SlugSolid float32 = -1

// SlugTexture is a painter's GPU half of SlugGlyphs.
type SlugTexture interface {
	// ResizeSlug replaces the texture with an empty one rows texels tall.
	ResizeSlug(rows int)
	// UploadSlug writes pix, whole rows of RGBA8 texels, from row y down.
	UploadSlug(y int, pix []byte)
}

// SlugQuad is one glyph to draw: its rectangle in destination pixels, the glyph
// units at its corners and the base of the glyph in the texture.
type SlugQuad struct {
	X1, Y1, X2, Y2 float32
	U1, V1, U2, V2 float32
	Base           uint32
}

// SlugGlyphs is a painter's glyph outlines, added as text needs them. The zero
// value is empty.
type SlugGlyphs struct {
	glyphs map[slugKey]slugGlyph
	data   []byte // whole rows, the texture's contents
	used   int    // texels in data that hold anything
	rows   int    // the texture's height
	synced int    // texels the texture holds

	resolve []slugGlyph
	quads   []slugQuad
	list    []int
	block   []uint32
}

type slugKey struct {
	face *font.Face
	gid  font.GID
}

// slugGlyph is a glyph's bounds in font units and where its block is.
type slugGlyph struct {
	minX, minY, maxX, maxY float32
	base                   uint32
	// unsupported marks a glyph Slug cannot draw: a colour emoji, one too
	// complex for the shader's loop or one that did not fit.
	unsupported bool
}

func (g slugGlyph) empty() bool { return g.maxX <= g.minX || g.maxY <= g.minY }

// TextQuads appends a quad per inked glyph of glyphs, text shaped by
// GlyphAtlas.Shape with its top-left at pos, to dst. Glyphs wholly outside the
// device pixel columns minX to maxX are left out. It reports false with dst
// unchanged when a glyph cannot be drawn this way.
func (s *SlugGlyphs) TextQuads(dst []SlugQuad, glyphs []PlacedGlyph, text *canvas.Text, pos fyne.Position, pixScale, minX, maxX float32) ([]SlugQuad, bool) {
	s.resolve = s.resolve[:0]
	for _, pg := range glyphs {
		g := s.glyph(pg.Face, pg.Glyph.GlyphID)
		if g.unsupported {
			return dst, false
		}
		s.resolve = append(s.resolve, g)
	}

	originX := float32(math.Round(float64(pos.X * pixScale)))
	originY := float32(math.Round(float64(pos.Y * pixScale)))
	for i, pg := range glyphs {
		g := s.resolve[i]
		if g.empty() {
			continue
		}
		scale := text.TextSize * pixScale / float32(pg.Face.Upem())
		penX, baseline := originX+pg.X, originY+pg.Y
		// A pixel of room on every side for the antialiased edge.
		x1, x2 := penX+g.minX*scale-1, penX+g.maxX*scale+1
		y1, y2 := baseline-g.maxY*scale-1, baseline-g.minY*scale+1
		if x2 < minX || x1 > maxX {
			continue
		}
		kx, ky := SlugUnits/(g.maxX-g.minX)/scale, SlugUnits/(g.maxY-g.minY)/scale
		dst = append(dst, SlugQuad{
			X1: x1, Y1: y1, X2: x2, Y2: y2,
			U1: (x1 - penX - g.minX*scale) * kx, V1: (baseline - y1 - g.minY*scale) * ky,
			U2: (x2 - penX - g.minX*scale) * kx, V2: (baseline - y2 - g.minY*scale) * ky,
			Base: g.base,
		})
	}
	return dst, true
}

// Sync puts everything added since the last call into tex, first replacing it
// with a taller one if it has run out of rows. There is always a texture after
// it, even for a batch of nothing but SlugSolid quads.
func (s *SlugGlyphs) Sync(tex SlugTexture) {
	rows := len(s.data) / slugRowBytes
	if s.rows == 0 || rows > s.rows {
		s.rows = max(s.rows, slugMinRows)
		for s.rows < rows {
			s.rows *= 2
		}
		tex.ResizeSlug(s.rows)
		s.synced = 0
	}
	if s.synced < s.used {
		from := s.synced / SlugTexWidth
		tex.UploadSlug(from, s.data[from*slugRowBytes:])
		s.synced = s.used
	}
}

// Rows is the height of the texture the last Sync left.
func (s *SlugGlyphs) Rows() int {
	return s.rows
}

// Reset forgets every glyph, for a painter whose texture has gone.
func (s *SlugGlyphs) Reset() {
	clear(s.glyphs)
	s.data, s.used, s.rows, s.synced = s.data[:0], 0, 0, 0
}

func (s *SlugGlyphs) glyph(face *font.Face, gid font.GID) slugGlyph {
	key := slugKey{face, gid}
	if g, ok := s.glyphs[key]; ok {
		return g
	}
	if s.glyphs == nil {
		s.glyphs = make(map[slugKey]slugGlyph)
	}
	g := s.add(face, gid)
	s.glyphs[key] = g
	return g
}

func (s *SlugGlyphs) add(face *font.Face, gid font.GID) slugGlyph {
	outline, ok := face.GlyphData(gid).(font.GlyphOutline)
	if !ok {
		return slugGlyph{unsupported: true}
	}
	s.quads = outlineQuads(s.quads[:0], outline.Segments)
	if len(s.quads) == 0 {
		return slugGlyph{}
	}
	g := slugGlyph{minX: math.MaxFloat32, minY: math.MaxFloat32, maxX: -math.MaxFloat32, maxY: -math.MaxFloat32}
	for _, q := range s.quads {
		g.minX, g.maxX = min(g.minX, q[0], q[2], q[4]), max(g.maxX, q[0], q[2], q[4])
		g.minY, g.maxY = min(g.minY, q[1], q[3], q[5]), max(g.maxY, q[1], q[3], q[5])
	}
	if g.empty() {
		return slugGlyph{}
	}

	for i, q := range s.quads {
		s.quads[i] = g.units(q)
	}

	// The points go after the headers and lists, whose length is only known
	// once the bands are cut, so they are collected apart and offset after.
	var points []uint32
	refs := make([]uint32, len(s.quads))
	for i, q := range s.quads {
		p1, p2, p3 := slugPair(q[0], q[1]), slugPair(q[2], q[3]), slugPair(q[4], q[5])
		if n := len(points); n > 0 && points[n-1] == p1 {
			refs[i] = u32(n - 1)
			points = append(points, p2, p3)
		} else {
			refs[i] = u32(n)
			points = append(points, p1, p2, p3)
		}
	}

	s.block = append(s.block[:0], make([]uint32, 2*SlugBands)...)
	var lists [2 * SlugBands][]int
	// Horizontal bands split y and sort by x; vertical ones the other way.
	kx, ky := g.scale()
	for axis, split := range [2]int{1, 0} {
		eps := float32(face.Upem()) * slugBandOverlap * ky
		if split == 0 {
			eps = float32(face.Upem()) * slugBandOverlap * kx
		}
		sortBy := 1 - split
		for b := range SlugBands {
			bottom := float32(b)*(SlugUnits+1)/SlugBands - eps
			top := float32(b+1)*(SlugUnits+1)/SlugBands + eps
			s.list = s.list[:0]
			for i, q := range s.quads {
				a, c, d := q[split], q[split+2], q[split+4]
				if a == c && c == d {
					continue // parallel to the ray, never crossed
				}
				if max(a, c, d) < bottom || min(a, c, d) > top {
					continue
				}
				s.list = append(s.list, i)
			}
			if len(s.list) > SlugMaxBandCurves {
				return slugGlyph{unsupported: true}
			}
			slices.SortFunc(s.list, func(i, j int) int {
				qi, qj := s.quads[i], s.quads[j]
				mi := max(qi[sortBy], qi[sortBy+2], qi[sortBy+4])
				mj := max(qj[sortBy], qj[sortBy+2], qj[sortBy+4])
				switch {
				case mi > mj:
					return -1
				case mi < mj:
					return 1
				}
				return 0
			})
			lists[axis*SlugBands+b] = append([]int(nil), s.list...)
		}
	}
	listLen := 0
	for _, l := range lists {
		listLen += len(l)
	}
	pointsAt := u32(2*SlugBands + listLen)
	for h, l := range lists {
		s.block[h] = u32(len(l)) | u32(len(s.block))<<slugHalf
		for _, i := range l {
			s.block = append(s.block, refs[i]+pointsAt)
		}
	}
	s.block = append(s.block, points...)
	if len(s.block) > SlugUnits {
		return slugGlyph{unsupported: true} // offsets would not fit 16 bits
	}

	base, ok := s.alloc(len(s.block))
	if !ok {
		return slugGlyph{unsupported: true}
	}
	for i, v := range s.block {
		binary.LittleEndian.PutUint32(s.data[(base+i)*slugTexelBytes:], v)
	}
	g.base = u32(base)
	return g
}

// alloc reserves n texels, growing data by whole rows, and returns the first.
func (s *SlugGlyphs) alloc(n int) (int, bool) {
	need := (s.used + n + SlugTexWidth - 1) / SlugTexWidth
	if need > slugMaxRows {
		return 0, false
	}
	if grow := need*slugRowBytes - len(s.data); grow > 0 {
		s.data = append(s.data, make([]byte, grow)...)
	}
	at := s.used
	s.used += n
	return at, true
}

func slugPair(lo, hi float32) uint32 {
	return uint32(lo) | uint32(hi)<<slugHalf
}

// scale is how many glyph units an outline unit is, across and up.
func (g *slugGlyph) scale() (kx, ky float32) {
	return SlugUnits / (g.maxX - g.minX), SlugUnits / (g.maxY - g.minY)
}

// units is q in glyph units, whole ones as the texture stores them.
func (g *slugGlyph) units(q slugQuad) slugQuad {
	kx, ky := g.scale()
	x := func(v float32) float32 { return float32(math.Round(float64((v - g.minX) * kx))) }
	y := func(v float32) float32 { return float32(math.Round(float64((v - g.minY) * ky))) }
	return [...]float32{x(q[0]), y(q[1]), x(q[2]), y(q[3]), x(q[4]), y(q[5])}
}

func u32(v int) uint32 {
	return uint32(v) //nolint:gosec // G115: sizes and offsets the callers keep far below 2³²
}

// slugQuad is a quadratic Bézier: x1 y1 x2 y2 x3 y3.
type slugQuad = [6]float32

// outlineQuads appends the outline as quadratic Béziers to dst, x1 y1 x2 y2
// x3 y3 each, closing open contours. A line is a curve whose control point is
// its end, the encoding Slug's reference recommends.
func outlineQuads(dst []slugQuad, segs []font.Segment) []slugQuad {
	var start, cur ot.SegmentPoint
	quad := func(c, to ot.SegmentPoint) {
		if c != cur || to != cur {
			dst = append(dst, [...]float32{cur.X, cur.Y, c.X, c.Y, to.X, to.Y})
		}
		cur = to
	}
	mid := func(p, q ot.SegmentPoint) ot.SegmentPoint {
		return ot.SegmentPoint{X: (p.X + q.X) / 2, Y: (p.Y + q.Y) / 2}
	}
	for _, seg := range segs {
		a := seg.Args
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			quad(start, start)
			start, cur = a[0], a[0]
		case ot.SegmentOpLineTo:
			quad(a[0], a[0])
		case ot.SegmentOpQuadTo:
			quad(a[0], a[1])
		case ot.SegmentOpCubeTo:
			// ponytail: halved and each half approximated by one quadratic,
			// which is invisible at text sizes; subdivide by error for huge text.
			ctrl := func(p0, p1, p2, p3 ot.SegmentPoint) ot.SegmentPoint {
				return ot.SegmentPoint{X: (3*(p1.X+p2.X) - p0.X - p3.X) / 4, Y: (3*(p1.Y+p2.Y) - p0.Y - p3.Y) / 4}
			}
			p0 := cur
			m01, m12, m23 := mid(p0, a[0]), mid(a[0], a[1]), mid(a[1], a[2])
			l, r := mid(m01, m12), mid(m12, m23)
			m := mid(l, r)
			quad(ctrl(p0, m01, l, m), m)
			quad(ctrl(m, r, m23, a[2]), a[2])
		}
	}
	quad(start, start)
	return dst
}
