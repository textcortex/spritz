package main

import (
	"net/http"
	"strings"
)

const (
	slackGatewayPrincipalIDHeader    = "X-Spritz-User-Id"
	slackGatewayPrincipalEmailHeader = "X-Spritz-User-Email"
)

type browserPrincipal struct {
	ID    string
	Email string
}

func requireBrowserPrincipal(w http.ResponseWriter, r *http.Request) (browserPrincipal, bool) {
	id := strings.TrimSpace(r.Header.Get(slackGatewayPrincipalIDHeader))
	if id == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return browserPrincipal{}, false
	}
	return browserPrincipal{
		ID:    id,
		Email: strings.TrimSpace(r.Header.Get(slackGatewayPrincipalEmailHeader)),
	}, true
}
