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

	"github.com/quic-go/quic-go/http3"

	"github.com/cdn-sim/cdn-sim/internal/transport"
)

// EmulatedQUICTransport implements transport.Transport by issuing real
// HTTPS requests over HTTP/3 (h3) on QUIC/UDP. It is the ground-truth
// counterpart to transport/modeled's QUICTransport.
//
// Session resumption / 0-RTT is not modeled explicitly here; the
// underlying quic-go http3.Transport handles session ticket reuse
// internally when both endpoints support it, so repeated connections to
// the same edge will naturally benefit from resumption without any
// special handling by the caller.
type EmulatedQUICTransport struct {
	client    *http.Client
	rt        *http3.Transport
	edgeURL   string
	tlsConfig *tls.Config
	cpu       *CPUTracker
}

// NewEmulatedQUICTransport constructs an HTTP/3-over-QUIC transport pointed
// at edgeURL (e.g. "https://edge-sg:8444"). The tlsConfig should trust
// the edge/origin certificate.
func NewEmulatedQUICTransport(edgeURL string, tlsConfig *tls.Config) *EmulatedQUICTransport {
	tc := tlsConfig.Clone()
	if len(tc.NextProtos) == 0 {
		tc.NextProtos = []string{"h3"}
	}
	rt := &http3.Transport{
		TLSClientConfig: tc,
	}
	return &EmulatedQUICTransport{
		client:    &http.Client{Transport: rt, Timeout: 60 * time.Second},
		rt:        rt,
		edgeURL:   edgeURL,
		tlsConfig: tc,
		cpu:       NewCPUTracker(),
	}
}

// FetchSegment issues a GET over HTTP/3 and returns a fully populated
// SegmentResponse. TTFB is captured via httptrace.
func (t *EmulatedQUICTransport) FetchSegment(ctx context.Context, req transport.SegmentRequest) (transport.SegmentResponse, error) {
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
		return transport.SegmentResponse{}, fmt.Errorf("emulated quic fetch %s: %w", url, err)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return transport.SegmentResponse{}, fmt.Errorf("emulated quic fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return transport.SegmentResponse{}, fmt.Errorf("emulated quic fetch %s: %w", url, err)
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

// FetchConcurrent fetches the given requests in parallel over independent
// QUIC streams multiplexed on the same connection. Unlike HTTP/2-over-TCP,
// loss on one stream does NOT head-of-line-block the others, which is the
// core behavioral difference this emulator exposes. Responses are returned
// in the same order as the input requests.
func (t *EmulatedQUICTransport) FetchConcurrent(ctx context.Context, reqs []transport.SegmentRequest) ([]transport.SegmentResponse, error) {
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
				cancel()
			}
		}(i)
	}
	wg.Wait()
	// HIGH-6 fix: do not hand back a partially-populated slice on error.
	for i, e := range errs {
		if e != nil {
			return nil, fmt.Errorf("emulated quic concurrent (req %d of %d): %w", i, len(reqs), e)
		}
	}
	return out, nil
}

// Handshake performs a lightweight HEAD /healthz over HTTP/3 and returns
// the elapsed duration. The resumption flag is informational: quic-go
// handles session ticket reuse internally.
func (t *EmulatedQUICTransport) Handshake(ctx context.Context, resumption bool) (time.Duration, error) {
	_ = resumption
	url := strings.TrimRight(t.edgeURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, fmt.Errorf("emulated quic handshake %s: %w", url, err)
	}
	start := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("emulated quic handshake %s: %w", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return time.Since(start), nil
}

// Close releases the underlying HTTP/3 transport. Tests should call this
// to avoid leaking UDP sockets during teardown.
func (t *EmulatedQUICTransport) Close() error {
	if t.rt != nil {
		return t.rt.Close()
	}
	return nil
}

// Protocol returns "quic-h3".
func (t *EmulatedQUICTransport) Protocol() string { return "quic-h3" }
