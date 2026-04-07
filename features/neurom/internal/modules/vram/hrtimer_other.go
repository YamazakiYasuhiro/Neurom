// Fallback timer for non-Windows platforms.
// On Linux/macOS, time.Now() already uses high-resolution monotonic clocks,
// so no special handling is needed.

//go:build !windows

package vram

import "time"

// hrTimestamp wraps time.Time for non-Windows platforms.
type hrTimestamp struct {
	t time.Time
}

// hrNow returns the current high-resolution timestamp.
func hrNow() hrTimestamp {
	return hrTimestamp{time.Now()}
}

// hrSince returns the time.Duration elapsed since start.
func hrSince(start hrTimestamp) time.Duration {
	return time.Since(start.t)
}
