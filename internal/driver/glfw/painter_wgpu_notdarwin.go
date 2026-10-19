//go:build (wgpu || !gl) && !darwin && !wasm && !test_web_driver

package glfw

import "github.com/go-gl/glfw/v3.4/glfw"

// followBackingScale has nothing to do: the surface takes the window's scale
// from the size it is configured with.
func followBackingScale(*glfw.Window) {}
