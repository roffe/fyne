//go:build !wgputest

package webgpu

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	paint "fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func TestSlugMatchesSoftware(t *testing.T) {
	test.NewTempApp(t)
	test.ApplyTheme(t, test.Theme())
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}
	paint.SlugText = true
	defer func() { paint.SlugText = false }()

	fg := theme.Color(theme.ColorNameForeground)
	big := canvas.NewText("Slug&@", fg)
	big.TextSize = 96
	boldItalic := canvas.NewText("Dd", fg)
	boldItalic.TextStyle, boldItalic.TextSize = fyne.TextStyle{Bold: true, Italic: true}, 42
	mono := canvas.NewText("func main() {}", fg)
	mono.TextStyle.Monospace = true
	// text between rectangles, so Slug and glyph batches interleave
	rect1, rect2 := canvas.NewRectangle(color.NRGBA{R: 200, A: 255}), canvas.NewRectangle(color.NRGBA{B: 200, A: 128})
	rect1.Resize(fyne.NewSize(40, 20))
	rect2.Move(fyne.NewPos(20, 10))
	rect2.Resize(fyne.NewSize(40, 20))
	over := canvas.NewText("over", color.White)
	over.Move(fyne.NewPos(2, 2))
	scroll := container.NewScroll(widget.NewLabel("some text\nis here\nand here"))
	scroll.Move(fyne.NewPos(10, 10))
	scroll.Resize(fyne.NewSize(50, 50))
	scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(-10, -10)})

	scenes := []struct {
		name string
		obj  fyne.CanvasObject
		w, h float32
	}{
		{"label", widget.NewLabel("The quick brown fox jumps"), 220, 40},
		{"big", big, big.MinSize().Width, big.MinSize().Height},
		{"bold_italic", boldItalic, 70, boldItalic.MinSize().Height},
		{"mono", mono, 140, 30},
		{"interleaved", container.NewWithoutLayout(rect1, over, rect2), 70, 40},
		{"clipped", container.NewWithoutLayout(scroll), 70, 70},
	}
	dump := os.Getenv("WGPU_DUMP")
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			c := test.NewCanvas()
			c.SetPadded(false)
			c.SetContent(s.obj)
			c.Resize(fyne.NewSize(s.w, s.h))
			p := NewPainter(c, nil)
			p.Init()
			defer p.Release()
			got := paintCanvas(p, c)
			if len(p.slugQuads) == 0 {
				t.Fatal("no glyph went through Slug")
			}
			ref := captureSoftware(c)
			bad := 0
			for y := ref.Bounds().Min.Y; y < ref.Bounds().Max.Y; y++ {
				for x := ref.Bounds().Min.X; x < ref.Bounds().Max.X; x++ {
					if channelDiff(got.At(x, y), ref.At(x, y)) > 64 {
						bad++
					}
				}
			}
			share := float64(bad) / float64(ref.Bounds().Dx()*ref.Bounds().Dy())
			t.Logf("%5.2f%% of pixels off", share*100)
			if dump != "" {
				writePNG(t, filepath.Join(dump, "slug_"+s.name+"_wgpu.png"), got)
				writePNG(t, filepath.Join(dump, "slug_"+s.name+"_software.png"), ref)
			}
			if share > 0.04 {
				t.Errorf("%.2f%% of pixels differ from the software painter", share*100)
			}
		})
	}
}
