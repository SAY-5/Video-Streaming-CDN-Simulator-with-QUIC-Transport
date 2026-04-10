package cache

import (
	"fmt"
	"math/rand"
)

// ZipfPopularity generates content identifiers drawn from a Zipf distribution.
// Under alpha ~1.05, roughly the top 10% of IDs receive the majority of
// requests, which models catalog popularity in video streaming workloads.
type ZipfPopularity struct {
	catalogSize int
	alpha       float64
	rng         *rand.Rand
	zipf        *rand.Zipf
}

// NewZipfPopularity builds a popularity generator. alpha must be > 1 per
// the math/rand Zipf API; typical values are 1.05..1.5.
func NewZipfPopularity(catalogSize int, alpha float64, rng *rand.Rand) *ZipfPopularity {
	if catalogSize <= 0 {
		catalogSize = 1
	}
	if alpha <= 1 {
		alpha = 1.05
	}
	// math/rand's Zipf: s>1, v>=1, imax. s maps to the skew.
	z := rand.NewZipf(rng, alpha, 1, uint64(catalogSize-1))
	return &ZipfPopularity{
		catalogSize: catalogSize,
		alpha:       alpha,
		rng:         rng,
		zipf:        z,
	}
}

// NextContentID returns the next content ID to request as "content-<n>".
func (z *ZipfPopularity) NextContentID() string {
	return fmt.Sprintf("content-%d", z.zipf.Uint64())
}

// NextIndex returns the next raw index, 0-based.
func (z *ZipfPopularity) NextIndex() int {
	return int(z.zipf.Uint64())
}

// CatalogSize returns the configured catalog size.
func (z *ZipfPopularity) CatalogSize() int { return z.catalogSize }
