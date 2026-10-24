//go:build ((!gles && !arm && !arm64) || darwin) && !wasm && !test_web_driver && !wgpu

package glfw

import "github.com/go-gl/glfw/v3.4/glfw"

func glWindowHints() {
	glfw.WindowHint(glfw.ClientAPI, glfw.OpenGLAPI)
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)

	glfw.WindowHint(glfw.CocoaGraphicsSwitching, glfw.True)
}
