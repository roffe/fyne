package canvas

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
)

// Declare conformity with CanvasObject interface
var _ fyne.CanvasObject = (*VideoFrame)(nil)

// VideoColorSpace says which matrix turns the samples of a VideoFrame into RGB.
//
// Since: 2.9
type VideoColorSpace int

const (
	// VideoColorSpaceBT601 is standard definition video's, and with FullRange JPEG's.
	//
	// Since: 2.9
	VideoColorSpaceBT601 VideoColorSpace = iota

	// VideoColorSpaceBT709 is HD video's.
	//
	// Since: 2.9
	VideoColorSpaceBT709

	// VideoColorSpaceBT2020 is UHD video's.
	//
	// Since: 2.9
	VideoColorSpaceBT2020
)

// VideoTransfer says how the samples of a VideoFrame encode light.
//
// Since: 2.9
type VideoTransfer int

const (
	// VideoTransferSDR is standard dynamic range video's gamma.
	//
	// Since: 2.9
	VideoTransferSDR VideoTransfer = iota

	// VideoTransferPQ is the perceptual quantizer of HDR10 (SMPTE ST 2084).
	//
	// Since: 2.9
	VideoTransferPQ

	// VideoTransferHLG is the hybrid log-gamma of HDR broadcasts (ARIB STD-B67).
	//
	// Since: 2.9
	VideoTransferHLG
)

// VideoImage is the picture of a VideoFrame in GPU memory: a two plane Vulkan
// image, luma then interleaved chroma, on the device the painter draws with
// (see driver/webgpu), as a video decoder sharing it leaves it. A timeline
// semaphore orders its users.
//
// Since: 2.9
type VideoImage struct {
	// Image is the VkImage, Semaphore its timeline VkSemaphore.
	Image, Semaphore uintptr

	// Use calls read with the VkImageLayout the image is in and the value of
	// the semaphore to wait for before reading it. When read reports that it
	// read the image, signalling value+1 once done, Use keeps the layout it
	// returns and moves the value on for the next user.
	Use func(read func(layout uint32, value uint64) (newLayout uint32, ok bool))
}

// VideoFrame describes a picture held the way video decoders make it, as luma
// and chroma planes, which painters turn into RGB as they draw it, on the GPU
// where there is one. A player shows a video by pointing the planes at each
// decoded frame in turn and calling Refresh, converting nothing on the CPU.
//
// Chroma has half the resolution both ways (4:2:0), either as separate U and
// V planes, as in I420, or interleaved in UV, as in NV12. Samples are 8-bit,
// or of up to 16 bits in two bytes, as in P010. HDR video is tone mapped to
// the range of an SDR screen. The planes are read when the frame is drawn
// after a Refresh, so they must not change until the next one.
//
// Since: 2.9
type VideoFrame struct {
	baseObject

	// Width and Height are the size of the picture in luma samples.
	Width, Height int

	// Depth is how many bits a sample has, 8 if 0. Samples of more take two
	// bytes, little endian, with the value in the low bits.
	Depth int

	// Y holds Height rows of Width luma samples, each YStride bytes after the last.
	Y       []byte
	YStride int

	// U and V hold (Height+1)/2 rows of (Width+1)/2 chroma samples, each
	// CStride bytes after the last. When U is nil, UV holds both, interleaved
	// U first, in rows of twice as many bytes.
	U, V, UV []byte
	CStride  int

	// ColorSpace is the matrix the samples convert with, and FullRange says that
	// they span 0 to 255 rather than video's 16 to 235, scaled to their Depth.
	ColorSpace VideoColorSpace
	FullRange  bool

	// Transfer is how the samples encode light. HDR video is taken to have
	// BT.2020 primaries, and Peak is the brightest it gets, in cd/m², as its
	// metadata says. Zero means 1000.
	Transfer VideoTransfer
	Peak     float32

	// Aspect is the width over height the picture is shown at before Rotation,
	// for samples that are not square. Zero shows Width by Height.
	Aspect float32

	// Rotation turns the picture clockwise by 90, 180 or 270 degrees.
	Rotation int

	// FillMode is how the picture fits the object's size: stretched, contained or covering.
	FillMode ImageFill

	// Image, when not nil, holds the picture on the GPU in place of the
	// planes, its chroma interleaved and its Depth 8, as NV12, or 16, as P010.
	// Painters drawing with another device draw nothing of it.
	Image *VideoImage
}

// Hide will set this frame to not be visible.
func (f *VideoFrame) Hide() {
	f.baseObject.Hide()

	repaint(f)
}

// Move the frame to a new position, relative to its parent / canvas.
func (f *VideoFrame) Move(pos fyne.Position) {
	if f.Position() == pos {
		return
	}

	f.baseObject.Move(pos)

	repaint(f)
}

// Refresh draws the frame from its planes again, uploading them into the
// textures it already has.
func (f *VideoFrame) Refresh() {
	cache.SetTextureDirty(f, image.Rect(0, 0, 1, 1))
	repaint(f)
}

// Resize on a frame updates the new size of this object, scaling the picture.
func (f *VideoFrame) Resize(s fyne.Size) {
	if s == f.Size() {
		return
	}

	f.baseObject.Resize(s)

	repaint(f)
}
