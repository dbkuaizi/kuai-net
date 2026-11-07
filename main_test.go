package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *httptest.Server {
	return httptest.NewServer(securityHeaders(newMux()))
}

func TestIndexServedWithCSP(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("CSP must allow Alpine.js expressions, got %q", csp)
	}
	if res.Header.Get("Cache-Control") == "" {
		t.Fatal("missing Cache-Control header")
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "main.js") {
		t.Fatal("index page does not reference main.js")
	}
}

func TestStaticAssetsEmbedded(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	for _, path := range []string{"/static/main.js", "/static/main.css", "/static/alpine.min.js"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, res.StatusCode)
		}
	}
}

func TestPing(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if string(body) != "ok" {
		t.Fatalf("ping body = %q, want ok", body)
	}
}

func TestDownloadHonoursSize(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/download?size=4096&duration=5")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 4096 {
		t.Fatalf("downloaded %d bytes, want 4096", len(body))
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if ce := res.Header.Get("Content-Encoding"); ce != "identity" {
		t.Fatalf("content-encoding = %q, want identity", ce)
	}
}

func TestDownloadWithoutSizeStreamsUntilDeadline(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/download?duration=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	// 不限制数据量时，应由时长结束连接；这里只验证能读到数据且不是立刻 EOF。
	buf := make([]byte, 256*1024)
	n, err := io.ReadFull(res.Body, buf)
	if err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	if n != len(buf) {
		t.Fatalf("read %d bytes, want %d", n, len(buf))
	}
}

func TestUploadDiscardsBody(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	payload := bytes.Repeat([]byte("x"), 10000)
	res, err := http.Post(srv.URL+"/api/upload", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if got, want := string(body), `{"bytes":10000}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	res, err := http.Post(srv.URL+"/api/download", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.StatusCode)
	}
}

func TestRequestedClampsDuration(t *testing.T) {
	old := *maxDuration
	*maxDuration = 5
	defer func() { *maxDuration = old }()

	req := httptest.NewRequest(http.MethodGet, "/api/download?duration=999", nil)
	d, size := requested(req)
	if d.String() != "5s" {
		t.Fatalf("duration = %s, want 5s", d)
	}
	if size != 0 {
		t.Fatalf("size = %d, want 0 (不限制)", size)
	}
}
