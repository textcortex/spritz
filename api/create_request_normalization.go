package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	spritzv1 "spritz.sh/operator/api/v1"
)

type createRequestError struct {
	status  int
	message string
	data    any
	err     error
}

func (e *createRequestError) Error() string {
	return e.message
}

func (e *createRequestError) Unwrap() error {
	return e.err
}

func newCreateRequestError(status int, err error) error {
	return &createRequestError{
		status:  status,
		message: err.Error(),
		err:     err,
	}
}

func newCreateRequestErrorWithData(status int, message string, data any, err error) error {
	return &createRequestError{
		status:  status,
		message: message,
		data:    data,
		err:     err,
	}
}

func writeCreateRequestError(c echo.Context, err error) error {
	var requestErr *createRequestError
	if errors.As(err, &requestErr) {
		if requestErr.data != nil {
			return writeJSendFailData(c, requestErr.status, requestErr.data)
		}
		return writeError(c, requestErr.status, requestErr.message)
	}
	return writeError(c, http.StatusInternalServerError, err.Error())
}

type normalizedCreateRequest struct {
	body                 createRequest
	fingerprintRequest   createRequest
	namespace            string
	owner                spritzv1.SpritzOwner
	userConfigKeys       map[string]json.RawMessage
	userConfigPayload    userConfigPayload
	normalizedUserConfig json.RawMessage
	requestedImage       bool
	requestedRepo        bool
	requestedNamespace   bool
	nameProvided         bool
	requestedNamePrefix  string
}

func validateReservedCreateAnnotations(annotations map[string]string) error {
	if len(annotations) == 0 {
		return nil
	}
	reservedKeys := []string{
		presetIDAnnotationKey,
		instanceClassAnnotationKey,
		instanceClassVersionAnnotationKey,
	}
	for _, key := range reservedKeys {
		if strings.TrimSpace(annotations[key]) != "" {
			return errors.New("annotations contain reserved control-plane keys")
		}
	}
	return nil
}

func (s *server) normalizeCreateRequest(_ context.Context, principal principal, body createRequest) (*normalizedCreateRequest, error) {
	body.Name = strings.TrimSpace(body.Name)
	body.NamePrefix = strings.TrimSpace(body.NamePrefix)
	applyTopLevelCreateFields(&body)
	normalizedPresetInputs, err := normalizePresetInputs(body.PresetInputs)
	if err != nil {
		return nil, newCreateRequestError(http.StatusBadRequest, err)
	}
	body.PresetInputs = normalizedPresetInputs
	if strings.TrimSpace(body.Spec.ServiceAccountName) != "" && !principalCanUseProvisionerFlow(principal) {
		return nil, newCreateRequestError(http.StatusForbidden, errors.New("spec.serviceAccountName is reserved for provisioner use"))
	}
	if !principal.isService() {
		if err := validateReservedCreateAnnotations(body.Annotations); err != nil {
			return nil, newCreateRequestError(http.StatusForbidden, err)
		}
	}
	if principal.isService() {
		if err := validateProvisionerRequestSurface(&body); err != nil {
			return nil, newCreateRequestError(http.StatusBadRequest, err)
		}
	}

	namespace, err := s.resolveSpritzNamespace(body.Namespace)
	if err != nil {
		return nil, newCreateRequestError(http.StatusForbidden, err)
	}
	requestedNamespace := s.namespaceOverrideRequested(body.Namespace, namespace)

	owner, err := normalizeCreateOwnerRequest(&body, principal, s.auth.enabled())
	if err != nil {
		if errors.Is(err, errForbidden) {
			return nil, newCreateRequestError(http.StatusForbidden, err)
		}
		return nil, newCreateRequestError(http.StatusBadRequest, err)
	}
	if body.OwnerRef != nil && strings.EqualFold(strings.TrimSpace(body.OwnerRef.Type), "external") {
		if !principal.isService() {
			return nil, newCreateRequestError(http.StatusForbidden, errForbidden)
		}
	} else {
		body.Spec.Owner = owner
	}
	fingerprintRequest := body

	requestedImage := strings.TrimSpace(body.Spec.Image) != ""
	requestedRepo := body.Spec.Repo != nil || len(body.Spec.Repos) > 0

	s.applyProvisionerDefaultPreset(&body, principal)
	if _, err := s.applyCreatePreset(&body); err != nil {
		return nil, newCreateRequestError(http.StatusBadRequest, err)
	}
	if body.PresetInputs != nil && strings.TrimSpace(body.PresetID) == "" {
		return nil, newCreateRequestError(http.StatusBadRequest, errors.New("presetInputs requires presetId"))
	}

	userConfigKeys, userConfigPayload, err := parseUserConfig(body.UserConfig)
	if err != nil {
		return nil, newCreateRequestError(http.StatusBadRequest, err)
	}
	var normalizedUserConfig json.RawMessage
	if principal.isService() && len(userConfigKeys) > 0 {
		return nil, newCreateRequestError(http.StatusBadRequest, errors.New("userConfig is not allowed for service principals"))
	}
	if len(userConfigKeys) > 0 {
		normalized, err := normalizeUserConfig(s.userConfigPolicy, userConfigKeys, userConfigPayload)
		if err != nil {
			return nil, newCreateRequestError(http.StatusBadRequest, err)
		}
		userConfigPayload = normalized
		encodedUserConfig, err := json.Marshal(userConfigPayload)
		if err != nil {
			return nil, newCreateRequestError(http.StatusBadRequest, errors.New("invalid userConfig"))
		}
		normalizedUserConfig = encodedUserConfig
		applyUserConfig(&body.Spec, userConfigKeys, userConfigPayload)
		if _, ok := userConfigKeys["image"]; ok {
			requestedImage = strings.TrimSpace(body.Spec.Image) != ""
		}
		if _, ok := userConfigKeys["repo"]; ok {
			requestedRepo = body.Spec.Repo != nil || len(body.Spec.Repos) > 0
		}
	}

	if err := validateCreateSpec(&body.Spec); err != nil {
		return nil, newCreateRequestError(http.StatusBadRequest, err)
	}

	return &normalizedCreateRequest{
		body:                 body,
		fingerprintRequest:   fingerprintRequest,
		namespace:            namespace,
		owner:                owner,
		userConfigKeys:       userConfigKeys,
		userConfigPayload:    userConfigPayload,
		normalizedUserConfig: normalizedUserConfig,
		requestedImage:       requestedImage,
		requestedRepo:        requestedRepo,
		requestedNamespace:   requestedNamespace,
		nameProvided:         body.Name != "",
		requestedNamePrefix:  strings.TrimSpace(fingerprintRequest.NamePrefix),
	}, nil
}

func validateCreateSpec(spec *spritzv1.SpritzSpec) error {
	if spec == nil {
		return errors.New("spec is required")
	}
	if spec.Image == "" {
		return errors.New("spec.image is required")
	}
	if spec.Repo != nil && len(spec.Repos) > 0 {
		return errors.New("spec.repo cannot be set when spec.repos is provided")
	}
	if spec.Repo != nil {
		if err := validateRepoDir(spec.Repo.Dir); err != nil {
			return err
		}
	}
	for _, repo := range spec.Repos {
		if err := validateRepoDir(repo.Dir); err != nil {
			return err
		}
	}
	if len(spec.SharedMounts) > 0 {
		normalized, err := normalizeSharedMounts(spec.SharedMounts)
		if err != nil {
			return err
		}
		spec.SharedMounts = normalized
	}
	return nil
}
