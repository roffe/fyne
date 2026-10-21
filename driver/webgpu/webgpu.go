// Package webgpu gives code that shares the GPU with Fyne's WebGPU painter,
// such as a video decoder or another API drawing pictures for it, the device
// it draws with.
package webgpu

import (
	"github.com/roffe/gowgpu/hal/vulkan"

	painter "fyne.io/fyne/v2/internal/painter/webgpu"
)

// VulkanDevice returns the Vulkan device the WebGPU painter draws with, as
// code making pictures for canvas.VideoFrame.Image needs it described, or
// false when windows paint with something else or none has been shown yet.
// Its QueueFamilies are empty when the GPU cannot decode video with Vulkan
// Video. It is safe to call from any goroutine.
//
// Since: 2.9
func VulkanDevice() (vulkan.Interop, bool) {
	return painter.VulkanInterop()
}

// D3D12Device returns the ID3D12Device the WebGPU painter draws with, for
// code making pictures for canvas.VideoFrame.Image on it, or false when
// windows paint with something else or none has been shown yet. It is safe
// to call from any goroutine.
//
// Since: 2.9
func D3D12Device() (uintptr, bool) {
	return painter.D3D12Device()
}

// MetalDevice returns the id<MTLDevice> the WebGPU painter draws with, for
// code making pictures for canvas.VideoFrame.Image on it, or false when
// windows paint with something else or none has been shown yet. It is safe
// to call from any goroutine.
//
// Since: 2.9
func MetalDevice() (uintptr, bool) {
	return painter.MetalDevice()
}
