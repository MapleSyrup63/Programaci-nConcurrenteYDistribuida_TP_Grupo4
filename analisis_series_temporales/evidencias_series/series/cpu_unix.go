//go:build !windows

package main

import (
	"syscall"
	"time"
)

// cpuTime devuelve el tiempo de CPU (usuario + sistema) consumido por el proceso.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
