// Package webgpu gives code that shares the GPU with Fyne's WebGPU painter,
// such as a video decoder, the device it draws with.
package webgpu

import (
	"github.com/roffe/gowgpu/hal/vulkan"

	painter "fyne.io/fyne/v2/internal/painter/webgpu"
)

// VulkanDevice returns the Vulkan device the WebGPU painter draws with, as a
// video decoder writing pictures for canvas.VideoFrame.Image needs it
// described, or false: when windows paint with something else, none has
// been shown yet, or the GPU cannot decode video with Vulkan Video. It is
// safe to call from any goroutine.
//
// Since: 2.9
func VulkanDevice() (vulkan.Interop, bool) {
	return painter.VulkanInterop()
}
