package video

import "time"

// ThroughputSample captures one observed segment fetch rate.
type ThroughputSample struct {
	Kbps      float64
	FetchTime time.Duration
}

// PlayerState is the current state visible to an ABR algorithm.
type PlayerState struct {
	BufferLevel        time.Duration
	MaxBuffer          time.Duration
	LastThroughputKbps float64
	ThroughputHistory  []ThroughputSample
	CurrentBitrateKbps int
	SegmentIndex       int
	StartupComplete    bool
	RebufferCount      int
}

// ABRDecision is the next bitrate the player should request.
type ABRDecision struct {
	BitrateKbps int
	Reason      string
}

// ABRAlgorithm is the pluggable interface for bitrate selection policies.
type ABRAlgorithm interface {
	SelectBitrate(state PlayerState, manifest Manifest) ABRDecision
	Name() string
}
