package emulated

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/cdn-sim/cdn-sim/internal/serverapi"
	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// buildSegmentURL converts a transport.SegmentRequest to the canonical
// /segment/<contentID>/<segIdx>/<bitrate> URL understood by the origin
// and edge servers.
//
// The simulator's video.SegmentID() produces IDs of the form
// "<contentID>/<bitrateKbps>/seg-<segIdx>". The server-side contract in
// internal/serverapi expects paths of the form
// "/segment/<contentID>/<segIdx>/<bitrateKbps>". This helper translates
// between the two representations so emulated transports can share the
// SegmentRequest type with modeled transports.
//
// If the SegmentID does not match the expected three-component shape the
// helper falls back to using it verbatim, which preserves backward
// compatibility with callers that already pass a path-ready ID.
func buildSegmentURL(edgeURL string, req transport.SegmentRequest) string {
	id := req.SegmentID
	parts := strings.Split(id, "/")
	base := strings.TrimRight(edgeURL, "/")
	if len(parts) == 3 && strings.HasPrefix(parts[2], "seg-") {
		contentID := parts[0]
		bitrate := parts[1]
		segIdx := strings.TrimPrefix(parts[2], "seg-")
		return base + serverapi.PathSegmentPrefix + contentID + "/" + segIdx + "/" + bitrate
	}
	return base + serverapi.PathSegmentPrefix + id
}

// EmulatedTCPTransport implements transport.Transport by issuing real
// HTTPS requests over HTTP/2 (h2) on TCP. It is the ground-truth
// counterpart to transport/modeled's TCPTransport and is primarily used
// for validating modeled predictions and for end-to-end benchmarking
// against real kernel networking.
type EmulatedTCPTransport struct {
	client    *http.Client
	edgeURL   string
	tlsConfig *tls.Config
	cpu       *CPUTracker
}

// NewEmulatedTCPTransport constructs an HTTP/2-over-TLS transport pointed
// at edgeURL (e.g. "https://edge-sg:8443"). The tlsConfig should trust
// the edge/origin certificate; tests typically pass
// &tls.Config{InsecureSkipVerify: true}.
func NewEmulatedTCPTransport(edgeURL string, tlsConfig *tls.Config) *EmulatedTCPTransport {
	// Clone so we can force ALPN to h2 without mutating caller state.
	tc := tlsConfig.Clone()
	if len(tc.NextProtos) == 0 {
		tc.NextProtos = []string{"h2"}
	}
	rt := &http2.Transport{
		TLSClientConfig: tc,
		AllowHTTP:       false,
	}
	return &EmulatedTCPTransport{
		client:    &http.Client{Transport: rt, Timeout: 60 * time.Second},
		edgeURL:   edgeURL,
		tlsConfig: tc,
		cpu:       NewCPUTracker(),
	}
}

// FetchSegment issues a GET against the edge for req and returns a fully
// populated SegmentResponse. TTFB is captured via httptrace so it reflects
// the real time to first response byte rather than total round-trip time.
func (t *EmulatedTCPTransport) FetchSegment(ctx context.Context, req transport.SegmentRequest) (transport.SegmentResponse, error) {
	url := buildSegmentURL(t.edgeURL, req)
	meas := t.cpu.Start()

	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			ttfb = time.Since(start)
		},
	}
	traceCtx := httptrace.WithClientTrace(ctx, trace)

	httpReq, err := http.NewRequestWithContext(traceCtx, http.MethodGet, url, nil)
	if err != nil {
		return transport.SegmentResponse{}, fmt.Errorf("emulated tcp fetch %s: %w", url, err)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return transport.SegmentResponse{}, fmt.Errorf("emulated tcp fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return transport.SegmentResponse{}, fmt.Errorf("emulated tcp fetch %s: %w", url, err)
	}
	total := time.Since(start)
	if ttfb == 0 {
		ttfb = total
	}

	n := int64(len(body))
	var goodput float64
	if total > 0 {
		goodput = (float64(n) * 8.0) / 1e6 / total.Seconds()
	}

	return transport.SegmentResponse{
		SegmentID:     req.SegmentID,
		BytesReceived: n,
		TTFB:          ttfb,
		TotalLatency:  total,
		GoodputMbps:   goodput,
		Protocol:      t.Protocol(),
		CPUTime:       meas.Stop(),
	}, nil
}

// FetchConcurrent fetches the given requests in parallel, sharing the
// underlying HTTP/2 connection. This faithfully exposes HTTP/2's TCP-layer
// head-of-line blocking under loss, which is exactly the behavior we want
// to observe in emulated mode. Responses are returned in the same order
// as the input requests.
func (t *EmulatedTCPTransport) FetchConcurrent(ctx context.Context, reqs []transport.SegmentRequest) ([]transport.SegmentResponse, error) {
	out := make([]transport.SegmentResponse, len(reqs))
	errs := make([]error, len(reqs))
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := t.FetchSegment(subCtx, reqs[i])
			out[i] = resp
			if err != nil {
				errs[i] = err
				cancel() // propagate cancellation to still-pending siblings
			}
		}(i)
	}
	wg.Wait()
	// HIGH-6 fix: on any failure, return nil instead of a partially-populated
	// slice so callers never see zero-value SegmentResponse entries.
	for i, e := range errs {
		if e != nil {
			return nil, fmt.Errorf("emulated tcp concurrent (req %d of %d): %w", i, len(reqs), e)
		}
	}
	return out, nil
}

// Handshake performs a lightweight HEAD /healthz and returns the elapsed
// duration. The resumption flag is informational: TLS 1.3 session ticket
// reuse is managed internally by the underlying http2.Transport.
func (t *EmulatedTCPTransport) Handshake(ctx context.Context, resumption bool) (time.Duration, error) {
	_ = resumption
	url := strings.TrimRight(t.edgeURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, fmt.Errorf("emulated tcp handshake %s: %w", url, err)
	}
	start := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("emulated tcp handshake %s: %w", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return time.Since(start), nil
}

// Protocol returns "tcp-h2".
func (t *EmulatedTCPTransport) Protocol() string { return "tcp-h2" }
