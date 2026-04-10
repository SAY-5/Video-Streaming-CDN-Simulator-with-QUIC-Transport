package modeled

import (
	"testing"
	"time"
)

func TestCongestionSingleWindow(t *testing.T) {
	// 1 cwnd of data (10 * 1460 = 14600 bytes) should complete in 1 RTT.
	c := NewCongestionController(50 * time.Millisecond)
	got := c.TransferTime(InitialCWND*DefaultMSS, nil)
	if got != 50*time.Millisecond {
		t.Fatalf("expected 1 RTT (50ms), got %v", got)
	}
}

func TestCongestionTwoWindows(t *testing.T) {
	// 2 * 14600 = 29200 bytes.
	// RTT1: send 14600, cwnd -> 29200 (slow-start double).
	// RTT2: send remaining 14600. Done.
	c := NewCongestionController(50 * time.Millisecond)
	got := c.TransferTime(2*InitialCWND*DefaultMSS, nil)
	if got != 100*time.Millisecond {
		t.Fatalf("expected 2 RTT (100ms), got %v", got)
	}
}

func TestCongestionLargeTransferNoLoss(t *testing.T) {
	// 14600*7 = 102200 exactly 7 cwnd-doublings short of 100KB=102400,
	// so RTT1=1*14600, RTT2=2, RTT3=4 (sum=7*14600=102200, 200 bytes left),
	// RTT4=send last 200. Total 4 RTTs.
	c := NewCongestionController(50 * time.Millisecond)
	got := c.TransferTime(100*1024, nil)
	if got != 200*time.Millisecond {
		t.Fatalf("expected 200ms (4 RTTs of slow-start), got %v", got)
	}
}

func TestCongestionLossIncreasesTime(t *testing.T) {
	c1 := NewCongestionController(50 * time.Millisecond)
	c2 := NewCongestionController(50 * time.Millisecond)
	size := int64(200 * 1024)
	tNoLoss := c1.TransferTime(size, nil)
	tWithLoss := c2.TransferTime(size, []int64{50000})
	if tWithLoss <= tNoLoss {
		t.Fatalf("expected loss to increase transfer time; noLoss=%v withLoss=%v", tNoLoss, tWithLoss)
	}
}

func TestCongestionZeroBytes(t *testing.T) {
	c := NewCongestionController(50 * time.Millisecond)
	if got := c.TransferTime(0, nil); got != 0 {
		t.Fatalf("expected 0 for empty transfer, got %v", got)
	}
}

func TestCongestionLossReducesCwnd(t *testing.T) {
	// Force an early loss to verify ssthresh / cwnd reset path behaves.
	c := NewCongestionController(20 * time.Millisecond)
	t1 := c.TransferTime(500*1024, []int64{1000, 20000, 80000})
	c2 := NewCongestionController(20 * time.Millisecond)
	t2 := c2.TransferTime(500*1024, nil)
	if t1 <= t2 {
		t.Fatalf("losses did not slow transfer: loss=%v clean=%v", t1, t2)
	}
}
