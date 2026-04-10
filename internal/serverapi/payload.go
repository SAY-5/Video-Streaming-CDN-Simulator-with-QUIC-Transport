// Package serverapi defines the shared HTTP API contract used by the
// origin server, edge server, and emulated client transports. Keeping the
// path scheme, header names, and payload generation in one place ensures
// origin/edge/client agree on the wire format.
package serverapi

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// Standard URL paths.
const (
	PathManifestPrefix = "/manifest/"
	PathSegmentPrefix  = "/segment/"
)

// Standard response headers.
const (
	HeaderCache         = "X-Cache"
	HeaderEdgeID        = "X-Edge-ID"
	HeaderOriginFetchMs = "X-Origin-Fetch-Ms"
	HeaderShieldHit     = "X-Shield-Hit"
	HeaderContentID     = "X-Content-ID"
)

// Cache header values.
const (
	CacheHit  = "HIT"
	CacheMiss = "MISS"
)

// SegmentPath returns the canonical URL path for a segment.
//   /segment/{contentID}/{segmentIndex}/{bitrateKbps}
func SegmentPath(contentID string, segIndex, bitrateKbps int) string {
	return fmt.Sprintf("%s%s/%d/%d", PathSegmentPrefix, contentID, segIndex, bitrateKbps)
}

// ParseSegmentPath extracts (contentID, segIndex, bitrateKbps) from a URL path.
func ParseSegmentPath(path string) (string, int, int, error) {
	if !strings.HasPrefix(path, PathSegmentPrefix) {
		return "", 0, 0, fmt.Errorf("not a segment path: %s", path)
	}
	rest := strings.TrimPrefix(path, PathSegmentPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return "", 0, 0, fmt.Errorf("malformed segment path: %s", path)
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, fmt.Errorf("bad segment index: %w", err)
	}
	br, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, 0, fmt.Errorf("bad bitrate: %w", err)
	}
	return parts[0], idx, br, nil
}

// ManifestPath returns the canonical URL path for a manifest.
func ManifestPath(contentID string) string {
	return PathManifestPrefix + contentID
}

// ParseManifestPath extracts the contentID from a manifest path.
func ParseManifestPath(path string) (string, error) {
	if !strings.HasPrefix(path, PathManifestPrefix) {
		return "", fmt.Errorf("not a manifest path: %s", path)
	}
	return strings.TrimPrefix(path, PathManifestPrefix), nil
}

// PayloadSize returns the deterministic payload size in bytes for a
// (contentID, segIndex, bitrateKbps) tuple. Sizes mimic the simulator's
// scene-complexity model so origin and modeled mode produce comparable
// payload distributions.
func PayloadSize(contentID string, segIndex, bitrateKbps, segmentSeconds int) int64 {
	if segmentSeconds <= 0 {
		segmentSeconds = 4
	}
	mean := float64(bitrateKbps*1000) / 8 * float64(segmentSeconds)
	// Per-segment scene factor in [0.5, 1.8], shared across bitrates.
	seed := contentSeed(contentID, segIndex)
	r := rand.New(rand.NewSource(seed))
	factor := 0.5 + r.Float64()*1.3
	return int64(mean * factor)
}

func contentSeed(contentID string, segIndex int) int64 {
	h := sha256.Sum256([]byte(contentID))
	v := int64(binary.BigEndian.Uint64(h[:8]))
	return v ^ int64(segIndex)*1_000_003
}

// FillPayload writes deterministic pseudo-random bytes into buf using a
// seed derived from (contentID, segIndex, bitrateKbps). Returns nothing
// because the buffer is mutated in place.
func FillPayload(buf []byte, contentID string, segIndex, bitrateKbps int) {
	seed := contentSeed(contentID, segIndex) ^ int64(bitrateKbps)*7919
	r := rand.New(rand.NewSource(seed))
	// Fill in 8-byte chunks for speed.
	i := 0
	for ; i+8 <= len(buf); i += 8 {
		binary.LittleEndian.PutUint64(buf[i:], r.Uint64())
	}
	for ; i < len(buf); i++ {
		buf[i] = byte(r.Intn(256))
	}
}
