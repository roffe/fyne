//go:build !linux

package main

import "math"

// ponytail: CPU and RSS columns are Linux only, add darwin/windows samplers when someone benches there.
func cpuTimes() (procMs, threadMs, rssMB float64) {
	return math.NaN(), math.NaN(), math.NaN()
}
