package main

import "syscall"

// cpuTimes returns process and calling-thread CPU time in ms and peak RSS in MB.
func cpuTimes() (procMs, threadMs, rssMB float64) {
	var self, thread syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	_ = syscall.Getrusage(syscall.RUSAGE_THREAD, &thread)
	return rusageMs(&self), rusageMs(&thread), float64(self.Maxrss) / 1024
}

func rusageMs(r *syscall.Rusage) float64 { return float64(r.Utime.Nano()+r.Stime.Nano()) / 1e6 }
