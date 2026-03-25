package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	slackProvider             = "slack"
	slackWorkspaceScope       = "workspace"
	defaultSlackPresetID      = "zeno"
	defaultConversationCWD    = "/home/dev"
	defaultSlackThreadRootTTL = 7 * 24 * time.Hour
)

type slackGateway struct {
	cfg         config
	httpClient  *http.Client
	state       *oauthStateManager
	dedupe      *dedupeStore
	threadRoots *slackThreadRootStore
	logger      *slog.Logger
	workers     sync.WaitGroup
}

var errSlackEventInFlight = errors.New("slack event is already being processed")

func newSlackGateway(cfg config, logger *slog.Logger) *slackGateway {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 15 * time.Second
	}
	if cfg.DedupeTTL <= 0 {
		cfg.DedupeTTL = 10 * time.Minute
	}
	if cfg.ProcessingTimeout <= 0 {
		cfg.ProcessingTimeout = 60 * time.Second
	}
	return &slackGateway{
		cfg:         cfg,
		httpClient:  &http.Client{Timeout: cfg.HTTPTimeout},
		state:       newOAuthStateManager(cfg.OAuthStateSecret, 15*time.Minute),
		dedupe:      newDedupeStore(cfg.DedupeTTL),
		threadRoots: newSlackThreadRootStore(defaultSlackThreadRootTTL),
		logger:      logger,
	}
}

func (g *slackGateway) routes() http.Handler {
	mux := http.NewServeMux()
	g.registerRoute(mux, "/healthz", g.handleHealthz)
	g.registerRoute(mux, "/slack/install", g.handleInstallRedirect)
	g.registerRoute(mux, "/slack/oauth/callback", g.handleOAuthCallback)
	g.registerRoute(mux, "/slack/events", g.handleSlackEvents)
	return mux
}

func (g *slackGateway) registerRoute(mux *http.ServeMux, route string, handler http.HandlerFunc) {
	mux.HandleFunc(route, handler)
	if prefix := g.publicPathPrefix(); prefix != "" {
		mux.HandleFunc(prefix+route, handler)
	}
}

func (g *slackGateway) publicPathPrefix() string {
	parsed, err := url.Parse(strings.TrimSpace(g.cfg.PublicURL))
	if err != nil {
		return ""
	}
	prefix := strings.TrimRight(strings.TrimSpace(parsed.Path), "/")
	if prefix == "" || prefix == "." || prefix == "/" {
		return ""
	}
	return prefix
}

func (g *slackGateway) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (g *slackGateway) waitForWorkers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (g *slackGateway) presetID() string {
	return firstNonEmpty(g.cfg.PresetID, defaultSlackPresetID)
}
