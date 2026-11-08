package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

//go:embed static/*
var assets embed.FS

var (
	addr = flag.String("addr", ":8080", "监听地址")
	// 与前端时长选项对齐（10/15/30 秒）。这是接口层的安全兜底：
	// 直接调 API 时可以把 duration 传得很大，靠它裁剪掉。
	maxDuration = flag.Int("max-duration", 30, "单次测速最大秒数")
)

func main() {
	flag.Parse()
	if *maxDuration < 1 {
		log.Fatal("max-duration must be positive")
	}
	mux := newMux()
	server := &http.Server{Addr: *addr, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 70 * time.Second}
	log.Printf("netest listening on http://%s", *addr)
	log.Fatal(server.ListenAndServe())
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", ping)
	mux.HandleFunc("/api/download", download)
	mux.HandleFunc("/api/upload", upload)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, assets, "static/index.html")
			return
		}
		http.FileServer(http.FS(assets)).ServeHTTP(w, r)
	})
	return mux
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Alpine.js evaluates declarative expressions at runtime, so its standard build
		// requires unsafe-eval. All scripts remain same-origin and are embedded assets.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-eval'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func ping(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func requested(r *http.Request) (time.Duration, int64) {
	duration := 10 * time.Second
	if v, err := strconv.Atoi(r.URL.Query().Get("duration")); err == nil && v > 0 {
		duration = time.Duration(v) * time.Second
	}
	if duration > time.Duration(*maxDuration)*time.Second {
		duration = time.Duration(*maxDuration) * time.Second
	}
	// size 为空或 0 表示不限数据量，仅由测试时长限制。
	var size int64
	if v, err := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64); err == nil && v > 0 {
		size = v
	}
	return duration, size
}

func download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	duration, size := requested(r)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 256*1024)
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	var sent int64
	for size == 0 || sent < size {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		default:
		}
		chunk := int64(len(buf))
		if size > 0 && size-sent < chunk {
			chunk = size - sent
		}
		if _, err := w.Write(buf[:chunk]); err != nil {
			return
		}
		sent += chunk
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	duration, size := requested(r)
	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()
	var body io.Reader = r.Body
	if size > 0 {
		body = io.LimitReader(r.Body, size)
	}
	n, err := io.Copy(io.Discard, readerWithContext{ctx, body})
	if err != nil && ctx.Err() == nil {
		http.Error(w, "upload failed", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"bytes":%d}`, n)
}

type readerWithContext struct {
	ctx context.Context
	r   io.Reader
}

func (r readerWithContext) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}
