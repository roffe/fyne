//go:build wgpu

package glfw

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	"github.com/gogpu/wgpu"
)

func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	return wgpu.SurfaceTargetFromWindowsHWND(0, uintptr(unsafe.Pointer(v.GetWin32Window())))
}
