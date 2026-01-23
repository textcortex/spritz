package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	listenAddr := ":" + envOrDefault("PORT", "8080")
	upstreamRaw := envOrDefault("SPRITZ_GATEWAY_UPSTREAM", "https://api.openai.com")
	stripPrefix := strings.TrimSpace(os.Getenv("SPRITZ_GATEWAY_STRIP_PREFIX"))

	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		log.Fatalf("invalid SPRITZ_GATEWAY_UPSTREAM: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if stripPrefix != "" && strings.HasPrefix(req.URL.Path, stripPrefix) {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
		}
		req.Host = upstream.Host
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: %v", err)
		http.Error(w, "gateway upstream error", http.StatusBadGateway)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", proxy)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("spritz gateway listening on %s -> %s", listenAddr, upstreamRedacted(upstream))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func upstreamRedacted(u *url.URL) string {
	copy := *u
	copy.User = nil
	return copy.String()
}
