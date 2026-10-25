package painter

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2/canvas"
)

// TestVideoImage encodes colours as each colour space, range and depth does
// and checks that they convert back, turned the way the frame says.
func TestVideoImage(t *testing.T) {
	colors := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}, {0, 0, 0, 255}, {40, 120, 200, 255}}
	for _, space := range []struct {
		cs     canvas.VideoColorSpace
		kr, kb float64
	}{{canvas.VideoColorSpaceBT601, 0.299, 0.114}, {canvas.VideoColorSpaceBT709, 0.2126, 0.0722}, {canvas.VideoColorSpaceBT2020, 0.2627, 0.0593}} {
		for _, full := range []bool{false, true} {
			for _, depth := range []int{8, 10, 16} {
				var yuv [][3]float64
				for _, c := range colors {
					yuv = append(yuv, encodeYUV(c, space.kr, space.kb))
				}
				f := blocks(yuv, depth, full, depth == 16)
				f.ColorSpace = space.cs
				img := VideoImage(f)
				for i, want := range colors {
					if got := img.RGBAAt(2*i+1, 1); diff(got, want) > 3 {
						t.Errorf("colour space %d full range %v depth %d: %v came back as %v", space.cs, full, depth, want, got)
					}
				}
			}
		}
	}

	// a frame 4 wide and 2 high, turned a quarter, is 2 wide and 4 high with
	// its left column along the top
	f := &canvas.VideoFrame{Width: 4, Height: 2, YStride: 4, CStride: 4, Rotation: 90, FullRange: true}
	f.Y, f.UV = []byte{10, 20, 30, 40, 50, 60, 70, 80}, []byte{128, 128, 128, 128}
	img := VideoImage(f)
	if img.Bounds() != image.Rect(0, 0, 2, 4) {
		t.Fatalf("turned frame is %v", img.Bounds())
	}
	for _, c := range []struct {
		x, y int
		want uint8
	}{{0, 0, 50}, {1, 0, 10}, {0, 3, 80}, {1, 3, 40}} {
		if got := img.RGBAAt(c.x, c.y).R; got != c.want {
			t.Errorf("turned (%d,%d) is %d, want %d", c.x, c.y, got, c.want)
		}
	}
}

// TestVideoHDR checks that greys of HDR video land on the BT.2390 curve:
// light below the knee keeps its share of SDR white, HDR's reference white is
// as bright in PQ as in HLG, and the peak and above are white.
func TestVideoHDR(t *testing.T) {
	for _, c := range []struct {
		name     string
		transfer canvas.VideoTransfer
		signals  []float64
		want     []uint8
	}{
		// black, 50 cd/m², 100, 203, 1000 (the peak) and 4000
		{"pq", canvas.VideoTransferPQ, []float64{0, 0.4403, 0.5081, 0.5807, 0.7518, 0.9026}, []uint8{0, 142, 189, 230, 255, 255}},
		// black, reference white and the peak
		{"hlg", canvas.VideoTransferHLG, []float64{0, 0.75, 1}, []uint8{0, 230, 255}},
	} {
		var grey [][3]float64
		for _, e := range c.signals {
			grey = append(grey, [3]float64{e, 0, 0})
		}
		f := blocks(grey, 10, false, false)
		f.ColorSpace, f.Transfer = canvas.VideoColorSpaceBT2020, c.transfer
		img := VideoImage(f)
		for i, want := range c.want {
			got := img.RGBAAt(2*i, 0)
			if d := diff(got, color.RGBA{want, want, want, 255}); d > 1 {
				t.Errorf("%s signal %v shows as %v, want %d", c.name, c.signals[i], got, want)
			}
		}
	}
}

// encodeYUV is Y', Pb and Pr of c.
func encodeYUV(c color.RGBA, kr, kb float64) [3]float64 {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	luma := kr*r + (1-kr-kb)*g + kb*b
	return [3]float64{luma, (b - luma) / (2 * (1 - kb)), (r - luma) / (2 * (1 - kr))}
}

// blocks is a frame of 2x2 blocks of each Y', Pb and Pr in yuv, stored as
// samples of depth bits, its chroma interleaved if asked.
func blocks(yuv [][3]float64, depth int, full, interleaved bool) *canvas.VideoFrame {
	b := 1
	if depth > 8 {
		b = 2
	}
	n := len(yuv)
	f := &canvas.VideoFrame{Width: 2 * n, Height: 2, Depth: depth, FullRange: full, YStride: 2 * n * b, CStride: n * b}
	put := func(p []byte, i int, v float64) {
		var s [2]byte
		binary.LittleEndian.PutUint16(s[:], uint16(min(max(v, 0), float64(int(1)<<depth-1))+0.5))
		copy(p[i*b:i*b+b], s[:b])
	}
	code := func(v, lo, span, mid float64) float64 { // v as the depth stores it
		k := float64(int(1) << (depth - 8))
		if full {
			return v*float64(int(1)<<depth-1) + mid*k
		}
		return (lo+span*v)*k + mid*k
	}
	f.Y = make([]byte, 2*f.YStride)
	if interleaved {
		f.CStride *= 2
		f.UV = make([]byte, f.CStride)
	} else {
		f.U, f.V = make([]byte, f.CStride), make([]byte, f.CStride)
	}
	for i, s := range yuv {
		y := code(s[0], 16, 219, 0)
		for _, at := range []int{2 * i, 2*i + 1, 2*n + 2*i, 2*n + 2*i + 1} {
			put(f.Y, at, y)
		}
		u, v := code(s[1], 0, 224, 128), code(s[2], 0, 224, 128)
		if interleaved {
			put(f.UV, 2*i, u)
			put(f.UV, 2*i+1, v)
		} else {
			put(f.U, i, u)
			put(f.V, i, v)
		}
	}
	return f
}

func diff(a, b color.RGBA) int {
	d := 0
	for _, c := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}} {
		d = max(d, int(c[0])-int(c[1]), int(c[1])-int(c[0]))
	}
	return d
}
