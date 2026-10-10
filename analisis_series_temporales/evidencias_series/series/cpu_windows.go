//go:build windows

package main

import (
	"syscall"
	"time"
)

// cpuTime devuelve el tiempo de CPU (usuario + sistema) consumido por el proceso.
func cpuTime() time.Duration {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var c, e, k, u syscall.Filetime
	if err := syscall.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	// Filetime se expresa en intervalos de 100 ns.
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}
