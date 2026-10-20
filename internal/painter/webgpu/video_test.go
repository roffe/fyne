//go:build !wgputest

package webgpu

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// TestVideoMatchesSoftware draws video frames, both chroma layouts, turned,
// contained and covering, and of two byte samples, on the GPU and with the
// software painter, which converts them on the CPU.
func TestVideoMatchesSoftware(t *testing.T) {
	test.NewTempApp(t)
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}
	for _, s := range []struct {
		name        string
		interleaved bool
		turn        int
		fill        canvas.ImageFill
		aspect      float32
		depth       int
	}{
		{"i420_contain", false, 0, canvas.ImageFillContain, 0, 8},
		{"nv12_stretch", true, 0, canvas.ImageFillStretch, 0, 8},
		{"i420_turned_cover", false, 90, canvas.ImageFillCover, 0, 8},
		{"nv12_turned_wide_pixels", true, 270, canvas.ImageFillContain, 2, 8},
		{"yuv420p10_stretch", false, 0, canvas.ImageFillStretch, 0, 10},
		{"p010_turned_contain", true, 180, canvas.ImageFillContain, 0, 16},
	} {
		t.Run(s.name, func(t *testing.T) {
			f := testFrame(50, 32, s.interleaved, s.depth) // 25 texel chroma rows, so interleaved rows are not 4 byte aligned
			f.Rotation, f.FillMode, f.Aspect = s.turn, s.fill, s.aspect
			c := test.NewCanvas()
			c.SetPadded(false)
			c.SetContent(f)
			c.Resize(fyne.NewSize(80, 60))
			p := NewPainter(c, nil)
			p.Init()
			defer p.Release()

			got := paintCanvas(p, c)
			ref := captureSoftware(c)
			bad := 0
			for y := ref.Bounds().Min.Y; y < ref.Bounds().Max.Y; y++ {
				for x := ref.Bounds().Min.X; x < ref.Bounds().Max.X; x++ {
					if channelDiff(got.At(x, y), ref.At(x, y)) > 24 {
						bad++
					}
				}
			}
			if share := float64(bad) / float64(ref.Bounds().Dx()*ref.Bounds().Dy()); share > 0.02 {
				t.Errorf("%.2f%% of pixels differ from the software painter", share*100)
				if dump := os.Getenv("WGPU_DUMP"); dump != "" {
					writePNG(t, filepath.Join(dump, "video_"+s.name+"_wgpu.png"), got)
					writePNG(t, filepath.Join(dump, "video_"+s.name+"_software.png"), ref)
				}
			}
		})
	}
}

// TestVideoHDRMatchesSoftware tone maps blocks of HDR greys and colours on the
// GPU and on the CPU. They are drawn a sample to a pixel, as near black the
// curves are too steep to compare pictures filtered before and after them.
func TestVideoHDRMatchesSoftware(t *testing.T) {
	test.NewTempApp(t)
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}
	signals := [][3]float64{{0, 0, 0}, {0.58, 0.58, 0.58}, {0.75, 0.75, 0.75}, {0.9, 0.9, 0.9}, {0.7, 0.4, 0.3}, {0.3, 0.65, 0.35}, {0.35, 0.4, 0.75}, {0.62, 0.52, 0.45}}
	for _, s := range []struct {
		name        string
		transfer    canvas.VideoTransfer
		depth       int
		interleaved bool
	}{
		{"pq_p010", canvas.VideoTransferPQ, 16, true},
		{"pq_yuv420p10", canvas.VideoTransferPQ, 10, false},
		{"hlg_p010", canvas.VideoTransferHLG, 16, true},
	} {
		t.Run(s.name, func(t *testing.T) {
			f := blockFrame(signals, s.depth, s.interleaved)
			f.Transfer, f.Peak = s.transfer, 4000
			c := test.NewCanvas()
			c.SetPadded(false)
			c.SetContent(f)
			c.Resize(fyne.NewSize(float32(f.Width), float32(f.Height)))
			p := NewPainter(c, nil)
			p.Init()
			defer p.Release()

			got := paintCanvas(p, c)
			ref := captureSoftware(c)
			for i, e := range signals {
				x, y := 8*i+4, 4
				if d := channelDiff(got.At(x, y), ref.At(x, y)); d > 3 {
					t.Errorf("signal %v is %v on the GPU, %v on the CPU", e, got.At(x, y), ref.At(x, y))
				}
			}
		})
	}
}

// TestVideoRefreshUpdatesPlanes checks that a refreshed frame shows its new
// samples from the textures it already had.
func TestVideoRefreshUpdatesPlanes(t *testing.T) {
	test.NewTempApp(t)
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}
	f := &canvas.VideoFrame{Width: 4, Height: 4, YStride: 4, CStride: 2, FullRange: true}
	f.Y, f.U, f.V = []byte{0: 76, 15: 0}, make([]byte, 4), make([]byte, 4) // red: Y 76, U 85, V 255
	fill := func(y, u, v byte) {
		for i := range f.Y {
			f.Y[i] = y
		}
		for i := range f.U {
			f.U[i], f.V[i] = u, v
		}
	}
	fill(76, 85, 255)
	c := test.NewCanvas()
	c.SetPadded(false)
	c.SetContent(f)
	c.Resize(fyne.NewSize(4, 4))
	p := NewPainter(c, nil)
	p.Init()
	defer p.Release()

	if got := paintCanvas(p, c).At(1, 1); channelDiff(got, color.NRGBA{R: 255, A: 255}) > 8 {
		t.Fatalf("frame drawn as %v, want red", got)
	}
	first := p.videoTextures(f)
	fill(29, 255, 107) // blue
	f.Refresh()
	if got := paintCanvas(p, c).At(1, 1); channelDiff(got, color.NRGBA{B: 255, A: 255}) > 8 {
		t.Errorf("refreshed frame drawn as %v, want blue", got)
	}
	if p.videoTextures(f) != first {
		t.Error("refresh made new textures rather than uploading into the old ones")
	}
}

// blockFrame is a BT.2020 frame of 8x8 blocks of the R'G'B' signals given, of
// depth bit samples in limited range.
func blockFrame(signals [][3]float64, depth int, interleaved bool) *canvas.VideoFrame {
	const kr, kb = 0.2627, 0.0593
	k := float64(int(1) << (depth - 8))
	f := &canvas.VideoFrame{Width: 8 * len(signals), Height: 8, Depth: depth, ColorSpace: canvas.VideoColorSpaceBT2020}
	f.YStride, f.CStride = 2*f.Width, f.Width
	if interleaved {
		f.CStride *= 2
		f.UV = make([]byte, 4*f.CStride)
	} else {
		f.U, f.V = make([]byte, 4*f.CStride), make([]byte, 4*f.CStride)
	}
	f.Y = make([]byte, 8*f.YStride)
	put := func(p []byte, at int, v float64) {
		s := uint16(v*k + 0.5)
		p[at], p[at+1] = byte(s), byte(s>>8)
	}
	for i, e := range signals {
		luma := kr*e[0] + (1-kr-kb)*e[1] + kb*e[2]
		u, v := 128+224*(e[2]-luma)/(2*(1-kb)), 128+224*(e[0]-luma)/(2*(1-kr))
		for y := range 8 {
			for x := 8 * i; x < 8*i+8; x++ {
				put(f.Y, y*f.YStride+2*x, 16+219*luma)
				if x%2 == 0 && y%2 == 0 {
					if interleaved {
						put(f.UV, y/2*f.CStride+2*x, u)
						put(f.UV, y/2*f.CStride+2*x+2, v)
					} else {
						put(f.U, y/2*f.CStride+x, u)
						put(f.V, y/2*f.CStride+x, v)
					}
				}
			}
		}
	}
	return f
}

// testFrame is a w by h BT.709 video frame of a red to green gradient across
// and a blue one down, of depth bit samples, with rows padded as decoders pad
// them.
func testFrame(w, h int, interleaved bool, depth int) *canvas.VideoFrame {
	const kr, kb = 0.2126, 0.0722
	n := 1
	if depth > 8 {
		n = 2
	}
	f := &canvas.VideoFrame{Width: w, Height: h, Depth: depth, YStride: w*n + 16, CStride: w*n + 16, ColorSpace: canvas.VideoColorSpaceBT709}
	put := func(p []byte, at int, v float64) { // v as 8 bits would hold it
		s := uint16(v*float64(int(1)<<(depth-8)) + 0.5)
		p[at] = byte(s)
		if n == 2 {
			p[at+1] = byte(s >> 8)
		}
	}
	cw, ch := (w+1)/2, (h+1)/2
	f.Y = make([]byte, h*f.YStride)
	chroma := make([][2]float64, cw*ch)
	for y := range h {
		for x := range w {
			r, g, b := float64(x)/float64(w-1), 1-float64(x)/float64(w-1), float64(y)/float64(h-1)
			luma := kr*r + (1-kr-kb)*g + kb*b
			put(f.Y, y*f.YStride+x*n, 16+219*luma)
			if x%2 == 0 && y%2 == 0 {
				chroma[y/2*cw+x/2] = [2]float64{128 + 224*(b-luma)/(2*(1-kb)), 128 + 224*(r-luma)/(2*(1-kr))}
			}
		}
	}
	if interleaved {
		f.UV = make([]byte, ch*f.CStride)
		for i, c := range chroma {
			o := i/cw*f.CStride + i%cw*2*n
			put(f.UV, o, c[0])
			put(f.UV, o+n, c[1])
		}
		return f
	}
	f.U, f.V = make([]byte, ch*f.CStride), make([]byte, ch*f.CStride)
	for i, c := range chroma {
		o := i/cw*f.CStride + i%cw*n
		put(f.U, o, c[0])
		put(f.V, o, c[1])
	}
	return f
}
