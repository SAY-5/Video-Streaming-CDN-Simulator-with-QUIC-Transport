package emulated

// TCPStats holds kernel TCP statistics extracted from a connection.
// On Linux these are populated from TCP_INFO via getsockopt; on other
// platforms (notably macOS used for local development) the fields
// remain zero and GetTCPInfo returns a zero-value struct with nil error.
type TCPStats struct {
	// RTTMs is the kernel's smoothed RTT estimate in milliseconds.
	RTTMs float64
	// RTTVarMs is the kernel's RTT variance in milliseconds.
	RTTVarMs float64
	// Retransmits is the cumulative retransmit count reported by the
	// kernel (tcpi_total_retrans on Linux).
	Retransmits int
	// BytesSent and BytesReceived are cumulative byte counters.
	BytesSent     int64
	BytesReceived int64
	// LossPackets is the kernel's cumulative lost-segment counter
	// (tcpi_lost on Linux).
	LossPackets int
	// ReorderPackets tracks reordering events where available.
	ReorderPackets int
	// Available is true when the stats were populated from a real
	// kernel; false on platforms where GetTCPInfo is a stub.
	Available bool
}
