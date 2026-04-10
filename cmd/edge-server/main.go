// edge-server is the CDN-sim edge cache: a real HTTP/2 + HTTP/3 server that
// serves cached video segments and falls through to an upstream (origin or
// shield) on miss. The upstream transport is selectable (TCP/H2 or QUIC/H3)
// at process startup, which is what makes the emulated stack able to study
// QUIC's compounding effect across CDN tiers.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/cdn-sim/cdn-sim/internal/cache"
	"github.com/cdn-sim/cdn-sim/internal/serverapi"
	"github.com/cdn-sim/cdn-sim/internal/servertls"
)

type cachedPayload struct {
	bytes       []byte
	contentType string
	contentID   string
}

func main() {
	addrH2 := flag.String("addr-h2", ":8443", "HTTP/2 listen address")
	addrH3 := flag.String("addr-h3", ":8444", "HTTP/3 listen address")
	certPath := flag.String("tls-cert", "/app/certs/server.crt", "TLS cert")
	keyPath := flag.String("tls-key", "/app/certs/server.key", "TLS key")
	originURLH2 := flag.String("origin-url-h2", "https://origin:8443", "upstream HTTP/2 URL")
	originURLH3 := flag.String("origin-url-h3", "https://origin:8444", "upstream HTTP/3 URL")
	originTransport := flag.String("origin-transport", "tcp", "upstream transport: tcp|quic")
	cacheSizeMB := flag.Int64("cache-size-mb", 1024, "edge cache size in MB")
	edgeID := flag.String("edge-id", "edge-default", "edge identifier (used in X-Edge-ID header)")
	caCertPath := flag.String("ca-cert", "/app/certs/server.crt", "CA cert to trust upstream (defaults to server cert for self-signed)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).With("edge", *edgeID)

	cert, err := servertls.LoadOrGenerate(*certPath, *keyPath, []string{"edge-sg", "edge-mumbai", "shield"}, []net.IP{net.IPv4(172, 21, 0, 20), net.IPv4(172, 22, 0, 30), net.IPv4(172, 22, 0, 40)})
	if err != nil {
		logger.Error("tls", "err", err)
		os.Exit(1)
	}

	upstreamPool, err := loadCertPool(*caCertPath)
	if err != nil {
		logger.Warn("ca cert load failed; using insecure-skip-verify for self-signed", "err", err)
	}

	// Cache holds payload bytes by canonical segment path.
	lru := cache.NewLRUCache(*cacheSizeMB * 1024 * 1024)
	// Sidecar map for actual byte payloads (cache.Item only stores size).
	var payloads sync.Map

	upstreamClient := buildUpstreamClient(*originTransport, upstreamPool)
	originBase := *originURLH2
	if *originTransport == "quic" {
		originBase = *originURLH3
	}

	var stats edgeStats

	mux := http.NewServeMux()
	mux.HandleFunc(serverapi.PathSegmentPrefix, segmentHandler(lru, &payloads, upstreamClient, originBase, *edgeID, &stats, logger))
	mux.HandleFunc(serverapi.PathManifestPrefix, manifestHandler(upstreamClient, originBase, *edgeID, logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/stats", stats.handler())

	// Force TLS 1.3 on both the H2 and H3 paths so QUIC and TCP share the
	// same handshake floor — otherwise an accidental TLS 1.2 negotiation on
	// the H2 port contaminates the protocol comparison (extra RTT for the
	// handshake, different cipher suites).
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1", "h3"},
		MinVersion:   tls.VersionTLS13,
	}

	var wg sync.WaitGroup
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	h2 := &http.Server{Addr: *addrH2, Handler: mux, TLSConfig: tlsConfig}
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("edge h2 listening", "addr", *addrH2, "upstream", originBase)
		if err := h2.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("h2", "err", err)
		}
	}()

	// quic-go's ListenAndServe uses the TLSConfig from the struct, unlike
	// ListenAndServeTLS which requires real cert file paths.
	h3 := &http3.Server{Addr: *addrH3, Handler: mux, TLSConfig: tlsConfig}
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("edge h3 listening", "addr", *addrH3)
		if err := h3.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("h3", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("edge shutting down", "hits", stats.hits.Load(), "misses", stats.misses.Load())
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	if err := h2.Shutdown(shCtx); err != nil {
		logger.Error("h2 shutdown", "err", err)
	}
	if err := h3.Close(); err != nil {
		logger.Error("h3 close", "err", err)
	}
	wg.Wait()
}

func loadCertPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certs found in %s", path)
	}
	return pool, nil
}

func buildUpstreamClient(transport string, pool *x509.CertPool) *http.Client {
	// When a CA cert pool is supplied (normal production path), use it for
	// real TLS verification. Only fall back to InsecureSkipVerify when no
	// CA was loaded — this preserves the self-signed dev workflow while
	// making the verification path actually active in tests and in real
	// emulated runs.
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if pool != nil {
		tlsCfg.RootCAs = pool
		// InsecureSkipVerify stays false: the pool pins the self-signed cert.
	} else {
		tlsCfg.InsecureSkipVerify = true
	}
	if transport == "quic" {
		rt := &http3.Transport{TLSClientConfig: tlsCfg}
		return &http.Client{Transport: rt, Timeout: 30 * time.Second}
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true},
		Timeout:   30 * time.Second,
	}
}

type edgeStats struct {
	hits   atomic.Int64
	misses atomic.Int64
}

func (s *edgeStats) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"hits":%d,"misses":%d}`, s.hits.Load(), s.misses.Load())
	}
}

func segmentHandler(lru *cache.LRUCache, payloads *sync.Map, client *http.Client, originBase, edgeID string, stats *edgeStats, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		now := time.Now()
		if _, ok := lru.Get(key, now); ok {
			if v, ok := payloads.Load(key); ok {
				p := v.(*cachedPayload)
				w.Header().Set(serverapi.HeaderCache, serverapi.CacheHit)
				w.Header().Set(serverapi.HeaderEdgeID, edgeID)
				w.Header().Set(serverapi.HeaderContentID, p.contentID)
				w.Header().Set("Content-Type", p.contentType)
				w.Header().Set("Content-Length", strconv.Itoa(len(p.bytes)))
				_, _ = w.Write(p.bytes)
				stats.hits.Add(1)
				return
			}
		}
		// Cache miss: fetch from upstream.
		stats.misses.Add(1)
		fetchStart := time.Now()
		req, err := http.NewRequestWithContext(r.Context(), "GET", originBase+key, nil)
		if err != nil {
			logger.Error("upstream request build", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			// Log the full error (with URL) server-side but never leak the
			// internal upstream topology in the client-facing 502 body.
			logger.Error("upstream fetch", "err", err, "url", originBase+key)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		// Cap upstream body at 32 MiB — segment payloads max out around
		// 12 Mbps × 4 s = 6 MB, so 32 MiB is a comfortable guard.
		const maxBodyBytes = 32 << 20
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if err != nil {
			logger.Error("upstream body read", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		fetchMs := time.Since(fetchStart).Milliseconds()
		contentID := resp.Header.Get(serverapi.HeaderContentID)
		ctype := resp.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		// Insert into cache + payload sidecar.
		lru.Put(cache.Item{Key: key, SizeBytes: int64(len(body)), Expiry: now.Add(time.Hour)}, now)
		payloads.Store(key, &cachedPayload{bytes: body, contentType: ctype, contentID: contentID})

		w.Header().Set(serverapi.HeaderCache, serverapi.CacheMiss)
		w.Header().Set(serverapi.HeaderEdgeID, edgeID)
		w.Header().Set(serverapi.HeaderContentID, contentID)
		w.Header().Set(serverapi.HeaderOriginFetchMs, strconv.FormatInt(fetchMs, 10))
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}
}

func manifestHandler(client *http.Client, originBase, edgeID string, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), "GET", originBase+r.URL.Path, nil)
		if err != nil {
			logger.Error("manifest request build", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			logger.Error("manifest upstream fetch", "err", err, "url", originBase+r.URL.Path)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		const maxManifestBytes = 1 << 20
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
		if err != nil {
			logger.Error("manifest body read", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		w.Header().Set(serverapi.HeaderEdgeID, edgeID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}
