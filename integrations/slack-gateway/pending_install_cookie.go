package main

import (
	"net/http"
	"strings"
	"time"
)

const pendingInstallCookieName = "spritz_slack_pending_install"

func (g *slackGateway) pendingInstallCookiePath() string {
	return g.publicPathPrefix() + "/api/slack/install/selection"
}

func (g *slackGateway) pendingInstallCookieSecure(r *http.Request) bool {
	if r != nil {
		if r.TLS != nil {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(g.cfg.PublicURL)), "https://")
}

func (g *slackGateway) setPendingInstallCookie(w http.ResponseWriter, r *http.Request, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingInstallCookieName,
		Value:    strings.TrimSpace(state),
		Path:     g.pendingInstallCookiePath(),
		Expires:  time.Now().UTC().Add(g.state.ttl),
		MaxAge:   int(g.state.ttl.Seconds()),
		HttpOnly: true,
		Secure:   g.pendingInstallCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *slackGateway) clearPendingInstallCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingInstallCookieName,
		Value:    "",
		Path:     g.pendingInstallCookiePath(),
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   g.pendingInstallCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *slackGateway) pendingInstallStateFromRequest(r *http.Request, explicitState string) string {
	if state := strings.TrimSpace(explicitState); state != "" {
		return state
	}
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(pendingInstallCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}
