package cache

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkLRUGet(b *testing.B) {
	c := NewLRUCache(100_000_000)
	now := time.Now()
	for i := 0; i < 10000; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i), SizeBytes: 1000}, now)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(fmt.Sprintf("k%d", i%10000), now)
	}
}

func BenchmarkLRUPut(b *testing.B) {
	c := NewLRUCache(100_000_000)
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i%10000), SizeBytes: 1000}, now)
	}
}

func BenchmarkARCGet(b *testing.B) {
	c := NewARCCache(100_000_000)
	now := time.Now()
	for i := 0; i < 10000; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i), SizeBytes: 1000}, now)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(fmt.Sprintf("k%d", i%10000), now)
	}
}

func BenchmarkARCPut(b *testing.B) {
	c := NewARCCache(100_000_000)
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Put(Item{Key: fmt.Sprintf("k%d", i%10000), SizeBytes: 1000}, now)
	}
}
