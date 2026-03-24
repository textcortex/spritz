package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalCreateSpritzRequest struct {
	Request json.RawMessage `json:"request"`
}

type internalSpritzSummary struct {
	Metadata    internalSpritzMetadata `json:"metadata"`
	Spec        internalSpritzSpec     `json:"spec"`
	Status      spritzv1.SpritzStatus  `json:"status"`
	AccessURL   string                 `json:"accessUrl,omitempty"`
	ChatURL     string                 `json:"chatUrl,omitempty"`
	InstanceURL string                 `json:"instanceUrl,omitempty"`
}

type internalSpritzMetadata struct {
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	CreationTimestamp metav1.Time `json:"creationTimestamp,omitempty"`
}

type internalSpritzSpec struct {
	Owner spritzv1.SpritzOwner `json:"owner"`
}

func internalProvisionerPrincipal() principal {
	return finalizePrincipal(
		"spritz-internal",
		"",
		nil,
		"",
		"spritz-internal",
		principalTypeService,
		[]string{scopeInstancesCreate, scopeInstancesAssignOwner},
		false,
	)
}

func summarizeInternalSpritz(spritz *spritzv1.Spritz) internalSpritzSummary {
	return internalSpritzSummary{
		Metadata: internalSpritzMetadata{
			Name:              spritz.Name,
			Namespace:         spritz.Namespace,
			CreationTimestamp: spritz.CreationTimestamp,
		},
		Spec: internalSpritzSpec{
			Owner: spritz.Spec.Owner,
		},
		Status:      spritz.Status,
		AccessURL:   spritzv1.AccessURLForSpritz(spritz),
		ChatURL:     spritzv1.ChatURLForSpritz(spritz),
		InstanceURL: spritzv1.InstanceURLForSpritz(spritz),
	}
}

func (s *server) createInternalSpritz(c echo.Context) error {
	var body internalCreateSpritzRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	encodedRequest := bytes.TrimSpace(body.Request)
	if len(encodedRequest) == 0 {
		return writeError(c, http.StatusBadRequest, "request is required")
	}

	clonedRequest := c.Request().Clone(c.Request().Context())
	clonedRequest.Body = io.NopCloser(bytes.NewReader(encodedRequest))
	clonedRequest.ContentLength = int64(len(encodedRequest))
	c.SetRequest(clonedRequest)
	c.Set(principalContextKey, internalProvisionerPrincipal())
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
	return writeJSON(c, http.StatusOK, summarizeInternalSpritz(&spritz))
}
