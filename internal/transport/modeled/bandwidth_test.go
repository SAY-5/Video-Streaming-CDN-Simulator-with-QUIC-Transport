package modeled

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestBandwidthTraceZeroVariability(t *testing.T) {
	bt := NewSyntheticTrace(10, 5.0, 0, rand.New(rand.NewSource(1)))
	for i := 0; i < 10; i++ {
		bw := bt.BandwidthAt(time.Duration(i) * time.Second)
		if bw != 5.0 {
			t.Fatalf("expected constant 5 Mbps, got %v at t=%d", bw, i)
		}
	}
}

func TestBandwidthTraceDuration(t *testing.T) {
	bt := NewSyntheticTrace(20, 5.0, 0.4, rand.New(rand.NewSource(1)))
	if bt.TotalDuration() < 19*time.Second || bt.TotalDuration() > 20*time.Second {
		t.Fatalf("duration %v not ~20s", bt.TotalDuration())
	}
}

func TestBandwidthTraceAverage(t *testing.T) {
	bt := NewSyntheticTrace(1000, 10.0, 0.4, rand.New(rand.NewSource(3)))
	acc := 0.0
	samples := 5000
	for i := 0; i < samples; i++ {
		at := time.Duration(i) * 200 * time.Millisecond
		acc += bt.BandwidthAt(at)
	}
	avg := acc / float64(samples)
	if math.Abs(avg-10.0) > 1.5 {
		t.Fatalf("average %.2f not ~10", avg)
	}
}

func TestBandwidthTraceDeterminism(t *testing.T) {
	a := NewSyntheticTrace(50, 8, 0.4, rand.New(rand.NewSource(42)))
	b := NewSyntheticTrace(50, 8, 0.4, rand.New(rand.NewSource(42)))
	if a.TotalDuration() != b.TotalDuration() {
		t.Fatal("different durations")
	}
	for i := 0; i < 50; i++ {
		at := time.Duration(i) * time.Second
		if a.BandwidthAt(at) != b.BandwidthAt(at) {
			t.Fatalf("mismatch at %v", at)
		}
	}
}

func TestBandwidthTraceBeyondEnd(t *testing.T) {
	bt := NewSyntheticTrace(5, 4, 0.2, rand.New(rand.NewSource(1)))
	far := bt.BandwidthAt(1 * time.Hour)
	if far <= 0 {
		t.Fatalf("unexpected zero bandwidth past end: %v", far)
	}
}
