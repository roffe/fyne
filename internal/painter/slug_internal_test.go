package painter

import (
	"encoding/binary"
	"image/color"
	"math"
	"slices"
	"testing"

	"github.com/go-text/typesetting/font"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

type fakeSlugTexture struct {
	rows   int
	pixels []byte
}

func (f *fakeSlugTexture) ResizeSlug(rows int) {
	f.rows = rows
	f.pixels = make([]byte, rows*slugRowBytes)
}

func (f *fakeSlugTexture) UploadSlug(y int, pix []byte) {
	copy(f.pixels[y*slugRowBytes:], pix)
}

// TestSlugGlyphs_Layout decodes every glyph of a string from what reached the
// texture, the way the shaders read it, and checks it against the outline: the
// curves are the outline's, and every band lists each curve that crosses it,
// sorted the way the shaders' early exit relies on.
func TestSlugGlyphs_Layout(t *testing.T) {
	text := canvas.NewText("Slug&@ gjQ%", color.White)
	var a GlyphAtlas
	var s SlugGlyphs
	tex := &fakeSlugTexture{}
	glyphs, ok := a.Shape(text, 1)
	require.True(t, ok)
	quads, ok := s.TextQuads(nil, glyphs, text, fyne.NewPos(0, 0), 1, -1e9, 1e9)
	require.True(t, ok)
	require.Len(t, quads, 10, "one per inked glyph")
	s.Sync(tex)
	require.Equal(t, s.Rows(), tex.rows)

	pair := func(i uint32) (lo, hi uint32) {
		v := binary.LittleEndian.Uint32(tex.pixels[i*slugTexelBytes:])
		return v & 0xffff, v >> 16
	}
	for gi, pg := range glyphs {
		g := s.glyph(pg.Face, pg.Glyph.GlyphID)
		if g.empty() {
			continue
		}
		var lists [2 * SlugBands][][6]float32
		for h := range lists {
			count, offset := pair(g.base + uint32(h))
			for i := range count {
				ref, _ := pair(g.base + offset + i)
				var c [6]float32
				for j := range uint32(3) {
					x, y := pair(g.base + ref + j)
					c[2*j], c[2*j+1] = float32(x), float32(y)
				}
				lists[h] = append(lists[h], c)
			}
		}

		outline, ok := pg.Face.GlyphData(pg.Glyph.GlyphID).(font.GlyphOutline)
		require.True(t, ok)
		want := outlineQuads(nil, outline.Segments)
		kx, ky := SlugUnits/(g.maxX-g.minX), SlugUnits/(g.maxY-g.minY)
		for i, q := range want {
			for j := 0; j < 6; j += 2 {
				q[j] = float32(math.Round(float64((q[j] - g.minX) * kx)))
				q[j+1] = float32(math.Round(float64((q[j+1] - g.minY) * ky)))
			}
			want[i] = q
		}
		var all [][6]float32
		for _, l := range lists {
			for _, c := range l {
				if !slices.Contains(all, c) {
					all = append(all, c)
				}
			}
		}
		assert.ElementsMatch(t, want, all, "glyph %d: the curves decoded are the outline's", gi)

		upem := float32(pg.Face.Upem())
		for axis, split := range [2]int{1, 0} {
			eps := upem / 1024 * ky
			if split == 0 {
				eps = upem / 1024 * kx
			}
			for b := range SlugBands {
				list := lists[axis*SlugBands+b]
				bottom := float32(b)*(SlugUnits+1)/SlugBands - eps
				top := float32(b+1)*(SlugUnits+1)/SlugBands + eps
				for _, c := range want {
					lo, hi := min(c[split], c[split+2], c[split+4]), max(c[split], c[split+2], c[split+4])
					if lo == hi || hi < bottom || lo > top {
						continue
					}
					assert.Contains(t, list, c, "glyph %d band %d/%d misses a curve", gi, axis, b)
				}
				sortBy := 1 - split
				assert.True(t, slices.IsSortedFunc(list, func(p, q [6]float32) int {
					return -cmpFloat(max(p[sortBy], p[sortBy+2], p[sortBy+4]), max(q[sortBy], q[sortBy+2], q[sortBy+4]))
				}), "glyph %d band %d/%d is not sorted by descending max", gi, axis, b)
			}
		}
	}
}

// TestSlugGlyphs_SyncGrows adds glyphs past the first texture's rows and
// checks the texture is replaced and refilled rather than written past.
func TestSlugGlyphs_SyncGrows(t *testing.T) {
	var a GlyphAtlas
	var s SlugGlyphs
	tex := &fakeSlugTexture{}
	text := canvas.NewText("a", color.White)
	for r := rune(0x21); s.used < (slugMinRows+1)*SlugTexWidth && r < 0x2000; r++ {
		text.Text = string(r)
		if glyphs, ok := a.Shape(text, 1); ok {
			s.TextQuads(nil, glyphs, text, fyne.Position{}, 1, -1e9, 1e9)
		}
		s.Sync(tex)
	}
	require.Greater(t, s.used, slugMinRows*SlugTexWidth, "the test font has too few glyphs")
	assert.Equal(t, 2*slugMinRows, tex.rows)
	assert.Equal(t, s.data[:s.used*slugTexelBytes], tex.pixels[:s.used*slugTexelBytes])
}

func cmpFloat(a, b float32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
