package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type testPhase int

const (
	phaseLatency testPhase = iota
	phaseDownload
	phaseUpload
)

func (p testPhase) String() string {
	return [...]string{"延迟", "下载", "上传"}[p]
}

type clientConfig struct {
	Base        *url.URL
	Connections int
	Duration    time.Duration
	Tests       [3]bool
}

// Input accepts an IP/hostname, host:port or HTTP(S) URL. A separate port is
// used when the address has no explicit port (HTTP defaults to this tool's 8080).
func parseTarget(address, port string) (*url.URL, error) {
	address, port = strings.TrimSpace(address), strings.TrimSpace(port)
	if address == "" {
		return nil, fault("targetRequired")
	}
	if port == "" {
		port = "8080"
	}
	if err := validatePort(port); err != nil {
		return nil, err
	}
	explicitScheme := strings.Contains(address, "://")
	if !explicitScheme {
		if ip := net.ParseIP(address); ip != nil && strings.Contains(address, ":") {
			address = "[" + address + "]"
		}
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), " \t\r\n") {
		return nil, fault("invalidTarget")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fault("invalidScheme")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fault("invalidParts")
	}
	if u.Port() != "" {
		if err := validatePort(u.Port()); err != nil {
			return nil, err
		}
	} else if !(explicitScheme && u.Scheme == "https") {
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fault("invalidPort")
	}
	return nil
}

type latencyResult struct {
	Min, Avg, Max, Jitter float64 // milliseconds; HTTP round trip, not ICMP
	Samples               int
}

func latencySummary(values []float64) latencyResult {
	if len(values) == 0 {
		return latencyResult{}
	}
	r := latencyResult{Min: values[0], Max: values[0], Samples: len(values)}
	for i, v := range values {
		r.Min, r.Max = math.Min(r.Min, v), math.Max(r.Max, v)
		r.Avg += v
		if i > 0 {
			r.Jitter += math.Abs(v - values[i-1])
		}
	}
	r.Avg /= float64(len(values))
	if len(values) > 1 {
		r.Jitter /= float64(len(values) - 1)
	}
	return r
}

// Progress is copied into messages; network goroutines never mutate UI state.
type testEvent struct {
	Phase   testPhase
	Elapsed time.Duration
	Mbps    float64
	Bytes   int64
	Probes  int
	Latency *latencyResult
	Speed   *speedResult
	Final   bool
	Err     error
}

type speedClient struct {
	config clientConfig
	http   *http.Client
}

func newSpeedClient(config clientConfig) *speedClient {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		DisableCompression:    true,
		MaxIdleConns:          config.Connections + 2,
		MaxIdleConnsPerHost:   config.Connections + 2,
		MaxConnsPerHost:       config.Connections,
		ResponseHeaderTimeout: 5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
	}
	return &speedClient{config, &http.Client{
		Transport: transport,
		Timeout:   config.Duration + 10*time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c *speedClient) endpoint(action string) string {
	u := *c.config.Base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/" + action
	q := url.Values{}
	q.Set("duration", strconv.Itoa(max(1, int(math.Ceil(c.config.Duration.Seconds())))))
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *speedClient) run(ctx context.Context, emit func(testEvent)) {
	defer c.http.CloseIdleConnections()
	// Always verify the peer before sending large amounts of data, even when
	// latency is unchecked. This also detects HTML/login pages returning 200.
	if _, err := c.probe(ctx); err != nil {
		emit(testEvent{Final: true, Err: clientFault{Key: "connectFailed", Detail: err.Error()}})
		return
	}
	for phase, enabled := range c.config.Tests {
		if !enabled || ctx.Err() != nil {
			continue
		}
		p := testPhase(phase)
		emit(testEvent{Phase: p})
		if p == phaseLatency {
			err := c.latency(ctx, emit)
			if err != nil {
				emit(testEvent{Phase: p, Final: true, Err: err})
				return
			}
		} else {
			result, err := c.throughput(ctx, p, emit)
			emit(testEvent{Phase: p, Speed: &result})
			if err != nil {
				emit(testEvent{Phase: p, Final: true, Err: err})
				return
			}
		}
	}
	emit(testEvent{Final: true, Err: ctx.Err()})
}

func (c *speedClient) probe(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("ping"), nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 32))
	if err != nil {
		return 0, err
	}
	if res.StatusCode != http.StatusOK || string(body) != "ok" {
		return 0, clientFault{Key: "wrongServer", Detail: fmt.Sprintf("HTTP %d", res.StatusCode)}
	}
	return time.Since(start), nil
}

func (c *speedClient) latency(ctx context.Context, emit func(testEvent)) error {
	values := make([]float64, 0, 10)
	started := time.Now()
	for i := 0; i < 10; i++ {
		d, err := c.probe(ctx)
		if err != nil {
			return err
		}
		values = append(values, float64(d)/float64(time.Millisecond))
		r := latencySummary(values)
		emit(testEvent{Phase: phaseLatency, Elapsed: time.Since(started), Probes: i + 1, Latency: &r})
	}
	return nil
}

func (c *speedClient) throughput(parent context.Context, phase testPhase, emit func(testEvent)) (speedResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, c.config.Duration)
	defer cancel()
	var total atomic.Int64
	// Bounded, reusable data; no whole-test buffering or disk writes.
	var payload []byte
	if phase == phaseUpload {
		payload = make([]byte, 1024*1024)
		if _, err := rand.Read(payload); err != nil {
			return speedResult{}, err
		}
	}
	results := make(chan error, c.config.Connections)
	for i := 0; i < c.config.Connections; i++ {
		go func() {
			if phase == phaseDownload {
				results <- c.download(ctx, &total)
			} else {
				results <- c.upload(ctx, &total, payload)
			}
		}()
	}
	profile := speedProfile{}
	profile.sample(0, 0)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	remaining := c.config.Connections
	var firstErr error
	for remaining > 0 {
		select {
		case err := <-results:
			remaining--
			if err != nil && ctx.Err() == nil && firstErr == nil {
				firstErr = err
				cancel() // Fail promptly and close all other in-flight requests.
			}
		case <-ticker.C:
			elapsed := time.Since(started)
			count := total.Load()
			mbps := profile.sample(elapsed, count)
			emit(testEvent{Phase: phase, Elapsed: elapsed, Mbps: mbps, Bytes: count})
		}
	}
	elapsed, count := time.Since(started), total.Load()
	profile.sample(elapsed, count)
	result := profile.result(elapsed, count)
	result.Stopped = parent.Err() != nil
	if firstErr != nil {
		return result, firstErr
	}
	if parent.Err() != nil {
		return result, parent.Err()
	}
	if count == 0 {
		return result, fault("noData")
	}
	return result, nil
}

func (c *speedClient) download(ctx context.Context, total *atomic.Int64) error {
	buf := make([]byte, 256*1024)
	// If a server caps duration below the client's choice, reconnect for the
	// remaining time instead of silently ending the entire phase early.
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("download"), nil)
		if err != nil {
			return err
		}
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/octet-stream") {
			res.Body.Close()
			return clientFault{Key: "downloadFailed", Detail: fmt.Sprintf("HTTP %d", res.StatusCode)}
		}
		n, err := io.CopyBuffer(byteCounter{total}, res.Body, buf)
		res.Body.Close()
		if err != nil {
			return err
		}
		if n == 0 {
			return fault("emptyDownload")
		}
	}
	return ctx.Err()
}

type byteCounter struct{ total *atomic.Int64 }

func (w byteCounter) Write(p []byte) (int, error) {
	w.total.Add(int64(len(p)))
	return len(p), nil
}

func (c *speedClient) upload(ctx context.Context, total *atomic.Int64, payload []byte) error {
	// Aim for an acknowledgement every 100 ms: small chunks keep slow links
	// responsive, while larger chunks avoid excessive HTTP overhead at speed.
	chunkSize := min(len(payload), 32*1024)
	for ctx.Err() == nil {
		started := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("upload"), bytes.NewReader(payload[:chunkSize]))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		var ack struct {
			Bytes int64 `json:"bytes"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&ack)
		// Consume the small response so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode != http.StatusOK || err != nil || ack.Bytes <= 0 || ack.Bytes > int64(chunkSize) {
			return clientFault{Key: "uploadFailed", Detail: fmt.Sprintf("HTTP %d", res.StatusCode)}
		}
		// Count only bytes the server accepted. Reading a request body on the
		// client only measures buffering and can overstate a slow upload.
		total.Add(ack.Bytes)
		if elapsed := time.Since(started); elapsed > 0 {
			next := float64(ack.Bytes) * float64(sampleInterval) / float64(elapsed)
			chunkSize = int(max(float64(min(16*1024, len(payload))), min(float64(len(payload)), next)))
		}
	}
	return ctx.Err()
}
