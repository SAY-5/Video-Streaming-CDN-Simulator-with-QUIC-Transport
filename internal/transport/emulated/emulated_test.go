package emulated

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/cdn-sim/cdn-sim/internal/serverapi"
	"github.com/cdn-sim/cdn-sim/internal/transport"
	"github.com/cdn-sim/cdn-sim/internal/video"
)

// testHandler mimics the edge+origin segment handler so the emulated
// transports can be exercised end-to-end without spinning up the full
// cmd binaries. It implements a one-entry cache keyed by full URL path
// so TestEmulatedCacheHitOnSecondFetch can observe HIT semantics.
type testHandler struct {
	mu       sync.Mutex
	cache    map[string][]byte
	hitCount int32
}

func newTestHandler() *testHandler {
	return &testHandler{cache: make(map[string][]byte)}
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	contentID, segIdx, bitrate, err := serverapi.ParseSegmentPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	buf, hit := h.cache[r.URL.Path]
	if !hit {
		size := serverapi.PayloadSize(contentID, segIdx, bitrate, 4)
		buf = make([]byte, size)
		serverapi.FillPayload(buf, contentID, segIdx, bitrate)
		h.cache[r.URL.Path] = buf
	}
	h.mu.Unlock()

	if hit {
		atomic.AddInt32(&h.hitCount, 1)
		w.Header().Set(serverapi.HeaderCache, serverapi.CacheHit)
	} else {
		w.Header().Set(serverapi.HeaderCache, serverapi.CacheMiss)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
	_, _ = w.Write(buf)
}

// generateTestCert returns a self-signed TLS cert valid for 127.0.0.1,
// usable for both HTTP/2 (TCP/TLS) and HTTP/3 (QUIC/TLS 1.3).
func generateTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cdn-sim-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

// startH2Server spins up an HTTP/2 TLS test server on 127.0.0.1:random.
// Returns the base URL (e.g. https://127.0.0.1:12345), the handler, and
// a cleanup func.
func startH2Server(t *testing.T) (string, *testHandler, func()) {
	t.Helper()
	h := newTestHandler()
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	return srv.URL, h, srv.Close
}

// startH3Server spins up an HTTP/3 (QUIC) test server on 127.0.0.1:random
// UDP. Returns the base URL, the handler, and a cleanup func.
func startH3Server(t *testing.T) (string, *testHandler, func()) {
	t.Helper()
	h := newTestHandler()
	cert := generateTestCert(t)
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"},
		MinVersion:   tls.VersionTLS13,
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	srv := &http3.Server{
		Handler:   h,
		TLSConfig: tlsConf,
	}
	go func() {
		_ = srv.Serve(udpConn)
	}()
	url := fmt.Sprintf("https://127.0.0.1:%d", udpConn.LocalAddr().(*net.UDPAddr).Port)
	cleanup := func() {
		_ = srv.Close()
		_ = udpConn.Close()
	}
	// Give the server a brief moment to be ready to accept packets.
	time.Sleep(50 * time.Millisecond)
	return url, h, cleanup
}

// insecureTLS returns a client TLS config that skips verification — safe
// for tests using self-signed certs bound to 127.0.0.1.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
}

func TestBuildSegmentURL(t *testing.T) {
	req := transport.SegmentRequest{SegmentID: video.SegmentID("content-3", 7, 1500)}
	got := buildSegmentURL("https://edge-sg:8443", req)
	want := "https://edge-sg:8443/segment/content-3/7/1500"
	if got != want {
		t.Fatalf("buildSegmentURL: got %q want %q", got, want)
	}
}

func TestEmulatedTCPFetchSegment(t *testing.T) {
	url, _, cleanup := startH2Server(t)
	defer cleanup()

	tr := NewEmulatedTCPTransport(url, insecureTLS())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := transport.SegmentRequest{SegmentID: video.SegmentID("content-1", 0, 800), BitrateKbps: 800}
	resp, err := tr.FetchSegment(ctx, req)
	if err != nil {
		t.Fatalf("FetchSegment: %v", err)
	}
	if resp.Protocol != "tcp-h2" {
		t.Errorf("protocol: got %q want tcp-h2", resp.Protocol)
	}
	want := serverapi.PayloadSize("content-1", 0, 800, 4)
	if resp.BytesReceived != want {
		t.Errorf("BytesReceived: got %d want %d", resp.BytesReceived, want)
	}
	if resp.TTFB <= 0 {
		t.Errorf("TTFB should be >0, got %v", resp.TTFB)
	}
	if resp.TotalLatency < resp.TTFB {
		t.Errorf("TotalLatency %v < TTFB %v", resp.TotalLatency, resp.TTFB)
	}
	if resp.GoodputMbps <= 0 {
		t.Errorf("GoodputMbps should be >0, got %v", resp.GoodputMbps)
	}
}

func TestEmulatedQUICFetchSegment(t *testing.T) {
	url, _, cleanup := startH3Server(t)
	defer cleanup()

	tr := NewEmulatedQUICTransport(url, insecureTLS())
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := transport.SegmentRequest{SegmentID: video.SegmentID("content-2", 1, 1500), BitrateKbps: 1500}
	resp, err := tr.FetchSegment(ctx, req)
	if err != nil {
		t.Fatalf("FetchSegment: %v", err)
	}
	if resp.Protocol != "quic-h3" {
		t.Errorf("protocol: got %q want quic-h3", resp.Protocol)
	}
	want := serverapi.PayloadSize("content-2", 1, 1500, 4)
	if resp.BytesReceived != want {
		t.Errorf("BytesReceived: got %d want %d", resp.BytesReceived, want)
	}
	if resp.TTFB <= 0 {
		t.Errorf("TTFB should be >0, got %v", resp.TTFB)
	}
	if resp.TotalLatency < resp.TTFB {
		t.Errorf("TotalLatency %v < TTFB %v", resp.TotalLatency, resp.TTFB)
	}
}

func TestEmulatedTCPFetchConcurrent(t *testing.T) {
	url, _, cleanup := startH2Server(t)
	defer cleanup()

	tr := NewEmulatedTCPTransport(url, insecureTLS())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reqs := make([]transport.SegmentRequest, 5)
	for i := range reqs {
		reqs[i] = transport.SegmentRequest{
			SegmentID:   video.SegmentID("content-c", i, 800),
			BitrateKbps: 800,
		}
	}
	resps, err := tr.FetchConcurrent(ctx, reqs)
	if err != nil {
		t.Fatalf("FetchConcurrent: %v", err)
	}
	if len(resps) != len(reqs) {
		t.Fatalf("resp count: got %d want %d", len(resps), len(reqs))
	}
	for i, r := range resps {
		if r.SegmentID != reqs[i].SegmentID {
			t.Errorf("resp %d id: got %q want %q", i, r.SegmentID, reqs[i].SegmentID)
		}
		want := serverapi.PayloadSize("content-c", i, 800, 4)
		if r.BytesReceived != want {
			t.Errorf("resp %d bytes: got %d want %d", i, r.BytesReceived, want)
		}
	}
}

func TestEmulatedQUICFetchConcurrent(t *testing.T) {
	url, _, cleanup := startH3Server(t)
	defer cleanup()

	tr := NewEmulatedQUICTransport(url, insecureTLS())
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reqs := make([]transport.SegmentRequest, 5)
	for i := range reqs {
		reqs[i] = transport.SegmentRequest{
			SegmentID:   video.SegmentID("content-q", i, 1500),
			BitrateKbps: 1500,
		}
	}
	resps, err := tr.FetchConcurrent(ctx, reqs)
	if err != nil {
		t.Fatalf("FetchConcurrent: %v", err)
	}
	if len(resps) != len(reqs) {
		t.Fatalf("resp count: got %d want %d", len(resps), len(reqs))
	}
	for i, r := range resps {
		if r.SegmentID != reqs[i].SegmentID {
			t.Errorf("resp %d id: got %q want %q", i, r.SegmentID, reqs[i].SegmentID)
		}
		want := serverapi.PayloadSize("content-q", i, 1500, 4)
		if r.BytesReceived != want {
			t.Errorf("resp %d bytes: got %d want %d", i, r.BytesReceived, want)
		}
	}
}

func TestEmulatedHandshake(t *testing.T) {
	h2URL, _, cleanup1 := startH2Server(t)
	defer cleanup1()
	h3URL, _, cleanup2 := startH3Server(t)
	defer cleanup2()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tcp := NewEmulatedTCPTransport(h2URL, insecureTLS())
	d, err := tcp.Handshake(ctx, false)
	if err != nil {
		t.Fatalf("tcp handshake: %v", err)
	}
	if d <= 0 {
		t.Errorf("tcp handshake duration: got %v", d)
	}

	q := NewEmulatedQUICTransport(h3URL, insecureTLS())
	defer q.Close()
	d2, err := q.Handshake(ctx, false)
	if err != nil {
		t.Fatalf("quic handshake: %v", err)
	}
	if d2 <= 0 {
		t.Errorf("quic handshake duration: got %v", d2)
	}
}

func TestEmulatedCacheHitOnSecondFetch(t *testing.T) {
	url, h, cleanup := startH2Server(t)
	defer cleanup()

	tr := NewEmulatedTCPTransport(url, insecureTLS())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := transport.SegmentRequest{SegmentID: video.SegmentID("content-cache", 0, 800), BitrateKbps: 800}

	if _, err := tr.FetchSegment(ctx, req); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := tr.FetchSegment(ctx, req); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if got := atomic.LoadInt32(&h.hitCount); got != 1 {
		t.Errorf("expected exactly 1 cache hit on second fetch, got %d", got)
	}
}

func TestCPUTrackerStartStop(t *testing.T) {
	ct := NewCPUTracker()
	m := ct.Start()
	// Do a small amount of work.
	sum := 0
	for i := 0; i < 100000; i++ {
		sum += i
	}
	_ = sum
	d := m.Stop()
	if d < 0 {
		t.Errorf("cpu delta negative: %v", d)
	}
}
