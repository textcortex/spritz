package main

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

func ensureAuthenticated(principal principal, enabled bool) error {
	if !enabled {
		return nil
	}
	if stringsTrim(principal.ID) == "" {
		return errUnauthenticated
	}
	return nil
}

func authorizeHumanOwnedAccess(principal principal, ownerID string, enabled bool) error {
	if !enabled {
		return nil
	}
	if principal.isAdminPrincipal() {
		return nil
	}
	if !principalCanAccessOwner(principal, ownerID) {
		return errForbidden
	}
	return nil
}

func authorizeExactOwnerAccess(principal principal, ownerID string, enabled bool) error {
	if !enabled {
		return nil
	}
	if principal.isAdminPrincipal() {
		return nil
	}
	if stringsTrim(principal.ID) == "" {
		return errUnauthenticated
	}
	if stringsTrim(ownerID) == "" || stringsTrim(principal.ID) != stringsTrim(ownerID) {
		return errForbidden
	}
	return nil
}

func authorizeCallerOwnerAccess(principal principal, ownerID string, enabled bool) error {
	if !enabled {
		return nil
	}
	if stringsTrim(principal.ID) == "" {
		return errUnauthenticated
	}
	if stringsTrim(ownerID) == "" || stringsTrim(principal.ID) != stringsTrim(ownerID) {
		return errForbidden
	}
	return nil
}

func authorizeHumanOnly(principal principal, enabled bool) error {
	if !enabled {
		return nil
	}
	if principal.isAdminPrincipal() {
		return nil
	}
	if !principal.isHuman() {
		return errForbidden
	}
	return nil
}

func authorizeServiceAction(principal principal, scope string, enabled bool) error {
	if !enabled {
		return nil
	}
	if principal.isAdminPrincipal() {
		return nil
	}
	if !principal.isService() {
		return errForbidden
	}
	if !principal.hasScope(scope) {
		return errForbidden
	}
	return nil
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func writeForbidden(c echo.Context) error {
	return writeError(c, http.StatusForbidden, "forbidden")
}
