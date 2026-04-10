// Package emulated implements transport.Transport using real HTTP/2 and
// HTTP/3 round trips over real sockets. It is the ground-truth counterpart
// to the modeled transports and is used to validate model predictions.
//
// This file defines the CPU tracker. At ByteDance scale, QUIC's userspace
// implementation costs more CPU than kernel TCP, so every emulated fetch
// is wrapped in a CPU measurement. The measurement is process-wide (via
// getrusage) and therefore slightly noisy for single-operation
// attribution, but it is accurate enough for comparing aggregate CPU
// spend between TCP and QUIC across many runs.
package emulated

import (
	"sync"
	"syscall"
	"time"
)

// CPUTracker measures CPU time consumed across goroutines for transport
// operations. The tracker itself is stateless beyond a mutex guarding
// concurrent getrusage calls, which on some platforms is not reentrant-safe.
type CPUTracker struct {
	mu sync.Mutex
}

// NewCPUTracker returns a ready-to-use CPUTracker.
func NewCPUTracker() *CPUTracker {
	return &CPUTracker{}
}

// CPUMeasurement is an in-flight CPU measurement started by CPUTracker.Start.
// Call Stop to finalize and retrieve the consumed CPU time.
type CPUMeasurement struct {
	tracker  *CPUTracker
	start    time.Time
	startCPU time.Duration
}

// Start begins a new CPU measurement. It captures the current process
// user+system CPU time via getrusage(RUSAGE_SELF).
func (ct *CPUTracker) Start() *CPUMeasurement {
	return &CPUMeasurement{
		tracker:  ct,
		start:    time.Now(),
		startCPU: ct.readCPU(),
	}
}

// Stop ends the measurement and returns the delta in process CPU time.
// If the measurement is nil (e.g. tracker was nil), Stop returns 0.
func (m *CPUMeasurement) Stop() time.Duration {
	if m == nil || m.tracker == nil {
		return 0
	}
	end := m.tracker.readCPU()
	d := end - m.startCPU
	if d < 0 {
		d = 0
	}
	return d
}

// readCPU reads process user+system CPU time via getrusage. Returns zero
// on platforms or error conditions where getrusage is not available.
func (ct *CPUTracker) readCPU() time.Duration {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	u := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	s := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return u + s
}
