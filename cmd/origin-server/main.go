// origin-server is the CDN-sim origin: a real HTTP/2 + HTTP/3 server that
// returns deterministic synthetic video segments. It is the bottom of the
// emulated content delivery stack — every cache miss eventually lands here.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/cdn-sim/cdn-sim/internal/serverapi"
	"github.com/cdn-sim/cdn-sim/internal/servertls"
)

func main() {
	addrH2 := flag.String("addr-h2", ":8443", "HTTP/2 (TCP/TLS) listen address")
	addrH3 := flag.String("addr-h3", ":8444", "HTTP/3 (QUIC/UDP) listen address")
	certPath := flag.String("tls-cert", "/app/certs/server.crt", "TLS cert file (auto-generated if missing)")
	keyPath := flag.String("tls-key", "/app/certs/server.key", "TLS key file (auto-generated if missing)")
	processingDelay := flag.Duration("processing-delay", 0, "synthetic origin processing delay added to every response")
	segmentSeconds := flag.Int("segment-seconds", 4, "default video segment duration (used for size calc)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cert, err := servertls.LoadOrGenerate(*certPath, *keyPath, []string{"origin", "shield", "edge-sg", "edge-mumbai"}, []net.IP{net.IPv4(172, 20, 0, 10)})
	if err != nil {
		logger.Error("tls", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc(serverapi.PathSegmentPrefix, segmentHandler(*processingDelay, *segmentSeconds, logger))
	mux.HandleFunc(serverapi.PathManifestPrefix, manifestHandler(logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	// Force TLS 1.3: HTTP/3 over QUIC mandates it anyway, and matching the
	// H2 path to the same floor removes an unintended TLS 1.2 vs 1.3
	// handshake-cost asymmetry from the TCP vs QUIC comparison.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1", "h3"},
		MinVersion:   tls.VersionTLS13,
	}

	var wg sync.WaitGroup
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// HTTP/2 server.
	h2 := &http.Server{
		Addr:      *addrH2,
		Handler:   mux,
		TLSConfig: tlsConfig,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("origin h2 listening", "addr", *addrH2)
		if err := h2.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("h2 server", "err", err)
		}
	}()

	// HTTP/3 server (QUIC). quic-go's ListenAndServeTLS requires actual cert
	// file paths; ListenAndServe uses the TLSConfig from the Server struct,
	// which is what we want.
	h3 := &http3.Server{
		Addr:      *addrH3,
		Handler:   mux,
		TLSConfig: tlsConfig,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("origin h3 listening", "addr", *addrH3)
		if err := h3.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("h3 server", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("origin shutting down")
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

// segmentHandler returns deterministic synthetic payloads keyed by
// (contentID, segIndex, bitrateKbps).
func segmentHandler(delay time.Duration, segmentSeconds int, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID, segIdx, bitrate, err := serverapi.ParseSegmentPath(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		size := serverapi.PayloadSize(contentID, segIdx, bitrate, segmentSeconds)
		buf := make([]byte, size)
		serverapi.FillPayload(buf, contentID, segIdx, bitrate)
		w.Header().Set(serverapi.HeaderContentID, contentID)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(buf)
		logger.Debug("origin segment served", "content", contentID, "seg", segIdx, "bitrate", bitrate, "bytes", size)
	}
}

// manifestHandler returns a tiny JSON manifest. Clients in modeled mode
// generate manifests locally; in emulated mode they may fetch this for
// realism.
func manifestHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID, err := serverapi.ParseManifestPath(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content_id":%q,"segments":30,"segment_seconds":4,"bitrates":[400,800,1500,3000,6000,12000]}`, contentID)
		logger.Debug("manifest served", "content", contentID)
	}
}
