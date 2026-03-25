package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type runtimeBindingOwnerPrincipal struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type runtimeBindingRuntimePrincipal struct {
	AuthnMode          string `json:"authnMode"`
	ServiceAccountName string `json:"serviceAccountName"`
}

type runtimeBindingResponse struct {
	InstanceID       string                         `json:"instanceId"`
	Namespace        string                         `json:"namespace"`
	OwnerPrincipal   runtimeBindingOwnerPrincipal   `json:"ownerPrincipal"`
	RuntimePrincipal runtimeBindingRuntimePrincipal `json:"runtimePrincipal"`
	PresetID         string                         `json:"presetId"`
	InstanceClassID  string                         `json:"instanceClassId"`
}

func (s *server) getRuntimeBinding(c echo.Context) error {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}
	instanceID := strings.TrimSpace(c.Param("instanceId"))
	if instanceID == "" {
		return writeError(c, http.StatusBadRequest, "instanceId required")
	}

	var spritz spritzv1.Spritz
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{
		Namespace: namespace,
		Name:      instanceID,
	}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	response, err := buildRuntimeBindingResponse(&spritz)
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, err.Error())
	}
	return writeJSON(c, http.StatusOK, response)
}

func buildRuntimeBindingResponse(spritz *spritzv1.Spritz) (runtimeBindingResponse, error) {
	if spritz == nil {
		return runtimeBindingResponse{}, fmt.Errorf("spritz instance is required")
	}

	instanceID := strings.TrimSpace(spritz.Name)
	if instanceID == "" {
		return runtimeBindingResponse{}, fmt.Errorf("instance name is required")
	}
	namespace := strings.TrimSpace(spritz.Namespace)
	if namespace == "" {
		return runtimeBindingResponse{}, fmt.Errorf("instance namespace is required")
	}

	ownerID := strings.TrimSpace(spritz.Spec.Owner.ID)
	if ownerID == "" {
		return runtimeBindingResponse{}, fmt.Errorf("spec.owner.id is required")
	}

	serviceAccountName := strings.TrimSpace(spritz.Spec.ServiceAccountName)
	if serviceAccountName == "" {
		return runtimeBindingResponse{}, fmt.Errorf("spec.serviceAccountName is required")
	}

	annotations := spritz.GetAnnotations()
	presetID := strings.TrimSpace(annotations[presetIDAnnotationKey])
	if presetID == "" {
		return runtimeBindingResponse{}, fmt.Errorf("metadata.annotations[%q] is required", presetIDAnnotationKey)
	}
	instanceClassID := strings.TrimSpace(annotations[instanceClassAnnotationKey])
	if instanceClassID == "" {
		return runtimeBindingResponse{}, fmt.Errorf("metadata.annotations[%q] is required", instanceClassAnnotationKey)
	}

	return runtimeBindingResponse{
		InstanceID: instanceID,
		Namespace:  namespace,
		OwnerPrincipal: runtimeBindingOwnerPrincipal{
			ID:   ownerID,
			Type: "user",
		},
		RuntimePrincipal: runtimeBindingRuntimePrincipal{
			AuthnMode:          "workload_identity",
			ServiceAccountName: serviceAccountName,
		},
		PresetID:        presetID,
		InstanceClassID: instanceClassID,
	}, nil
}
