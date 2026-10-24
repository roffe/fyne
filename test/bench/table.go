package main

import (
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/theme"
)

var (
	tableBg   = color.NRGBA{0x1a, 0x1a, 0x19, 0xff}
	tableFg   = color.NRGBA{0xf0, 0xef, 0xec, 0xff}
	tableDim  = color.NRGBA{0xc3, 0xc2, 0xb7, 0xff}
	tableLine = color.NRGBA{0x38, 0x38, 0x35, 0xff}
	tableWin  = color.NRGBA{0x0c, 0xa3, 0x0c, 0x47}
)

// Header labels for the metric columns, in TSV order from fps on.
var tableCols = [][2]string{
	{"", "fps"},
	{"frame p50", "ms"},
	{"frame p99", "ms"},
	{"main CPU", "ms/frame"},
	{"proc CPU", "ms/frame"},
	{"CPU", "%"},
	{"alloc", "KB/frame"},
	{"RSS", "MB"},
	{"startup", "ms"},
}

// renderTable draws a results TSV as a PNG next to it, with the best value of
// each metric per scene in green.
func renderTable(src, sub string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	type group struct {
		name string
		rows [][]string
	}
	var groups []*group
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n")[1:] {
		r := strings.Split(l, "\t")
		if len(r) != 4+len(tableCols) {
			continue // FAILED
		}
		name := r[1] + " " + r[2]
		if r[1] == "widgets" {
			name = "widgets " + r[2] + " tiles"
			if r[3] == "0" {
				name = r[2] + " tiles, 1 changing"
			}
		}
		if len(groups) == 0 || groups[len(groups)-1].name != name {
			groups = append(groups, &group{name: name})
		}
		g := groups[len(groups)-1]
		g.rows = append(g.rows, r)
	}

	a := app.New()
	a.Settings().SetTheme(theme.DarkTheme())
	bold := fyne.TextStyle{Bold: true}
	const pad, cellPad, rowH, size, headSize = 32, 12, 30, 15, 13
	measure := func(s string, sz float32) float32 { return fyne.MeasureText(s, sz, bold).Width }

	widths := make([]float32, 2+len(tableCols))
	widths[0], widths[1] = measure("test", headSize), measure("build", headSize)
	for i, c := range tableCols {
		widths[2+i] = max(measure(c[0], headSize), measure(c[1], headSize))
	}
	for _, g := range groups {
		widths[0] = max(widths[0], measure(g.name, size))
		for _, r := range g.rows {
			widths[1] = max(widths[1], measure(r[0], size))
			for i, v := range r[4:] {
				widths[2+i] = max(widths[2+i], measure(v, size))
			}
		}
	}
	var tableW float32
	for i := range widths {
		widths[i] += 2 * cellPad
		tableW += widths[i]
	}

	var objs []fyne.CanvasObject
	text := func(s string, c color.Color, sz float32, strong bool, x, y, w float32, align fyne.TextAlign) {
		t := canvas.NewText(s, c)
		t.TextSize, t.TextStyle, t.Alignment = sz, fyne.TextStyle{Bold: strong}, align
		t.Move(fyne.NewPos(x+cellPad, y))
		t.Resize(fyne.NewSize(w-2*cellPad, rowH))
		objs = append(objs, t)
	}

	text("Fyne bench", tableFg, 26, true, pad-cellPad, pad, 400, fyne.TextAlignLeading)
	// The subtitle names both commits, so it widens the image rather than clip.
	pageW := max(tableW, fyne.MeasureText(sub, 14, fyne.TextStyle{}).Width)
	text(sub, tableDim, 14, false, pad-cellPad, pad+28, pageW+2*cellPad, fyne.TextAlignLeading)
	y := float32(pad + 70)
	x := float32(pad)
	text("test", tableDim, headSize, true, x-cellPad, y+rowH/2, widths[0], fyne.TextAlignLeading)
	x += widths[0]
	text("build", tableDim, headSize, true, x, y+rowH/2, widths[1], fyne.TextAlignLeading)
	x += widths[1]
	for i, c := range tableCols {
		text(c[0], tableDim, headSize, true, x, y, widths[2+i], fyne.TextAlignTrailing)
		text(c[1], tableDim, headSize, true, x, y+rowH/2, widths[2+i], fyne.TextAlignTrailing)
		x += widths[2+i]
	}
	y += rowH*1.5 + 8

	for _, g := range groups {
		sep := canvas.NewLine(tableLine)
		sep.Position1, sep.Position2 = fyne.NewPos(pad, y), fyne.NewPos(pad+tableW, y)
		objs = append(objs, sep)
		y += 6

		best := make([]float64, len(tableCols))
		for i := range tableCols {
			for j, r := range g.rows {
				v, _ := strconv.ParseFloat(r[4+i], 64)
				if j == 0 || (i == 0 && v > best[i]) || (i > 0 && v < best[i]) {
					best[i] = v
				}
			}
		}
		text(g.name, tableFg, size, true, pad-cellPad, y, widths[0], fyne.TextAlignLeading)
		for _, r := range g.rows {
			x := pad + widths[0]
			text(r[0], tableDim, size, false, x, y, widths[1], fyne.TextAlignLeading)
			x += widths[1]
			for i, s := range r[4:] {
				v, _ := strconv.ParseFloat(s, 64)
				win := len(g.rows) > 1 && v == best[i]
				if win {
					bg := canvas.NewRectangle(tableWin)
					bg.CornerRadius = 4
					bg.Move(fyne.NewPos(x, y+1))
					bg.Resize(fyne.NewSize(widths[2+i], rowH-2))
					objs = append(objs, bg)
				}
				text(s, tableFg, size, win, x, y, widths[2+i], fyne.TextAlignTrailing)
				x += widths[2+i]
			}
			y += rowH
		}
		y += 4
	}

	y += 12
	key := canvas.NewRectangle(tableWin)
	key.CornerRadius = 4
	key.Move(fyne.NewPos(pad, y+4))
	key.Resize(fyne.NewSize(measure("bold green", 14)+12, rowH-8))
	objs = append(objs, key)
	text("bold green", tableFg, 14, true, pad-cellPad+6, y, 200, fyne.TextAlignLeading)
	text("= best in that test. Lower is better, except fps.", tableDim, 14, false, pad-cellPad+measure("bold green", 14)+18, y, tableW, fyne.TextAlignLeading)
	y += rowH + pad

	bg := canvas.NewRectangle(tableBg)
	bg.Resize(fyne.NewSize(pad*2+pageW, y))
	c := software.NewCanvas()
	c.SetPadded(false)
	c.SetScale(2)
	c.SetContent(container.NewWithoutLayout(append([]fyne.CanvasObject{bg}, objs...)...))
	c.Resize(bg.Size())

	f, err := os.Create(strings.TrimSuffix(src, ".tsv") + ".png")
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, c.Capture())
}
