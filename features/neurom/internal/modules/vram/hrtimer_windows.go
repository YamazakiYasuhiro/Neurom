// High-resolution timer for Windows using QueryPerformanceCounter.
// On Windows, time.Now() can return identical values for operations
// completing within the system clock resolution (~100μs-15.6ms depending
// on configuration), causing time.Since() to return 0. QPC provides
// sub-microsecond precision regardless of the system clock settings.

//go:build windows

package vram

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	procQPC    = kernel32.NewProc("QueryPerformanceCounter")
	procQPF    = kernel32.NewProc("QueryPerformanceFrequency")
	qpcFreqNs  int64 // frequency in ticks per second
)

func init() {
	procQPF.Call(uintptr(unsafe.Pointer(&qpcFreqNs)))
}

// hrTimestamp is a high-resolution timestamp from QPC.
type hrTimestamp int64

// hrNow returns the current QPC counter value.
func hrNow() hrTimestamp {
	var counter int64
	procQPC.Call(uintptr(unsafe.Pointer(&counter)))
	return hrTimestamp(counter)
}

// hrSince returns the time.Duration elapsed since start using QPC.
func hrSince(start hrTimestamp) time.Duration {
	now := hrNow()
	ticks := int64(now) - int64(start)
	// Convert QPC ticks to nanoseconds: ticks * 1e9 / frequency
	return time.Duration(ticks * 1_000_000_000 / qpcFreqNs)
}
