//go:build linux

package emulated

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// GetTCPInfo extracts kernel TCP_INFO from a net.Conn on Linux using
// getsockopt(TCP_INFO). Returns a populated TCPStats with Available=true
// on success. Returns a zero stats with Available=false if the connection
// is not a TCPConn (e.g. a QUIC UDP connection).
func GetTCPInfo(conn net.Conn) (*TCPStats, error) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		// Not a TCP connection (QUIC, test harness, etc.). Not an
		// error — caller should just treat stats as unavailable.
		return &TCPStats{}, nil
	}
	sc, err := tc.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("tcp syscall conn: %w", err)
	}
	var info *unix.TCPInfo
	var innerErr error
	ctrlErr := sc.Control(func(fd uintptr) {
		info, innerErr = unix.GetsockoptTCPInfo(int(fd), syscall.IPPROTO_TCP, unix.TCP_INFO)
	})
	if ctrlErr != nil {
		return nil, fmt.Errorf("tcp control: %w", ctrlErr)
	}
	if innerErr != nil {
		return nil, fmt.Errorf("getsockopt TCP_INFO: %w", innerErr)
	}
	return &TCPStats{
		RTTMs:          float64(info.Rtt) / 1000.0,    // Rtt is in microseconds
		RTTVarMs:       float64(info.Rttvar) / 1000.0, // same unit
		Retransmits:    int(info.Total_retrans),
		LossPackets:    int(info.Lost),
		ReorderPackets: int(info.Reordering),
		Available:      true,
	}, nil
}
