# bench

Renders stress scenes with this fyne checkout and measures frame time, CPU, allocations, memory and startup. Run it before and after a change to see what the change costs.

## Running it

```
cd test/bench
BASE=develop ./bench.sh
```

That builds the bench from your working tree and from `BASE` with the GL and WebGPU painters, and runs the whole matrix interleaved, base and head back to back per scene. Every painter runs twice, with text through the glyph atlas and through Slug (`FYNE_TEXT=slug`, rows ending in `-slug`). Slug is only run on head, and not for rects, which has no text. The base build lives in a sibling worktree (`../fyne-bench-base`) and gets the same bench code and a copy of your go.work, so the only thing that differs is fyne itself.

Windows will pop up one after another. Leave them alone and don't cover them, a hidden window on Wayland stops getting frame callbacks and the numbers go bad.

Painters: `gl` is the GL painter, `wgpu-<api>` the WebGPU painter on that API (`vulkan`, `gl`, `dx12` or `metal`, set with `FYNE_WGPU_BACKEND`).

Results land in `bin/results-<platform>.tsv`, and with `BASE` set `bin/results-<platform>-diff.tsv` shows base>head and the change in % for frame times, main thread CPU and allocations. The `#` line has CPU, GPU, kernel, session, Go version and both commits. Post it together with the numbers. `bin/results-<platform>.png` is the same table as an image, the best value per scene in green.

```
./bench.sh                     only the working tree
D=3s ./bench.sh                shorter runs, 8s by default
PLATFORM=x11 ./bench.sh        force X11/XWayland, default follows your session
PAINTERS=gl ./bench.sh         only some painters, default "gl wgpu-vulkan wgpu-gl"
TEXT=atlas ./bench.sh          only some text renderers, default "atlas slug"
./bench.sh rects 5000 1        one scene/n/u instead of the matrix, goes to bin/last.tsv
```

The binaries end up in `bin/` and take a few extra flags if you want to dig:

```
bin/head-wgpu -scene widgets -n 500 -u 0 -prof bin/x    writes bin/x.cpu and bin/x.allocs (pprof)
bin/head-wgpu -scene text -n 2000 -shot shot.png        saves a PNG of the window when the run ends
FYNE_TEXT=slug FYNE_WGPU_BACKEND=gl bin/head-wgpu ...   what the -slug and wgpu-gl rows run
bin/head-gl -table bin/last.tsv                         renders bin/last.png from a results TSV
```

Run-to-run noise is a few %, so rerun the scene before trusting a small diff.

## Requirements

The script and the CPU/RSS columns are Linux only for now, they come from getrusage. The bench itself builds everywhere, the CPU columns just print NaN.

- A C compiler and the usual Fyne build deps, see https://docs.fyne.io/started/
- A Vulkan driver for the WebGPU painter
- `glxinfo` is optional, it's only used to print the GPU name

## The scenes

**rects** (1000/5000/20000): N canvas objects spread over the window in a grid, every other one a rounded rect and the rest circles, all with a 1px stroke. Every frame every single one gets moved (they orbit inside their cell). No refreshes, just Move(), so that's pure draw throughput.

**text** (500/2000/8000): N canvas.Text in a grid, 8-14pt depending on how many there are. Every frame every one gets a new number like "-532.7" and a Refresh(). That's the whole text path, shaping, rasterizing or atlas, and texture uploads.

**zoom** (100/1000): N labels whose text size sweeps between 0.2x and 1.8x every few seconds, like a zooming or animated view. The atlas caches glyphs per size, so it keeps rasterising; Slug draws any size from the outlines it already has. This is the scene where they differ most.

**widgets** (50/500/2000 tiles): tiles in a 10 wide grid inside a VScroll. Each tile is a Label, ProgressBar, Slider, Entry and a Check + Button. Every frame every tile gets SetText on the label and SetValue on the bar and slider. 50 tiles is about one screen, the bigger ones are mostly scrolled off, so those are more refresh/layout/MinSize cost than drawing.

**widgets 500, u=0**: same dashboard but only one tile gets updated per frame. That's the "big app with one spinner going" case, what a mostly static UI costs per frame when something small moves.

Everything runs in a 1920x1080 window with a tiny raster on top that gets refreshed every frame, so only frames that actually got painted are counted, not just ticks.

## The columns

| column | what it is |
|---|---|
| fps | painted frames per second, capped at 60 |
| frame_p50_ms, frame_p99_ms | animation tick to after present, per frame. This is the number to compare |
| main_cpu_ms | CPU time on the render thread per painted frame |
| proc_cpu_ms | CPU time for the whole process per painted frame (GC, driver threads) |
| cpu_pct | process CPU, 100 = one core |
| alloc_kb | Go heap allocated per painted frame |
| rss_mb | peak resident memory |
| startup_ms | exec to first painted frame |

## Keeping it fair

All builds use `migrated_fynedo`. Without it every Refresh does a runtime.Stack to check it's on main, and that ate up to 79% of the CPU in the text scene.

Vsync is off (`__GL_SYNC_TO_VBLANK=0`, `vblank_mode=0`, the WebGPU painter picks mailbox on its own), so frame times don't include swap waits.

On Wayland Fyne only paints after the compositor's frame callback, which rounds fps down to 60/30/20 once a frame plus compositor latency goes over 16.7ms. Look at frame time, or run `PLATFORM=x11` to see fps without that.
