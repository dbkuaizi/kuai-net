package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	for _, tc := range []struct{ address, port, want string }{
		{"192.168.5.8", "8080", "http://192.168.5.8:8080"},
		{"speed.local", "9000", "http://speed.local:9000"},
		{"speed.local:9001", "8080", "http://speed.local:9001"},
		{"http://speed.local:9000/", "8080", "http://speed.local:9000"},
		{"https://speed.local/net/", "8080", "https://speed.local/net"},
		{"::1", "8080", "http://[::1]:8080"},
		{"[2001:db8::1]:9000", "8080", "http://[2001:db8::1]:9000"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			u, err := parseTarget(tc.address, tc.port)
			if err != nil || u.String() != tc.want {
				t.Fatalf("parseTarget(%q, %q) = %v, %v; want %s", tc.address, tc.port, u, err, tc.want)
			}
		})
	}
	for _, address := range []string{"", "http://", "ftp://example.com", "bad host", "http://x:invalid", "http://u:p@x", "http://x/?q=1"} {
		if _, err := parseTarget(address, "8080"); err == nil {
			t.Errorf("accepted invalid address %q", address)
		}
	}
	for _, port := range []string{"0", "65536", "abc", "-1"} {
		if _, err := parseTarget("localhost", port); err == nil {
			t.Errorf("accepted invalid port %q", port)
		}
	}
}

func testClient(serverURL string, duration time.Duration, connections int) *speedClient {
	u, _ := url.Parse(serverURL)
	return newSpeedClient(clientConfig{u, connections, duration, [3]bool{true, true, true}})
}

// Rate-limit the fixture to exercise sampling and timing without generating
// gigabytes of loopback traffic. Only the client owns the overall deadline.
func TestClientDownloadReconnectsUntilDeadline(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		for i := 0; i < 3; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(15 * time.Millisecond):
			}
			_, _ = w.Write(make([]byte, 8192))
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	c := testClient(srv.URL, 400*time.Millisecond, 2)
	defer c.http.CloseIdleConnections()
	var ticks int
	started := time.Now()
	res, err := c.throughput(context.Background(), phaseDownload, func(e testEvent) { ticks++ })
	if err != nil || res.Bytes == 0 || ticks == 0 || requests.Load() <= 2 {
		t.Fatalf("result=%+v err=%v ticks=%d requests=%d", res, err, ticks, requests.Load())
	}
	if elapsed := time.Since(started); elapsed < 350*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("client did not honor its deadline: %v", elapsed)
	}
}

func TestClientUploadUsesServerAcknowledgement(t *testing.T) {
	var acknowledged atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Millisecond):
		}
		// Deliberately acknowledge fewer bytes than the request body.
		// Counting reads from the client body would fail this assertion.
		acknowledged.Add(1234)
		_ = json.NewEncoder(w).Encode(map[string]int{"bytes": 1234})
	}))
	defer srv.Close()
	c := testClient(srv.URL, 250*time.Millisecond, 1)
	defer c.http.CloseIdleConnections()
	res, err := c.throughput(context.Background(), phaseUpload, func(testEvent) {})
	if err != nil || res.Bytes == 0 || res.Bytes%1234 != 0 || res.Bytes > acknowledged.Load() {
		t.Fatalf("result=%+v err=%v acknowledged=%d", res, err, acknowledged.Load())
	}
}

func TestClientCancelsBlockedRequests(t *testing.T) {
	for _, phase := range []testPhase{phaseDownload, phaseUpload} {
		t.Run(phase.String(), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == phaseUpload {
					_, _ = io.Copy(io.Discard, r.Body)
				}
				entered <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer srv.Close()
			defer close(release)
			c := testClient(srv.URL, 30*time.Second, 1)
			defer c.http.CloseIdleConnections()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				r, err := c.throughput(ctx, phase, func(testEvent) {})
				if !r.Stopped {
					done <- errors.New("missing stopped marker")
					return
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("request never reached fixture")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancel did not interrupt request")
			}
		})
	}
}

func TestClientRejectsWrongServerAndHTTPFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<html>login</html>")
	}))
	defer srv.Close()
	c := testClient(srv.URL, time.Second, 1)
	defer c.http.CloseIdleConnections()
	var events []testEvent
	c.run(context.Background(), func(e testEvent) { events = append(events, e) })
	if len(events) != 1 || !events[0].Final || events[0].Err == nil {
		t.Fatalf("wrong server accepted: %+v", events)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "broken", 503) }))
	defer srv2.Close()
	c2 := testClient(srv2.URL, 30*time.Second, 4)
	defer c2.http.CloseIdleConnections()
	start := time.Now()
	_, err := c2.throughput(context.Background(), phaseDownload, func(testEvent) {})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("HTTP failure should stop promptly: %v", err)
	}
}

func TestClientPipelineCompletesAllStages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", ping)
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Millisecond)
		upload(w, r)
	})
	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(15 * time.Millisecond):
				if _, err := w.Write(make([]byte, 8192)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testClient(srv.URL, 350*time.Millisecond, 2)
	var latency *latencyResult
	var speeds [2]*speedResult
	var final bool
	c.run(context.Background(), func(e testEvent) {
		if e.Latency != nil {
			latency = e.Latency
		}
		if e.Speed != nil {
			speeds[int(e.Phase)-1] = e.Speed
		}
		if e.Final {
			final = true
			if e.Err != nil {
				t.Fatalf("pipeline failed: %v", e.Err)
			}
		}
	})
	if !final || latency == nil || latency.Samples != 10 || speeds[0] == nil || speeds[1] == nil || speeds[0].Bytes == 0 || speeds[1].Bytes == 0 {
		t.Fatalf("pipeline omitted results: latency=%+v download=%+v upload=%+v final=%v", latency, speeds[0], speeds[1], final)
	}
}

func TestClientUsesExistingAPI(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	c := testClient(srv.URL, time.Second, 1)
	defer c.http.CloseIdleConnections()
	if err := c.latency(context.Background(), func(e testEvent) {
		if e.Probes == 10 && (e.Latency == nil || e.Latency.Samples != 10) {
			t.Fatal("latency summary missing")
		}
	}); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = c.upload(ctx, &count, make([]byte, 4096))
	if count.Load() == 0 || count.Load()%4096 != 0 {
		t.Fatalf("existing API did not acknowledge upload: %d", count.Load())
	}
}
