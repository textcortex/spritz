package main

import (
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type acpAgentResponse struct {
	Spritz spritzv1.Spritz `json:"spritz"`
}

func (s *server) listACPAgents(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	namespace := s.requestNamespace(c)
	list := &spritzv1.SpritzList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := s.client.List(c.Request().Context(), list, opts...); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	records := make([]acpAgentResponse, 0, len(list.Items))
	for _, item := range list.Items {
		if s.auth.enabled() && !principal.IsAdmin && item.Spec.Owner.ID != principal.ID {
			continue
		}
		if item.Status.Phase != "Ready" || item.Status.ACP == nil || item.Status.ACP.State != "ready" {
			continue
		}
		records = append(records, acpAgentResponse{Spritz: item})
	}
	sort.Slice(records, func(i, j int) bool {
		left := displayAgentName(&records[i].Spritz)
		right := displayAgentName(&records[j].Spritz)
		if left == right {
			return records[i].Spritz.Name < records[j].Spritz.Name
		}
		return left < right
	})

	return writeJSON(c, http.StatusOK, map[string]any{"items": records})
}
