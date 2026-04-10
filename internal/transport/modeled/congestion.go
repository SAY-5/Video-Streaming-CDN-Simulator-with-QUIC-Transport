package modeled

import "time"

// DefaultMSS is the maximum segment size used by the congestion model (bytes).
const DefaultMSS int64 = 1460

// InitialCWND is the initial congestion window in units of MSS (RFC 6928).
const InitialCWND int64 = 10

// CongestionController models TCP/QUIC Reno congestion window behavior on a
// per-RTT basis. It is intentionally simple: it does not model ACK clocking,
// pacing, or sub-RTT cwnd evolution. Its job is to produce a realistic number
// of RTTs to transfer a given number of bytes given a list of loss events.
type CongestionController struct {
	cwnd        int64
	ssthresh    int64
	mss         int64
	rtt         time.Duration
	inSlowStart bool
}

// NewCongestionController initialises Reno state with the given round-trip
// time estimate.
func NewCongestionController(rtt time.Duration) *CongestionController {
	return &CongestionController{
		cwnd:        InitialCWND * DefaultMSS,
		ssthresh:    1 << 30, // effectively unbounded initially
		mss:         DefaultMSS,
		rtt:         rtt,
		inSlowStart: true,
	}
}

// TransferTime calculates time to transfer totalBytes given a sorted slice of
// byte offsets at which loss events occur. Each loss triggers the Reno
// response: ssthresh = cwnd/2, cwnd = ssthresh, exit slow-start.
//
// Algorithm (RTT-by-RTT simulation):
//  1. Start with cwnd = InitialCWND * MSS, in slow-start.
//  2. Each RTT, send min(cwnd, remaining_bytes) bytes.
//  3. Slow-start (cwnd < ssthresh): cwnd doubles each RTT.
//  4. Congestion avoidance (cwnd >= ssthresh): cwnd += MSS per RTT.
//  5. On a loss event falling within this RTT's sent range: apply Reno, and
//     add one extra RTT for fast retransmit recovery.
//  6. Total time = num_RTTs * RTT.
func (c *CongestionController) TransferTime(totalBytes int64, lossAtBytes []int64) time.Duration {
	if totalBytes <= 0 {
		return 0
	}
	sent := int64(0)
	rtts := int64(0)
	lossIdx := 0
	// Reset to fresh state per transfer so a controller instance is reusable
	// across tests, but this method is usually called on a freshly-constructed
	// controller.
	cwnd := c.cwnd
	ssthresh := c.ssthresh
	mss := c.mss

	for sent < totalBytes {
		// Bytes that will be sent in this RTT.
		remaining := totalBytes - sent
		toSend := cwnd
		if toSend > remaining {
			toSend = remaining
		}
		// MED-9 guard: with the current constants (InitialCWND*MSS=14600,
		// ssthresh floor 2*MSS=2920) cwnd cannot reach 0, but defend
		// against future parameter changes that would otherwise
		// produce an infinite loop here.
		if toSend <= 0 {
			break
		}
		windowStart := sent
		windowEnd := sent + toSend
		rtts++
		sent = windowEnd

		// Check for loss events in this window.
		lossInWindow := false
		for lossIdx < len(lossAtBytes) && lossAtBytes[lossIdx] < windowEnd {
			if lossAtBytes[lossIdx] >= windowStart {
				lossInWindow = true
			}
			lossIdx++
		}

		if lossInWindow {
			// Fast retransmit: one extra RTT to recover.
			rtts++
			// Reno: halve cwnd, exit slow-start.
			ssthresh = cwnd / 2
			if ssthresh < 2*mss {
				ssthresh = 2 * mss
			}
			cwnd = ssthresh
			continue
		}

		// No loss in this window: grow cwnd.
		if cwnd < ssthresh {
			// Slow-start: cwnd doubles.
			cwnd *= 2
		} else {
			// Congestion avoidance: cwnd += MSS per RTT.
			cwnd += mss
		}
	}

	return time.Duration(rtts) * c.rtt
}
