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
	slackProvider        = "slack"
	slackWorkspaceScope  = "workspace"
	defaultSlackPresetID = "zeno"
)

type slackGateway struct {
	cfg        config
	httpClient *http.Client
	state      *oauthStateManager
	dedupe     *dedupeStore
	logger     *slog.Logger
	workers    sync.WaitGroup
}

var errSlackEventInFlight = errors.New("slack event is already being processed")

func newSlackGateway(cfg config, logger *slog.Logger) *slackGateway {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 15 * time.Second
	}
	if strings.TrimSpace(cfg.BrowserAuthHeaderID) == "" {
		cfg.BrowserAuthHeaderID = envOrDefault("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")
	}
	if strings.TrimSpace(cfg.BrowserAuthHeaderEmail) == "" {
		cfg.BrowserAuthHeaderEmail = envOrDefault("SPRITZ_AUTH_HEADER_EMAIL", "X-Spritz-User-Email")
	}
	if cfg.DedupeTTL <= 0 {
		cfg.DedupeTTL = 10 * time.Minute
	}
	if cfg.ProcessingTimeout <= 0 {
		cfg.ProcessingTimeout = 120 * time.Second
	}
	if cfg.SessionRetryInterval <= 0 {
		cfg.SessionRetryInterval = time.Second
	}
	if cfg.StatusMessageDelay <= 0 {
		cfg.StatusMessageDelay = 5 * time.Second
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 120 * time.Second
	}
	if cfg.PromptRetryInitial <= 0 {
		cfg.PromptRetryInitial = 250 * time.Millisecond
	}
	if cfg.PromptRetryMax <= 0 {
		cfg.PromptRetryMax = 2 * time.Second
	}
	if cfg.PromptRetryMax < cfg.PromptRetryInitial {
		cfg.PromptRetryMax = cfg.PromptRetryInitial
	}
	if cfg.PromptRetryTimeout <= 0 {
		cfg.PromptRetryTimeout = 8 * time.Second
	}
	return &slackGateway{
		cfg:        cfg,
		httpClient: &http.Client{},
		state:      newOAuthStateManager(cfg.OAuthStateSecret, 15*time.Minute),
		dedupe:     newDedupeStore(cfg.DedupeTTL),
		logger:     logger,
	}
}

func (g *slackGateway) routes() http.Handler {
	mux := http.NewServeMux()
	g.registerRoute(mux, "/healthz", g.handleHealthz)
	g.registerRoute(mux, "/slack/install", g.handleInstallRedirect)
	g.registerRoute(mux, "/slack/install/select", g.handleInstallTargetSelection)
	g.registerRoute(mux, "/slack/install/result", g.handleInstallResult)
	g.registerRoute(mux, "/slack/workspaces", g.handleWorkspaceManagement)
	g.registerRoute(mux, "/slack/workspaces/target", g.handleWorkspaceTarget)
	g.registerRoute(mux, "/slack/workspaces/test", g.handleWorkspaceTest)
	g.registerRoute(mux, "/slack/workspaces/disconnect", g.handleWorkspaceDisconnect)
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

// requestContext preserves an existing caller deadline and only falls back to
// the gateway HTTP timeout when the caller did not already bound the request.
func (g *slackGateway) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g.cfg.HTTPTimeout <= 0 {
		return ctx, func() {}
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, g.cfg.HTTPTimeout)
}
