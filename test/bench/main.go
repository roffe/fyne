// Command bench renders a stress scene and prints one TSV row of frame
// metrics. bench.sh runs it over a matrix of scenes and painters, optionally
// against a base revision to compare a change with.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const winW, winH = 1920, 1080

var palette = []color.Color{
	color.NRGBA{0xef, 0x53, 0x50, 0xff},
	color.NRGBA{0xff, 0xa7, 0x26, 0xff},
	color.NRGBA{0xff, 0xee, 0x58, 0xff},
	color.NRGBA{0x66, 0xbb, 0x6a, 0xff},
	color.NRGBA{0x42, 0xa5, 0xf5, 0xff},
	color.NRGBA{0xab, 0x47, 0xbc, 0xff},
}

var scenes = map[string]func(n int) (fyne.CanvasObject, func(t float64, i int)){
	"rects":   rects,
	"text":    texts,
	"widgets": widgets,
	"zoom":    zoom,
}

type options struct {
	label, scene, prof, shot string
	n                        int
	u                        float64
	warmup, dur              time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.label, "label", "fyne", "build name printed in the first column")
	flag.StringVar(&o.scene, "scene", "rects", "rects | text | widgets | zoom")
	flag.IntVar(&o.n, "n", 2000, "elements (widgets scene: tiles of 6 widgets)")
	flag.Float64Var(&o.u, "u", 1, "fraction of elements updated per frame, 0 = only one")
	flag.DurationVar(&o.warmup, "warmup", 2*time.Second, "settle time after the first frame")
	flag.DurationVar(&o.dur, "d", 8*time.Second, "measurement window")
	flag.StringVar(&o.prof, "prof", "", "write <prof>.cpu and <prof>.allocs pprof files (skews the numbers a bit)")
	flag.StringVar(&o.shot, "shot", "", "save a PNG of the window when the run ends, to check what got drawn")
	header := flag.Bool("header", false, "print the TSV header and exit")
	table := flag.String("table", "", "render this results TSV as a PNG next to it and exit")
	sub := flag.String("sub", "", "subtitle for -table")
	flag.Parse()
	if *table != "" {
		if err := renderTable(*table, *sub); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *header {
		fmt.Println("build\tscene\tn\tu\tfps\tframe_p50_ms\tframe_p99_ms\tmain_cpu_ms\tproc_cpu_ms\tcpu_pct\talloc_kb\trss_mb\tstartup_ms")
		return
	}
	if _, ok := scenes[o.scene]; !ok || o.n < 1 {
		fmt.Fprintln(os.Stderr, "bad -scene or -n")
		os.Exit(2)
	}
	run(o)
}

func run(o options) {
	// bench.sh passes the exec time, so startup includes runtime and package init.
	start := time.Now()
	if ns, err := strconv.ParseInt(os.Getenv("BENCH_T0"), 10, 64); err == nil {
		start = time.Unix(0, ns)
	}

	a := app.New()
	w := a.NewWindow("fyne bench " + o.label)
	content, update := scenes[o.scene](o.n)

	// The probe is painted last and refreshed every tick, so its generator
	// runs exactly once per painted frame.
	var paints int
	var firstPaint time.Time
	px := image.NewRGBA(image.Rect(0, 0, 1, 1))
	probe := canvas.NewRaster(func(int, int) image.Image {
		paints++
		if paints == 1 {
			firstPaint = time.Now()
		}
		return px
	})
	probe.Resize(fyne.NewSize(4, 4))

	k := max(1, int(o.u*float64(o.n)))
	var ticks, next int
	var m0 *sample
	frames := make([]float64, 0, 4096)
	var anim *fyne.Animation
	anim = fyne.NewAnimation(time.Second, func(float32) {
		now := time.Now()
		ticks++
		t := now.Sub(start).Seconds()
		for range k {
			update(t, next)
			next = (next + 1) % o.n
		}
		probe.Refresh()

		switch {
		case paints == 0 || now.Sub(firstPaint) < o.warmup:
			return
		case m0 == nil:
			m0 = take(paints)
			if o.prof != "" {
				startProfile(o.prof)
			}
			return
		case now.Sub(m0.t) >= o.dur:
			m1 := take(paints)
			if o.prof != "" {
				stopProfile(o.prof)
			}
			report(o, m0, m1, frames, ms(firstPaint.Sub(start)))
			if o.shot != "" {
				screenshot(w, o.shot)
			}
			anim.Stop()
			a.Quit()
			return
		}

		// Do runs right after this frame's paint+present. A slow frame can let the
		// next tick go first (Fyne's loop selects randomly), so only keep samples
		// where no tick ran in between and the frame was actually painted.
		tick, p := ticks, paints
		fyne.Do(func() {
			if tick == ticks && paints > p {
				frames = append(frames, ms(time.Since(now)))
			}
		})
	})
	anim.RepeatCount = fyne.AnimationRepeatForever
	anim.Start()

	w.SetPadded(false)
	w.SetContent(container.NewStack(content, container.NewWithoutLayout(probe)))
	w.Resize(fyne.NewSize(winW, winH))
	w.ShowAndRun()
}

func report(o options, m0, m1 *sample, frames []float64, startup float64) {
	f := float64(m1.paints - m0.paints)
	secs := m1.t.Sub(m0.t).Seconds()
	proc := m1.procMs - m0.procMs
	slices.Sort(frames)
	pct := func(p float64) float64 {
		if len(frames) == 0 {
			return math.NaN()
		}
		return frames[int(p*float64(len(frames)-1))]
	}
	fmt.Printf("%s\t%s\t%d\t%g\t%.1f\t%.2f\t%.2f\t%.2f\t%.2f\t%.0f\t%.1f\t%.0f\t%.0f\n",
		o.label, o.scene, o.n, o.u, f/secs, pct(0.5), pct(0.99),
		(m1.threadMs-m0.threadMs)/f, proc/f, proc/10/secs,
		float64(m1.mem.TotalAlloc-m0.mem.TotalAlloc)/1024/f,
		m1.rssMB, startup)
}

func startProfile(prefix string) {
	f, err := os.Create(prefix + ".cpu")
	if err == nil {
		err = pprof.StartCPUProfile(f)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func stopProfile(prefix string) {
	pprof.StopCPUProfile()
	f, err := os.Create(prefix + ".allocs")
	if err == nil {
		err = pprof.Lookup("allocs").WriteTo(f, 0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func screenshot(w fyne.Window, path string) {
	f, err := os.Create(path)
	if err == nil {
		err = png.Encode(f, w.Canvas().Capture())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

type sample struct {
	t                       time.Time
	paints                  int
	procMs, threadMs, rssMB float64
	mem                     runtime.MemStats
}

// take must run on the main thread, the thread CPU time is read from the
// calling thread, which Fyne locks as its render thread.
func take(paints int) *sample {
	s := &sample{t: time.Now(), paints: paints}
	s.procMs, s.threadMs, s.rssMB = cpuTimes()
	runtime.ReadMemStats(&s.mem)
	return s
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }

// grid spreads n cells evenly over the window.
func grid(n int) (cols int, cw, ch float32) {
	cols = int(math.Ceil(math.Sqrt(float64(n) * winW / winH)))
	rows := (n + cols - 1) / cols
	return cols, winW / float32(cols), winH / float32(rows)
}

// rects: rounded rectangles and circles with strokes, each orbiting in its cell.
func rects(n int) (fyne.CanvasObject, func(float64, int)) {
	cols, cw, ch := grid(n)
	s := min(cw, ch) * 0.6
	r := (min(cw, ch) - s) / 2
	objs := make([]fyne.CanvasObject, n)
	for i := range objs {
		if i%2 == 0 {
			rect := canvas.NewRectangle(palette[i%len(palette)])
			rect.CornerRadius = s / 4
			rect.StrokeColor, rect.StrokeWidth = color.White, 1
			objs[i] = rect
		} else {
			c := canvas.NewCircle(palette[i%len(palette)])
			c.StrokeColor, c.StrokeWidth = color.White, 1
			objs[i] = c
		}
		objs[i].Resize(fyne.NewSize(s, s))
	}
	return container.NewWithoutLayout(objs...), func(t float64, i int) {
		a := 3*t + float64(i)
		objs[i].Move(fyne.NewPos(
			float32(i%cols)*cw+(cw-s)/2+r*float32(math.Cos(a)),
			float32(i/cols)*ch+(ch-s)/2+r*float32(math.Sin(a))))
	}
}

// texts: a grid of live numbers, re-rendered from new strings like a data view.
func texts(n int) (fyne.CanvasObject, func(float64, int)) {
	cols, cw, ch := grid(n)
	ts := make([]*canvas.Text, n)
	objs := make([]fyne.CanvasObject, n)
	for i := range ts {
		ts[i] = canvas.NewText("0", palette[i%len(palette)])
		ts[i].TextSize = max(8, min(14, ch*0.6))
		ts[i].Move(fyne.NewPos(float32(i%cols)*cw, float32(i/cols)*ch))
		ts[i].Resize(fyne.NewSize(cw, ch))
		objs[i] = ts[i]
	}
	return container.NewWithoutLayout(objs...), func(t float64, i int) {
		ts[i].Text = strconv.FormatFloat(1000*math.Sin(t+float64(i)), 'f', 1, 64)
		ts[i].Refresh()
	}
}

// zoom: labels whose size sweeps 0.2x to 1.8x every few seconds, like a zooming
// or animated view. The atlas caches a glyph per size in 64ths of a pixel, a
// range this wide does not fit, so it keeps rasterising; Slug draws any size
// from the outlines it already has. Labels overlap at the top of the sweep.
func zoom(n int) (fyne.CanvasObject, func(float64, int)) {
	cols, cw, ch := grid(n)
	size := ch * 0.5
	ts := make([]*canvas.Text, n)
	objs := make([]fyne.CanvasObject, n)
	for i := range ts {
		ts[i] = canvas.NewText("Zoom 42", palette[i%len(palette)])
		ts[i].TextSize = size
		ts[i].Move(fyne.NewPos(float32(i%cols)*cw, float32(i/cols)*ch))
		objs[i] = ts[i]
	}
	return container.NewWithoutLayout(objs...), func(t float64, i int) {
		ts[i].TextSize = size * float32(1+0.8*math.Sin(t+float64(i)))
		ts[i].Refresh()
	}
}

// widgets: a scrolling dashboard of tiles (label, progress bar, slider, entry,
// check, button). Updates hit the label, bar and slider of a tile.
func widgets(n int) (fyne.CanvasObject, func(float64, int)) {
	labels := make([]*widget.Label, n)
	bars := make([]*widget.ProgressBar, n)
	sliders := make([]*widget.Slider, n)
	tiles := make([]fyne.CanvasObject, n)
	for i := range tiles {
		labels[i] = widget.NewLabel("0")
		bars[i] = widget.NewProgressBar()
		sliders[i] = widget.NewSlider(0, 1000)
		e := widget.NewEntry()
		e.SetText("tile " + strconv.Itoa(i))
		tiles[i] = container.NewVBox(labels[i], bars[i], sliders[i], e,
			container.NewHBox(widget.NewCheck("On", nil), widget.NewButton("Go", nil)))
	}
	return container.NewVScroll(container.NewGridWithColumns(10, tiles...)), func(t float64, i int) {
		v := 0.5 + 0.5*math.Sin(t+float64(i))
		labels[i].SetText(strconv.FormatFloat(1000*v, 'f', 1, 64))
		bars[i].SetValue(v)
		sliders[i].SetValue(1000 * v)
	}
}
