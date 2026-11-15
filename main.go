package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	if err := runCommand(os.Args[1:]); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCommand(args []string) error {
	if len(args) > 0 && (args[0] == "client" || args[0] == "c") {
		flags := flag.NewFlagSet("kuainet "+args[0], flag.ContinueOnError)
		flags.Usage = func() {
			fmt.Fprintln(flags.Output(), "用法：kuainet client（或 kuainet c）\n打开终端测速客户端，在界面中输入服务端 IP 和端口并选择配置。")
		}
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("请在 TUI 界面中填写服务端地址；用法：kuainet %s", args[0])
		}
		return runClientTUI()
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("未知子命令 %q；使用 kuainet 启动服务端，kuainet client（或 c）启动客户端", args[0])
	}
	flag.CommandLine.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "用法：kuainet [服务端参数]\n      kuainet client | c\n\n无子命令时启动 HTTP 测速服务；client / c 打开 TUI 客户端。\n\n服务端参数：")
		flag.PrintDefaults()
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		return err
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("服务端参数无效；使用 kuainet -h 查看帮助")
	}
	if *maxDuration < 1 {
		return fmt.Errorf("max-duration must be positive")
	}
	mux := newMux()
	server := &http.Server{Addr: *addr, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 70 * time.Second}
	log.Printf("netest listening on http://%s", *addr)
	return server.ListenAndServe()
}

func newMux() *http.ServeMux {
	index := renderIndex()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", ping)
	mux.HandleFunc("/api/download", download)
	mux.HandleFunc("/api/upload", upload)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/static/" || r.URL.Path == "/static/index.html" {
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
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
