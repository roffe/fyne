package painter

import (
	"encoding/binary"
	"image"
	"math"

	"fyne.io/fyne/v2/canvas"
)

// sdrWhite is the light, in cd/m², that HDR video shows white at on an SDR
// screen: HDR's reference white (ITU-R BT.2408).
const sdrWhite = 203

// The weights of red and blue in luma: BT.601's, BT.709's and BT.2020's.
const (
	bt601Kr, bt601Kb   = 0.299, 0.114
	bt709Kr, bt709Kb   = 0.2126, 0.0722
	bt2020Kr, bt2020Kb = 0.2627, 0.0593
)

// Limited range at 8 bits: luma from 16 to 235, chroma 224 steps around 128.
const (
	lumaFoot, lumaSteps      = 16, 219
	chromaMid, chromaSteps   = 128, 224
	bitsPerByte, maxDepthBit = 8, 16
)

// SMPTE ST 2084's PQ curve, its light a fraction of pqPeak cd/m².
const (
	pqM1, pqM2       = 0.1593017578125, 78.84375
	pqC1, pqC2, pqC3 = 0.8359375, 18.8515625, 18.6875
	pqPeak           = 10000
)

// ARIB STD-B67's HLG curve, and the screen BT.2100 shows it on: hlgScreen
// cd/m², with a system gamma of 1.2 from the 1/5 power of the luma.
const (
	hlgA, hlgB, hlgC = 0.17883277, 0.28466892, 0.55991073
	hlgSceneDiv      = 12
	hlgScreen        = 1000
	hlgGammaPow      = 0.2
	hlgLumaFloor     = 1e-6
	bt1886Gamma      = 2.4
)

// bt2020To709 turns linear BT.2020 light into BT.709's primaries (ITU-R BT.2087).
//
//revive:disable-next-line:add-constant the matrix is the standard's
var bt2020To709 = [3][3]float64{{1.660491, -0.587641, -0.072850}, {-0.124550, 1.132900, -0.008349}, {-0.018151, -0.100579, 1.118730}}

// VideoWide reports whether f's samples take two bytes, not one.
func VideoWide(f *canvas.VideoFrame) bool {
	return f.Depth > bitsPerByte
}

// VideoMatrix returns the rows that turn a sample of f, as (y, u, v, 1) with
// each a fraction of the most its bytes hold, into red, green and blue.
func VideoMatrix(f *canvas.VideoFrame) [3][4]float32 {
	kr, kb := bt601Kr, bt601Kb
	switch f.ColorSpace {
	case canvas.VideoColorSpaceBT709:
		kr, kb = bt709Kr, bt709Kb
	case canvas.VideoColorSpaceBT2020:
		kr, kb = bt2020Kr, bt2020Kb
	}
	kg := 1 - kr - kb
	depth, most := bitsPerByte, float64(0xff)
	if VideoWide(f) {
		depth, most = min(f.Depth, maxDepthBit), 0xffff
	}
	k := float64(int(1) << (depth - bitsPerByte)) // 16 to 235 are 64 to 940 at 10 bits
	ys, yo, cs := most/(lumaSteps*k), lumaFoot*k/most, most/(chromaSteps*k)
	if f.FullRange {
		ys = most / float64(int(1)<<depth-1)
		yo, cs = 0, ys
	}
	co := chromaMid * k / most
	row := func(u, v float64) [4]float32 {
		return [4]float32{float32(ys), float32(u), float32(v), float32(-ys*yo - (u+v)*co)}
	}
	return [3][4]float32{
		row(0, cs*2*(1-kr)),
		row(-cs*2*(1-kb)*kb/kg, -cs*2*(1-kr)*kr/kg),
		row(cs*2*(1-kb), 0),
	}
}

// VideoHDR returns how f is tone mapped: its transfer, 0 for SDR, 1 for PQ
// and 2 for HLG, then its peak as a PQ signal, and as fractions of that, SDR
// white and where the curve bringing the peak down to it starts.
func VideoHDR(f *canvas.VideoFrame) [4]float32 {
	if f.Transfer != canvas.VideoTransferPQ && f.Transfer != canvas.VideoTransferHLG {
		return [4]float32{}
	}
	peak := float64(f.Peak)
	if peak <= 0 || f.Transfer == canvas.VideoTransferHLG {
		peak = hlgScreen
	}
	top := pqSignal(min(max(peak, sdrWhite), pqPeak) / pqPeak)
	white := pqSignal(sdrWhite/float64(pqPeak)) / top
	return [4]float32{float32(f.Transfer), float32(top), float32(white), float32(1.5*white - 0.5)}
}

// VideoTurns returns f's rotation in clockwise quarter turns, 0 to 3.
func VideoTurns(f *canvas.VideoFrame) int {
	const quarter = 90
	return (f.Rotation/quarter%4 + 4) % 4
}

// VideoAspect returns the width over height f is shown at, turned.
func VideoAspect(f *canvas.VideoFrame) float32 {
	a := f.Aspect
	if a <= 0 && f.Height > 0 {
		a = float32(f.Width) / float32(f.Height)
	}
	if VideoTurns(f)%2 == 1 && a > 0 {
		a = 1 / a
	}
	return a
}

// VideoPlanes reports whether f's planes hold every sample its size and
// strides say they do, which a painter checks before reading them.
func VideoPlanes(f *canvas.VideoFrame) bool {
	b := sampleBytes(f)
	cw, ch := (f.Width+1)/2*b, (f.Height+1)/2
	if f.Width <= 0 || f.Height <= 0 || f.YStride < f.Width*b || len(f.Y) < (f.Height-1)*f.YStride+f.Width*b {
		return false
	}
	if f.U == nil {
		return f.CStride >= 2*cw && len(f.UV) >= (ch-1)*f.CStride+2*cw
	}
	return f.CStride >= cw && len(f.U) >= (ch-1)*f.CStride+cw && len(f.V) >= (ch-1)*f.CStride+cw
}

// VideoImage converts f to RGB on the CPU, turned as it is shown, for a
// painter that cannot do it as it draws.
func VideoImage(f *canvas.VideoFrame) *image.RGBA {
	m := VideoMatrix(f)
	hdr := VideoHDR(f)
	turns := VideoTurns(f)
	b := sampleBytes(f)
	sample := func(p []byte, i int) float32 {
		if b == 1 {
			return float32(p[i]) / 0xff
		}
		return float32(binary.LittleEndian.Uint16(p[i:])) / 0xffff
	}
	w, h := f.Width, f.Height
	out := image.Rect(0, 0, w, h)
	if turns%2 == 1 {
		out = image.Rect(0, 0, h, w)
	}
	img := image.NewRGBA(out)
	for y := range h {
		c := y / 2 * f.CStride
		for x := range w {
			var u, v float32
			if f.U == nil {
				u, v = sample(f.UV, c+x/2*2*b), sample(f.UV, c+x/2*2*b+b)
			} else {
				u, v = sample(f.U, c+x/2*b), sample(f.V, c+x/2*b)
			}
			yuv := [3]float32{sample(f.Y, y*f.YStride+x*b), u, v}
			var rgb [3]float64
			for i, row := range m {
				rgb[i] = float64(min(max(row[0]*yuv[0]+row[1]*yuv[1]+row[2]*yuv[2]+row[3], 0), 1))
			}
			if hdr[0] != 0 {
				rgb = sdr(rgb, hdr)
			}
			dx, dy := x, y
			switch turns {
			case 1:
				dx, dy = h-1-y, x
			case 2:
				dx, dy = w-1-x, h-1-y
			case 3:
				dx, dy = y, w-1-x
			}
			px := img.Pix[img.PixOffset(dx, dy):]
			for i, c := range rgb {
				px[i] = uint8(c*0xff + 0.5)
			}
			px[3] = 0xff
		}
	}
	return img
}

func sampleBytes(f *canvas.VideoFrame) int {
	if VideoWide(f) {
		return 2
	}
	return 1
}

// sdr tone maps e, an HDR signal in BT.2020, to an SDR one in BT.709, as
// VideoHDR says, which the shaders do the same way: the brightest channel of
// the light follows the BT.2390 curve from the video's peak down to SDR white,
// and the others keep their share of it, so colours keep their hue.
//
// ponytail: colours outside BT.709 are clipped, which shifts the hue of the
// most saturated; gamut mapping if that shows.
func sdr(e [3]float64, hdr [4]float32) [3]float64 {
	var l [3]float64 // as a fraction of 10000 cd/m²
	if hdr[0] == 2 {
		l = hlgLight(e)
	} else {
		for i := range e {
			l[i] = pqLight(e[i])
		}
	}
	var lin [3]float64
	for i, row := range bt2020To709 {
		lin[i] = row[0]*l[0] + row[1]*l[1] + row[2]*l[2]
	}
	l = lin
	top, white, knee := float64(hdr[1]), float64(hdr[2]), float64(hdr[3])
	if m := max(l[0], l[1], l[2]); m > 0 {
		if x := min(pqSignal(m)/top, 1); x > knee {
			t := (x - knee) / (1 - knee)
			t2, t3 := t*t, t*t*t
			s := (2*t3-3*t2+1)*knee + (t3-2*t2+t)*(1-knee) + (3*t2-2*t3)*white
			for i := range l {
				l[i] *= pqLight(s*top) / m
			}
		}
	}
	for i := range l {
		l[i] = math.Pow(min(max(l[i]*pqPeak/sdrWhite, 0), 1), 1/bt1886Gamma) // as SDR video is shown
	}
	return l
}

// pqLight and pqSignal are SMPTE ST 2084's curve between signal and light,
// light as a fraction of 10000 cd/m².
func pqLight(e float64) float64 {
	p := math.Pow(min(max(e, 0), 1), 1/pqM2)
	return math.Pow(max(p-pqC1, 0)/(pqC2-pqC3*p), 1/pqM1)
}

func pqSignal(l float64) float64 {
	p := math.Pow(max(l, 0), pqM1)
	return math.Pow((pqC1+pqC2*p)/(1+pqC3*p), pqM2)
}

// hlgLight is the light HLG's signal e makes on a 1000 cd/m² screen, as a
// fraction of 10000 cd/m²: ARIB STD-B67's scene light, then BT.2100's system
// gamma of 1.2.
func hlgLight(e [3]float64) [3]float64 {
	var s [3]float64
	for i, v := range e {
		if v <= 0.5 {
			s[i] = v * v / 3
		} else {
			s[i] = (math.Exp((v-hlgC)/hlgA) + hlgB) / hlgSceneDiv
		}
	}
	luma := bt2020Kr*s[0] + (1-bt2020Kr-bt2020Kb)*s[1] + bt2020Kb*s[2]
	gain := hlgScreen / float64(pqPeak) * math.Pow(max(luma, hlgLumaFloor), hlgGammaPow)
	return [3]float64{s[0] * gain, s[1] * gain, s[2] * gain}
}
