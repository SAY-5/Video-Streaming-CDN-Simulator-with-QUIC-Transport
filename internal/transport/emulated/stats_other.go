//go:build !linux

package emulated

import "net"

// GetTCPInfo is a no-op on non-Linux platforms. Callers should check
// TCPStats.Available and treat false as "stats unavailable for this
// host kernel".
func GetTCPInfo(conn net.Conn) (*TCPStats, error) {
	_ = conn
	return &TCPStats{Available: false}, nil
}
