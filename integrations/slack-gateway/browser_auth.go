package main

import (
	"net/http"
	"strings"
)

type browserPrincipal struct {
	ID    string
	Email string
}

func requireBrowserPrincipal(cfg config, w http.ResponseWriter, r *http.Request) (browserPrincipal, bool) {
	id := strings.TrimSpace(r.Header.Get(cfg.BrowserAuthHeaderID))
	if id == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return browserPrincipal{}, false
	}
	return browserPrincipal{
		ID:    id,
		Email: strings.TrimSpace(r.Header.Get(cfg.BrowserAuthHeaderEmail)),
	}, true
}
