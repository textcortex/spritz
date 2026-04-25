package main

import (
	"net/http"
	"strings"
	"time"
)

const pendingInstallCookieName = "spritz_slack_pending_install"

func pendingInstallCookieNameForRequest(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return pendingInstallCookieName
	}
	var suffix strings.Builder
	for _, char := range requestID {
		if char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '-' || char == '_' {
			suffix.WriteRune(char)
		}
	}
	if suffix.Len() == 0 {
		return pendingInstallCookieName
	}
	return pendingInstallCookieName + "_" + suffix.String()
}

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

func (g *slackGateway) setPendingInstallCookie(w http.ResponseWriter, r *http.Request, requestID, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingInstallCookieNameForRequest(requestID),
		Value:    strings.TrimSpace(state),
		Path:     g.pendingInstallCookiePath(),
		Expires:  time.Now().UTC().Add(g.state.ttl),
		MaxAge:   int(g.state.ttl.Seconds()),
		HttpOnly: true,
		Secure:   g.pendingInstallCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *slackGateway) clearPendingInstallCookie(w http.ResponseWriter, r *http.Request, requestID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pendingInstallCookieNameForRequest(requestID),
		Value:    "",
		Path:     g.pendingInstallCookiePath(),
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   g.pendingInstallCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *slackGateway) pendingInstallStateFromRequest(r *http.Request, requestID, explicitState string) string {
	if state := strings.TrimSpace(explicitState); state != "" {
		return state
	}
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(pendingInstallCookieNameForRequest(requestID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}
