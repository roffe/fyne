//go:build !darwin && !windows

package glfw

func startStallTimer(*gLDriver) {}
