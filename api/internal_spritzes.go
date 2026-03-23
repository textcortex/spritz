package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalCreatePrincipal struct {
	ID      string   `json:"id"`
	Email   string   `json:"email,omitempty"`
	Teams   []string `json:"teams,omitempty"`
	Type    string   `json:"type,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Issuer  string   `json:"issuer,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`
}

type internalCreateSpritzRequest struct {
	Principal internalCreatePrincipal `json:"principal"`
	Request   json.RawMessage         `json:"request"`
}

func (p internalCreatePrincipal) normalize() (principal, error) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return principal{}, errors.New("principal.id is required")
	}
	principalTypeValue := normalizePrincipalType(p.Type, principalTypeService)
	return finalizePrincipal(
		id,
		strings.TrimSpace(p.Email),
		append([]string(nil), p.Teams...),
		strings.TrimSpace(p.Subject),
		strings.TrimSpace(p.Issuer),
		principalTypeValue,
		append([]string(nil), p.Scopes...),
		false,
	), nil
}

func (s *server) createInternalSpritz(c echo.Context) error {
	var body internalCreateSpritzRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	internalPrincipal, err := body.Principal.normalize()
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	encodedRequest := bytes.TrimSpace(body.Request)
	if len(encodedRequest) == 0 {
		return writeError(c, http.StatusBadRequest, "request is required")
	}

	clonedRequest := c.Request().Clone(c.Request().Context())
	clonedRequest.Body = io.NopCloser(bytes.NewReader(encodedRequest))
	clonedRequest.ContentLength = int64(len(encodedRequest))
	c.SetRequest(clonedRequest)
	c.Set(principalContextKey, internalPrincipal)
	return s.createSpritz(c)
}

func (s *server) getInternalSpritz(c echo.Context) error {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "name required")
	}

	var spritz spritzv1.Spritz
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, &spritz)
}
